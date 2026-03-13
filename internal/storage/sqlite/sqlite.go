package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"openlight/internal/models"
	"openlight/internal/skills"
	"openlight/migrations"
)

type Repository struct {
	db     *sql.DB
	logger *slog.Logger
}

func New(ctx context.Context, path string, logger *slog.Logger) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	repo := &Repository{
		db:     db,
		logger: logger,
	}

	if err := repo.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := repo.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return repo, nil
}

func (r *Repository) SaveMessage(ctx context.Context, message models.Message) error {
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}

	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO messages (telegram_user_id, telegram_chat_id, role, text, created_at) VALUES (?, ?, ?, ?, ?)`,
		message.TelegramUserID,
		message.TelegramChatID,
		message.Role,
		message.Text,
		message.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	return nil
}

func (r *Repository) ListMessagesByChat(ctx context.Context, chatID int64, limit int) ([]models.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, telegram_user_id, telegram_chat_id, role, text, created_at
		 FROM messages
		 WHERE telegram_chat_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		chatID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var message models.Message
		if err := rows.Scan(
			&message.ID,
			&message.TelegramUserID,
			&message.TelegramChatID,
			&message.Role,
			&message.Text,
			&message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return messages, nil
}

func (r *Repository) SaveSkillCall(ctx context.Context, call models.SkillCall) error {
	if call.CreatedAt.IsZero() {
		call.CreatedAt = time.Now().UTC()
	}

	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO skill_calls (skill_name, input_text, args_json, status, error_text, duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		call.SkillName,
		call.InputText,
		call.ArgsJSON,
		call.Status,
		call.ErrorText,
		call.DurationMS,
		call.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert skill call: %w", err)
	}

	return nil
}

func (r *Repository) AddNote(ctx context.Context, text string) (models.Note, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `INSERT INTO notes (text, created_at) VALUES (?, ?)`, text, now)
	if err != nil {
		return models.Note{}, fmt.Errorf("insert note: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return models.Note{}, fmt.Errorf("fetch inserted note id: %w", err)
	}

	return models.Note{
		ID:        id,
		Text:      text,
		CreatedAt: now,
	}, nil
}

func (r *Repository) ListNotes(ctx context.Context, limit int) ([]models.Note, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, text, created_at FROM notes ORDER BY created_at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	var notes []models.Note
	for rows.Next() {
		var note models.Note
		if err := rows.Scan(&note.ID, &note.Text, &note.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notes: %w", err)
	}

	return notes, nil
}

func (r *Repository) DeleteNote(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete note: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete note rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: note #%d", skills.ErrNotFound, id)
	}

	return nil
}

func (r *Repository) SetSetting(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key,
		value,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert setting: %w", err)
	}
	return nil
}

func (r *Repository) GetSetting(ctx context.Context, key string) (models.Setting, bool, error) {
	var setting models.Setting
	err := r.db.QueryRowContext(
		ctx,
		`SELECT key, value, updated_at FROM settings WHERE key = ?`,
		key,
	).Scan(&setting.Key, &setting.Value, &setting.UpdatedAt)
	if err == sql.ErrNoRows {
		return models.Setting{}, false, nil
	}
	if err != nil {
		return models.Setting{}, false, fmt.Errorf("query setting: %w", err)
	}
	return setting, true, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) configure(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	}

	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite with %q: %w", statement, err)
		}
	}

	return nil
}

func (r *Repository) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		content, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		if _, err := r.db.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
		}

		if r.logger != nil {
			r.logger.Debug("applied sqlite migration", "name", entry.Name())
		}
	}

	return nil
}
