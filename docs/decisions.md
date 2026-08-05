# 工程决策

## 1. 分层架构而非 Hexagonal/Clean Architecture

**决策**：Handler → Service → Repository 分层，具体类型依赖。

**原因**：Phase 1-3 的业务逻辑简单（CRUD + 认证 + 消息），Hexagonal 架构增加的接口层在当前阶段只有仪式感。当 Phase 4 需要替换 repository 实现时，Service 层的依赖已经可以抽象为接口。

**取舍**：单元测试需 fake 实现而非 mock 框架。对当前规模可接受。

## 2. UUID 而非 SERIAL 主键

**决策**：所有表使用 `UUID DEFAULT gen_random_uuid()`。

**原因**：如果先使用 SERIAL 以后迁移到 UUID，每个外键都会断裂。UUID 索引比 BIGINT 大约 2 倍（百万用户 ~32 MB vs ~16 MB），在现代硬件上可忽略。

## 3. 好友双向记录模型

**决策**：好友关系存储两行 `(A, B)` 和 `(B, A)`。

**替代方案**：单行 + `user_id < friend_id` 规范排序。

**原因**："我的好友"查询是 `WHERE user_id = ? AND status = 'accepted'`——简单的索引查找。单行方案需要 `WHERE (user_id = ? OR friend_id = ?)`，OR 两侧不能同时有效使用索引。

**取舍**：双倍存储。一条好友关系约 64 bytes，1000 好友 = 64 KB。可接受。

## 4. JWT Access Token + 不透明 Refresh Token

**决策**：Access token 是 JWT（无状态），refresh token 是随机 hex 字符串（有状态，存 Redis）。

**原因**：
- Access token 每次请求都校验 → 必须快。JWT 是本地 HMAC 校验，无网络调用。
- Refresh token 每 15 分钟才用一次 → Redis 查询可接受。
- 不透明 refresh token 不含声明——攻击者偷到也得不到信息。Redis 只存 SHA-256 哈希，Redis 被攻破时 raw token 不暴露。

**Token 旋转**：每次 refresh 使旧 token 失效并签发新对。如果攻击者盗用 refresh token，合法用户下次 refresh 会失败（旧 token 已被消费），自然发现泄露。

## 5. bcrypt 而非 Argon2

**决策**：使用 bcrypt DefaultCost (10)。

**原因**：`golang.org/x/crypto/bcrypt` 在标准库扩展生态中，Argon2 需要第三方库。bcrypt 的 ~100ms 哈希时间对学习项目足够——攻击者每 CPU 核每秒只能尝试 ~10 个密码。

## 6. Redis GETDEL 做 Token 刷新

**决策**：使用 Redis `GETDEL` 命令（原子性 获取+删除），而非分别 `GET` + `DEL`。

**问题**：两个并发刷新请求可能同时通过 `GET` 检查，然后各自执行 `DEL`，两份新 token 对都从同一个旧 token 签发——这是重放攻击。

`GETDEL` 将 check-then-act 竞态变为单原子操作——只有第一个调用者获取到值。

**取舍**：需要 Redis 6.2.0+。项目使用 Redis 7，无问题。

## 7. 数据库事务保护好友操作

**决策**：`AcceptRequest` 和 `RemoveFriend` 在 PostgreSQL 事务中执行。

**无事务的风险**：Accept 做两步：(1) UPDATE 已存在行的状态，(2) INSERT 反向行。如果第 2 步失败，数据库不一致——A 在 B 的好友列表中，但反之不然。

实现：`postgres.RunTx` 包装 pgx 事务生命周期。Repository 提供 `*Tx` 后缀的事务版本方法。

**取舍**：`*Tx` 后缀约定造成代码重复（每个方法有 pool 版和 tx 版），但保持直观可见。

## 8. 组合根模式（手动 DI）

**决策**：`cmd/server/main.go` 手动构造所有依赖并组装。无 DI 框架。

**原因**：10-15 个组件的依赖图在一个文件中完全可见，不需要框架的语义学习成本。

## 9. Hub + Client Goroutine 模型

**决策**：使用 gorilla/websocket chat 示例模式——Hub goroutine + channel-based 注册 + per-connection readPump/writePump。

**原因**：
- Go WebSocket 最广泛部署的模式，经过数千生产系统验证
- Hub 单 goroutine 事件循环消除变更时锁竞争
- 读写分离：readPump 处理慢消息时 writePump 仍能发送 ping

**取舍**：每个连接 2 个 goroutine（~8 KB 栈）。10K 连接 ≈ 80 MB goroutine 开销。接近 100K 连接时需要 shard Hub。

## 10. Channel-as-Signal（关闭通知）

**决策**：连接关闭通过关闭 `send` channel 发信号，而非 mutex + bool flag。

**原因**：关闭一个 channel 会广播给所有接收者。writePump 阻塞在 `<-c.send`，channel 关闭时立即以 `ok == false` 唤醒。无需轮询、条件变量或定时重试。

Go 惯用法："close a channel to signal completion"。

## 11. 先持久化后投递

**决策**：消息先写入 PostgreSQL，再路由到接收方 WebSocket。

**为什么先持久化**：
- 零消息丢失：服务器在接收和投递之间崩溃，消息已在 DB
- 离线消息免费：接收方离线时消息已持久化，上线时直接加载
- 实现简单：不需要确认协议、重试队列或对账机制

**取舍**：每条在线消息约 1-5ms DB 写入延迟。高性能 IM（WhatsApp、微信）先投递后异步持久化，但需要确认链（发送方→服务端→接收方→ACK）。此复杂度推迟到 Phase 4。

## 12. JSON 而非 Protobuf

**决策**：WebSocket 消息使用 JSON 编码。

**原因**：JSON 人类可读——开发期间调试价值巨大。Protobuf 需要 schema 编译、代码生成，线格式需专门工具检查。

**何时切换**：Phase 4 可扩展性。Protobuf 在线路上小 3-5 倍，编解码快 5-10 倍。`ws.Envelope` 类型抽象了编码——切换到 protobuf 只需改 marshal/unmarshal 调用，不改变 API。

## 13. 设备限制在 HTTP 层而非 WebSocket 层

**决策**：在 `gateway.Handler.Handle()` 升级前检查设备数，而非 Hub.Run() 升级后。

**原因**：
- 提前失败：客户端收到干净的 JSON 错误（429），而非 WebSocket close frame——更易处理
- 避免浪费：不在即将被拒绝的连接上做 HTTP 升级和 goroutine 创建
- 职责分离：Hub.Run() 只负责注册/注销，准入控制是 handler 的职责

**取舍**：`Find` 检查和 `register` channel 发送之间存在 TOCTOU 竞态——另一个连接可能在此期间注册。对 3 设备限制来说，此竞态无害（可能变成 4 而非 3）。更严格的限制应用 Redis 原子计数器。

## 14. 服务端发起心跳

**决策**：服务端每 54 秒发 WebSocket ping，浏览器自动回复 pong。

**为什么服务端发起**：
- 浏览器 WebSocket API 不向 JavaScript 暴露 ping/pong 控制帧
- 服务端是需要感知死连接的一方（清理 Hub 状态）
- gorilla/websocket 的 `SetReadDeadline` + `PongHandler` 模式实现简单

**常量**：`pongWait = 60s`（deadline），`pingPeriod = 54s`（= pongWait × 0.9，留 10% 网络抖动余量）。

## 15. 服务端生成消息 ID

**决策**：消息 ID 由服务端生成（`UUID DEFAULT gen_random_uuid()`），非客户端生成。

**原因**：服务端是消息排序和存在的唯一真相源。客户端生成 ID 需要：冲突解决（"两个客户端用了同一个 ID 怎么办？"）、去重（"这是重试还是重复？"）、信任（"客户端时间戳可信吗？"）。

**取舍**：发送方要等 ACK 才知道消息 ID（~5-10ms）。UI 可以先本地展示"乐观"版本，收到 ACK 后替换为确认版本。

## 16. 回调模式打破包循环依赖

**决策**：`gateway.Hub` 用函数指针回调（`OnMessage`、`OnConnect`），而非直接导入 `service.MessageService`。

**为什么**：`gateway` 已导入 `service`（`AuthService`）。如果 `service` 也导入 `gateway`（`Hub`），会形成循环依赖。Go 的解法：在消费者包中定义接口（`MessageRouter`），在生产者包中用函数指针回调。

**组合根模式的实际应用**：`main.go` 是唯一知道两个包的文件，也是组装回调的天然位置。

## 17. MessageService 的窄接口

**决策**：`MessageService` 将 repo 存储为未导出的窄接口（`messageStore`、`friendChecker`），而非具体 `*postgres.XxxRepo` 类型。

**原因**：无需真实数据库即可测试。接口是未导出的——外部包不应依赖它们，它们是 service 包的实现细节。

**取舍**：两层接口（service 中的未导出接口 + postgres 中的导出具体类型）。导出的 `NewMessageService` 构造函数仍接受具体类型，调用者（main.go）不需要知道未导出接口。

## 18. 每条消息做好友关系检查

**决策**：发送方必须是接收方的已接受好友才能发消息。自我消息豁免。

**原因**：使好友系统有意义——好友关系是基本社交契约。不做检查意味着任何用户可以向任何其他人发送垃圾消息。

**性能考虑**：每条消息需要一次 `FindByUserAndFriend` 查询（friends 表上的索引查找）。Phase 4 可缓存到 Redis 以减少 DB 负载。

## 19. 1v1 消息不需要 conversations 表

**决策**：会话从 messages 表通过 SQL 派生，不另建表。

**原因**：conversations 表增加写路径复杂度（每条 `INSERT INTO messages` 还要 `UPSERT INTO conversations`），而读路径的好处不在此阶段。

**何时重新考虑**：Phase 3 群聊。群聊有元数据（群名、头像、成员数）不应放在 messages 表上。那时 conversations 表变得必要。

## 20. 消息撤回设计

**决策**：
- 仅发送者可撤回
- 2 分钟时间窗口（与微信/Slack 一致）
- 幂等：重复撤回返回成功而非错误
- DB 保留原始内容但 API 层清空（审计需要）
- 广播 `message.recalled` 到双方（所有发送方设备 + 接收方若在线）
- 离线消息投递排除已撤回消息（`AND recalled_at IS NULL`）

**取舍**：接收方离线时撤回不会在重连时推送 `message.recalled` 通知——但消息本身不会被投递（已从 offline delivery 排除）。对话历史重新加载时会正确显示。

## 21. 跨实例消息路由：Redis Pub/Sub 广播

**决策**：使用单一共享 Redis Pub/Sub channel (`im:deliver`) 做跨实例路由。所有网关实例发布到同一 channel，收到后尝试本地投递。消息包含 `SourceInstance` ID，源实例跳过以避免双重投递。

**替代方案**：每实例独立 channel（需要知道所有实例 ID），或 Redis Streams（更强可靠但更复杂）。

**原因**：
- 广播是最简单的实现——新实例加入无需配置
- Pub/Sub 无存储开销——消息已持久化在 DB
- SourceInstance 过滤干净地解决双重投递
- 对于学习项目规模，广播的浪费可忽略

**取舍**：每条消息每个实例都会收到一个 Redis 消息。未来高吞吐时可切换为精确路由。

## 22. Kafka 渐进式引入

**决策**：不改变现有 DB 写入路径，Kafka 作为附加事件总线。Producer 是 fire-and-forget——失败只记日志，不阻塞消息发送。

**替代方案**：
- B1: 完全异步（Gateway 只写 Kafka，Worker 写 DB）——改变热路径语义，有消息丢失风险
- B2: 可切换的双路径——过度设计，当前不需要

**原因**：
- DB 是真相源——已有事务保证，Kafka 是目前不需要的优化
- Kafka 事件用于下游消费者（搜索索引、分析），不用于关键路径
- 可随时升级到 B1——只需改变一个写入点

**取舍**：每条消息多一次 Kafka produce 调用（<1ms，异步，不阻塞）。如果 Kafka 不可达，零影响。

## 23. 单二进制多模式

**决策**：一个二进制，通过 `SERVER_MODE` 环境变量选择模式（all/gateway/api/worker）。

**替代方案**：多个独立二进制（`cmd/gateway`, `cmd/api`, `cmd/worker`）。

**原因**：
- 一个 Docker 镜像、多种部署方式
- 开发阶段无需管理多个 go module 或构建目标
- Go 的 switch 模式干净——不同入口共享所有代码
- 未来可拆分为独立二进制（只需复制 main.go）

**取舍**：所有模式的依赖都打包在一个二进制中（Kafka client、pgx、Redis）。大小可忽略（~20MB）。
