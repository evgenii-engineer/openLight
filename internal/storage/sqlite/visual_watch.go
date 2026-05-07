package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
	"openlight/internal/storage"
)

const visualWatchSelect = `
	SELECT id, telegram_user_id, telegram_chat_id, name, url, keywords,
	       notify_on_change, notify_on_keywords, diff_threshold, interval_seconds,
	       cooldown_seconds, baseline_path, last_screenshot_path,
	       last_changed_fraction, last_keywords_seen, last_checked_at,
	       last_changed_at, last_alerted_at, enabled, created_at, updated_at
	  FROM visual_watches`

func (r *Repository) CreateVisualWatch(ctx context.Context, watch models.VisualWatch) (models.VisualWatch, error) {
	now := time.Now().UTC()
	if watch.CreatedAt.IsZero() {
		watch.CreatedAt = now
	}
	if watch.UpdatedAt.IsZero() {
		watch.UpdatedAt = now
	}
	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO visual_watches (
			telegram_user_id,
			telegram_chat_id,
			name,
			url,
			keywords,
			notify_on_change,
			notify_on_keywords,
			diff_threshold,
			interval_seconds,
			cooldown_seconds,
			baseline_path,
			last_screenshot_path,
			last_changed_fraction,
			last_keywords_seen,
			last_checked_at,
			last_changed_at,
			last_alerted_at,
			enabled,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		watch.TelegramUserID,
		watch.TelegramChatID,
		watch.Name,
		watch.URL,
		joinKeywords(watch.Keywords),
		boolToInt(watch.NotifyOnChange),
		boolToInt(watch.NotifyOnKeywords),
		watch.DiffThreshold,
		int64(watch.Interval/time.Second),
		int64(watch.Cooldown/time.Second),
		watch.BaselinePath,
		watch.LastScreenshotPath,
		watch.LastChangedFraction,
		joinKeywords(watch.LastKeywordsSeen),
		nullableTime(watch.LastCheckedAt),
		nullableTime(watch.LastChangedAt),
		nullableTime(watch.LastAlertedAt),
		boolToInt(watch.Enabled),
		watch.CreatedAt,
		watch.UpdatedAt,
	)
	if err != nil {
		return models.VisualWatch{}, fmt.Errorf("insert visual_watch: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return models.VisualWatch{}, fmt.Errorf("fetch inserted visual_watch id: %w", err)
	}
	watch.ID = id
	return watch, nil
}

func (r *Repository) GetVisualWatch(ctx context.Context, id int64) (models.VisualWatch, bool, error) {
	row := r.db.QueryRowContext(ctx, visualWatchSelect+" WHERE id = ?", id)
	watch, err := scanVisualWatch(row)
	if err == sql.ErrNoRows {
		return models.VisualWatch{}, false, nil
	}
	if err != nil {
		return models.VisualWatch{}, false, fmt.Errorf("query visual_watch: %w", err)
	}
	return watch, true, nil
}

func (r *Repository) ListVisualWatches(ctx context.Context, options storage.VisualWatchListOptions) ([]models.VisualWatch, error) {
	query := visualWatchSelect
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
		return nil, fmt.Errorf("query visual_watches: %w", err)
	}
	defer rows.Close()

	var result []models.VisualWatch
	for rows.Next() {
		watch, scanErr := scanVisualWatch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan visual_watch: %w", scanErr)
		}
		result = append(result, watch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate visual_watches: %w", err)
	}
	return result, nil
}

func (r *Repository) UpdateVisualWatch(ctx context.Context, watch models.VisualWatch) error {
	watch.UpdatedAt = time.Now().UTC()
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE visual_watches
		    SET telegram_user_id = ?,
		        telegram_chat_id = ?,
		        name = ?,
		        url = ?,
		        keywords = ?,
		        notify_on_change = ?,
		        notify_on_keywords = ?,
		        diff_threshold = ?,
		        interval_seconds = ?,
		        cooldown_seconds = ?,
		        baseline_path = ?,
		        last_screenshot_path = ?,
		        last_changed_fraction = ?,
		        last_keywords_seen = ?,
		        last_checked_at = ?,
		        last_changed_at = ?,
		        last_alerted_at = ?,
		        enabled = ?,
		        updated_at = ?
		  WHERE id = ?`,
		watch.TelegramUserID,
		watch.TelegramChatID,
		watch.Name,
		watch.URL,
		joinKeywords(watch.Keywords),
		boolToInt(watch.NotifyOnChange),
		boolToInt(watch.NotifyOnKeywords),
		watch.DiffThreshold,
		int64(watch.Interval/time.Second),
		int64(watch.Cooldown/time.Second),
		watch.BaselinePath,
		watch.LastScreenshotPath,
		watch.LastChangedFraction,
		joinKeywords(watch.LastKeywordsSeen),
		nullableTime(watch.LastCheckedAt),
		nullableTime(watch.LastChangedAt),
		nullableTime(watch.LastAlertedAt),
		boolToInt(watch.Enabled),
		watch.UpdatedAt,
		watch.ID,
	)
	if err != nil {
		return fmt.Errorf("update visual_watch: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update visual_watch rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: visual_watch #%d", skills.ErrNotFound, watch.ID)
	}
	return nil
}

func (r *Repository) DeleteVisualWatch(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM visual_watches WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete visual_watch: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete visual_watch rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: visual_watch #%d", skills.ErrNotFound, id)
	}
	return nil
}

func scanVisualWatch(scanner interface {
	Scan(dest ...any) error
}) (models.VisualWatch, error) {
	var watch models.VisualWatch
	var keywords, lastKeywords string
	var notifyOnChange, notifyOnKeywords, enabled int
	var intervalSeconds, cooldownSeconds int64
	var lastCheckedAt, lastChangedAt, lastAlertedAt sql.NullTime

	if err := scanner.Scan(
		&watch.ID,
		&watch.TelegramUserID,
		&watch.TelegramChatID,
		&watch.Name,
		&watch.URL,
		&keywords,
		&notifyOnChange,
		&notifyOnKeywords,
		&watch.DiffThreshold,
		&intervalSeconds,
		&cooldownSeconds,
		&watch.BaselinePath,
		&watch.LastScreenshotPath,
		&watch.LastChangedFraction,
		&lastKeywords,
		&lastCheckedAt,
		&lastChangedAt,
		&lastAlertedAt,
		&enabled,
		&watch.CreatedAt,
		&watch.UpdatedAt,
	); err != nil {
		return models.VisualWatch{}, err
	}

	watch.Keywords = splitKeywords(keywords)
	watch.LastKeywordsSeen = splitKeywords(lastKeywords)
	watch.NotifyOnChange = notifyOnChange != 0
	watch.NotifyOnKeywords = notifyOnKeywords != 0
	watch.Enabled = enabled != 0
	watch.Interval = time.Duration(intervalSeconds) * time.Second
	watch.Cooldown = time.Duration(cooldownSeconds) * time.Second
	watch.LastCheckedAt = nullTime(lastCheckedAt)
	watch.LastChangedAt = nullTime(lastChangedAt)
	watch.LastAlertedAt = nullTime(lastAlertedAt)
	return watch, nil
}

func joinKeywords(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	return strings.Join(cleaned, "\n")
}

func splitKeywords(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "\n")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}
	return result
}
