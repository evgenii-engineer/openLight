package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type IncomingMessage struct {
	MessageID  int64
	ChatID     int64
	UserID     int64
	Text       string
	Source     string
	Audio      *AudioAttachment
	Image      *ImageAttachment
	CallbackID string
	IsCallback bool
}

type AudioAttachment struct {
	Kind     string
	FileID   string
	FileName string
	MimeType string
}

// ImageAttachment captures the metadata we need to download a photo or image
// document the user sent. Captions are surfaced separately on
// IncomingMessage.Text so existing routing keeps working.
type ImageAttachment struct {
	Kind     string // "photo" for inline photos, "document" for image attachments
	FileID   string
	FileName string
	MimeType string
	Caption  string
}

type DownloadedFile struct {
	Path     string
	FileName string
	Cleanup  func() error
}

type Button struct {
	Text         string
	CallbackData string
}

type Bot struct {
	token       string
	baseURL     string
	mode        string
	pollTimeout time.Duration
	webhook     WebhookOptions
	client      *http.Client
	logger      *slog.Logger
}

type Options struct {
	Token       string
	BaseURL     string
	Mode        string
	PollTimeout time.Duration
	Webhook     WebhookOptions
	Logger      *slog.Logger
}

type WebhookOptions struct {
	URL                string
	ListenAddr         string
	SecretToken        string
	DropPendingUpdates bool
}

const maxTelegramMessageRunes = 3500

func NewBot(options Options) *Bot {
	return &Bot{
		token:       strings.TrimSpace(options.Token),
		baseURL:     strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"),
		mode:        strings.ToLower(strings.TrimSpace(options.Mode)),
		pollTimeout: options.PollTimeout,
		webhook: WebhookOptions{
			URL:                strings.TrimSpace(options.Webhook.URL),
			ListenAddr:         strings.TrimSpace(options.Webhook.ListenAddr),
			SecretToken:        strings.TrimSpace(options.Webhook.SecretToken),
			DropPendingUpdates: options.Webhook.DropPendingUpdates,
		},
		client: &http.Client{
			Timeout: options.PollTimeout + 10*time.Second,
		},
		logger: options.Logger,
	}
}

func (b *Bot) Poll(ctx context.Context, handler func(context.Context, IncomingMessage) error) error {
	if b.mode == "webhook" {
		return b.serveWebhook(ctx, handler)
	}
	return b.pollUpdates(ctx, handler)
}

func (b *Bot) pollUpdates(ctx context.Context, handler func(context.Context, IncomingMessage) error) error {
	if err := b.deleteWebhook(ctx, false); err != nil {
		return err
	}

	var offset int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			return err
		}

		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			message, ok := update.incomingMessage()
			if !ok {
				continue
			}

			b.acknowledgeCallback(ctx, message)
			err := handler(ctx, message)
			if err != nil {
				return err
			}
		}
	}
}

func (b *Bot) serveWebhook(ctx context.Context, handler func(context.Context, IncomingMessage) error) error {
	path, err := webhookPath(b.webhook.URL)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, b.webhookHandler(ctx, handler))

	listener, err := net.Listen("tcp", b.webhook.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen telegram webhook: %w", err)
	}
	defer listener.Close()

	// WriteTimeout and IdleTimeout protect the listener from slow-loris
	// style stalls on long-lived TCP connections. The handler itself only
	// reads JSON from Telegram, so a few seconds is generous.
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErrors <- fmt.Errorf("serve telegram webhook: %w", err)
		}
	}()

	if err := b.setWebhook(ctx); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return err
	}

	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !strings.Contains(err.Error(), "closed network connection") {
			return fmt.Errorf("shutdown telegram webhook server: %w", err)
		}
		return ctx.Err()
	}
}

func (b *Bot) SendText(ctx context.Context, chatID int64, text string) error {
	for _, chunk := range splitTelegramMessage(text) {
		if err := b.sendMessage(ctx, chatID, chunk, nil); err != nil {
			return fmt.Errorf("send telegram message: %w", err)
		}
	}
	return nil
}

// SendPhoto uploads the file at path as an inline photo. Caption is optional
// and will be truncated to Telegram's 1024-character limit.
func (b *Bot) SendPhoto(ctx context.Context, chatID int64, path, caption string) error {
	return b.uploadFile(ctx, "/sendPhoto", "photo", chatID, path, caption)
}

// SendDocument uploads the file at path as a generic document. Useful when
// the file is not an image or when the caller wants Telegram to preserve the
// original filename.
func (b *Bot) SendDocument(ctx context.Context, chatID int64, path, caption string) error {
	return b.uploadFile(ctx, "/sendDocument", "document", chatID, path, caption)
}

func (b *Bot) uploadFile(ctx context.Context, method, field string, chatID int64, path, caption string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("telegram %s: file path is required", method)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("telegram %s: open %s: %w", method, path, err)
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("telegram %s: write chat_id: %w", method, err)
	}
	if trimmed := strings.TrimSpace(caption); trimmed != "" {
		if err := writer.WriteField("caption", truncateCaption(trimmed)); err != nil {
			return fmt.Errorf("telegram %s: write caption: %w", method, err)
		}
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filepath.Base(path)))
	if mimeType := guessFileMime(path); mimeType != "" {
		header.Set("Content-Type", mimeType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("telegram %s: create part: %w", method, err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("telegram %s: copy file: %w", method, err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("telegram %s: close writer: %w", method, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL(method), &buf)
	if err != nil {
		return fmt.Errorf("telegram %s: build request: %w", method, err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	response, err := b.client.Do(request)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("telegram %s returned %d: %s", method, response.StatusCode, strings.TrimSpace(string(text)))
	}
	var decoded apiResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("telegram %s: decode response: %w", method, err)
	}
	if !decoded.OK {
		if strings.TrimSpace(decoded.Description) == "" {
			return fmt.Errorf("telegram %s returned ok=false", method)
		}
		return fmt.Errorf("telegram %s returned ok=false: %s", method, strings.TrimSpace(decoded.Description))
	}
	return nil
}

func truncateCaption(caption string) string {
	const maxCaptionRunes = 1024
	runes := []rune(caption)
	if len(runes) <= maxCaptionRunes {
		return caption
	}
	return string(runes[:maxCaptionRunes-1]) + "…"
}

func guessFileMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func (b *Bot) SendTextWithButtons(ctx context.Context, chatID int64, text string, buttons [][]Button) error {
	chunks := splitTelegramMessage(text)
	for idx, chunk := range chunks {
		var keyboard [][]Button
		if idx == len(chunks)-1 {
			keyboard = buttons
		}
		if err := b.sendMessage(ctx, chatID, chunk, keyboard); err != nil {
			return fmt.Errorf("send telegram message with buttons: %w", err)
		}
	}
	return nil
}

func (b *Bot) EditMessageText(ctx context.Context, chatID, messageID int64, text string, buttons [][]Button) error {
	chunks := splitTelegramMessage(text)
	primary := chunks[0]
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       primary,
	}
	if markup := inlineKeyboardMarkup(buttons); markup != nil && len(chunks) == 1 {
		payload["reply_markup"] = markup
	}
	if err := b.call(ctx, "/editMessageText", payload, nil); err != nil {
		return fmt.Errorf("edit telegram message: %w", err)
	}
	if len(chunks) > 1 {
		for idx, chunk := range chunks[1:] {
			var kb [][]Button
			if idx == len(chunks)-2 {
				kb = buttons
			}
			if err := b.sendMessage(ctx, chatID, chunk, kb); err != nil {
				return fmt.Errorf("send overflow telegram chunk: %w", err)
			}
		}
	}
	return nil
}

func (b *Bot) SendTextWithReplyKeyboard(ctx context.Context, chatID int64, text string, rows [][]string, persistent bool) error {
	payload := map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": replyKeyboardMarkup(rows, persistent),
	}
	if err := b.call(ctx, "/sendMessage", payload, nil); err != nil {
		return fmt.Errorf("send telegram message with reply keyboard: %w", err)
	}
	return nil
}

func (b *Bot) RemoveReplyKeyboard(ctx context.Context, chatID int64, text string) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]any{
			"remove_keyboard": true,
		},
	}
	if err := b.call(ctx, "/sendMessage", payload, nil); err != nil {
		return fmt.Errorf("remove telegram reply keyboard: %w", err)
	}
	return nil
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

type update struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
}

type tgMessage struct {
	MessageID int64        `json:"message_id"`
	Text      string       `json:"text"`
	Caption   string       `json:"caption"`
	Chat      tgChat       `json:"chat"`
	From      tgUser       `json:"from"`
	Voice     *tgFile      `json:"voice"`
	Audio     *tgFile      `json:"audio"`
	Photo     []tgPhotoSize `json:"photo"`
	Document  *tgFile      `json:"document"`
}

type tgPhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Data    string     `json:"data"`
	Message *tgMessage `json:"message"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

type tgRemoteFile struct {
	FilePath string `json:"file_path"`
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	var updates []update
	err := b.call(ctx, "/getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(b.pollTimeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	if err != nil {
		return nil, fmt.Errorf("get telegram updates: %w", err)
	}
	return updates, nil
}

func (b *Bot) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s%s", b.baseURL, b.token, method)
}

func (b *Bot) fileURL(path string) string {
	return fmt.Sprintf("%s/file/bot%s/%s", b.baseURL, b.token, strings.TrimLeft(path, "/"))
}

func (b *Bot) setWebhook(ctx context.Context) error {
	err := b.call(ctx, "/setWebhook", map[string]any{
		"url":                  b.webhook.URL,
		"allowed_updates":      []string{"message", "callback_query"},
		"drop_pending_updates": b.webhook.DropPendingUpdates,
		"secret_token":         b.webhook.SecretToken,
	}, nil)
	if err != nil {
		return fmt.Errorf("set telegram webhook: %w", err)
	}
	return nil
}

func (b *Bot) deleteWebhook(ctx context.Context, dropPendingUpdates bool) error {
	err := b.call(ctx, "/deleteWebhook", map[string]any{
		"drop_pending_updates": dropPendingUpdates,
	}, nil)
	if err != nil {
		return fmt.Errorf("delete telegram webhook: %w", err)
	}
	return nil
}

type BotCommand struct {
	Command     string
	Description string
}

func (b *Bot) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	payload := make([]map[string]string, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		cmd := sanitizeCommandName(c.Command)
		desc := strings.TrimSpace(c.Description)
		if cmd == "" || desc == "" {
			continue
		}
		if _, dup := seen[cmd]; dup {
			continue
		}
		seen[cmd] = struct{}{}
		if utf8.RuneCountInString(desc) > 256 {
			desc = string([]rune(desc)[:256])
		}
		payload = append(payload, map[string]string{
			"command":     cmd,
			"description": desc,
		})
	}
	if err := b.call(ctx, "/setMyCommands", map[string]any{"commands": payload}, nil); err != nil {
		return fmt.Errorf("set telegram commands: %w", err)
	}
	return nil
}

func sanitizeCommandName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	var b strings.Builder
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				continue
			}
			b.WriteRune(r)
		case r == '-', r == ' ', r == '.':
			b.WriteRune('_')
		}
		if b.Len() == 32 {
			break
		}
	}
	return b.String()
}

func (b *Bot) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload for %s: %w", method, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL(method), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram request for %s: %w", method, err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := b.client.Do(request)
	if err != nil {
		return fmt.Errorf("call telegram %s: %w", method, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("telegram %s returned %d: %s", method, response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded apiResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode telegram response for %s: %w", method, err)
	}

	if !decoded.OK {
		if strings.TrimSpace(decoded.Description) == "" {
			return fmt.Errorf("telegram %s returned ok=false", method)
		}
		return fmt.Errorf("telegram %s returned ok=false: %s", method, strings.TrimSpace(decoded.Description))
	}

	if out == nil || len(decoded.Result) == 0 || string(decoded.Result) == "null" {
		return nil
	}

	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("decode telegram result for %s: %w", method, err)
	}

	return nil
}

func (b *Bot) webhookHandler(ctx context.Context, handler func(context.Context, IncomingMessage) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		if b.webhook.SecretToken != "" {
			provided := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
			// subtle.ConstantTimeCompare avoids leaking the secret one
			// byte at a time via response-time differences. The length
			// comparison is required because ConstantTimeCompare returns
			// 0 for length mismatches without doing the actual compare.
			if len(provided) != len(b.webhook.SecretToken) ||
				subtle.ConstantTimeCompare([]byte(provided), []byte(b.webhook.SecretToken)) != 1 {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}

		var update update
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&update); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)

		message, ok := update.incomingMessage()
		if !ok {
			return
		}

		go func() {
			b.acknowledgeCallback(ctx, message)
			err := handler(ctx, message)
			if err != nil && b.logger != nil {
				b.logger.Error("handle telegram webhook message", "error", err, "chat_id", message.ChatID, "user_id", message.UserID)
			}
		}()
	}
}

func (b *Bot) acknowledgeCallback(ctx context.Context, message IncomingMessage) {
	if !message.IsCallback || strings.TrimSpace(message.CallbackID) == "" {
		return
	}
	if ackErr := b.answerCallbackQuery(ctx, message.CallbackID); ackErr != nil && b.logger != nil {
		b.logger.Error("answer telegram callback query", "error", ackErr, "chat_id", message.ChatID, "user_id", message.UserID)
	}
}

func (u update) incomingMessage() (IncomingMessage, bool) {
	if u.Message != nil {
		if image := u.Message.imageAttachment(); image != nil {
			caption := strings.TrimSpace(u.Message.Caption)
			text := caption
			if text == "" {
				text = strings.TrimSpace(u.Message.Text)
			}
			return IncomingMessage{
				MessageID: u.Message.MessageID,
				ChatID:    u.Message.Chat.ID,
				UserID:    u.Message.From.ID,
				Text:      text,
				Source:    "telegram_image",
				Image:     image,
			}, true
		}
	}

	if u.Message != nil && strings.TrimSpace(u.Message.Text) != "" {
		return IncomingMessage{
			MessageID: u.Message.MessageID,
			ChatID:    u.Message.Chat.ID,
			UserID:    u.Message.From.ID,
			Text:      u.Message.Text,
			Source:    "telegram",
		}, true
	}

	if u.Message != nil {
		if audio := u.Message.audioAttachment(); audio != nil {
			return IncomingMessage{
				MessageID: u.Message.MessageID,
				ChatID:    u.Message.Chat.ID,
				UserID:    u.Message.From.ID,
				Source:    "telegram_voice",
				Audio:     audio,
			}, true
		}
	}

	if u.CallbackQuery == nil || u.CallbackQuery.Message == nil || strings.TrimSpace(u.CallbackQuery.Data) == "" {
		return IncomingMessage{}, false
	}

	return IncomingMessage{
		MessageID:  u.CallbackQuery.Message.MessageID,
		ChatID:     u.CallbackQuery.Message.Chat.ID,
		UserID:     u.CallbackQuery.From.ID,
		Text:       u.CallbackQuery.Data,
		Source:     "telegram_callback",
		CallbackID: strings.TrimSpace(u.CallbackQuery.ID),
		IsCallback: true,
	}, true
}

func (m *tgMessage) imageAttachment() *ImageAttachment {
	if m == nil {
		return nil
	}
	caption := strings.TrimSpace(m.Caption)
	if len(m.Photo) > 0 {
		// Telegram returns multiple photo sizes; use the largest (last).
		largest := m.Photo[len(m.Photo)-1]
		if strings.TrimSpace(largest.FileID) == "" {
			return nil
		}
		return &ImageAttachment{
			Kind:     "photo",
			FileID:   strings.TrimSpace(largest.FileID),
			FileName: "photo.jpg",
			MimeType: "image/jpeg",
			Caption:  caption,
		}
	}
	if m.Document != nil && strings.TrimSpace(m.Document.FileID) != "" {
		mime := strings.ToLower(strings.TrimSpace(m.Document.MimeType))
		if !strings.HasPrefix(mime, "image/") {
			return nil
		}
		name := strings.TrimSpace(m.Document.FileName)
		if name == "" {
			name = "image"
		}
		return &ImageAttachment{
			Kind:     "document",
			FileID:   strings.TrimSpace(m.Document.FileID),
			FileName: name,
			MimeType: mime,
			Caption:  caption,
		}
	}
	return nil
}

func (m *tgMessage) audioAttachment() *AudioAttachment {
	if m == nil {
		return nil
	}
	if m.Voice != nil && strings.TrimSpace(m.Voice.FileID) != "" {
		return &AudioAttachment{
			Kind:     "voice",
			FileID:   strings.TrimSpace(m.Voice.FileID),
			FileName: "voice.ogg",
			MimeType: strings.TrimSpace(m.Voice.MimeType),
		}
	}
	if m.Audio != nil && strings.TrimSpace(m.Audio.FileID) != "" {
		name := strings.TrimSpace(m.Audio.FileName)
		if name == "" {
			name = "audio"
		}
		return &AudioAttachment{
			Kind:     "audio",
			FileID:   strings.TrimSpace(m.Audio.FileID),
			FileName: name,
			MimeType: strings.TrimSpace(m.Audio.MimeType),
		}
	}
	return nil
}

func (b *Bot) DownloadFile(ctx context.Context, fileID string) (DownloadedFile, error) {
	var remote tgRemoteFile
	if err := b.call(ctx, "/getFile", map[string]any{"file_id": strings.TrimSpace(fileID)}, &remote); err != nil {
		return DownloadedFile{}, fmt.Errorf("get telegram file: %w", err)
	}
	if strings.TrimSpace(remote.FilePath) == "" {
		return DownloadedFile{}, fmt.Errorf("get telegram file: empty file path")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.fileURL(remote.FilePath), nil)
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("create telegram file request: %w", err)
	}

	response, err := b.client.Do(request)
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("download telegram file: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return DownloadedFile{}, fmt.Errorf("download telegram file returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	suffix := filepath.Ext(remote.FilePath)
	tempFile, err := os.CreateTemp("", "openlight-telegram-*"+suffix)
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("create temp telegram file: %w", err)
	}

	cleanup := func() error {
		return os.Remove(tempFile.Name())
	}

	if _, err := io.Copy(tempFile, response.Body); err != nil {
		_ = tempFile.Close()
		_ = cleanup()
		return DownloadedFile{}, fmt.Errorf("write temp telegram file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = cleanup()
		return DownloadedFile{}, fmt.Errorf("close temp telegram file: %w", err)
	}

	return DownloadedFile{
		Path:     tempFile.Name(),
		FileName: filepath.Base(remote.FilePath),
		Cleanup:  cleanup,
	}, nil
}

func webhookPath(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse telegram webhook url: %w", err)
	}
	if parsed.Path == "" {
		return "", fmt.Errorf("telegram webhook url must include a path")
	}
	return parsed.EscapedPath(), nil
}

func splitTelegramMessage(text string) []string {
	if text == "" {
		return []string{""}
	}
	if utf8.RuneCountInString(text) <= maxTelegramMessageRunes {
		return []string{text}
	}

	runes := []rune(text)
	chunks := make([]string, 0, len(runes)/maxTelegramMessageRunes+1)
	start := 0
	for start < len(runes) {
		end := start + maxTelegramMessageRunes
		if end >= len(runes) {
			chunks = append(chunks, string(runes[start:]))
			break
		}

		split := preferredSplitIndex(runes, start, end)
		chunk := strings.TrimRight(string(runes[start:split]), "\n")
		if chunk == "" {
			chunk = string(runes[start:split])
		}
		chunks = append(chunks, chunk)

		start = split
		for start < len(runes) && runes[start] == '\n' {
			start++
		}
	}

	return chunks
}

func preferredSplitIndex(runes []rune, start, end int) int {
	floor := start + maxTelegramMessageRunes/2
	for idx := end; idx > floor; idx-- {
		if runes[idx-1] == '\n' {
			return idx
		}
	}
	return end
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string, buttons [][]Button) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup := inlineKeyboardMarkup(buttons); markup != nil {
		payload["reply_markup"] = markup
	}
	return b.call(ctx, "/sendMessage", payload, nil)
}

// SendChatAction triggers the Telegram "user is typing..." indicator (or
// other action types like "upload_photo"). The indicator auto-clears after
// ~5 seconds or when the bot's next message arrives, whichever comes
// first. Cheap call — used to give users immediate feedback while we run
// the routing + skill pipeline.
func (b *Bot) SendChatAction(ctx context.Context, chatID int64, action string) error {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "typing"
	}
	return b.call(ctx, "/sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  action,
	}, nil)
}

func (b *Bot) answerCallbackQuery(ctx context.Context, callbackID string) error {
	return b.call(ctx, "/answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
	}, nil)
}

func replyKeyboardMarkup(rows [][]string, persistent bool) map[string]any {
	keyboard := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		items := make([]map[string]string, 0, len(row))
		for _, label := range row {
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			items = append(items, map[string]string{"text": label})
		}
		if len(items) > 0 {
			keyboard = append(keyboard, items)
		}
	}
	markup := map[string]any{
		"keyboard":          keyboard,
		"resize_keyboard":   true,
		"is_persistent":     persistent,
		"input_field_placeholder": "Type or tap…",
	}
	return markup
}

func inlineKeyboardMarkup(buttons [][]Button) map[string]any {
	rows := make([][]map[string]string, 0, len(buttons))
	for _, row := range buttons {
		items := make([]map[string]string, 0, len(row))
		for _, button := range row {
			text := strings.TrimSpace(button.Text)
			data := strings.TrimSpace(button.CallbackData)
			if text == "" || data == "" {
				continue
			}
			items = append(items, map[string]string{
				"text":          text,
				"callback_data": data,
			})
		}
		if len(items) > 0 {
			rows = append(rows, items)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return map[string]any{
		"inline_keyboard": rows,
	}
}
