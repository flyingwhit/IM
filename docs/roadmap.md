# 路线图

## 已完成

### Phase 1 — 核心后端

- 用户注册（bcrypt 密码哈希）
- 登录（JWT access token + 不透明 refresh token）
- JWT 认证中间件
- PostgreSQL 用户持久化
- Redis session 存储（refresh token）
- 好友系统（发送请求、接受、拒绝、删除）
- 好友双向记录模型（事务保护）
- Token 旋转（GETDEL 原子操作）

**关键实现点**：
- UUID 主键（为分布式场景做准备）
- 分层架构（Handler → Service → Repository）
- 组合根模式（手动 DI，无全局状态）
- 领域错误信封（AppError 在层间传播，handler 映射 HTTP 状态码）

### Phase 2 — 实时消息

- WebSocket 网关（gorilla/websocket）
- Hub + Client goroutine 模型
- 在线状态（Redis presence key + TTL，Hub 内存注册表）
- 私聊消息（先持久化后投递）
- 服务端心跳（每 54s ping，60s pongWait）
- 离线消息存储和重连投递
- 设备限制（每用户最多 3 并发连接）
- 消息送达确认（message.ack: sent/delivered）

**关键实现点**：
- 双路径架构（HTTP REST + WebSocket 并行，共享 AuthService）
- 循环依赖解决（MessageRouter 接口 + 回调函数指针）
- JSON 信封协议（`{"type": "...", "payload": {...}}`）
- 游标分页对话历史

### Phase 3 — 消息系统增强

- 消息撤回（2 分钟窗口，仅发送者，幂等，广播通知）
- 对话历史 API（`GET /api/v1/messages?peer=&before=&limit=`）
- 消息内容屏蔽（已撤回消息在 API 响应中清空内容）
- 离线投递排除已撤回消息

**关键实现点**：
- `recalled_at` 数据库列（迁移 000005）
- `message.recall` / `message.recalled` WebSocket 事件
- 服务层授权检查（发送者校验 + 时间限制 + 幂等）
- Repository 竞态保护（`UPDATE ... WHERE recalled_at IS NULL`）

---

## 当前状态

### 工作正常的功能

- 用户注册/登录/JWT 认证
- 好友系统（完整的请求-接受-拒绝-删除生命周期）
- WebSocket 实时消息
- 在线状态
- 离线消息投递
- 消息撤回
- 对话历史

### 功能缺口

- 无用户搜索/查找 API（`GET /api/v1/users/:id`）
- 在线状态无批量查询
- 好友列表无分页
- 消息无 REST 发送端点（仅 WebSocket）
- 已发出好友请求无法取消
- 在线状态无隐私控制

### 已知技术问题

- `MessageNewPayload` 缺少 `recalled_at` 字段
- `FriendWithUser` 字段语义在不同 API 中不一致
- 在线状态 REST API 仅依赖 Redis（Redis 故障 = 查询失败），Hub 内存状态未作为 fallback
- `model/request.go` 中 `FriendRequestAction` 死代码
- 已送达消息被撤回时，离线接收方重连后无 `message.recalled` 通知

---

## 下一步

### Phase 4 — 可扩展性（建议优先）

1. **多 WebSocket 网关**：跨实例消息路由（Redis pub/sub），在线状态共享存储
2. **Kafka 事件总线**：消息异步持久化管线，解耦实时投递和持久化
3. **服务分解**：将 auth、friend、message 拆分为独立服务

### Phase 5 — 可观测性

1. 结构化日志（zerolog 或 slog）
2. Prometheus metrics（连接数、消息延迟、错误率）
3. Health check 增强（PostgreSQL/Redis 连通性）

### Phase 6 — 生产就绪

1. Docker Compose 一键部署
2. CI/CD pipeline
3. 配置管理改进
4. 负载测试（WebSocket 连接和消息吞吐）

### 功能增强（可在任意阶段插入）

- 群聊（Phase 3.x）
- 已读回执（Phase 3.x）
- 消息搜索
- 文件/图片上传
- 推送通知（移动端后台）
