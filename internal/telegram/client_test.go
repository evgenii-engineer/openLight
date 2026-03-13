package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestBotPollAndSendText(t *testing.T) {
	t.Parallel()

	var getUpdatesCalls int32
	var sentText atomic.Value

	bot := NewBot("TOKEN", "https://telegram.invalid", time.Second, nil)
	bot.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
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
