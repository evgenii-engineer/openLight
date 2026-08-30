-- 0001_memory_rag.sql
--
-- Schema for openLight's automatic long-term memory. This database is
-- separate from agent.db: it lives on the SSD next to the RAW archive so
-- the whole memory subsystem is one movable directory, and so a memory
-- write storm never contends with the agent's message log on a Pi.
--
-- Invariant: nothing here stores binary content. memory_sources.raw_path
-- points at the archived original on disk; the vector database stores
-- embeddings keyed by memory_chunks.id.

CREATE TABLE IF NOT EXISTS memory_sources (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT '',
    external_id  TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    mime_type    TEXT NOT NULL DEFAULT '',
    raw_path     TEXT NOT NULL DEFAULT '',
    hash         TEXT NOT NULL,
    bytes        INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'pending',
    chat_id      INTEGER NOT NULL DEFAULT 0,
    user_id      INTEGER NOT NULL DEFAULT 0,
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);

-- Content-hash dedup: re-sending the same file must not re-index it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_sources_hash ON memory_sources (hash);
CREATE INDEX IF NOT EXISTS idx_memory_sources_status ON memory_sources (status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_sources_created ON memory_sources (created_at DESC);

CREATE TABLE IF NOT EXISTS memory_chunks (
    id         TEXT PRIMARY KEY,
    source_id  TEXT NOT NULL,
    ordinal    INTEGER NOT NULL DEFAULT 0,
    text       TEXT NOT NULL,
    tokens     INTEGER NOT NULL DEFAULT 0,
    heading    TEXT NOT NULL DEFAULT '',
    metadata   TEXT NOT NULL DEFAULT '{}',
    indexed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (source_id) REFERENCES memory_sources (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_chunks_source ON memory_chunks (source_id, ordinal);

CREATE TABLE IF NOT EXISTS memory_jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id     TEXT NOT NULL,
    kind          TEXT NOT NULL DEFAULT 'ingest',
    status        TEXT NOT NULL DEFAULT 'pending',
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    next_retry_at TIMESTAMP NOT NULL,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

-- The claim query scans (status, next_retry_at); the partial unique index
-- keeps a source from queueing the same kind of work twice while an
-- earlier attempt is still outstanding.
CREATE INDEX IF NOT EXISTS idx_memory_jobs_claim ON memory_jobs (status, next_retry_at, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_jobs_active
    ON memory_jobs (source_id, kind)
    WHERE status IN ('pending', 'running');

CREATE TABLE IF NOT EXISTS memory_facts (
    id            TEXT PRIMARY KEY,
    subject       TEXT NOT NULL,
    predicate     TEXT NOT NULL,
    value         TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT 'other',
    confidence    REAL NOT NULL DEFAULT 0.5,
    source_id     TEXT NOT NULL DEFAULT '',
    valid_from    TIMESTAMP NOT NULL,
    valid_to      TIMESTAMP,
    superseded_by TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

-- Current-fact lookup: valid_to IS NULL means "still true".
CREATE INDEX IF NOT EXISTS idx_memory_facts_current ON memory_facts (subject, predicate, valid_to);
CREATE INDEX IF NOT EXISTS idx_memory_facts_updated ON memory_facts (updated_at DESC);

CREATE TABLE IF NOT EXISTS memory_episodes (
    id                TEXT PRIMARY KEY,
    chat_id           INTEGER NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'open',
    turn_count        INTEGER NOT NULL DEFAULT 0,
    started_at        TIMESTAMP NOT NULL,
    last_activity_at  TIMESTAMP NOT NULL,
    closed_at         TIMESTAMP,
    summary_source_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_memory_episodes_open ON memory_episodes (status, last_activity_at);
CREATE INDEX IF NOT EXISTS idx_memory_episodes_chat ON memory_episodes (chat_id, status);

CREATE TABLE IF NOT EXISTS memory_episode_turns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    episode_id TEXT NOT NULL,
    role       TEXT NOT NULL,
    text       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (episode_id) REFERENCES memory_episodes (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_episode_turns ON memory_episode_turns (episode_id, id);
