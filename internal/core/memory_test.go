package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openlight/internal/auth"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/skills/notes"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
	"openlight/internal/utils"
)

// recordingMemory captures what the agent hands to long-term memory.
type recordingMemory struct {
	mu sync.Mutex

	turns []recordedTurn
	files []MemoryFile

	duplicate bool
	err       error
}

type recordedTurn struct {
	chatID int64
	role   string
	text   string
}

func (m *recordingMemory) RecordTurn(chatID int64, role, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = append(m.turns, recordedTurn{chatID: chatID, role: role, text: text})
}

func (m *recordingMemory) IngestFile(_ context.Context, file MemoryFile) (MemoryReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files = append(m.files, file)
	if m.err != nil {
		return MemoryReceipt{}, m.err
	}
	return MemoryReceipt{SourceID: "source-1", Duplicate: m.duplicate}, nil
}

func (m *recordingMemory) snapshot() ([]recordedTurn, []MemoryFile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedTurn(nil), m.turns...), append([]MemoryFile(nil), m.files...)
}

func newMemoryTestAgent(t *testing.T, transport *fakeTransport) *Agent {
	t.Helper()

	repo, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(skills.NewHelpSkill(registry))

	return NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		nil,
		time.Second,
	)
}

func TestAgentRecordsBothSidesOfTheConversation(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	agent := newMemoryTestAgent(t, transport)

	memory := &recordingMemory{}
	agent.SetMemory(memory)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100, Text: "/note buy milk",
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	turns, _ := memory.snapshot()
	if len(turns) != 2 {
		t.Fatalf("expected the user turn and the reply, got %+v", turns)
	}
	if turns[0].role != "user" || turns[0].text != "/note buy milk" {
		t.Fatalf("user turn wrong: %+v", turns[0])
	}
	if turns[1].role != "assistant" || !strings.HasPrefix(turns[1].text, "Saved note") {
		t.Fatalf("assistant turn wrong: %+v", turns[1])
	}
}

func TestAgentRecordsTheSameRedactedTextAsTheMessageLog(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	agent := newMemoryTestAgent(t, transport)

	memory := &recordingMemory{}
	agent.SetMemory(memory)

	// Long-term memory must apply exactly the redaction policy the
	// message log already applies — no weaker, since memory outlives the
	// conversation. `user add` is the command the redactor knows about.
	raw := "/user add synapse alice hunter2"
	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100, Text: raw,
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	turns, _ := memory.snapshot()
	if len(turns) == 0 {
		t.Fatal("nothing was recorded")
	}
	if turns[0].text != utils.RedactSensitiveText(raw) {
		t.Fatalf("memory got %q, want the redacted form %q", turns[0].text, utils.RedactSensitiveText(raw))
	}
	if strings.Contains(turns[0].text, "hunter2") {
		t.Fatalf("the password reached long-term memory: %q", turns[0].text)
	}
}

func TestAgentArchivesInboundDocuments(t *testing.T) {
	ctx := context.Background()

	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7 fake"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	transport := &fakeTransport{filePath: pdfPath}
	agent := newMemoryTestAgent(t, transport)
	memory := &recordingMemory{}
	agent.SetMemory(memory)

	err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100,
		Source: "telegram_document",
		Document: &telegram.DocumentAttachment{
			FileID:   "file-1",
			FileName: "report.pdf",
			MimeType: "application/pdf",
			FileSize: 13,
			Caption:  "quarterly numbers",
		},
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	_, files := memory.snapshot()
	if len(files) != 1 {
		t.Fatalf("expected one archived file, got %d", len(files))
	}
	file := files[0]
	if file.Kind != "documents" || file.MIMEType != "application/pdf" || file.FileName != "report.pdf" {
		t.Fatalf("archived file metadata is wrong: %+v", file)
	}
	if file.Path != pdfPath {
		t.Fatalf("archived path = %q, want the downloaded file", file.Path)
	}
	if file.Metadata["caption"] != "quarterly numbers" {
		t.Fatalf("caption was not carried through: %+v", file.Metadata)
	}

	// The user gets an immediate acknowledgement — the original is
	// already safe on disk even though indexing has not started.
	if len(transport.sent) != 1 || !strings.Contains(transport.sent[0], "report.pdf") {
		t.Fatalf("unexpected reply: %#v", transport.sent)
	}
}

func TestAgentTellsTheUserWhenADocumentIsAlreadyKnown(t *testing.T) {
	ctx := context.Background()

	pdfPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7 fake"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	transport := &fakeTransport{filePath: pdfPath}
	agent := newMemoryTestAgent(t, transport)
	agent.SetMemory(&recordingMemory{duplicate: true})

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100,
		Document: &telegram.DocumentAttachment{FileID: "f", FileName: "report.pdf", MimeType: "application/pdf"},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(transport.sent) != 1 || !strings.Contains(strings.ToLower(transport.sent[0]), "already in memory") {
		t.Fatalf("unexpected reply: %#v", transport.sent)
	}
}

func TestAgentReportsArchiveFailuresInsteadOfPretendingSuccess(t *testing.T) {
	ctx := context.Background()

	pdfPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	transport := &fakeTransport{filePath: pdfPath}
	agent := newMemoryTestAgent(t, transport)
	agent.SetMemory(&recordingMemory{err: errors.New("disk full")})

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100,
		Document: &telegram.DocumentAttachment{FileID: "f", FileName: "report.pdf", MimeType: "application/pdf"},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(transport.sent) != 1 || !strings.Contains(transport.sent[0], "could not save") {
		t.Fatalf("a failed archive must be reported, got %#v", transport.sent)
	}
}

func TestAgentIgnoresDocumentsWhenMemoryIsDisabled(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	agent := newMemoryTestAgent(t, transport)

	// No SetMemory call: this is exactly the previous behaviour, where a
	// non-image document was dropped without a reply.
	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100,
		Document: &telegram.DocumentAttachment{FileID: "f", FileName: "report.pdf", MimeType: "application/pdf"},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(transport.sent) != 0 {
		t.Fatalf("expected silence with memory disabled, got %#v", transport.sent)
	}
}

func TestAgentRejectsOversizedDocuments(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	agent := newMemoryTestAgent(t, transport)

	memory := &recordingMemory{}
	agent.SetMemory(memory)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200, UserID: 100,
		Document: &telegram.DocumentAttachment{
			FileID: "f", FileName: "huge.bin", MimeType: "application/octet-stream",
			FileSize: maxInboundDocumentBytes + 1,
		},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if _, files := memory.snapshot(); len(files) != 0 {
		t.Fatalf("an oversized file was downloaded anyway: %+v", files)
	}
	if len(transport.sent) != 1 || !strings.Contains(transport.sent[0], "too large") {
		t.Fatalf("unexpected reply: %#v", transport.sent)
	}
}

func TestAgentBlocksDocumentsFromUnauthorizedUsers(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	agent := newMemoryTestAgent(t, transport)

	memory := &recordingMemory{}
	agent.SetMemory(memory)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 999, UserID: 999,
		Document: &telegram.DocumentAttachment{FileID: "f", FileName: "x.pdf", MimeType: "application/pdf"},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if _, files := memory.snapshot(); len(files) != 0 {
		t.Fatalf("an unauthorized upload reached memory: %+v", files)
	}
	if len(transport.sent) != 1 || transport.sent[0] != "access denied" {
		t.Fatalf("unexpected reply: %#v", transport.sent)
	}
}
