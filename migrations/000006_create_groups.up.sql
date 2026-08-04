-- Groups: basic group information.
-- owner_id is the user who created the group and has admin privileges.
CREATE TABLE groups (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) NOT NULL,
    owner_id   UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Group members: tracks who belongs to which group.
-- Composite primary key enforces one record per user per group.
-- role: 'owner' or 'member' — the owner is also recorded as a member.
CREATE TABLE group_members (
    group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id),
    role      VARCHAR(20) NOT NULL DEFAULT 'member',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

-- Query "my groups" — find all groups a user belongs to.
CREATE INDEX idx_group_members_user ON group_members(user_id);
