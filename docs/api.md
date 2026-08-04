# API 设计

## 约定

### URL 模式

```
/api/v1/<resource>/<action>
```

版本前缀允许 `/api/v2` 的破坏性变更不影响已有客户端。

### 认证

- **Access Token**（JWT，15 分钟 TTL）：通过 `Authorization: Bearer <token>` 传递。短生命周期意味着不需要撤销机制——到期前攻击者来不及造成重大损害。
- **Refresh Token**（不透明随机字符串，7 天 TTL）：仅用于 `/auth/refresh`。存储 SHA-256 哈希在 Redis 中，支持撤销。实现 token 旋转：每次刷新使旧 token 失效并签发新 token 对。

为什么不用 session？JWT 是无状态的——任何网关实例都可以校验而无需共享 session 存储。对 Phase 4 多网关架构很重要。

### 错误格式

所有错误响应遵循：
```json
{"error": "<人类可读的消息>"}
```

HTTP 状态码由领域错误类型决定：
- `ErrUnauthorized` → 401
- `ErrNotFound` → 404
- `ErrDuplicate`/`ErrConflict` → 409
- `ErrForbidden` → 403
- `ErrInvalidInput` → 400
- 其他 → 500

### 请求/响应

全部 JSON。请求验证用 Gin 的 `binding` 标签。可选字段用 `omitempty`。

---

## REST API

### 认证（公开）

| 方法 | 路径 | 请求体 | 响应 | 备注 |
|------|------|--------|------|------|
| POST | `/api/v1/auth/register` | `{username, email, password}` | `{id, username, email}` (201) | 密码 bcrypt，3-50 字符用户名，6-100 字符密码 |
| POST | `/api/v1/auth/login` | `{username, password}` | `{access_token, refresh_token, expires_in}` | 返回 token 对 |
| POST | `/api/v1/auth/refresh` | `{refresh_token}` | `{access_token, refresh_token, expires_in}` | 轮换旧 token |
| POST | `/api/v1/auth/logout` | `{refresh_token}` | `{message}` | 删除 refresh token |

登录返回统一的错误信息 "invalid username or password"（无论用户名存不存在），防止用户枚举攻击。

**已知问题**：注册后不自动返回 token，需额外一次 login 调用。

### 用户（需认证）

| 方法 | 路径 | 请求体 | 响应 |
|------|------|--------|------|
| GET | `/api/v1/users/me` | — | 完整用户对象 |
| PUT | `/api/v1/users/me` | `{nickname?, avatar_url?}` | 更新后的用户对象 |
| GET | `/api/v1/users/:id/online` | — | `{user_id, online}` |

`PUT /users/me` 对可选字段使用指针：`nil` 表示"不改"，非 nil 表示"更新为此值"。消除了"字段是省略了还是设空？"的歧义。

**已知问题**：
- 缺少 `GET /api/v1/users/:id` — 无法查看其他用户资料
- 缺少用户搜索 API（`GET /api/v1/users/search?q=`）
- `UpdateProfile` 缺少 nickname 长度和 avatar URL 格式验证
- 在线状态查询需逐个调用，缺少批量接口（`POST /api/v1/users/online/batch`）
- 在线状态查询不做隐私控制——任何认证用户可以查任何人的在线状态

### 好友（需认证）

| 方法 | 路径 | 用途 |
|------|------|------|
| POST | `/api/v1/friends/requests` | 发送好友请求（body: `{target_id}`） |
| GET | `/api/v1/friends` | 已接受的好友列表 |
| GET | `/api/v1/friends/requests` | 待处理的收到的好友请求 |
| PUT | `/api/v1/friends/requests/:id/accept` | 接受请求 |
| PUT | `/api/v1/friends/requests/:id/reject` | 拒绝请求 |
| DELETE | `/api/v1/friends/:id` | 删除好友 |

**Accept 流程**：在一个事务中 (1) UPDATE 请求状态为 accepted，(2) INSERT 反向记录。要么全部成功，要么全部回滚。

**Reject 流程**：验证请求属于当前用户且状态为 pending，然后 DELETE 记录。

**Remove 流程**：验证操作者是好友关系的一方，然后在一个事务中删除两个方向的记录。

**已知问题**：
- `FriendWithUser` 响应中 `user_id`/`friend_id` 的语义在不同 API 中不同（好友列表 vs 请求列表），容易混淆
- 发送者无法取消已发出的好友请求（缺少 `DELETE /api/v1/friends/requests/:id` 供发送者使用）
- 好友列表无分页
- `model/request.go` 中的 `FriendRequestAction` 结构体未被使用（死代码）

### 消息（需认证）

| 方法 | 路径 | 查询参数 | 响应 |
|------|------|----------|------|
| GET | `/api/v1/messages` | `peer` (必填), `before` (游标), `limit` (1-100, 默认 50) | `{messages, next_cursor}` |

**游标分页**：使用 `created_at < $cursor` 而非 OFFSET。当新消息在翻页期间到达时，不会出现重复或遗漏。

**已撤回消息**：content 被清空，客户端通过 `recalled_at` 字段判断显示"消息已撤回"占位符。

**已知问题**：
- 消息只能通过 WebSocket 发送，无 REST fallback（`POST /api/v1/messages`）
- `MessageNewPayload`（WebSocket）缺少 `recalled_at` 字段，客户端无法在实时投递中区分空消息和已撤回消息

### 健康检查（公开）

| 方法 | 路径 | 响应 |
|------|------|------|
| GET | `/health` | `{"status": "ok"}` |

在 `/api/v1` 之外——这是基础设施端点，不是业务 API。负载均衡器、K8s 探针和监控工具不应受 API 版本变更影响。

---

## WebSocket API

### 连接

```
GET /ws?token=<jwt>
```

认证在查询参数中（WebSocket JavaScript API 不支持自定义请求头）。token 在升级前校验，校验失败返回 401 JSON 而非升级。设备限制：每用户最多 3 个并发连接（超过返回 429）。

### 协议

所有消息使用 JSON 信封格式：
```json
{"type": "<消息类型>", "payload": {...}}
```

### 客户端 → 服务端

| 类型 | Payload | 用途 |
|------|---------|------|
| `message.send` | `{to, content, content_type?}` | 发送私聊消息 |
| `message.recall` | `{message_id}` | 撤回已发送消息 |
| `ping` | — | 心跳（每 30 秒） |

### 服务端 → 客户端

| 类型 | Payload | 用途 |
|------|---------|------|
| `message.new` | `{id, from, content, content_type?, created_at}` | 收到新消息 |
| `message.ack` | `{id, status}` | 送达确认（status: "sent" 或 "delivered"） |
| `message.recalled` | `{message_id, recalled_at}` | 消息已撤回通知 |
| `pong` | — | 心跳响应 |
| `error` | `{code, message}` | 服务端错误 |

### 错误码

| Code | 含义 |
|------|------|
| `parse_error` | JSON 解析失败 |
| `invalid_message` | 校验失败（空内容、过长等） |
| `not_friends` | 双方不是好友 |
| `server_error` | 内部错误 |
| `message_not_found` | 撤回时消息不存在 |
| `not_sender` | 非发送者尝试撤回 |
| `recall_time_exceeded` | 超过 2 分钟撤回窗口 |

### 心跳机制

服务端发起 ping：每 54 秒发送 WebSocket ping 帧，浏览器自动回复 pong。pongWait 为 60 秒。60 秒内未收到 pong → readPump 退出 → 清理连接。

为什么服务端发起？浏览器 WebSocket API 不暴露 ping/pong 控制帧给 JavaScript，只有浏览器内部的 WebSocket 实现能响应 ping。服务端是需要感知死连接的一方（清理 Hub 状态和释放资源）。
