package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
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
	pollTimeout time.Duration
	client      *http.Client
	logger      *slog.Logger
}

func NewBot(token, baseURL string, pollTimeout time.Duration, logger *slog.Logger) *Bot {
	return &Bot{
		token:       strings.TrimSpace(token),
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		pollTimeout: pollTimeout,
		client: &http.Client{
			Timeout: pollTimeout + 10*time.Second,
		},
		logger: logger,
	}
}

func (b *Bot) Poll(ctx context.Context, handler func(context.Context, IncomingMessage) error) error {
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

func (b *Bot) SendText(ctx context.Context, chatID int64, text string) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sendMessage payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL("/sendMessage"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create sendMessage request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := b.client.Do(request)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("telegram sendMessage returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	return nil
}

type updatesResponse struct {
	OK     bool     `json:"ok"`
	Result []update `json:"result"`
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
	payload := map[string]any{
		"offset":          offset,
		"timeout":         int(b.pollTimeout.Seconds()),
		"allowed_updates": []string{"message"},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal getUpdates payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL("/getUpdates"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create getUpdates request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := b.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("get telegram updates: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("telegram getUpdates returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded updatesResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode getUpdates response: %w", err)
	}

	if !decoded.OK {
		return nil, fmt.Errorf("telegram getUpdates returned ok=false")
	}

	return decoded.Result, nil
}

func (b *Bot) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s%s", b.baseURL, b.token, method)
}
