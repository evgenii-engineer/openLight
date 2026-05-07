CREATE TABLE IF NOT EXISTS visual_watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_user_id INTEGER NOT NULL,
    telegram_chat_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    keywords TEXT NOT NULL DEFAULT '',
    notify_on_change INTEGER NOT NULL DEFAULT 1,
    notify_on_keywords INTEGER NOT NULL DEFAULT 0,
    diff_threshold REAL NOT NULL DEFAULT 0.15,
    interval_seconds INTEGER NOT NULL DEFAULT 900,
    cooldown_seconds INTEGER NOT NULL DEFAULT 1800,
    baseline_path TEXT NOT NULL DEFAULT '',
    last_screenshot_path TEXT NOT NULL DEFAULT '',
    last_changed_fraction REAL NOT NULL DEFAULT 0,
    last_keywords_seen TEXT NOT NULL DEFAULT '',
    last_checked_at TIMESTAMP NULL,
    last_changed_at TIMESTAMP NULL,
    last_alerted_at TIMESTAMP NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_visual_watches_chat ON visual_watches (telegram_chat_id, enabled, id);
CREATE INDEX IF NOT EXISTS idx_visual_watches_enabled ON visual_watches (enabled, id);
