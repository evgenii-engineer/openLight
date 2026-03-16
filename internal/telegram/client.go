package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type IncomingMessage struct {
	MessageID int64
	ChatID    int64
	UserID    int64
	Text      string
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

			if update.Message == nil || strings.TrimSpace(update.Message.Text) == "" {
				continue
			}

			message := IncomingMessage{
				MessageID: update.Message.MessageID,
				ChatID:    update.Message.Chat.ID,
				UserID:    update.Message.From.ID,
				Text:      update.Message.Text,
			}

			if err := handler(ctx, message); err != nil {
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

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
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
		err := b.call(ctx, "/sendMessage", map[string]any{
			"chat_id": chatID,
			"text":    chunk,
		}, nil)
		if err != nil {
			return fmt.Errorf("send telegram message: %w", err)
		}
	}
	return nil
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

type update struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      tgChat `json:"chat"`
	From      tgUser `json:"from"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	var updates []update
	err := b.call(ctx, "/getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(b.pollTimeout.Seconds()),
		"allowed_updates": []string{"message"},
	}, &updates)
	if err != nil {
		return nil, fmt.Errorf("get telegram updates: %w", err)
	}
	return updates, nil
}

func (b *Bot) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s%s", b.baseURL, b.token, method)
}

func (b *Bot) setWebhook(ctx context.Context) error {
	err := b.call(ctx, "/setWebhook", map[string]any{
		"url":                  b.webhook.URL,
		"allowed_updates":      []string{"message"},
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

		if b.webhook.SecretToken != "" && r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != b.webhook.SecretToken {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
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
			if err := handler(ctx, message); err != nil && b.logger != nil {
				b.logger.Error("handle telegram webhook message", "error", err, "chat_id", message.ChatID, "user_id", message.UserID)
			}
		}()
	}
}

func (u update) incomingMessage() (IncomingMessage, bool) {
	if u.Message == nil || strings.TrimSpace(u.Message.Text) == "" {
		return IncomingMessage{}, false
	}

	return IncomingMessage{
		MessageID: u.Message.MessageID,
		ChatID:    u.Message.Chat.ID,
		UserID:    u.Message.From.ID,
		Text:      u.Message.Text,
	}, true
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
