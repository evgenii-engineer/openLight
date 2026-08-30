// Package memory implements openLight's automatic long-term memory: a
// three-level store where every inbound artefact is first archived
// verbatim on durable local storage (RAW), then chunked and indexed into
// a vector database (searchable RAG), and finally distilled by the smart
// model into episode summaries and structured facts.
//
// The layering is deliberate. RAW is the source of truth: the vector
// index is disposable and can always be rebuilt from it (see Reindex).
// SQLite holds metadata, the ingestion queue, and the structured facts;
// it never holds binary blobs. The vector database holds embeddings and
// small text payloads only.
//
// Every heavy operation (embedding, summarisation, fact extraction,
// vision) is asynchronous and runs off the reply path, so a slow or
// offline brain node degrades memory quality but never the agent's
// ability to answer.
package memory

import (
	"context"
	"time"
)

// Source types. These double as the RAW storage sub-directory name, so
// keep them filesystem-safe and stable — changing one orphans existing
// archives.
const (
	TypeTelegram     = "telegram"
	TypeVoice        = "voice"
	TypeImage        = "images"
	TypeDocument     = "documents"
	TypeConversation = "conversations"
)

// Ingestion status of a source row.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

// Job kinds and states for the persistent ingestion queue.
const (
	JobIngest    = "ingest"
	JobSummarize = "summarize"
	JobFacts     = "facts"

	JobPending   = "pending"
	JobRunning   = "running"
	JobCompleted = "completed"
	JobFailed    = "failed"
)

// Episode lifecycle.
const (
	EpisodeOpen       = "open"
	EpisodeClosed     = "closed"
	EpisodeSummarized = "summarized"
)

// Fact categories. Free-form strings are accepted from the extractor but
// normalised onto this set so retrieval ranking stays predictable.
const (
	CategoryHardware    = "hardware"
	CategoryProject     = "project"
	CategoryPreference  = "preference"
	CategoryDecision    = "decision"
	CategoryEntity      = "entity"
	CategoryEnvironment = "environment"
	CategoryOther       = "other"
)

// Item is what callers hand to Manager.Ingest. Exactly one of Path or
// Text must be set: Path points at a file the caller already has on disk
// (it is copied into RAW storage, never moved, so the caller keeps
// ownership of the original), Text carries inline content that the
// manager persists itself.
type Item struct {
	// Type is one of the Type* constants. Defaults to TypeDocument.
	Type string

	// Source labels the origin, e.g. "telegram:chat:<id>". Free-form;
	// surfaced in retrieval provenance.
	Source string

	// ExternalID is the origin's own identifier (Telegram message id,
	// file id, episode id). Optional; used for provenance only.
	ExternalID string

	// Title is a short human label shown in provenance and search output.
	Title string

	// Path is the local file to archive. Mutually exclusive with Text.
	Path string

	// Text is inline content to archive. Mutually exclusive with Path.
	Text string

	// MIMEType is the declared content type. When empty it is guessed
	// from the file extension, falling back to text/plain for Text items.
	MIMEType string

	// Filename overrides the archived file name. Optional.
	Filename string

	ChatID int64
	UserID int64

	// Metadata is arbitrary JSON-serialisable provenance stored alongside
	// the source row and copied onto every chunk payload.
	Metadata map[string]string

	// CreatedAt defaults to time.Now().UTC() when zero.
	CreatedAt time.Time
}

// Source is a persisted RAW artefact plus its ingestion state.
type Source struct {
	ID         string
	Type       string
	Source     string
	ExternalID string
	Title      string
	MIMEType   string
	RawPath    string
	Hash       string
	Bytes      int64
	Status     string
	ChatID     int64
	UserID     int64
	Metadata   map[string]string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Document is one extraction output. A single source may yield several
// documents (a PDF page range, an image's description plus its OCR text).
type Document struct {
	// Title distinguishes documents produced from the same source.
	Title string

	// Text is the extracted plain text. Empty documents are dropped.
	Text string

	// Metadata is merged onto each chunk produced from this document.
	Metadata map[string]string
}

// Chunk is an indexed slice of a document, the unit stored in the vector
// database. Text lives in SQLite too so the index stays rebuildable and
// so retrieval can render provenance without a second round trip.
type Chunk struct {
	ID        string
	SourceID  string
	Ordinal   int
	Text      string
	Tokens    int
	Heading   string
	Metadata  map[string]string
	IndexedAt time.Time
	CreatedAt time.Time
}

// Job is a persistent unit of background work. Jobs survive restarts:
// anything left in JobRunning when the process died is reclaimed to
// JobPending on the next start.
type Job struct {
	ID          int64
	SourceID    string
	Kind        string
	Status      string
	Attempts    int
	LastError   string
	NextRetryAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Fact is a distilled, long-lived statement extracted from a source.
// Facts are never deleted on update: superseding a fact closes the old
// row's validity interval and links it to its replacement, so "what did
// I believe last March" stays answerable.
type Fact struct {
	ID           string
	Subject      string
	Predicate    string
	Value        string
	Category     string
	Confidence   float64
	SourceID     string
	ValidFrom    time.Time
	ValidTo      time.Time // zero means "still current"
	SupersededBy string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Episode groups consecutive conversation turns in one chat. The raw
// turns stay queryable; what gets indexed into RAG is the distilled
// summary, so "ага"/"ок"/"спасибо" never becomes a searchable chunk.
type Episode struct {
	ID              string
	ChatID          int64
	Status          string
	TurnCount       int
	StartedAt       time.Time
	LastActivityAt  time.Time
	ClosedAt        time.Time
	SummarySourceID string
}

// Turn is one persisted conversation message inside an episode.
type Turn struct {
	ID        int64
	EpisodeID string
	Role      string
	Text      string
	CreatedAt time.Time
}

// SearchOptions tunes a single retrieval call. Zero values fall back to
// the configured retrieval defaults.
type SearchOptions struct {
	// Candidates is the vector top-K before ranking and dedup.
	Candidates int

	// MaxResults caps the chunks returned after ranking.
	MaxResults int

	// ChatID, when non-zero, boosts chunks originating from that chat.
	ChatID int64

	// Types, when non-empty, restricts results to those source types.
	Types []string
}

// Result is one retrieved chunk with full provenance. The provenance
// fields exist so a later "откуда ты это знаешь?" can be answered
// without re-running the search.
type Result struct {
	ChunkID    string
	SourceID   string
	SourceType string
	Source     string
	ExternalID string
	Title      string
	Path       string
	Text       string
	Score      float64
	Timestamp  time.Time
	Metadata   map[string]string
}

// Manager is the memory subsystem's public surface. Every method is safe
// to call when the subsystem is disabled or degraded: writes queue or
// no-op, reads return empty results, and nothing returns an error that
// would justify failing the user's request.
type Manager interface {
	// Ingest archives the item to RAW storage and enqueues indexing.
	// Returns quickly; the heavy work happens on the background workers.
	// A duplicate (same content hash) is recognised and not re-indexed.
	Ingest(ctx context.Context, item Item) (Source, error)

	// Search runs the vector retrieval pipeline.
	Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error)

	// Remember stores a structured fact, superseding any current fact
	// with the same subject/predicate.
	Remember(ctx context.Context, fact Fact) (Fact, error)

	// Recall returns current facts relevant to the query.
	Recall(ctx context.Context, query string, limit int) ([]Fact, error)

	// Forget closes a fact's validity interval (facts) or removes a
	// source and its chunks from the index (sources). The RAW archive is
	// never touched.
	Forget(ctx context.Context, id string) error

	// Reindex rebuilds the vector index from RAW storage and SQLite.
	Reindex(ctx context.Context, opts ReindexOptions) (int, error)

	// Status returns a cheap snapshot for /status and the CLI.
	Status(ctx context.Context) Status
}

// ReindexOptions selects what a reindex pass covers.
type ReindexOptions struct {
	// All rebuilds every source, recreating the collection first.
	All bool

	// SourceID limits the pass to a single source.
	SourceID string

	// Failed re-enqueues only sources whose last ingestion failed.
	Failed bool
}

// Status is the snapshot rendered by `openlight memory status` and the
// Memory block of /status. Building it must stay cheap — counters come
// from SQLite aggregates and cached health probes, never from a fresh
// network round trip on the request path.
type Status struct {
	Enabled bool

	VectorOnline     bool
	VectorError      string
	EmbeddingsOnline bool
	EmbeddingsError  string

	Sources     int64
	Chunks      int64
	Facts       int64
	Episodes    int64
	PendingJobs int64
	FailedJobs  int64

	RawBytes    int64
	DBBytes     int64
	VectorCount int64

	LastIngestAt time.Time
	LastError    string
}
