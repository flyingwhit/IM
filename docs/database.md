# 数据库设计

## 为什么用 PostgreSQL

- **ACID 事务**：好友系统操作（accept = update status + create reverse record）必须原子性。PostgreSQL 保证。
- **UUID 原生支持**：`gen_random_uuid()` 避免应用层 ID 生成和水平扩展时的碰撞问题。
- **丰富索引**：复合索引、部分索引、JOIN 性能。

## Schema 设计

### 用户表 (users)

```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(50) UNIQUE NOT NULL,
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname      VARCHAR(100),
    avatar_url    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

索引：`username`（登录查找）、`email`（未来邮箱登录）。

### 好友表 (friends)

```sql
CREATE TABLE friends (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    friend_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, friend_id)
);
```

索引：`user_id`、`friend_id`、`status`。

**双向记录模型**：A 和 B 成为好友后存两行 `(A, B, accepted)` 和 `(B, A, accepted)`。"我的好友"查询是 `WHERE user_id = ? AND status = 'accepted'`——简单索引查找。单行方案 `WHERE (user_id = ? OR friend_id = ?)` 无法在 OR 两侧同时高效使用索引。

### 消息表 (messages)

```sql
CREATE TABLE messages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id     UUID NOT NULL REFERENCES users(id),
    receiver_id   UUID NOT NULL REFERENCES users(id),
    content       TEXT NOT NULL,
    content_type  VARCHAR(20) NOT NULL DEFAULT 'text',
    status        VARCHAR(20) NOT NULL DEFAULT 'sent',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    recalled_at   TIMESTAMPTZ  -- NULL 表示未撤回
);
```

索引：
- `(sender_id, receiver_id, created_at DESC)` — 对话查询（BitmapOr 合并 OR 两侧）
- `(receiver_id, status, created_at)` — 离线消息拉取
- `(sender_id, created_at DESC)` — 已发送消息历史

**为什么用 VARCHAR 而非 ENUM？** PostgreSQL ENUM 类型需要用 `ALTER TYPE ... ADD VALUE` 扩展。VARCHAR + CHECK 约束添加新状态（如 "read"）只需简单迁移。

**为什么没有 conversations 表？** 1v1 场景下会话可以从 messages 表派生。群聊时会需要独立的 conversations 表存储群元数据。

**存储估算**：每条约 200 bytes。100K ≈ 20 MB，1M ≈ 200 MB，10M ≈ 2 GB。

### Refresh Token 表 (refresh_tokens) — 未使用

迁移 000003 创建了 `refresh_tokens` 表，但实际运行中 Redis 存储 session（`refresh:<sha256(token)>` → userID + TTL）。表保留用于未来审计需求。

## 缓存 (Redis)

| Key 模式 | 值 | TTL | 用途 |
|----------|-----|-----|------|
| `refresh:<sha256(token)>` | userID | 168h (7天) | Refresh token 验证，GETDEL 原子旋转 |
| `presence:<userID>` | "1" | 60s | 在线状态标记，崩溃安全网 |

**Presence TTL 设计**：正常断开 Hub 主动 DEL，TTL 仅处理崩溃场景（服务器宕机后 60s 内僵尸 key 自动过期）。60s 与 WebSocket pongWait 保持一致。
