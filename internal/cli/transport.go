package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"openlight/internal/telegram"
)

type Transport struct {
	in     io.Reader
	out    io.Writer
	chatID int64
	userID int64
}

type Options struct {
	In     io.Reader
	Out    io.Writer
	ChatID int64
	UserID int64
}

func NewTransport(options Options) *Transport {
	return &Transport{
		in:     options.In,
		out:    options.Out,
		chatID: options.ChatID,
		userID: options.UserID,
	}
}

func (t *Transport) Poll(ctx context.Context, handler func(context.Context, telegram.IncomingMessage) error) error {
	if t.in == nil {
		return nil
	}

	scanner := bufio.NewScanner(t.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var messageID int64 = 1
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		text := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(text) == "" {
			continue
		}

		if err := handler(ctx, telegram.IncomingMessage{
			MessageID: messageID,
			ChatID:    t.chatID,
			UserID:    t.userID,
			Text:      text,
		}); err != nil {
			return err
		}
		messageID++
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (t *Transport) SendText(_ context.Context, _ int64, text string) error {
	if t.out == nil {
		return nil
	}

	_, err := fmt.Fprintln(t.out, text)
	return err
}
