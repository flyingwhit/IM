# Database Design

## Why PostgreSQL

- **ACID transactions**: Friend system operations (accept = update status + create reverse record) must be atomic. PostgreSQL guarantees this.
- **UUID support**: Native `gen_random_uuid()` avoids application-level ID generation and collision issues when scaling horizontally (Phase 4).
- **Rich indexing**: Partial indexes, composite indexes, and JOIN performance needed for friend queries.

## Schema Decisions

### UUID vs SERIAL for Primary Keys

Chose UUID. Trade-offs:

| | UUID | SERIAL |
|---|------|--------|
| Distributed systems | No collision across shards | Requires ID generator service |
| Security | Doesn't leak user count | `GET /users/42` reveals growth rate |
| Index size | Larger, slower scans | Smaller, faster |
| Readability | Hard to debug | Easy to read |

For Phase 1-3 (single instance), SERIAL would be faster. UUID is chosen for Phase 4 readiness and because the indexing overhead is negligible at our scale.

### Friends Table: Dual-Record Model

When A and B become friends, we store two rows:
- `(user_id=A, friend_id=B, status=accepted)`
- `(user_id=B, friend_id=A, status=accepted)`

Alternative: single record with `user_id < friend_id` canonical ordering.

**Why dual-record**: "Get my friends" becomes `WHERE user_id = ? AND status = 'accepted'` — a simple indexed lookup. The single-record approach requires `WHERE (user_id = ? OR friend_id = ?) AND status = 'accepted'` which can't use an index efficiently on both sides of the OR. Storage cost (one extra row per friendship) is negligible.

### Refresh Tokens: Separate Table

Stored independently from users to support:
- **Multiple devices**: One user can have many refresh tokens (phone + web + desktop)
- **Bulk revocation**: "Log out all devices" = `DELETE FROM refresh_tokens WHERE user_id = ?`
- **Expiry cleanup**: Tokens auto-expire via Redis TTL; the DB table is the source of truth for audit/debugging

### Password Hash Storage

`password_hash VARCHAR(255)` stores bcrypt output. bcrypt DefaultCost (10) is chosen because:
- ~100ms per hash on modern hardware — slow enough to deter brute force
- Standard Go library (`golang.org/x/crypto/bcrypt`) — no custom crypto

## Indexes

| Table | Index | Purpose |
|-------|-------|---------|
| `users` | `username` | Login lookup (`WHERE username = ?`) |
| `users` | `email` | Future: email-based login or duplicate check |
| `friends` | `user_id` | "My friends" query |
| `friends` | `friend_id` | "Who sent me requests" query |
| `friends` | `status` | Filter pending vs accepted |
| `refresh_tokens` | `user_id` | Bulk revocation |
| `refresh_tokens` | `expires_at` | Cleanup expired tokens |
| `refresh_tokens` | `token_hash` | Token lookup during refresh |
| `messages` | `(sender_id, receiver_id, created_at DESC)` | Conversation query (BitmapOr on both sides of OR) |
| `messages` | `(receiver_id, status, created_at)` | Undelivered message pull |
| `messages` | `(sender_id, created_at DESC)` | Sent message history |

## Messages Table

### Schema

```sql
CREATE TABLE messages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id     UUID NOT NULL REFERENCES users(id),
    receiver_id   UUID NOT NULL REFERENCES users(id),
    content       TEXT NOT NULL,
    content_type  VARCHAR(20) NOT NULL DEFAULT 'text',
    status        VARCHAR(20) NOT NULL DEFAULT 'sent',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Design Decisions

**Why not a conversations table?** For 1-on-1 messaging, conversations can be derived from messages via `SELECT DISTINCT
CASE WHEN sender_id=$1 THEN receiver_id ELSE sender_id END`. A separate
conversations table adds write-path complexity (must INSERT into two tables)
for a read-path optimization we don't need yet. Revisit when group chat arrives.

**Why VARCHAR for status instead of ENUM?** PostgreSQL ENUM types require
`ALTER TYPE ... ADD VALUE` to extend. With VARCHAR + CHECK constraint, adding
a new status (e.g., "read") is a simple CHECK constraint migration.

**Why three compound indexes?** Each covers a distinct query pattern:
1. `idx_messages_conversation` — the hot path: loading chat history. The composite key enables index-only ORDER BY.
2. `idx_messages_receiver_status` — offline message pull. `receiver_id` first for selectivity, then `status` and `created_at`.
3. `idx_messages_sender` — "messages I sent" queries, less frequent but important for the sender's perspective.

**Cursor-based pagination**: `GET /api/v1/messages?peer=<id>&before=<cursor>&limit=50`.
Uses `created_at < $cursor` in the WHERE clause. More stable than offset-based
pagination when new messages arrive between page requests — no duplicate or
missed messages.

### Storage Estimates

Per row: ~200 bytes (UUID×2 = 32 bytes + TEXT avg 100 bytes + metadata ~68 bytes).
- 100K messages ≈ 20 MB
- 1M messages ≈ 200 MB  
- 10M messages ≈ 2 GB

Archiving strategy (TODO Phase 5+): partition by month, archive cold partitions.
