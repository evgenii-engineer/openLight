-- 0005_message_retention.sql
--
-- Speeds up the per-chat history lookup in ListMessagesByChat
-- (telegram_chat_id + ORDER BY created_at DESC, id DESC) and adds a
-- created_at index to both messages and skill_calls so the retention
-- cleanup pass can range-scan instead of doing a full table scan once
-- the database grows past a few thousand rows on a Pi.

CREATE INDEX IF NOT EXISTS idx_messages_chat_created_at
    ON messages (telegram_chat_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_messages_created_at
    ON messages (created_at);

CREATE INDEX IF NOT EXISTS idx_skill_calls_created_at
    ON skill_calls (created_at);
