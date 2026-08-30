package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"openlight/internal/memory/vectorstore"
)

// The write path: archiving an item, the persistent queue, and the
// worker that turns a source into indexed chunks.

// Ingest archives an item and queues it for indexing. It returns as soon
// as the bytes are safely on the SSD and the job row is committed —
// nothing here waits on an embedding, so the agent's reply path is not
// coupled to the Mac mini's availability.
func (s *Service) Ingest(ctx context.Context, item Item) (Source, error) {
	// A nil service is the "memory disabled" case. Every entry point
	// tolerates it so callers never have to guard, and turning the
	// subsystem off really does restore the previous behaviour.
	if s == nil {
		return Source{}, nil
	}
	if item.Type == "" {
		item.Type = TypeDocument
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}

	stored, err := s.deps.Raw.Put(item)
	if err != nil {
		s.recordError(err)
		s.logger.Warn("memory.ingest.failed", "stage", "raw", "type", item.Type, "error", err)
		return Source{}, err
	}

	source := Source{
		ID:         newID(),
		Type:       sourceTypeDir(item.Type),
		Source:     item.Source,
		ExternalID: item.ExternalID,
		Title:      item.Title,
		MIMEType:   stored.MIME,
		RawPath:    stored.Path,
		Hash:       stored.Hash,
		Bytes:      stored.Bytes,
		Status:     StatusPending,
		ChatID:     item.ChatID,
		UserID:     item.UserID,
		Metadata:   item.Metadata,
		CreatedAt:  item.CreatedAt.UTC(),
	}

	persisted, duplicate, err := s.deps.Store.InsertSource(ctx, source)
	if err != nil {
		s.recordError(err)
		s.logger.Warn("memory.ingest.failed", "stage", "metadata", "type", source.Type, "error", err)
		return Source{}, err
	}

	if duplicate {
		// Same bytes already archived. Re-index only if the previous
		// attempt never completed, so a re-sent file is free.
		s.logger.Debug("memory.ingest.duplicate",
			"source_id", persisted.ID, "type", persisted.Type, "status", persisted.Status)
		if persisted.Status == StatusCompleted {
			return persisted, nil
		}
	}

	s.logger.Info("memory.ingest.received",
		"source_id", persisted.ID,
		"type", persisted.Type,
		"mime", persisted.MIMEType,
		"bytes", persisted.Bytes,
		"duplicate", duplicate,
	)

	if err := s.deps.Store.EnqueueJob(ctx, persisted.ID, JobIngest); err != nil {
		s.recordError(err)
		s.logger.Warn("memory.ingest.failed", "stage", "enqueue", "source_id", persisted.ID, "error", err)
		return persisted, err
	}
	return persisted, nil
}

func (s *Service) workerLoop(ctx context.Context, index int) {
	logger := s.logger.With("worker", index)
	// Stagger workers so they do not all wake and contend for the single
	// SQLite connection on the same tick.
	timer := time.NewTimer(time.Duration(index) * 250 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		worked := s.processNext(ctx, logger)

		next := s.opts.Ingestion.PollInterval
		if worked {
			// Drain the queue eagerly while there is work to do.
			next = 50 * time.Millisecond
		}
		timer.Reset(next)
	}
}

// processNext claims and runs a single job. Returns true when a job was
// processed (successfully or not), so the worker knows to keep draining.
func (s *Service) processNext(ctx context.Context, logger *slog.Logger) bool {
	job, err := s.deps.Store.ClaimJob(ctx, time.Now().UTC())
	if errors.Is(err, ErrNotFound) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("memory.queue.claim_failed", "error", err)
		}
		return false
	}

	var runErr error
	switch job.Kind {
	case JobSummarize:
		runErr = s.runSummarizeJob(ctx, job)
	case JobFacts:
		runErr = s.runFactsJob(ctx, job)
	default:
		runErr = s.runIngestJob(ctx, job)
	}

	if runErr == nil {
		if err := s.deps.Store.CompleteJob(context.WithoutCancel(ctx), job.ID); err != nil {
			logger.Warn("memory.queue.complete_failed", "job_id", job.ID, "error", err)
		}
		return true
	}

	s.handleJobFailure(ctx, logger, job, runErr)
	return true
}

// handleJobFailure decides between "retry later" and "park".
//
// The distinction matters: a transient outage (Mac mini asleep, Qdrant
// restarting) must retry indefinitely with a capped interval, because
// the data is fine and only the backend is missing. A permanent problem
// (a .zip, a scanned PDF with no OCR path) is parked immediately so it
// stops consuming worker time, and shows up in `memory pending` where a
// human can act on it.
func (s *Service) handleJobFailure(ctx context.Context, logger *slog.Logger, job Job, runErr error) {
	// The context ending during shutdown is not a real failure; leave
	// the job pending and let the next process pick it up.
	if ctx.Err() != nil {
		return
	}
	ctx = context.WithoutCancel(ctx)

	s.recordError(runErr)
	attempts := job.Attempts + 1

	if errors.Is(runErr, ErrUnsupportedSource) {
		logger.Info("memory.ingest.failed",
			"job_id", job.ID, "source_id", job.SourceID, "reason", "unsupported", "error", runErr)
		if err := s.deps.Store.FailJob(ctx, job.ID, runErr.Error()); err != nil {
			logger.Warn("memory.queue.park_failed", "job_id", job.ID, "error", err)
		}
		_ = s.deps.Store.SetSourceStatus(ctx, job.SourceID, StatusSkipped)
		return
	}

	transient := errors.Is(runErr, ErrEmbeddingsUnavailable) ||
		errors.Is(runErr, vectorstore.ErrUnavailable) ||
		errors.Is(runErr, context.DeadlineExceeded)

	if !transient && attempts >= s.opts.Ingestion.MaxAttempts {
		logger.Warn("memory.ingest.failed",
			"job_id", job.ID, "source_id", job.SourceID, "attempts", attempts, "reason", "max_attempts", "error", runErr)
		if err := s.deps.Store.FailJob(ctx, job.ID, runErr.Error()); err != nil {
			logger.Warn("memory.queue.park_failed", "job_id", job.ID, "error", err)
		}
		_ = s.deps.Store.SetSourceStatus(ctx, job.SourceID, StatusFailed)
		return
	}

	delay := backoff(s.opts.Ingestion.RetryBase, s.opts.Ingestion.RetryMaxInterval, attempts)
	logger.Info("memory.ingest.failed",
		"job_id", job.ID,
		"source_id", job.SourceID,
		"attempts", attempts,
		"transient", transient,
		"retry_in", delay.String(),
		"error", runErr,
	)
	if err := s.deps.Store.RescheduleJob(ctx, job.ID, attempts, runErr.Error(), time.Now().UTC().Add(delay)); err != nil {
		logger.Warn("memory.queue.reschedule_failed", "job_id", job.ID, "error", err)
	}
}

// backoff doubles the base delay per attempt, capped. Deterministic (no
// jitter) because the queue is single-digit-concurrency and predictable
// retry timing makes the CLI's "next retry" column meaningful.
func backoff(base, max time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	if delay > max {
		return max
	}
	return delay
}

// runIngestJob is the full pipeline for one source:
// extract → chunk → embed → upsert → persist → completed.
func (s *Service) runIngestJob(ctx context.Context, job Job) error {
	source, err := s.deps.Store.Source(ctx, job.SourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Source deleted underneath us; nothing to do.
			return nil
		}
		return err
	}

	if _, statErr := os.Stat(source.RawPath); statErr != nil {
		return fmt.Errorf("%w: raw file missing at %s", ErrUnsupportedSource, source.RawPath)
	}

	_ = s.deps.Store.SetSourceStatus(ctx, source.ID, StatusProcessing)

	documents, err := s.deps.Extractors.Extract(ctx, source)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return fmt.Errorf("%w: extraction produced no text", ErrUnsupportedSource)
	}

	chunks := s.buildChunks(source, documents)
	if len(chunks) == 0 {
		return fmt.Errorf("%w: chunking produced nothing", ErrUnsupportedSource)
	}

	dimensions, err := s.vectorDimensions(ctx)
	if err != nil {
		return err
	}
	if err := s.deps.Vectors.EnsureCollection(ctx, dimensions); err != nil {
		return err
	}

	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}

	embedStart := time.Now()
	vectors, err := s.deps.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	s.logger.Debug("memory.embed.duration",
		"source_id", source.ID, "chunks", len(chunks), "duration_ms", time.Since(embedStart).Milliseconds())

	if len(vectors) != len(chunks) {
		return fmt.Errorf("%w: embedder returned %d vectors for %d chunks", ErrEmbeddingsUnavailable, len(vectors), len(chunks))
	}

	points := make([]vectorstore.Point, 0, len(chunks))
	now := time.Now().UTC()
	for i, chunk := range chunks {
		chunks[i].IndexedAt = now
		points = append(points, vectorstore.Point{
			ID:     chunk.ID,
			Vector: vectors[i],
			Payload: map[string]any{
				"chunk_id":    chunk.ID,
				"source_id":   source.ID,
				"source_type": source.Type,
				"source":      source.Source,
				"title":       source.Title,
				"chat_id":     source.ChatID,
				"ordinal":     int64(chunk.Ordinal),
				"created_at":  source.CreatedAt.Unix(),
			},
		})
	}

	// Vector first, then SQLite. If the process dies between the two the
	// job is still pending, the re-run upserts the same point ids, and
	// the result is identical — whereas the reverse order could leave
	// SQLite claiming chunks that the index does not have.
	if err := s.deps.Vectors.Upsert(ctx, points); err != nil {
		return err
	}
	if err := s.deps.Store.ReplaceChunks(ctx, source.ID, chunks); err != nil {
		return err
	}
	if err := s.deps.Store.SetSourceStatus(ctx, source.ID, StatusCompleted); err != nil {
		return err
	}

	s.logger.Info("memory.ingest.completed",
		"source_id", source.ID, "type", source.Type, "chunks", len(chunks))
	return nil
}

func (s *Service) buildChunks(source Source, documents []Document) []Chunk {
	var (
		chunks  []Chunk
		ordinal int
		now     = time.Now().UTC()
	)
	for _, document := range documents {
		for _, piece := range ChunkText(document.Text, s.opts.Chunking) {
			metadata := map[string]string{}
			for key, value := range source.Metadata {
				metadata[key] = value
			}
			for key, value := range document.Metadata {
				metadata[key] = value
			}
			if strings.TrimSpace(document.Title) != "" {
				metadata["document"] = document.Title
			}
			chunks = append(chunks, Chunk{
				ID:        newID(),
				SourceID:  source.ID,
				Ordinal:   ordinal,
				Text:      piece.Text,
				Tokens:    piece.Tokens,
				Heading:   piece.Heading,
				Metadata:  metadata,
				CreatedAt: now,
			})
			ordinal++
		}
	}
	return chunks
}

func (s *Service) vectorDimensions(ctx context.Context) (int, error) {
	s.dimensionsMu.Lock()
	cached := s.dimensions
	s.dimensionsMu.Unlock()
	if cached > 0 {
		return cached, nil
	}
	if s.deps.Embedder == nil {
		return 0, fmt.Errorf("%w: no embedder configured", ErrEmbeddingsUnavailable)
	}
	dimensions, err := s.deps.Embedder.Dimensions(ctx)
	if err != nil {
		return 0, err
	}
	s.dimensionsMu.Lock()
	s.dimensions = dimensions
	s.dimensionsMu.Unlock()
	return dimensions, nil
}
