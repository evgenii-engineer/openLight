CREATE TABLE IF NOT EXISTS watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_user_id INTEGER NOT NULL,
    telegram_chat_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    threshold REAL NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    reaction_mode TEXT NOT NULL,
    action_type TEXT NOT NULL DEFAULT 'none',
    cooldown_seconds INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    incident_state TEXT NOT NULL DEFAULT 'clear',
    condition_since TIMESTAMP NULL,
    last_triggered_at TIMESTAMP NULL,
    last_checked_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_watches_chat_id ON watches (telegram_chat_id, enabled, id);
CREATE INDEX IF NOT EXISTS idx_watches_enabled ON watches (enabled, id);

CREATE TABLE IF NOT EXISTS watch_incidents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    telegram_chat_id INTEGER NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    details TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    reaction_mode TEXT NOT NULL,
    action_type TEXT NOT NULL DEFAULT 'none',
    action_status TEXT NOT NULL DEFAULT 'none',
    action_prompt TEXT NOT NULL DEFAULT '',
    action_requested_at TIMESTAMP NULL,
    action_expires_at TIMESTAMP NULL,
    action_completed_at TIMESTAMP NULL,
    report TEXT NOT NULL DEFAULT '',
    opened_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_watch_incidents_watch_id ON watch_incidents (watch_id, status, id);
CREATE INDEX IF NOT EXISTS idx_watch_incidents_chat_pending ON watch_incidents (telegram_chat_id, action_status, action_expires_at);
