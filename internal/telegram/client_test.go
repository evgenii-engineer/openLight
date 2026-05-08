package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBotPollAndSendText(t *testing.T) {
	t.Parallel()

	var getUpdatesCalls int32
	var sentText atomic.Value

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/deleteWebhook":
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			case "/botTOKEN/getUpdates":
				call := atomic.AddInt32(&getUpdatesCalls, 1)
				if call == 1 {
					return jsonResponse(map[string]any{
						"ok": true,
						"result": []map[string]any{
							{
								"update_id": 1,
								"message": map[string]any{
									"message_id": 10,
									"text":       "/ping",
									"chat":       map[string]any{"id": 200},
									"from":       map[string]any{"id": 100},
								},
							},
						},
					}), nil
				}
				return jsonResponse(map[string]any{"ok": true, "result": []any{}}), nil
			case "/botTOKEN/sendMessage":
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode sendMessage payload: %v", err)
				}
				sentText.Store(payload["text"])
				return jsonResponse(map[string]any{"ok": true, "result": map[string]any{}}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Poll(ctx, func(ctx context.Context, message IncomingMessage) error {
		if message.Text != "/ping" || message.ChatID != 200 || message.UserID != 100 {
			t.Fatalf("unexpected message: %#v", message)
		}
		cancel()
		return bot.SendText(ctx, message.ChatID, "pong")
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}

	if got, _ := sentText.Load().(string); got != "pong" {
		t.Fatalf("expected sendMessage to be called with pong, got %q", got)
	}
}

func TestBotPollHandlesCallbackQueries(t *testing.T) {
	t.Parallel()

	var getUpdatesCalls int32
	var answeredCallback atomic.Value

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/deleteWebhook":
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			case "/botTOKEN/getUpdates":
				call := atomic.AddInt32(&getUpdatesCalls, 1)
				if call == 1 {
					return jsonResponse(map[string]any{
						"ok": true,
						"result": []map[string]any{
							{
								"update_id": 1,
								"callback_query": map[string]any{
									"id":   "cb-1",
									"data": "watch:yes:7",
									"from": map[string]any{"id": 100},
									"message": map[string]any{
										"message_id": 10,
										"chat":       map[string]any{"id": 200},
									},
								},
							},
						},
					}), nil
				}
				return jsonResponse(map[string]any{"ok": true, "result": []any{}}), nil
			case "/botTOKEN/answerCallbackQuery":
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode answerCallbackQuery payload: %v", err)
				}
				answeredCallback.Store(payload["callback_query_id"])
				cancel()
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	err := bot.Poll(ctx, func(_ context.Context, message IncomingMessage) error {
		if message.Text != "watch:yes:7" || message.ChatID != 200 || message.UserID != 100 {
			t.Fatalf("unexpected callback message: %#v", message)
		}
		if !message.IsCallback || message.CallbackID != "cb-1" {
			t.Fatalf("expected callback metadata, got %#v", message)
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}

	if got, _ := answeredCallback.Load().(string); got != "cb-1" {
		t.Fatalf("expected callback query to be acknowledged, got %q", got)
	}
}

func TestBotPollAcknowledgesCallbackBeforeHandlerCompletes(t *testing.T) {
	t.Parallel()

	var getUpdatesCalls int32
	callbackAcked := make(chan struct{}, 1)
	releaseHandler := make(chan struct{})
	handlerStarted := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/deleteWebhook":
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			case "/botTOKEN/getUpdates":
				call := atomic.AddInt32(&getUpdatesCalls, 1)
				if call == 1 {
					return jsonResponse(map[string]any{
						"ok": true,
						"result": []map[string]any{
							{
								"update_id": 1,
								"callback_query": map[string]any{
									"id":   "cb-early",
									"data": "watch:action:7:restart_service",
									"from": map[string]any{"id": 100},
									"message": map[string]any{
										"message_id": 10,
										"chat":       map[string]any{"id": 200},
									},
								},
							},
						},
					}), nil
				}
				return jsonResponse(map[string]any{"ok": true, "result": []any{}}), nil
			case "/botTOKEN/answerCallbackQuery":
				select {
				case callbackAcked <- struct{}{}:
				default:
				}
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- bot.Poll(ctx, func(_ context.Context, message IncomingMessage) error {
			if message.CallbackID != "cb-early" {
				t.Fatalf("unexpected callback message: %#v", message)
			}
			select {
			case handlerStarted <- struct{}{}:
			default:
			}
			<-releaseHandler
			cancel()
			return nil
		})
	}()

	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler start")
	}

	select {
	case <-callbackAcked:
	case <-time.After(2 * time.Second):
		t.Fatal("expected callback to be acknowledged before handler completion")
	}

	close(releaseHandler)

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}
}

func TestBotPollHandlesVoiceMessages(t *testing.T) {
	t.Parallel()

	var getUpdatesCalls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/deleteWebhook":
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			case "/botTOKEN/getUpdates":
				call := atomic.AddInt32(&getUpdatesCalls, 1)
				if call == 1 {
					return jsonResponse(map[string]any{
						"ok": true,
						"result": []map[string]any{
							{
								"update_id": 1,
								"message": map[string]any{
									"message_id": 10,
									"chat":       map[string]any{"id": 200},
									"from":       map[string]any{"id": 100},
									"voice": map[string]any{
										"file_id":   "voice-1",
										"mime_type": "audio/ogg",
									},
								},
							},
						},
					}), nil
				}
				return jsonResponse(map[string]any{"ok": true, "result": []any{}}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	err := bot.Poll(ctx, func(_ context.Context, message IncomingMessage) error {
		if message.Audio == nil || message.Audio.FileID != "voice-1" {
			t.Fatalf("unexpected voice message: %#v", message)
		}
		cancel()
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}
}

func TestBotDownloadFile(t *testing.T) {
	t.Parallel()

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/getFile":
				return jsonResponse(map[string]any{
					"ok": true,
					"result": map[string]any{
						"file_path": "voice/file.ogg",
					},
				}), nil
			case "/file/botTOKEN/voice/file.ogg":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("voice-bytes")),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	file, err := bot.DownloadFile(context.Background(), "voice-1")
	if err != nil {
		t.Fatalf("DownloadFile returned error: %v", err)
	}
	defer file.Cleanup()

	content, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "voice-bytes" {
		t.Fatalf("unexpected downloaded content: %q", string(content))
	}
}

func TestBotWebhookReceivesMessages(t *testing.T) {
	t.Parallel()

	addr := freeTCPAddr(t)
	webhookReady := make(chan struct{}, 1)
	messages := make(chan IncomingMessage, 1)

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "webhook",
		PollTimeout: time.Second,
		Webhook: WebhookOptions{
			URL:         "https://example.com/telegram/webhook",
			ListenAddr:  addr,
			SecretToken: "secret",
		},
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/setWebhook":
				select {
				case webhookReady <- struct{}{}:
				default:
				}
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- bot.Poll(ctx, func(ctx context.Context, message IncomingMessage) error {
			messages <- message
			cancel()
			return nil
		})
	}()

	select {
	case <-webhookReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for setWebhook")
	}

	requestBody, err := json.Marshal(map[string]any{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 10,
			"text":       "hello",
			"chat":       map[string]any{"id": 200},
			"from":       map[string]any{"id": 100},
		},
	})
	if err != nil {
		t.Fatalf("marshal webhook request: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, "http://"+addr+"/telegram/webhook", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("create webhook request: %v", err)
	}
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send webhook request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected webhook status: %d", response.StatusCode)
	}

	select {
	case message := <-messages:
		if message.Text != "hello" || message.ChatID != 200 || message.UserID != 100 {
			t.Fatalf("unexpected message: %#v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook message")
	}

	err = <-errCh
	if err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}
}

func TestBotWebhookRejectsWrongSecret(t *testing.T) {
	t.Parallel()

	addr := freeTCPAddr(t)
	webhookReady := make(chan struct{}, 1)

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "webhook",
		PollTimeout: time.Second,
		Webhook: WebhookOptions{
			URL:         "https://example.com/telegram/webhook",
			ListenAddr:  addr,
			SecretToken: "real-secret",
		},
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/botTOKEN/setWebhook" {
				select {
				case webhookReady <- struct{}{}:
				default:
				}
				return jsonResponse(map[string]any{"ok": true, "result": true}), nil
			}
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- bot.Poll(ctx, func(ctx context.Context, message IncomingMessage) error {
			t.Errorf("handler should not have been called")
			return nil
		})
	}()

	select {
	case <-webhookReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for setWebhook")
	}

	cases := []struct {
		name   string
		secret string
	}{
		{"empty", ""},
		{"wrong same length", "wrng-secret"},
		{"wrong shorter", "real"},
		{"wrong longer", "real-secret-extra"},
	}
	for _, tc := range cases {
		body := []byte(`{"update_id":1,"message":{"message_id":10,"text":"x","chat":{"id":1},"from":{"id":1}}}`)
		request, err := http.NewRequest(http.MethodPost, "http://"+addr+"/telegram/webhook", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		if tc.secret != "" {
			request.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.secret)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("send request: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("[%s] expected 401, got %d", tc.name, response.StatusCode)
		}
	}

	cancel()
	err := <-errCh
	if err != nil && err != context.Canceled {
		t.Fatalf("Poll returned error: %v", err)
	}
}

func TestBotSendTextSplitsLongMessages(t *testing.T) {
	t.Parallel()

	sentTexts := make([]string, 0, 2)
	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/botTOKEN/sendMessage":
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode sendMessage payload: %v", err)
				}
				text, _ := payload["text"].(string)
				sentTexts = append(sentTexts, text)
				return jsonResponse(map[string]any{"ok": true, "result": map[string]any{}}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	longText := strings.Repeat("a", maxTelegramMessageRunes+100)
	if err := bot.SendText(context.Background(), 200, longText); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}

	if len(sentTexts) != 2 {
		t.Fatalf("expected 2 sendMessage calls, got %d", len(sentTexts))
	}
	if !slices.Equal(sentTexts, splitTelegramMessage(longText)) {
		t.Fatalf("unexpected chunks: %#v", sentTexts)
	}
}

func TestBotSendTextWithButtons(t *testing.T) {
	t.Parallel()

	var payload map[string]any
	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/botTOKEN/sendMessage" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode sendMessage payload: %v", err)
			}
			return jsonResponse(map[string]any{"ok": true, "result": map[string]any{}}), nil
		}),
	}

	err := bot.SendTextWithButtons(context.Background(), 200, "choose", [][]Button{
		{
			{Text: "Restart", CallbackData: "watch:yes:1"},
			{Text: "Ignore", CallbackData: "watch:no:1"},
		},
	})
	if err != nil {
		t.Fatalf("SendTextWithButtons returned error: %v", err)
	}

	replyMarkup, ok := payload["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("expected reply_markup in payload, got %#v", payload)
	}
	keyboard, ok := replyMarkup["inline_keyboard"].([]any)
	if !ok || len(keyboard) != 1 {
		t.Fatalf("unexpected inline keyboard: %#v", replyMarkup)
	}
	row, ok := keyboard[0].([]any)
	if !ok || len(row) != 2 {
		t.Fatalf("unexpected inline keyboard row: %#v", keyboard)
	}
	first, ok := row[0].(map[string]any)
	if !ok || first["callback_data"] != "watch:yes:1" {
		t.Fatalf("unexpected first button: %#v", row[0])
	}
}

func TestBotSendPhotoUploadsMultipart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	imagePath := dir + "/photo.png"
	if err := os.WriteFile(imagePath, []byte("PNG-DATA"), 0o644); err != nil {
		t.Fatalf("write photo: %v", err)
	}

	var receivedContentType string
	var receivedBody []byte

	bot := NewBot(Options{
		Token:       "TOKEN",
		BaseURL:     "https://telegram.invalid",
		Mode:        "polling",
		PollTimeout: time.Second,
	})
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/botTOKEN/sendPhoto" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			receivedContentType = r.Header.Get("Content-Type")
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			receivedBody = body
			return jsonResponse(map[string]any{"ok": true, "result": map[string]any{"message_id": 7}}), nil
		}),
	}

	if err := bot.SendPhoto(context.Background(), 99, imagePath, "look at this"); err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}
	if !strings.HasPrefix(receivedContentType, "multipart/form-data") {
		t.Fatalf("expected multipart content-type, got %q", receivedContentType)
	}
	bodyStr := string(receivedBody)
	if !strings.Contains(bodyStr, "PNG-DATA") {
		t.Fatalf("expected file bytes in multipart body")
	}
	if !strings.Contains(bodyStr, "look at this") {
		t.Fatalf("expected caption in multipart body")
	}
	if !strings.Contains(bodyStr, "name=\"chat_id\"") {
		t.Fatalf("expected chat_id field in multipart body")
	}
	if !strings.Contains(bodyStr, "name=\"photo\"") {
		t.Fatalf("expected photo field in multipart body")
	}
}

func TestBotIncomingMessageSurfacesPhoto(t *testing.T) {
	t.Parallel()

	upd := update{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 5,
			Caption:   "describe please",
			Chat:      tgChat{ID: 100},
			From:      tgUser{ID: 200},
			Photo: []tgPhotoSize{
				{FileID: "small", Width: 90, Height: 60, FileSize: 1024},
				{FileID: "large", Width: 1280, Height: 720, FileSize: 100000},
			},
		},
	}
	msg, ok := upd.incomingMessage()
	if !ok {
		t.Fatalf("expected incomingMessage to surface photo update")
	}
	if msg.Image == nil {
		t.Fatalf("expected Image attachment")
	}
	if msg.Image.FileID != "large" {
		t.Fatalf("expected largest photo size, got %q", msg.Image.FileID)
	}
	if msg.Image.Caption != "describe please" {
		t.Fatalf("expected caption to be surfaced, got %q", msg.Image.Caption)
	}
	if msg.Text != "describe please" {
		t.Fatalf("expected text to mirror caption, got %q", msg.Text)
	}
	if msg.Source != "telegram_image" {
		t.Fatalf("expected telegram_image source, got %q", msg.Source)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(payload any) *http.Response {
	body, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	defer listener.Close()

	return strings.TrimPrefix(listener.Addr().String(), "[::]")
}
