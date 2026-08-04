CREATE TABLE messages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id     UUID NOT NULL REFERENCES users(id),
    receiver_id   UUID NOT NULL REFERENCES users(id),
    content       TEXT NOT NULL,
    content_type  VARCHAR(20) NOT NULL DEFAULT 'text',
    status        VARCHAR(20) NOT NULL DEFAULT 'sent',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Conversation query: fetch messages between two users, newest first.
-- Covers: WHERE (sender_id=$1 AND receiver_id=$2)
--            OR (sender_id=$2 AND receiver_id=$1)
--         ORDER BY created_at DESC
-- PostgreSQL can use this index for both sides of the OR by scanning
-- two index ranges and merging them (BitmapOr).
CREATE INDEX idx_messages_conversation
    ON messages(sender_id, receiver_id, created_at DESC);

-- Offline message pull: messages sent to a user that haven't been delivered.
-- Covers: WHERE receiver_id=$1 AND status='sent' ORDER BY created_at ASC
CREATE INDEX idx_messages_receiver_status
    ON messages(receiver_id, status, created_at);

-- Sender history: messages sent by a specific user, newest first.
-- Covers: WHERE sender_id=$1 ORDER BY created_at DESC
CREATE INDEX idx_messages_sender
    ON messages(sender_id, created_at DESC);
