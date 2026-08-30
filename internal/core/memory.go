package core

import (
	"context"
)

// Memory is the agent's view of the long-term memory subsystem. It is
// stated here, in the agent's own vocabulary, so internal/core keeps no
// dependency on the memory package or its types — the runtime adapts
// between the two.
//
// Every method must be safe to call on an unhealthy subsystem and must
// not add latency to the reply path: RecordTurn queues, IngestFile does
// a local file copy and a SQLite insert and nothing else.
type Memory interface {
	// RecordTurn stores one conversation turn for later distillation.
	// Never blocks.
	RecordTurn(chatID int64, role, text string)

	// IngestFile archives a file and queues it for indexing. Returns
	// the durable archive path and whether the content was already
	// known. An error means the bytes were NOT saved.
	IngestFile(ctx context.Context, file MemoryFile) (MemoryReceipt, error)
}

// MemoryFile describes an inbound artefact to archive.
type MemoryFile struct {
	// Path is the local file to copy into the archive.
	Path string

	// Kind is "documents", "images", "voice", or "telegram".
	Kind string

	FileName string
	MIMEType string
	Title    string

	// Source labels the origin, e.g. "telegram:chat:123".
	Source string

	// ExternalID is the Telegram message or file id.
	ExternalID string

	ChatID int64
	UserID int64

	// Metadata carries pre-computed text (a whisper transcript, a
	// vision description, OCR output) so the ingestion worker does not
	// have to redo work the agent already paid for.
	Metadata map[string]string
}

// MemoryReceipt is what the agent learns after archiving.
type MemoryReceipt struct {
	SourceID  string
	Duplicate bool
}

// recordTurn is the agent's guarded entry point for conversation
// capture. Nil memory (subsystem disabled) is a no-op.
func (a *Agent) recordTurn(chatID int64, role, text string) {
	if a.memory == nil || chatID == 0 || text == "" {
		return
	}
	a.memory.RecordTurn(chatID, role, text)
}

// SetMemory installs the optional long-term memory subsystem.
func (a *Agent) SetMemory(memory Memory) {
	a.memory = memory
}
