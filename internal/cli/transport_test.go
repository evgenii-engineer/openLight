package cli

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"openlight/internal/telegram"
)

func TestTransportPollReadsLinesAsMessages(t *testing.T) {
	t.Parallel()

	var messages []telegram.IncomingMessage
	transport := NewTransport(Options{
		In:     bytes.NewBufferString("ping\n\nstatus\n"),
		ChatID: 200,
		UserID: 100,
	})

	err := transport.Poll(context.Background(), func(_ context.Context, message telegram.IncomingMessage) error {
		messages = append(messages, message)
		return nil
	})
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %#v", messages)
	}
	if want := []string{"ping", "status"}; !reflect.DeepEqual([]string{messages[0].Text, messages[1].Text}, want) {
		t.Fatalf("unexpected texts: %#v", messages)
	}
	if messages[0].ChatID != 200 || messages[0].UserID != 100 {
		t.Fatalf("unexpected message identity: %#v", messages[0])
	}
}

func TestTransportSendTextWritesReply(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	transport := NewTransport(Options{Out: &output})
	if err := transport.SendText(context.Background(), 200, "pong"); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if got := output.String(); got != "pong\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}
