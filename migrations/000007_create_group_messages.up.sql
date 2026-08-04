-- Group messages: messages sent within a group chat.
-- Independent from private messages (messages table) for cleaner FK references,
-- different indexing needs, and clear code separation.
CREATE TABLE group_messages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id     UUID NOT NULL REFERENCES groups(id),
    sender_id    UUID NOT NULL REFERENCES users(id),
    content      TEXT NOT NULL,
    content_type VARCHAR(20) NOT NULL DEFAULT 'text',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    recalled_at  TIMESTAMPTZ
);

-- Group message history: fetch messages for a group, newest first.
-- Covers: WHERE group_id=$1 ORDER BY created_at DESC
CREATE INDEX idx_group_messages_group
    ON group_messages(group_id, created_at DESC);
