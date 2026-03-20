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
	"openlight/internal/storage"
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

func (r *Repository) CreateWatch(ctx context.Context, watch models.Watch) (models.Watch, error) {
	now := time.Now().UTC()
	if watch.CreatedAt.IsZero() {
		watch.CreatedAt = now
	}
	if watch.UpdatedAt.IsZero() {
		watch.UpdatedAt = now
	}
	if watch.IncidentState == "" {
		watch.IncidentState = models.WatchIncidentStateClear
	}

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO watches (
			telegram_user_id,
			telegram_chat_id,
			name,
			kind,
			target,
			threshold,
			duration_seconds,
			reaction_mode,
			action_type,
			cooldown_seconds,
			enabled,
			incident_state,
			condition_since,
			last_triggered_at,
			last_checked_at,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		watch.TelegramUserID,
		watch.TelegramChatID,
		watch.Name,
		watch.Kind,
		watch.Target,
		watch.Threshold,
		int64(watch.Duration/time.Second),
		watch.ReactionMode,
		watch.ActionType,
		int64(watch.Cooldown/time.Second),
		boolToInt(watch.Enabled),
		watch.IncidentState,
		nullableTime(watch.ConditionSince),
		nullableTime(watch.LastTriggeredAt),
		nullableTime(watch.LastCheckedAt),
		watch.CreatedAt,
		watch.UpdatedAt,
	)
	if err != nil {
		return models.Watch{}, fmt.Errorf("insert watch: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return models.Watch{}, fmt.Errorf("fetch inserted watch id: %w", err)
	}

	watch.ID = id
	return watch, nil
}

func (r *Repository) ListWatches(ctx context.Context, options storage.WatchListOptions) ([]models.Watch, error) {
	query := `
		SELECT id, telegram_user_id, telegram_chat_id, name, kind, target, threshold,
		       duration_seconds, reaction_mode, action_type, cooldown_seconds, enabled,
		       incident_state, condition_since, last_triggered_at, last_checked_at,
		       created_at, updated_at
		FROM watches`

	args := make([]any, 0, 2)
	clauses := make([]string, 0, 2)
	if options.ChatID != 0 {
		clauses = append(clauses, "telegram_chat_id = ?")
		args = append(args, options.ChatID)
	}
	if options.EnabledOnly {
		clauses = append(clauses, "enabled = 1")
	}
	if len(clauses) > 0 {
		query += " WHERE " + joinClauses(clauses)
	}
	query += " ORDER BY telegram_chat_id ASC, id ASC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query watches: %w", err)
	}
	defer rows.Close()

	var watches []models.Watch
	for rows.Next() {
		watch, scanErr := scanWatch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan watch: %w", scanErr)
		}
		watches = append(watches, watch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate watches: %w", err)
	}

	return watches, nil
}

func (r *Repository) GetWatch(ctx context.Context, id int64) (models.Watch, bool, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT id, telegram_user_id, telegram_chat_id, name, kind, target, threshold,
		        duration_seconds, reaction_mode, action_type, cooldown_seconds, enabled,
		        incident_state, condition_since, last_triggered_at, last_checked_at,
		        created_at, updated_at
		   FROM watches
		  WHERE id = ?`,
		id,
	)
	watch, err := scanWatchRow(row)
	if err == sql.ErrNoRows {
		return models.Watch{}, false, nil
	}
	if err != nil {
		return models.Watch{}, false, fmt.Errorf("query watch: %w", err)
	}
	return watch, true, nil
}

func (r *Repository) UpdateWatch(ctx context.Context, watch models.Watch) error {
	watch.UpdatedAt = time.Now().UTC()
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE watches
		    SET telegram_user_id = ?,
		        telegram_chat_id = ?,
		        name = ?,
		        kind = ?,
		        target = ?,
		        threshold = ?,
		        duration_seconds = ?,
		        reaction_mode = ?,
		        action_type = ?,
		        cooldown_seconds = ?,
		        enabled = ?,
		        incident_state = ?,
		        condition_since = ?,
		        last_triggered_at = ?,
		        last_checked_at = ?,
		        updated_at = ?
		  WHERE id = ?`,
		watch.TelegramUserID,
		watch.TelegramChatID,
		watch.Name,
		watch.Kind,
		watch.Target,
		watch.Threshold,
		int64(watch.Duration/time.Second),
		watch.ReactionMode,
		watch.ActionType,
		int64(watch.Cooldown/time.Second),
		boolToInt(watch.Enabled),
		watch.IncidentState,
		nullableTime(watch.ConditionSince),
		nullableTime(watch.LastTriggeredAt),
		nullableTime(watch.LastCheckedAt),
		watch.UpdatedAt,
		watch.ID,
	)
	if err != nil {
		return fmt.Errorf("update watch: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update watch rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: watch #%d", skills.ErrNotFound, watch.ID)
	}

	return nil
}

func (r *Repository) DeleteWatch(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM watches WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete watch: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete watch rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: watch #%d", skills.ErrNotFound, id)
	}

	return nil
}

func (r *Repository) CreateWatchIncident(ctx context.Context, incident models.WatchIncident) (models.WatchIncident, error) {
	now := time.Now().UTC()
	if incident.OpenedAt.IsZero() {
		incident.OpenedAt = now
	}
	if incident.CreatedAt.IsZero() {
		incident.CreatedAt = now
	}
	if incident.UpdatedAt.IsZero() {
		incident.UpdatedAt = now
	}
	if incident.Status == "" {
		incident.Status = models.WatchIncidentStatusOpen
	}
	if incident.ActionStatus == "" {
		incident.ActionStatus = models.WatchActionStatusNone
	}

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO watch_incidents (
			watch_id,
			telegram_chat_id,
			summary,
			details,
			status,
			reaction_mode,
			action_type,
			action_status,
			action_prompt,
			action_requested_at,
			action_expires_at,
			action_completed_at,
			report,
			opened_at,
			resolved_at,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		incident.WatchID,
		incident.TelegramChatID,
		incident.Summary,
		incident.Details,
		incident.Status,
		incident.ReactionMode,
		incident.ActionType,
		incident.ActionStatus,
		incident.ActionPrompt,
		nullableTime(incident.ActionRequestedAt),
		nullableTime(incident.ActionExpiresAt),
		nullableTime(incident.ActionCompletedAt),
		incident.Report,
		incident.OpenedAt,
		nullableTime(incident.ResolvedAt),
		incident.CreatedAt,
		incident.UpdatedAt,
	)
	if err != nil {
		return models.WatchIncident{}, fmt.Errorf("insert watch incident: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return models.WatchIncident{}, fmt.Errorf("fetch inserted watch incident id: %w", err)
	}

	incident.ID = id
	return incident, nil
}

func (r *Repository) GetWatchIncident(ctx context.Context, id int64) (models.WatchIncident, bool, error) {
	row := r.db.QueryRowContext(ctx, watchIncidentSelectBase+` WHERE wi.id = ?`, id)
	incident, err := scanWatchIncidentRow(row)
	if err == sql.ErrNoRows {
		return models.WatchIncident{}, false, nil
	}
	if err != nil {
		return models.WatchIncident{}, false, fmt.Errorf("query watch incident: %w", err)
	}
	return incident, true, nil
}

func (r *Repository) GetOpenWatchIncident(ctx context.Context, watchID int64) (models.WatchIncident, bool, error) {
	row := r.db.QueryRowContext(
		ctx,
		watchIncidentSelectBase+` WHERE wi.watch_id = ? AND wi.status = ? ORDER BY wi.opened_at DESC, wi.id DESC LIMIT 1`,
		watchID,
		models.WatchIncidentStatusOpen,
	)
	incident, err := scanWatchIncidentRow(row)
	if err == sql.ErrNoRows {
		return models.WatchIncident{}, false, nil
	}
	if err != nil {
		return models.WatchIncident{}, false, fmt.Errorf("query open watch incident: %w", err)
	}
	return incident, true, nil
}

func (r *Repository) ListWatchIncidents(ctx context.Context, options storage.WatchIncidentListOptions) ([]models.WatchIncident, error) {
	query := watchIncidentSelectBase
	args := make([]any, 0, 3)
	clauses := make([]string, 0, 2)
	if options.ChatID != 0 {
		clauses = append(clauses, "wi.telegram_chat_id = ?")
		args = append(args, options.ChatID)
	}
	if options.WatchID != 0 {
		clauses = append(clauses, "wi.watch_id = ?")
		args = append(args, options.WatchID)
	}
	if len(clauses) > 0 {
		query += " WHERE " + joinClauses(clauses)
	}
	query += " ORDER BY wi.opened_at DESC, wi.id DESC"
	if options.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, options.Limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query watch incidents: %w", err)
	}
	defer rows.Close()

	var incidents []models.WatchIncident
	for rows.Next() {
		incident, scanErr := scanWatchIncident(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan watch incident: %w", scanErr)
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate watch incidents: %w", err)
	}

	return incidents, nil
}

func (r *Repository) ListPendingWatchIncidents(ctx context.Context, chatID int64, now time.Time) ([]models.WatchIncident, error) {
	rows, err := r.db.QueryContext(
		ctx,
		watchIncidentSelectBase+`
		 WHERE wi.telegram_chat_id = ?
		   AND wi.status = ?
		   AND wi.action_status = ?
		   AND wi.action_expires_at IS NOT NULL
		   AND wi.action_expires_at >= ?
		 ORDER BY wi.opened_at DESC, wi.id DESC`,
		chatID,
		models.WatchIncidentStatusOpen,
		models.WatchActionStatusPending,
		now.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("query pending watch incidents: %w", err)
	}
	defer rows.Close()

	var incidents []models.WatchIncident
	for rows.Next() {
		incident, scanErr := scanWatchIncident(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan pending watch incident: %w", scanErr)
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending watch incidents: %w", err)
	}

	return incidents, nil
}

func (r *Repository) ListExpiredPendingWatchIncidents(ctx context.Context, now time.Time) ([]models.WatchIncident, error) {
	rows, err := r.db.QueryContext(
		ctx,
		watchIncidentSelectBase+`
		 WHERE wi.status = ?
		   AND wi.action_status = ?
		   AND wi.action_expires_at IS NOT NULL
		   AND wi.action_expires_at < ?
		 ORDER BY wi.opened_at ASC, wi.id ASC`,
		models.WatchIncidentStatusOpen,
		models.WatchActionStatusPending,
		now.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("query expired watch incidents: %w", err)
	}
	defer rows.Close()

	var incidents []models.WatchIncident
	for rows.Next() {
		incident, scanErr := scanWatchIncident(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan expired watch incident: %w", scanErr)
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired watch incidents: %w", err)
	}

	return incidents, nil
}

func (r *Repository) UpdateWatchIncident(ctx context.Context, incident models.WatchIncident) error {
	incident.UpdatedAt = time.Now().UTC()
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE watch_incidents
		    SET summary = ?,
		        details = ?,
		        status = ?,
		        reaction_mode = ?,
		        action_type = ?,
		        action_status = ?,
		        action_prompt = ?,
		        action_requested_at = ?,
		        action_expires_at = ?,
		        action_completed_at = ?,
		        report = ?,
		        opened_at = ?,
		        resolved_at = ?,
		        updated_at = ?
		  WHERE id = ?`,
		incident.Summary,
		incident.Details,
		incident.Status,
		incident.ReactionMode,
		incident.ActionType,
		incident.ActionStatus,
		incident.ActionPrompt,
		nullableTime(incident.ActionRequestedAt),
		nullableTime(incident.ActionExpiresAt),
		nullableTime(incident.ActionCompletedAt),
		incident.Report,
		incident.OpenedAt,
		nullableTime(incident.ResolvedAt),
		incident.UpdatedAt,
		incident.ID,
	)
	if err != nil {
		return fmt.Errorf("update watch incident: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update watch incident rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: watch incident #%d", skills.ErrNotFound, incident.ID)
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

const watchIncidentSelectBase = `
	SELECT wi.id, wi.watch_id, w.name, wi.telegram_chat_id, wi.summary, wi.details,
	       wi.status, wi.reaction_mode, wi.action_type, wi.action_status,
	       wi.action_prompt, wi.action_requested_at, wi.action_expires_at,
	       wi.action_completed_at, wi.report, wi.opened_at, wi.resolved_at,
	       wi.created_at, wi.updated_at
	  FROM watch_incidents wi
	  JOIN watches w ON w.id = wi.watch_id`

func scanWatch(scanner interface {
	Scan(dest ...any) error
}) (models.Watch, error) {
	var watch models.Watch
	var durationSeconds int64
	var cooldownSeconds int64
	var enabled int
	var conditionSince sql.NullTime
	var lastTriggeredAt sql.NullTime
	var lastCheckedAt sql.NullTime

	err := scanner.Scan(
		&watch.ID,
		&watch.TelegramUserID,
		&watch.TelegramChatID,
		&watch.Name,
		&watch.Kind,
		&watch.Target,
		&watch.Threshold,
		&durationSeconds,
		&watch.ReactionMode,
		&watch.ActionType,
		&cooldownSeconds,
		&enabled,
		&watch.IncidentState,
		&conditionSince,
		&lastTriggeredAt,
		&lastCheckedAt,
		&watch.CreatedAt,
		&watch.UpdatedAt,
	)
	if err != nil {
		return models.Watch{}, err
	}

	watch.Duration = time.Duration(durationSeconds) * time.Second
	watch.Cooldown = time.Duration(cooldownSeconds) * time.Second
	watch.Enabled = enabled != 0
	watch.ConditionSince = nullTime(conditionSince)
	watch.LastTriggeredAt = nullTime(lastTriggeredAt)
	watch.LastCheckedAt = nullTime(lastCheckedAt)

	return watch, nil
}

func scanWatchRow(row *sql.Row) (models.Watch, error) {
	return scanWatch(row)
}

func scanWatchIncident(scanner interface {
	Scan(dest ...any) error
}) (models.WatchIncident, error) {
	var incident models.WatchIncident
	var actionRequestedAt sql.NullTime
	var actionExpiresAt sql.NullTime
	var actionCompletedAt sql.NullTime
	var resolvedAt sql.NullTime

	err := scanner.Scan(
		&incident.ID,
		&incident.WatchID,
		&incident.WatchName,
		&incident.TelegramChatID,
		&incident.Summary,
		&incident.Details,
		&incident.Status,
		&incident.ReactionMode,
		&incident.ActionType,
		&incident.ActionStatus,
		&incident.ActionPrompt,
		&actionRequestedAt,
		&actionExpiresAt,
		&actionCompletedAt,
		&incident.Report,
		&incident.OpenedAt,
		&resolvedAt,
		&incident.CreatedAt,
		&incident.UpdatedAt,
	)
	if err != nil {
		return models.WatchIncident{}, err
	}

	incident.ActionRequestedAt = nullTime(actionRequestedAt)
	incident.ActionExpiresAt = nullTime(actionExpiresAt)
	incident.ActionCompletedAt = nullTime(actionCompletedAt)
	incident.ResolvedAt = nullTime(resolvedAt)
	return incident, nil
}

func scanWatchIncidentRow(row *sql.Row) (models.WatchIncident, error) {
	return scanWatchIncident(row)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func joinClauses(values []string) string {
	if len(values) == 0 {
		return ""
	}

	result := values[0]
	for idx := 1; idx < len(values); idx++ {
		result += " AND " + values[idx]
	}
	return result
}
