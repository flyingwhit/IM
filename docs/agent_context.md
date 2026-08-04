# Agent Context

AI agents working on this repository should read this file first.

## 项目概述

IM（Instant Messaging）后端，Go 语言编写。学习项目，目标是掌握后端工程、分布式系统设计和 AI 辅助开发。

## 技术栈

| 组件 | 技术 |
|------|------|
| HTTP 框架 | Gin |
| 数据库 | PostgreSQL (pgx/v5) |
| 缓存 | Redis (go-redis/v9) |
| WebSocket | gorilla/websocket |
| 认证 | JWT HS256 + bcrypt |
| 协议 | JSON (REST + WebSocket) |

## 当前实现状态

### 已完成 (Phase 1-3)

- **认证**：注册/登录/JWT/refresh token 旋转/logout
- **好友**：完整的请求-接受-拒绝-删除生命周期，双向记录模型（事务保护）
- **WebSocket**：Hub+Client goroutine 模型，心跳，设备限制 (≤3)
- **消息**：先持久化后投递，送达确认 (ACK)，离线投递
- **撤回**：2 分钟窗口，仅发送者，幂等，广播通知
- **在线状态**：Redis presence key + Hub 内存注册表
- **对话历史**：游标分页

### 未完成

- 用户搜索/查找 API
- 批量在线状态查询
- 群聊
- 已读回执
- 文件上传
- 推送通知

## 目录结构

```
cmd/server/main.go          — 组合根（所有依赖组装）
internal/
  config/                    — 环境变量配置
  model/                     — 领域类型、DTO、领域错误
  middleware/                 — JWT 认证、CORS
  handler/                   — HTTP handler（auth, user, friend, message, presence, health）
  service/                   — 业务逻辑（auth, friend, message）
  repository/
    postgres/                — PostgreSQL 数据访问
    redis/                   — Redis（session, presence）
  gateway/                   — WebSocket（Hub, Client, Handler）
  router/                    — Gin 路由注册
  ws/                        — WebSocket 协议类型
migrations/                  — SQL 迁移文件
tools/im-client/             — 浏览器测试客户端（vanilla JS）
docs/                        — 文档（中文）
```

## 架构关键决策

1. **分层架构**：Handler → Service → Repository，具体类型依赖
2. **循环依赖解决**：`service.MessageRouter` 接口 + Hub 函数指针回调（`OnMessage`、`OnConnect`）
3. **好友双向记录**：`(A, B)` 和 `(B, A)` 两行，避免 OR 查询
4. **先持久化后投递**：消息先写 PostgreSQL 再路由，零消息丢失
5. **服务端生成 ID**：所有 ID 是 UUID（消息、用户、好友关系）
6. **JSON 协议**：WebSocket 使用 `{"type": "...", "payload": {...}}` 信封
7. **游标分页**：消息历史用 `created_at < cursor`，不用 OFFSET

## 已知问题（给 review/testing agent）

| # | 问题 | 文件位置 |
|---|------|----------|
| 1 | 缺少 `GET /api/v1/users/:id` — 无用户查找 API | `internal/router/router.go` |
| 2 | 在线状态仅查 Redis — Redis 故障 = 所有查询 500 | `internal/handler/presence.go` |
| 3 | `MessageNewPayload` 缺少 `recalled_at` — 客户端难以区分空消息和已撤回 | `internal/ws/types.go:54-61` |
| 4 | `FriendRequestAction` 死代码 — 定义但未使用 | `internal/model/request.go:35-38` |
| 5 | `UpdateProfile` 无输入验证 — nickname/avatar_url 无长度/格式约束 | `internal/model/request.go:28-31` |
| 6 | 好友列表无分页 | `internal/handler/friend.go:43-53` |
| 7 | 消息无 REST 发送端点 | `internal/router/router.go` |
| 8 | 已送达消息离线撤回 — 接收方重连后无 `message.recalled` 通知 | `internal/service/message.go:262-282` |
| 9 | 迁移 000003 的表未使用 — Redis 代替了 PostgreSQL 存 refresh token | `migrations/000003_*` |
| 10 | 数据库 migration 000004 不含 `recalled_at` 列 — 由 000005 补充 | `migrations/000004_*` |

## 给 coding agent 的注意事项

- **函数长度**：保持在 50 行以内
- **注释**：解释 WHY，不解释 WHAT
- **提交格式**：`feat:`/`fix:`/`refactor:`/`test:`/`docs:`
- **测试**：优先写 Go 标准测试，不引入外部测试框架
- **错误处理**：用 `model.AppError` + sentinel，通过 `errorStatus()` 映射 HTTP 状态码
- **事务**：多步写操作用 `postgres.RunTx`，repository 方法提供 `*Tx` 事务版本
- **依赖注入**：始终通过构造函数，在 `main.go` 中组装
- **教学模式**：先解释设计再写代码，不直接给出最终实现

## 给 testing agent 的注意事项

- Service 测试用 fake 实现（见 `message_test.go` 中的 `fakeMessageStore`、`fakeRouter` 等）
- Repository 测试可以 mock pgxpool 或用 testcontainers
- Hub 测试见 `hub_test.go` — 不依赖真实 WebSocket 连接
- 运行测试：`go test ./...`
- 构建并在 tmp/ 下运行：`go build -o ./tmp/server ./cmd/server`
