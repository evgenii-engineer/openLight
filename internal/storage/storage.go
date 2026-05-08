package storage

import (
	"context"
	"time"

	"openlight/internal/models"
)

type WatchListOptions struct {
	ChatID      int64
	EnabledOnly bool
}

type WatchIncidentListOptions struct {
	ChatID  int64
	WatchID int64
	Limit   int
}

type VisualWatchListOptions struct {
	ChatID      int64
	EnabledOnly bool
}

// The interfaces below are deliberately narrow. Each captures the
// methods one specific consumer needs. The wide Repository interface
// at the bottom composes them all so existing callers keep working
// unchanged; new callers should depend on the smallest interface they
// actually need so their tests don't have to stub the world.

type MessageStore interface {
	SaveMessage(ctx context.Context, message models.Message) error
	ListMessagesByChat(ctx context.Context, chatID int64, limit int) ([]models.Message, error)
}

type SkillCallStore interface {
	SaveSkillCall(ctx context.Context, call models.SkillCall) error
}

type NoteStore interface {
	AddNote(ctx context.Context, text string) (models.Note, error)
	ListNotes(ctx context.Context, limit int) ([]models.Note, error)
	DeleteNote(ctx context.Context, id int64) error
}

type MemoryStore interface {
	AddMemory(ctx context.Context, memory models.Memory) (models.Memory, error)
	ListMemories(ctx context.Context, limit int) ([]models.Memory, error)
	SearchMemories(ctx context.Context, query string, limit int) ([]models.Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
}

type WatchStore interface {
	CreateWatch(ctx context.Context, watch models.Watch) (models.Watch, error)
	ListWatches(ctx context.Context, options WatchListOptions) ([]models.Watch, error)
	GetWatch(ctx context.Context, id int64) (models.Watch, bool, error)
	UpdateWatch(ctx context.Context, watch models.Watch) error
	DeleteWatch(ctx context.Context, id int64) error
}

type WatchIncidentStore interface {
	CreateWatchIncident(ctx context.Context, incident models.WatchIncident) (models.WatchIncident, error)
	GetWatchIncident(ctx context.Context, id int64) (models.WatchIncident, bool, error)
	GetOpenWatchIncident(ctx context.Context, watchID int64) (models.WatchIncident, bool, error)
	ListWatchIncidents(ctx context.Context, options WatchIncidentListOptions) ([]models.WatchIncident, error)
	ListPendingWatchIncidents(ctx context.Context, chatID int64, now time.Time) ([]models.WatchIncident, error)
	ListExpiredPendingWatchIncidents(ctx context.Context, now time.Time) ([]models.WatchIncident, error)
	UpdateWatchIncident(ctx context.Context, incident models.WatchIncident) error
}

type VisualWatchStore interface {
	CreateVisualWatch(ctx context.Context, watch models.VisualWatch) (models.VisualWatch, error)
	GetVisualWatch(ctx context.Context, id int64) (models.VisualWatch, bool, error)
	ListVisualWatches(ctx context.Context, options VisualWatchListOptions) ([]models.VisualWatch, error)
	UpdateVisualWatch(ctx context.Context, watch models.VisualWatch) error
	DeleteVisualWatch(ctx context.Context, id int64) error
}

type SettingsStore interface {
	SetSetting(ctx context.Context, key, value string) error
	GetSetting(ctx context.Context, key string) (models.Setting, bool, error)
}

type MaintenanceStore interface {
	PruneOlderThan(ctx context.Context, cutoff time.Time) (messages, skillCalls int64, err error)
}

// Repository is the union of every per-domain store, plus Close. It
// stays here so existing callers and the SQLite implementation keep
// compiling unchanged. New callers should pick the narrowest
// sub-interface they need rather than depending on this aggregate.
type Repository interface {
	MessageStore
	SkillCallStore
	NoteStore
	MemoryStore
	WatchStore
	WatchIncidentStore
	VisualWatchStore
	SettingsStore
	MaintenanceStore
	Close() error
}
