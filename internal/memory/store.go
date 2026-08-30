package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"openlight/internal/memory/migrations"
)

// ErrNotFound is returned by the store when a lookup by id misses.
var ErrNotFound = errors.New("memory: not found")

// Store is the SQLite persistence layer for the memory subsystem:
// source metadata, chunk text, the ingestion queue, structured facts,
// and conversation episodes. It owns its own database file (separate
// from agent.db) and applies its own migrations.
type Store struct {
	db   *sql.DB
	path string
}

// OpenStore opens (creating if needed) the memory database at path and
// applies the embedded migrations.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create memory sqlite dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open memory sqlite: %w", err)
	}

	// Single connection, same as the agent repository: modernc/sqlite is
	// happiest serialised, and the ingestion workers are bounded anyway.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &Store{db: db, path: path}
	if err := store.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Path returns the database file location (used by the status report).
func (s *Store) Path() string { return s.path }

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure memory sqlite with %q: %w", statement, err)
		}
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read memory migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		content, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return fmt.Errorf("read memory migration %s: %w", entry.Name(), err)
		}
		if _, err := s.db.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("execute memory migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// --- sources -------------------------------------------------------------

// InsertSource persists a new source row. When a row with the same
// content hash already exists, the existing row is returned with
// duplicate=true and nothing is written — this is the dedup guarantee
// that keeps re-sent files from being embedded twice.
func (s *Store) InsertSource(ctx context.Context, source Source) (Source, bool, error) {
	if existing, err := s.SourceByHash(ctx, source.Hash); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Source{}, false, err
	}

	if source.CreatedAt.IsZero() {
		source.CreatedAt = time.Now().UTC()
	}
	source.UpdatedAt = source.CreatedAt

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO memory_sources
		   (id, type, source, external_id, title, mime_type, raw_path, hash, bytes,
		    status, chat_id, user_id, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		source.ID, source.Type, source.Source, source.ExternalID, source.Title,
		source.MIMEType, source.RawPath, source.Hash, source.Bytes, source.Status,
		source.ChatID, source.UserID, marshalMetadata(source.Metadata),
		source.CreatedAt, source.UpdatedAt,
	)
	if err != nil {
		// A concurrent insert of the same hash loses the race; treat it
		// as the duplicate it is rather than failing the ingestion.
		if existing, lookupErr := s.SourceByHash(ctx, source.Hash); lookupErr == nil {
			return existing, true, nil
		}
		return Source{}, false, fmt.Errorf("insert memory source: %w", err)
	}
	return source, false, nil
}

const sourceColumns = `id, type, source, external_id, title, mime_type, raw_path, hash,
	bytes, status, chat_id, user_id, metadata, created_at, updated_at`

func (s *Store) SourceByHash(ctx context.Context, hash string) (Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM memory_sources WHERE hash = ?`, hash)
	return scanSource(row)
}

func (s *Store) Source(ctx context.Context, id string) (Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM memory_sources WHERE id = ?`, id)
	return scanSource(row)
}

// ListSources returns sources newest-first, optionally filtered by status.
func (s *Store) ListSources(ctx context.Context, status string, limit int) ([]Source, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + sourceColumns + ` FROM memory_sources`
	args := []any{}
	if strings.TrimSpace(status) != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memory sources: %w", err)
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *Store) SetSourceStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_sources SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("update memory source status: %w", err)
	}
	return nil
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_sources WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete memory source: %w", err)
	}
	return nil
}

// --- chunks --------------------------------------------------------------

// ReplaceChunks swaps a source's chunk set atomically. Reindexing a
// source therefore never leaves a half-written mix of old and new
// chunks visible to a concurrent search.
func (s *Store) ReplaceChunks(ctx context.Context, sourceID string, chunks []Chunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chunk replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_chunks WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("clear memory chunks: %w", err)
	}
	now := time.Now().UTC()
	for _, chunk := range chunks {
		if chunk.CreatedAt.IsZero() {
			chunk.CreatedAt = now
		}
		var indexedAt any
		if !chunk.IndexedAt.IsZero() {
			indexedAt = chunk.IndexedAt
		}
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO memory_chunks (id, source_id, ordinal, text, tokens, heading, metadata, indexed_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chunk.ID, sourceID, chunk.Ordinal, chunk.Text, chunk.Tokens,
			chunk.Heading, marshalMetadata(chunk.Metadata), indexedAt, chunk.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert memory chunk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk replace: %w", err)
	}
	return nil
}

func (s *Store) ChunksBySource(ctx context.Context, sourceID string) ([]Chunk, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, source_id, ordinal, text, tokens, heading, metadata, indexed_at, created_at
		   FROM memory_chunks WHERE source_id = ? ORDER BY ordinal`,
		sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("query memory chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// ChunksByIDs loads chunks in bulk for a vector search result set.
func (s *Store) ChunksByIDs(ctx context.Context, ids []string) (map[string]Chunk, error) {
	if len(ids) == 0 {
		return map[string]Chunk{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, source_id, ordinal, text, tokens, heading, metadata, indexed_at, created_at
		   FROM memory_chunks WHERE id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query memory chunks by id: %w", err)
	}
	defer rows.Close()

	chunks, err := scanChunks(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Chunk, len(chunks))
	for _, chunk := range chunks {
		out[chunk.ID] = chunk
	}
	return out, nil
}

// --- jobs ----------------------------------------------------------------

// EnqueueJob adds work to the persistent queue. When an active job of
// the same kind already exists for the source the call is a no-op, so
// callers can enqueue defensively without creating duplicates.
func (s *Store) EnqueueJob(ctx context.Context, sourceID, kind string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO memory_jobs (source_id, kind, status, attempts, last_error, next_retry_at, created_at, updated_at)
		 VALUES (?, ?, 'pending', 0, '', ?, ?, ?)`,
		sourceID, kind, now, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return fmt.Errorf("enqueue memory job: %w", err)
	}
	return nil
}

// ClaimJob atomically moves the oldest due pending job to running and
// returns it. Returns ErrNotFound when the queue has nothing due.
func (s *Store) ClaimJob(ctx context.Context, now time.Time) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(
		ctx,
		`SELECT id, source_id, kind, status, attempts, last_error, next_retry_at, created_at, updated_at
		   FROM memory_jobs
		  WHERE status = 'pending' AND next_retry_at <= ?
		  ORDER BY next_retry_at, id
		  LIMIT 1`,
		now,
	)
	job, err := scanJob(row)
	if err != nil {
		return Job{}, err
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE memory_jobs SET status = 'running', updated_at = ? WHERE id = ?`,
		now, job.ID,
	); err != nil {
		return Job{}, fmt.Errorf("claim memory job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("commit claim: %w", err)
	}
	job.Status = JobRunning
	return job, nil
}

// CompleteJob removes a finished job from the queue. Completed work is
// deleted rather than archived: the source row already carries the
// outcome, and an unbounded jobs table is a liability on a Pi.
func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("complete memory job: %w", err)
	}
	return nil
}

// RescheduleJob records a failed attempt and sets the next retry time.
// The job stays pending forever: a permanently offline Mac mini must not
// cause data loss, and `memory retry` / `memory pending` make the
// backlog visible.
func (s *Store) RescheduleJob(ctx context.Context, id int64, attempts int, lastError string, nextRetryAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_jobs SET status = 'pending', attempts = ?, last_error = ?, next_retry_at = ?, updated_at = ? WHERE id = ?`,
		attempts, truncate(lastError, 500), nextRetryAt, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("reschedule memory job: %w", err)
	}
	return nil
}

// FailJob parks a job that can never succeed (unsupported format,
// missing RAW file). It stays queryable through `memory pending` but is
// no longer retried automatically.
func (s *Store) FailJob(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_jobs SET status = 'failed', last_error = ?, updated_at = ? WHERE id = ?`,
		truncate(reason, 500), time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("fail memory job: %w", err)
	}
	return nil
}

// ReclaimRunningJobs returns jobs abandoned by a previous process to the
// pending state. Called once at startup — this is what makes ingestion
// survive a restart mid-job.
func (s *Store) ReclaimRunningJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_jobs SET status = 'pending', updated_at = ? WHERE status = 'running'`,
		time.Now().UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("reclaim memory jobs: %w", err)
	}
	count, _ := res.RowsAffected()
	return count, nil
}

// RetryFailedJobs moves parked jobs back into the queue, due immediately.
func (s *Store) RetryFailedJobs(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_jobs SET status = 'pending', attempts = 0, next_retry_at = ?, updated_at = ? WHERE status = 'failed'`,
		now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("retry memory jobs: %w", err)
	}
	count, _ := res.RowsAffected()
	return count, nil
}

// ListJobs returns queued work newest-first for the CLI.
func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, source_id, kind, status, attempts, last_error, next_retry_at, created_at, updated_at
		   FROM memory_jobs ORDER BY status, next_retry_at, id LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list memory jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// --- facts ---------------------------------------------------------------

// CurrentFact returns the live fact for a subject/predicate pair.
func (s *Store) CurrentFact(ctx context.Context, subject, predicate string) (Fact, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, subject, predicate, value, category, confidence, source_id,
		        valid_from, valid_to, superseded_by, created_at, updated_at
		   FROM memory_facts
		  WHERE subject = ? AND predicate = ? AND valid_to IS NULL
		  ORDER BY valid_from DESC LIMIT 1`,
		subject, predicate,
	)
	return scanFact(row)
}

// InsertFact writes a new fact row.
func (s *Store) InsertFact(ctx context.Context, fact Fact) error {
	var validTo any
	if !fact.ValidTo.IsZero() {
		validTo = fact.ValidTo
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO memory_facts
		   (id, subject, predicate, value, category, confidence, source_id, valid_from, valid_to, superseded_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fact.ID, fact.Subject, fact.Predicate, fact.Value, fact.Category, fact.Confidence,
		fact.SourceID, fact.ValidFrom, validTo, fact.SupersededBy, fact.CreatedAt, fact.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert memory fact: %w", err)
	}
	return nil
}

// SupersedeFact closes an existing fact's validity window and links it to
// its replacement. The old row is kept: history is the point.
func (s *Store) SupersedeFact(ctx context.Context, id string, validTo time.Time, supersededBy string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_facts SET valid_to = ?, superseded_by = ?, updated_at = ? WHERE id = ?`,
		validTo, supersededBy, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("supersede memory fact: %w", err)
	}
	return nil
}

// SearchFacts does a LIKE scan over current facts. Structured memory is
// a small table (hundreds of rows, not millions) and it is a secondary
// signal next to vector search, so a scan is the right trade here.
func (s *Store) SearchFacts(ctx context.Context, terms []string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `SELECT id, subject, predicate, value, category, confidence, source_id,
	                 valid_from, valid_to, superseded_by, created_at, updated_at
	            FROM memory_facts WHERE valid_to IS NULL`
	args := []any{}
	if len(terms) > 0 {
		clauses := make([]string, 0, len(terms))
		for _, term := range terms {
			clauses = append(clauses, `(subject LIKE ? OR predicate LIKE ? OR value LIKE ?)`)
			pattern := "%" + term + "%"
			args = append(args, pattern, pattern, pattern)
		}
		query += ` AND (` + strings.Join(clauses, " OR ") + `)`
	}
	query += ` ORDER BY confidence DESC, valid_from DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory facts: %w", err)
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		fact, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

// ListFacts returns the most recently updated current facts.
func (s *Store) ListFacts(ctx context.Context, limit int) ([]Fact, error) {
	return s.SearchFacts(ctx, nil, limit)
}

// ForgetFact closes a fact by id without a replacement.
func (s *Store) ForgetFact(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_facts SET valid_to = ?, updated_at = ? WHERE id = ? AND valid_to IS NULL`,
		time.Now().UTC(), time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("forget memory fact: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- episodes ------------------------------------------------------------

// OpenEpisode returns the chat's open episode, creating one when absent.
func (s *Store) OpenEpisode(ctx context.Context, chatID int64, id string, now time.Time) (Episode, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, chat_id, status, turn_count, started_at, last_activity_at, closed_at, summary_source_id
		   FROM memory_episodes WHERE chat_id = ? AND status = 'open' ORDER BY started_at DESC LIMIT 1`,
		chatID,
	)
	episode, err := scanEpisode(row)
	if err == nil {
		return episode, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Episode{}, err
	}

	episode = Episode{
		ID:             id,
		ChatID:         chatID,
		Status:         EpisodeOpen,
		StartedAt:      now,
		LastActivityAt: now,
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO memory_episodes (id, chat_id, status, turn_count, started_at, last_activity_at)
		 VALUES (?, ?, 'open', 0, ?, ?)`,
		episode.ID, chatID, now, now,
	)
	if err != nil {
		return Episode{}, fmt.Errorf("create memory episode: %w", err)
	}
	return episode, nil
}

// AppendTurn records one conversation turn and bumps the episode's
// activity clock.
func (s *Store) AppendTurn(ctx context.Context, episodeID, role, text string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO memory_episode_turns (episode_id, role, text, created_at) VALUES (?, ?, ?, ?)`,
		episodeID, role, text, now,
	); err != nil {
		return fmt.Errorf("insert episode turn: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE memory_episodes SET turn_count = turn_count + 1, last_activity_at = ? WHERE id = ?`,
		now, episodeID,
	); err != nil {
		return fmt.Errorf("update episode activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit append turn: %w", err)
	}
	return nil
}

// ListIdleEpisodes returns open episodes with no activity since cutoff.
func (s *Store) ListIdleEpisodes(ctx context.Context, cutoff time.Time, minTurns, limit int) ([]Episode, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, chat_id, status, turn_count, started_at, last_activity_at, closed_at, summary_source_id
		   FROM memory_episodes
		  WHERE status = 'open' AND last_activity_at <= ? AND turn_count >= ?
		  ORDER BY last_activity_at LIMIT ?`,
		cutoff, minTurns, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list idle episodes: %w", err)
	}
	defer rows.Close()

	var episodes []Episode
	for rows.Next() {
		episode, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, episode)
	}
	return episodes, rows.Err()
}

// EpisodeTurns loads an episode's raw conversation in order.
func (s *Store) EpisodeTurns(ctx context.Context, episodeID string, limit int) ([]Turn, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, episode_id, role, text, created_at FROM memory_episode_turns
		  WHERE episode_id = ? ORDER BY id LIMIT ?`,
		episodeID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query episode turns: %w", err)
	}
	defer rows.Close()

	var turns []Turn
	for rows.Next() {
		var turn Turn
		if err := rows.Scan(&turn.ID, &turn.EpisodeID, &turn.Role, &turn.Text, &turn.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan episode turn: %w", err)
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}

// CloseEpisode marks an episode finished and, once summarised, links the
// summary source that represents it in RAG.
func (s *Store) CloseEpisode(ctx context.Context, episodeID, status, summarySourceID string, now time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memory_episodes SET status = ?, closed_at = ?, summary_source_id = ? WHERE id = ?`,
		status, now, summarySourceID, episodeID,
	)
	if err != nil {
		return fmt.Errorf("close memory episode: %w", err)
	}
	return nil
}

// --- stats ---------------------------------------------------------------

// Counts aggregates the numbers shown by /status and `memory status`.
// One query per table, all cheap index-only counts.
func (s *Store) Counts(ctx context.Context) (sources, chunks, facts, episodes, pending, failed int64, err error) {
	scalars := []struct {
		query string
		out   *int64
	}{
		{`SELECT COUNT(*) FROM memory_sources`, &sources},
		{`SELECT COUNT(*) FROM memory_chunks`, &chunks},
		{`SELECT COUNT(*) FROM memory_facts WHERE valid_to IS NULL`, &facts},
		{`SELECT COUNT(*) FROM memory_episodes`, &episodes},
		{`SELECT COUNT(*) FROM memory_jobs WHERE status IN ('pending','running')`, &pending},
		{`SELECT COUNT(*) FROM memory_jobs WHERE status = 'failed'`, &failed},
	}
	for _, scalar := range scalars {
		if qErr := s.db.QueryRowContext(ctx, scalar.query).Scan(scalar.out); qErr != nil {
			return 0, 0, 0, 0, 0, 0, fmt.Errorf("memory counts: %w", qErr)
		}
	}
	return sources, chunks, facts, episodes, pending, failed, nil
}

// LastIngestAt reports when a source was last completed.
func (s *Store) LastIngestAt(ctx context.Context) (time.Time, error) {
	var at sql.NullTime
	err := s.db.QueryRowContext(
		ctx,
		`SELECT MAX(updated_at) FROM memory_sources WHERE status = 'completed'`,
	).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("last ingest at: %w", err)
	}
	if !at.Valid {
		return time.Time{}, nil
	}
	return at.Time, nil
}

// --- scanning helpers ----------------------------------------------------

type scanner interface{ Scan(dest ...any) error }

func scanSource(row scanner) (Source, error) {
	var (
		source   Source
		metadata string
	)
	err := row.Scan(
		&source.ID, &source.Type, &source.Source, &source.ExternalID, &source.Title,
		&source.MIMEType, &source.RawPath, &source.Hash, &source.Bytes, &source.Status,
		&source.ChatID, &source.UserID, &metadata, &source.CreatedAt, &source.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("scan memory source: %w", err)
	}
	source.Metadata = unmarshalMetadata(metadata)
	return source, nil
}

func scanChunks(rows *sql.Rows) ([]Chunk, error) {
	var chunks []Chunk
	for rows.Next() {
		var (
			chunk     Chunk
			metadata  string
			indexedAt sql.NullTime
		)
		if err := rows.Scan(
			&chunk.ID, &chunk.SourceID, &chunk.Ordinal, &chunk.Text, &chunk.Tokens,
			&chunk.Heading, &metadata, &indexedAt, &chunk.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan memory chunk: %w", err)
		}
		chunk.Metadata = unmarshalMetadata(metadata)
		if indexedAt.Valid {
			chunk.IndexedAt = indexedAt.Time
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func scanJob(row scanner) (Job, error) {
	var job Job
	err := row.Scan(
		&job.ID, &job.SourceID, &job.Kind, &job.Status, &job.Attempts,
		&job.LastError, &job.NextRetryAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("scan memory job: %w", err)
	}
	return job, nil
}

func scanFact(row scanner) (Fact, error) {
	var (
		fact    Fact
		validTo sql.NullTime
	)
	err := row.Scan(
		&fact.ID, &fact.Subject, &fact.Predicate, &fact.Value, &fact.Category,
		&fact.Confidence, &fact.SourceID, &fact.ValidFrom, &validTo,
		&fact.SupersededBy, &fact.CreatedAt, &fact.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	if err != nil {
		return Fact{}, fmt.Errorf("scan memory fact: %w", err)
	}
	if validTo.Valid {
		fact.ValidTo = validTo.Time
	}
	return fact, nil
}

func scanEpisode(row scanner) (Episode, error) {
	var (
		episode  Episode
		closedAt sql.NullTime
	)
	err := row.Scan(
		&episode.ID, &episode.ChatID, &episode.Status, &episode.TurnCount,
		&episode.StartedAt, &episode.LastActivityAt, &closedAt, &episode.SummarySourceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, ErrNotFound
	}
	if err != nil {
		return Episode{}, fmt.Errorf("scan memory episode: %w", err)
	}
	if closedAt.Valid {
		episode.ClosedAt = closedAt.Time
	}
	return episode, nil
}

func marshalMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return "{}"
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func unmarshalMetadata(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}
	}
	metadata := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return map[string]string{}
	}
	return metadata
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint")
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}
