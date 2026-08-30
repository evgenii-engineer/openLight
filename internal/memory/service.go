package memory

// Service wiring: configuration, collaborators, lifecycle, and the
// status snapshot. The three halves of what it does live next door —
// ingest.go (write path), retrieve.go (read path), episodes.go
// (conversation capture).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openlight/internal/memory/vectorstore"
)

// RetrievalOptions bounds the read path.
type RetrievalOptions struct {
	Mode             RetrievalMode
	Candidates       int
	MaxResults       int
	MaxContextTokens int
	MaxFacts         int
}

func (o RetrievalOptions) normalized() RetrievalOptions {
	if o.Mode == "" {
		o.Mode = ModeHeuristic
	}
	if o.Candidates <= 0 {
		o.Candidates = 8
	}
	if o.MaxResults <= 0 {
		o.MaxResults = 5
	}
	if o.MaxResults > o.Candidates {
		o.MaxResults = o.Candidates
	}
	if o.MaxContextTokens <= 0 {
		o.MaxContextTokens = 500
	}
	if o.MaxFacts <= 0 {
		o.MaxFacts = 5
	}
	return o
}

// IngestionOptions bounds the write path.
type IngestionOptions struct {
	// Workers is the number of concurrent job processors. One is the
	// right default on a Pi: the expensive step is a network call to the
	// Mac mini, and more workers mostly means more contention on the
	// single SQLite connection.
	Workers int

	// PollInterval is how often an idle worker re-checks the queue.
	PollInterval time.Duration

	// RetryBase is the first backoff step; each attempt doubles it.
	RetryBase time.Duration

	// RetryMaxInterval caps the backoff.
	RetryMaxInterval time.Duration

	// MaxAttempts parks a job after this many failures. Transient
	// backend outages (embeddings or vector store down) do not count
	// against it — an offline Mac mini must never cause data loss.
	MaxAttempts int
}

func (o IngestionOptions) normalized() IngestionOptions {
	if o.Workers <= 0 {
		o.Workers = 1
	}
	if o.Workers > 4 {
		o.Workers = 4
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 5 * time.Second
	}
	if o.RetryBase <= 0 {
		o.RetryBase = 5 * time.Second
	}
	if o.RetryMaxInterval <= 0 {
		o.RetryMaxInterval = 10 * time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 12
	}
	return o
}

// ConversationOptions controls automatic conversation memory.
type ConversationOptions struct {
	// AutoMemory records turns at all.
	AutoMemory bool

	// Summarize distills closed episodes through the smart model. With
	// it off, raw turns are still recorded and queryable but nothing is
	// indexed into RAG.
	Summarize bool

	// IdleTimeout closes an episode after this much silence.
	IdleTimeout time.Duration

	// MaxTurns force-closes a long-running episode so a day-long chat
	// still produces intermediate summaries.
	MaxTurns int

	// MinTurns skips episodes too short to be worth a smart-model call.
	MinTurns int

	// CheckInterval is how often the consolidator looks for idle
	// episodes.
	CheckInterval time.Duration
}

func (o ConversationOptions) normalized() ConversationOptions {
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 10 * time.Minute
	}
	if o.MaxTurns <= 0 {
		o.MaxTurns = 40
	}
	if o.MinTurns <= 0 {
		o.MinTurns = 2
	}
	if o.CheckInterval <= 0 {
		o.CheckInterval = time.Minute
	}
	return o
}

// Options is everything the service needs to run.
type Options struct {
	Root       string
	DBPath     string
	Collection string

	Chunking      ChunkOptions
	Retrieval     RetrievalOptions
	Ingestion     IngestionOptions
	Conversations ConversationOptions

	// FactsEnabled promotes extracted facts into structured memory.
	FactsEnabled bool

	// AutoProvision lets the service set itself up on first start: pull
	// the embedding model onto the brain node and create the vector
	// collection. Without it an operator has to run `ollama pull` by
	// hand before memory can index anything.
	AutoProvision bool

	// HealthTTL is how long a backend health probe result is reused.
	// Status must stay cheap: /status is hit often and must never block
	// on a network round trip to a dead Mac mini.
	HealthTTL time.Duration
}

// Deps are the collaborators injected by the runtime.
type Deps struct {
	Store      *Store
	Raw        *RawStore
	Vectors    vectorstore.Store
	Embedder   Embedder
	Extractors Extractors
	Chatter    Chatter
	Logger     *slog.Logger
}

// Service is the concrete Manager. It owns the ingestion workers, the
// conversation consolidator, and the cached health/status snapshot.
type Service struct {
	opts Options
	deps Deps

	logger *slog.Logger

	running atomic.Bool
	turns   chan turnEvent

	healthMu     sync.Mutex
	healthAt     time.Time
	vectorErr    error
	embedErr     error
	rawBytesAt   time.Time
	rawBytes     int64
	lastErrorMu  sync.Mutex
	lastError    string
	dimensionsMu sync.Mutex
	dimensions   int
}

// Service must satisfy the public Manager contract.
var _ Manager = (*Service)(nil)

type turnEvent struct {
	chatID int64
	role   string
	text   string
	at     time.Time
}

// New builds a service. It does not touch the network: an unreachable
// Qdrant or Mac mini surfaces later as a degraded status, never as a
// startup failure that would take the whole agent down with it.
func New(opts Options, deps Deps) (*Service, error) {
	if deps.Store == nil {
		return nil, errors.New("memory: store is required")
	}
	if deps.Raw == nil {
		return nil, errors.New("memory: raw store is required")
	}
	if deps.Vectors == nil {
		deps.Vectors = vectorstore.Noop{}
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}

	opts.Chunking = opts.Chunking.normalized()
	opts.Retrieval = opts.Retrieval.normalized()
	opts.Ingestion = opts.Ingestion.normalized()
	opts.Conversations = opts.Conversations.normalized()
	if opts.HealthTTL <= 0 {
		opts.HealthTTL = 30 * time.Second
	}

	return &Service{
		opts:   opts,
		deps:   deps,
		logger: deps.Logger,
		turns:  make(chan turnEvent, 256),
	}, nil
}

// Close releases the store and vector connections.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var errs []string
	if s.deps.Vectors != nil {
		if err := s.deps.Vectors.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if s.deps.Store != nil {
		if err := s.deps.Store.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("memory close: %s", strings.Join(errs, "; "))
}

// Run starts the background workers and blocks until ctx is cancelled.
// It is the only place goroutines are spawned, and the count is bounded
// by Ingestion.Workers plus two fixed helpers — a Pi must not end up
// with a goroutine per source.
func (s *Service) Run(ctx context.Context) error {
	if s == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	if reclaimed, err := s.deps.Store.ReclaimRunningJobs(ctx); err != nil {
		s.logger.Warn("memory.queue.reclaim_failed", "error", err)
	} else if reclaimed > 0 {
		// Jobs left mid-flight by a previous process. Requeuing them is
		// what makes "restart during ingestion" a non-event.
		s.logger.Info("memory.queue.reclaimed", "jobs", reclaimed)
	}

	s.running.Store(true)
	defer s.running.Store(false)

	var wg sync.WaitGroup
	for i := 0; i < s.opts.Ingestion.Workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			s.workerLoop(ctx, index)
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.turnWriterLoop(ctx)
	}()

	if s.opts.Conversations.AutoMemory {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.consolidatorLoop(ctx)
		}()
	}

	if s.opts.AutoProvision {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.bootstrapLoop(ctx, bootstrapRetryBase)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

// bootstrapRetryBase is the first backoff step for the bootstrap loop,
// doubling up to five minutes. Sized for "the Mac mini is not awake
// yet", which is the overwhelmingly common reason to be here.
const bootstrapRetryBase = 5 * time.Second

// bootstrapLoop provisions the subsystem's own prerequisites: it pulls
// the embedding model onto the brain node if it is missing, then creates
// the vector collection.
//
// It runs in the background and retries until it succeeds, because on a
// Pi the usual failure is simply that the Mac mini is not awake yet.
// Nothing else waits on it — the ingestion workers create the collection
// lazily too, so a slow or failed bootstrap costs a little first-index
// latency and nothing more.
func (s *Service) bootstrapLoop(ctx context.Context, delay time.Duration) {
	if delay <= 0 {
		delay = bootstrapRetryBase
	}
	const maxDelay = 5 * time.Minute

	for {
		if err := s.provision(ctx); err == nil {
			return
		} else if ctx.Err() != nil {
			return
		} else {
			s.logger.Info("memory.bootstrap.retry", "in", delay.String(), "error", err)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

// provision performs one bootstrap attempt.
func (s *Service) provision(ctx context.Context) error {
	if provisioner, ok := s.deps.Embedder.(ModelProvisioner); ok {
		// No deadline: pulling an embedding model is a multi-gigabyte
		// download, and cutting it short would just restart it later.
		pulled, err := provisioner.EnsureModel(ctx)
		if err != nil {
			return fmt.Errorf("embedding model: %w", err)
		}
		if pulled {
			s.logger.Info("memory.bootstrap.model_pulled", "model", s.deps.Embedder.Model())
		}
	}

	dimensions, err := s.vectorDimensions(ctx)
	if err != nil {
		return fmt.Errorf("embedding dimensions: %w", err)
	}
	if err := s.deps.Vectors.EnsureCollection(ctx, dimensions); err != nil {
		return fmt.Errorf("vector collection: %w", err)
	}

	s.logger.Info("memory.bootstrap.ready",
		"model", s.deps.Embedder.Model(), "dimensions", dimensions, "collection", s.opts.Collection)
	return nil
}

// Status assembles the snapshot for /status and the CLI. Every field
// comes from a SQLite aggregate or a cached probe, so this stays safe to
// call on a request path.
func (s *Service) Status(ctx context.Context) Status {
	if s == nil {
		return Status{}
	}
	status := Status{Enabled: true}

	sources, chunks, facts, episodes, pending, failed, err := s.deps.Store.Counts(ctx)
	if err != nil {
		status.LastError = err.Error()
	} else {
		status.Sources = sources
		status.Chunks = chunks
		status.Facts = facts
		status.Episodes = episodes
		status.PendingJobs = pending
		status.FailedJobs = failed
	}

	if at, err := s.deps.Store.LastIngestAt(ctx); err == nil {
		status.LastIngestAt = at
	}

	vectorErr, embedErr := s.health(ctx)
	status.VectorOnline = vectorErr == nil
	if vectorErr != nil {
		status.VectorError = vectorErr.Error()
	}
	status.EmbeddingsOnline = embedErr == nil
	if embedErr != nil {
		status.EmbeddingsError = embedErr.Error()
	}

	if status.VectorOnline {
		if count, err := s.deps.Vectors.Count(ctx); err == nil {
			status.VectorCount = count
		}
	}

	status.RawBytes = s.cachedRawBytes()
	if info, err := os.Stat(s.deps.Store.Path()); err == nil {
		status.DBBytes = info.Size()
	}

	s.lastErrorMu.Lock()
	if status.LastError == "" {
		status.LastError = s.lastError
	}
	s.lastErrorMu.Unlock()

	return status
}

// health probes the backends at most once per HealthTTL. Two consumers
// (/status and the CLI) sharing one cached result is the difference
// between a status page that renders instantly and one that hangs for
// the length of a TCP timeout when the Mac mini is asleep.
func (s *Service) health(ctx context.Context) (vectorErr, embedErr error) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	if time.Since(s.healthAt) < s.opts.HealthTTL && !s.healthAt.IsZero() {
		return s.vectorErr, s.embedErr
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	s.vectorErr = s.deps.Vectors.Health(probeCtx)
	if s.deps.Embedder != nil {
		s.embedErr = s.deps.Embedder.Health(probeCtx)
	} else {
		s.embedErr = fmt.Errorf("%w: not configured", ErrEmbeddingsUnavailable)
	}
	s.healthAt = time.Now()
	return s.vectorErr, s.embedErr
}

// cachedRawBytes walks the archive at most once every five minutes.
func (s *Service) cachedRawBytes() int64 {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if !s.rawBytesAt.IsZero() && time.Since(s.rawBytesAt) < 5*time.Minute {
		return s.rawBytes
	}
	s.rawBytes = s.deps.Raw.Bytes()
	s.rawBytesAt = time.Now()
	return s.rawBytes
}

func (s *Service) recordError(err error) {
	if err == nil {
		return
	}
	s.lastErrorMu.Lock()
	s.lastError = truncate(err.Error(), 200)
	s.lastErrorMu.Unlock()
}

// Store exposes the underlying store for the CLI's read-only reporting.
func (s *Service) Store() *Store {
	if s == nil {
		return nil
	}
	return s.deps.Store
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
