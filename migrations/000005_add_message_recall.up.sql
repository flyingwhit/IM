-- Add recalled_at column to support message recall (撤回消息).
-- NULL means the message has not been recalled; non-NULL records when it was.
-- No index needed: recall looks up by primary key (id), and the column is
-- only checked in application-layer filtering for conversation queries.
ALTER TABLE messages ADD COLUMN recalled_at TIMESTAMPTZ;
