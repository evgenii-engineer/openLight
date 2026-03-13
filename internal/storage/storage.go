package storage

import (
	"context"

	"openlight/internal/models"
)

type Repository interface {
	SaveMessage(ctx context.Context, message models.Message) error
	ListMessagesByChat(ctx context.Context, chatID int64, limit int) ([]models.Message, error)
	SaveSkillCall(ctx context.Context, call models.SkillCall) error
	AddNote(ctx context.Context, text string) (models.Note, error)
	ListNotes(ctx context.Context, limit int) ([]models.Note, error)
	DeleteNote(ctx context.Context, id int64) error
	SetSetting(ctx context.Context, key, value string) error
	GetSetting(ctx context.Context, key string) (models.Setting, bool, error)
	Close() error
}
