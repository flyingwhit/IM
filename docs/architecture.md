# 系统架构

## 概述

IM 后端采用 **分层架构**，四层职责：

```
Handler (Gin)  →  Service (业务逻辑)  →  Repository (数据访问)  →  DB/Cache
  HTTP 参数绑定      业务规则编排            SQL/Redis 命令        PostgreSQL/Redis
```

### 为什么用分层架构

- **可学习性**：每层单一职责，追踪请求链路直观
- **可演进性**：Phase 4 服务拆分时，层边界已存在，切分是机械性的
- **避免过度设计**：Phase 1-3 的业务逻辑简单，Clean Architecture 引入的接口层只是仪式感

取舍：层间用具体类型依赖（无接口层），单元测试需 fake 实现。对当前规模是可接受的。

## 模块职责

| 模块 | 职责 | 禁止 |
|------|------|------|
| `handler` | 绑定 HTTP 参数，调用 service，格式化响应 | 业务逻辑，直接 DB 访问 |
| `service` | 业务规则，编排，token 管理 | HTTP 相关，SQL |
| `repository` | 数据访问，SQL/Redis 命令 | 业务规则，HTTP 相关 |
| `middleware` | 横切关注点：认证、CORS | 业务逻辑 |
| `model` | 共享类型，领域错误 | 任何 I/O 或逻辑 |
| `config` | 环境变量加载 | 业务默认值 |
| `router` | 路由注册，中间件绑定 | handler 逻辑 |
| `gateway` | WebSocket 升级、连接生命周期、消息路由 | 业务逻辑、持久化 |
| `ws` | WebSocket 消息协议类型和编解码 | I/O 或业务逻辑 |

## 请求流

```
Client → Gin Router → 中间件 (JWT) → Handler → Service → Repository → DB/Redis
                                                        ↑
                                                AppError 在层间传播
                                                handler 将 sentinel 映射到 HTTP 状态码
```

关键设计：
- **Context 传播**：`c.Request.Context()` 贯穿所有层。如果客户端断开连接，context 取消，PostgreSQL 查询会自动中止。
- **错误约定**：Repository 返回 `AppError{Err: sentinel, Message: "人可读的消息"}`。Service 传播或包装。Handler 通过 `errorStatus()` 将 sentinel 映射为 HTTP 状态码。

## WebSocket 架构

### 双路径设计

```
                       ┌──────────────────────┐
                       │     Gin Router        │
                       │  /api/v1/*  │  /ws    │
                       │  (HTTP)     │  (WS)   │
                       └──────┬──────┴────┬─────┘
                              │           │
                        Handler       gateway.Handler
                         (req/resp)     (upgrade)
                              │           │
                        Service      ┌───┴────┐
                              │      │  Hub   │
                        Repository   │ Client │
                              │      └────────┘
                         DB/Redis        │
                                    AuthService
                                 (JWT 校验，与 HTTP 复用)
```

**为什么 WebSocket 不放 handler/?** HTTP handler 的生命周期以毫秒计，WebSocket 连接以分钟/小时计。两者放在同一包会模糊两种不同的执行模型：短生命周期的 goroutine-per-request vs 长生命周期的 goroutine-per-connection。

### Hub + Client Goroutine 模型

**Hub** — 单 goroutine 拥有连接注册表。所有 register/unregister 变更通过 channel 到达，顺序处理。变更期间无需锁。

**Client** — 每个 WebSocket 连接有两个 goroutine：
- **readPump**：阻塞读取 WebSocket → 分发到 handler。出错时（断开连接），发信号 unregister 并关闭 send channel。
- **writePump**：从有缓冲 channel 读取 → 写入 WebSocket。同时发送周期性 ping。channel 是唯一写入者（gorilla/websocket 禁止并发写）。

### 连接生命周期

```
  连接                      活跃                      断开
  ─────────               ─────────                 ────────────
  GET /ws?token=           readPump:                 readPump:
  ├─ JWT 校验               ReadMessage() loop        ReadMessage 错误
  ├─ 设备数限制 (≤3)        │                         │
  ├─ HTTP 101 Upgrade       ├─ message.send           defer:
  ├─ Client 创建            │  → MessageService       ├─ close(send)
  ├─ register → Hub         │                         ├─ conn.Close()
  ├─ go writePump()         writePump:                └─ unregister → Hub
  └─ go readPump()          ├─ ← send channel
                             ├─ ticker → ping         writePump:
                             └─ WriteMessage()        ← channel 关闭
                                                      WriteCloseMessage()
                                                      return
```

**关键洞察**：HTTP handler (`Handle`) 启动两个 goroutine 后立即返回。连接剩余的全部生命周期（分钟到小时）由 readPump 和 writePump 管理。Gin 的超时不再适用——连接已从 HTTP hijack 出来。

### 消息路由

```
发送方 readPump → MessageService → DB (持久化) → Hub.SendToUser(receiverID)
                                                        │
                                                  Hub.Find(receiverID)
                                                  → 接收方所有 Client
                                                  → 每个 Client 的 send channel
                                                  → 接收方 writePump
```

`Hub.SendToUser` 将消息信封只 marshal **一次**，然后将相同 `[]byte` 推送到该用户的所有连接。当用户有多设备时避免重复 JSON 编码。

### 并发安全

| 操作 | 机制 | 原因 |
|------|------|------|
| Register/unregister | Channel → 单 goroutine | 序列化 map 变更 |
| Find/IsOnline | RWMutex 读锁 | 多并发读，互不阻塞 |
| WebSocket 写 | 每连接单 goroutine | gorilla/websocket 不支持并发写 |
| 发送到连接 | 有缓冲 channel (256) + 非阻塞发送 | 防止慢客户端阻塞 Hub |

## 消息系统架构 (Phase 3)

### 消息流

```
  Client A                         Server                          Client B
     │                                │                                │
     │── message.send ──────────────▶│                                │
     │                                │  readPump → Hub.OnMessage     │
     │                                │  → MessageService             │
     │                                │     ├─ 解析 + 校验            │
     │                                │     ├─ 检查好友关系            │
     │                                │     ├─ INSERT INTO messages   │
     │                                │     ├─ Hub.Find(B)            │
     │                                │     └─ 路由:                  │
     │                                │        在线 → message.new ──▶│
     │                                │        离线 → 跳过             │
     │◀── message.ack ───────────────│                                │
```

### 消息状态生命周期

```
  sent ─────────→ delivered ─────────→ read (Phase 3.x)
  (已持久化)       (WebSocket 已写入)    (客户端 ACK)
```

- **sent**：INSERT 时的默认值。消息在 DB 中但接收方离线。
- **delivered**：Hub 找到至少一个连接并推送消息。
- **read**：保留。将通过客户端的 `message.read` WebSocket 帧实现。

### 离线消息投递

WebSocket 连接时 (`OnConnect` → `DeliverOfflineMessages`)：
1. 查询 `WHERE receiver_id=$1 AND status='sent' AND recalled_at IS NULL ORDER BY created_at ASC`
2. 逐条推送 `message.new` 到接收方
3. 更新状态为 `delivered`

### 消息撤回

```
  Client A                         Server                          Client B
     │                                │                                │
     │── message.recall ─────────────▶│                                │
     │                                │  MessageService.handleRecall  │
     │                                │     ├─ 解析 message_id        │
     │                                │     ├─ FindByID               │
     │                                │     ├─ 鉴权 (仅发送者)         │
     │                                │     ├─ 时间限制 (2分钟)        │
     │                                │     ├─ 幂等检查               │
     │                                │     ├─ UPDATE recalled_at     │
     │                                │     └─ 广播:                  │
     │◀── message.recalled ──────────│                                │
     │                                │── message.recalled ──────────▶│
```

- **授权**：只有消息发送者可以撤回
- **时间限制**：2 分钟窗口（与微信/Slack 一致）
- **幂等**：已撤回消息重复请求返回成功但不更新 DB
- **广播**：通知发送方（所有设备）和接收方（若在线）
- **对话历史**：已撤回消息的 content 在 API 响应中被清空，保留 `recalled_at` 时间戳

### 避免循环依赖

`gateway` 和 `service` 包有双向关系：
- `gateway` 需要 `service`（`AuthService`、`MessageService` 回调）
- `service` 需要 `gateway.Hub`（消息路由）

通过两种模式解决：

1. **消费者包中定义接口**：`service` 定义 `MessageRouter` 接口（`SendToUser`、`IsOnline`），`gateway.Hub` 通过 Go 结构类型满足它
2. **函数指针回调**：Hub 存储 `OnMessage`、`OnConnect` 函数指针，由 `main.go` 设置

## 多实例扩展架构 (Phase 4)

### 跨实例消息路由

```
Gateway-1                      Gateway-2
   │                               │
   │ 1. A sends msg to B           │
   │ 2. DB insert                  │
   │ 3. Hub.SendToUser(B)          │
   │    ├─ 本地投递 (若 B 在本地)    │
   │    └─ broker.Publish() ────► Redis Pub/Sub
   │                                      │
   │                               broker.Subscribe 收到
   │                               ├─ 跳过 (source=gw-1)
   │                               └─ 本地投递 (若 B 在本地)
```

**关键设计**：
- Hub.SendToUser 本地投递后 publish 到 Redis `im:deliver`
- 所有实例订阅同一 channel，收到后尝试本地投递
- 源实例跳过自己发出的消息（SourceInstance 匹配）
- IsOnline 先查本地 Hub，再查 Redis（跨实例感知）

### Kafka 事件管道

```
Gateway ── DB write (同步) ──► PostgreSQL (source of truth)
        └── Kafka produce (异步 fire-and-forget) ──► Worker consume ──► (未来: 搜索索引)
```

**设计决策**：渐进式引入（B3 方案）。Producer 失败不阻塞消息发送——DB 是唯一真相源。

### 多模式启动

同一二进制，通过 `SERVER_MODE` 环境变量控制：
- `all`：全部组件（默认，开发用）
- `worker`：仅 Kafka consumer（独立扩缩）
- `gateway` / `api`：未来独立部署

## 依赖注入

`cmd/server/main.go` 是**组合根**——所有具体类型被组装在一起的唯一位置。没有 DI 框架，没有全局状态。每个组件通过构造函数接收其依赖。

## 演进路径

- **Phase 1（核心后端）**：✅ 完成。用户注册/登录、JWT 认证、好友系统、PostgreSQL/Redis。
- **Phase 2（实时消息）**：✅ 完成。WebSocket 网关、在线状态、私聊、心跳、离线消息。
- **Phase 3（消息系统）**：✅ 完成。消息持久化、撤回、对话历史、游标分页。
- **Phase 4（可扩展性）**：✅ 完成。多网关实例（Redis Pub/Sub 跨实例路由）、Kafka 事件总线（异步消息管道）、多模式启动（all/gateway/api/worker）。
- **Phase 5（可观测性）**：✅ 完成。结构化日志 (slog)、Prometheus metrics (RED 方法 + WebSocket + Kafka + Go runtime)、Grafana 配置、健康检查 (liveness + readiness)。
- **Phase 6（生产就绪）**：✅ 完成。Dockerfile 多阶段构建、docker-compose 整合 (含可选 Kafka profile)、配置校验、优雅关闭 (30s 超时保护)、GitHub Actions CI、vegeta 负载测试脚本。

## 部署架构

### Docker 多阶段构建

```
Stage 1 (golang:1.26-alpine)        Stage 2 (alpine:3.22)
  COPY go.mod go.sum ./                COPY --from=builder /app/server .
  RUN go mod download                  RUN apk add ca-certificates tzdata
  COPY . .                             USER appuser (非 root)
  RUN go build -o server               EXPOSE 8080
```

最终镜像 ~15MB，不含编译工具。`CGO_ENABLED=0` 产生纯静态二进制，可运行在 Alpine (musl libc) 上。

### docker-compose 服务编排

```
docker compose up -d                        # PG + Redis + IM server
docker compose --profile kafka up -d         # 附加 Kafka + Zookeeper
docker compose -f ... -f configs/docker-compose.observability.yml up -d  # 附加 Prometheus + Grafana
```

Kafka 通过 Docker Compose **profiles** 变为可选：不传 `--profile kafka` 时服务正常启动，Kafka 集成被禁用。

### 优雅关闭顺序

```
1. Hub         — 停止接受 WebSocket 投递，退订 broker
2. HTTP server — 排空进行中的请求 (5s grace)
3. Deferred    — broker.Close → kafka.Close → redis.Close → pool.Close (LIFO)
```

30 秒总超时保护：任何 `Close()` 卡住时进程强制退出。
