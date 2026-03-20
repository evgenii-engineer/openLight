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

type Repository interface {
	SaveMessage(ctx context.Context, message models.Message) error
	ListMessagesByChat(ctx context.Context, chatID int64, limit int) ([]models.Message, error)
	SaveSkillCall(ctx context.Context, call models.SkillCall) error
	AddNote(ctx context.Context, text string) (models.Note, error)
	ListNotes(ctx context.Context, limit int) ([]models.Note, error)
	DeleteNote(ctx context.Context, id int64) error
	CreateWatch(ctx context.Context, watch models.Watch) (models.Watch, error)
	ListWatches(ctx context.Context, options WatchListOptions) ([]models.Watch, error)
	GetWatch(ctx context.Context, id int64) (models.Watch, bool, error)
	UpdateWatch(ctx context.Context, watch models.Watch) error
	DeleteWatch(ctx context.Context, id int64) error
	CreateWatchIncident(ctx context.Context, incident models.WatchIncident) (models.WatchIncident, error)
	GetWatchIncident(ctx context.Context, id int64) (models.WatchIncident, bool, error)
	GetOpenWatchIncident(ctx context.Context, watchID int64) (models.WatchIncident, bool, error)
	ListWatchIncidents(ctx context.Context, options WatchIncidentListOptions) ([]models.WatchIncident, error)
	ListPendingWatchIncidents(ctx context.Context, chatID int64, now time.Time) ([]models.WatchIncident, error)
	ListExpiredPendingWatchIncidents(ctx context.Context, now time.Time) ([]models.WatchIncident, error)
	UpdateWatchIncident(ctx context.Context, incident models.WatchIncident) error
	SetSetting(ctx context.Context, key, value string) error
	GetSetting(ctx context.Context, key string) (models.Setting, bool, error)
	Close() error
}
