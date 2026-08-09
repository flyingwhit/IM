# 学习笔记

## Go 并发：优雅关闭

`signal.Notify` 捕获 OS 信号 → `srv.Shutdown(ctx)` 停止接受新连接但让进行中的请求完成 → `defer` 按创建相反顺序关闭资源。

关键教训：`log.Fatalf` 调用 `os.Exit(1)` 跳过所有 defers。永远不要在需要清理的 goroutine 中使用。用 channel 将错误传播到主 goroutine。

## Go 并发：Channel-as-Signal

Go 中关闭 channel 是广播——所有阻塞在 `<-ch` 的 goroutine 立即唤醒。

我们的连接关闭利用这一点：readPump 检测到断开 → `close(c.send)` 广播"连接结束"。writePump 阻塞在 `<-c.send`，channel 关闭时以 `ok == false` 唤醒，干净退出。

**Why not done channel?** 单独的 `done chan struct{}` 要求 writePump 同时 select `send` 和 `done`——更复杂。关闭 send channel 本身一举两得：唤醒 writePump 并阻止新消息发送。

## Go 并发：非阻塞 Channel 发送

```go
select {
case c.send <- data:
    // 消息入队
default:
    // buffer 满——丢弃消息
}
```

**为什么丢弃而非阻塞？** 如果 writePump 慢（网络拥塞），阻塞发送方（Hub.SendToUser）会阻塞整个消息路由管道。一个慢客户端会延迟所有客户端的消息。丢弃是安全的——消息已持久化在 PostgreSQL，接收方可后续取回。

## Go 并发：Hub Goroutine 模式

单 goroutine 拥有可变状态。所有变更通过 channel 到达，顺序处理——变更期间无需锁。

"通过共享内存来通信"的反转：不是多个 goroutine 共享 map + mutex 保护，而是一个 goroutine 拥有 map，其他 goroutine 通过 channel 与它通信。

**为什么 channel 做变更但 RWMutex 做读？** Find/IsOnline 需要立即返回——不能等 channel 往返。读远多于写且互不冲突，RWMutex 理想：多并发读者，排他写者。

## Go 设计：打破循环依赖

**问题**：`gateway` 导入 `service`，`service` 需要 `gateway.Hub`——Go 禁止循环导入。

**解法——两种互补模式**：

1. **消费者包中定义接口**：`service` 定义 `MessageRouter` 接口。`gateway.Hub` 通过 Go 结构类型满足它。打破了一个方向的循环。

2. **函数指针回调**：Hub 存储函数指针而非导入 service 类型。组合根（`main.go`）设置这些指针。Hub 完全不知道它们做什么。

Go 的结构类型意味着你可以在使用的包中定义接口，其他包的类型只要有匹配的方法就自动满足——与 Java/C# 必须显式声明 `implements` 相反。

## 安全：Token 重放攻击

Refresh token 应单次使用。没有原子性的话，两个并发请求可能都通过校验，都签发新 token——重放攻击。

Redis `GETDEL` 解决了这个问题：原子性的"获取值并删除 key"。第一个调用者获取值；第二个获取 `nil`。没有竞态窗口。

**生产实践**：对任何共享资源上的 check-then-consume 操作，使用原子原语。数据库有 `SELECT ... FOR UPDATE`。Redis 有 `GETDEL`、`SET NX` 和 Lua 脚本。

## 安全：用户枚举

登录时返回"用户名或密码错误"而非"用户不存在"/"密码错误"。攻击者无法区分两者，防止枚举有效用户名。

## 数据库：为什么事务重要

`AcceptRequest` 做两次写：UPDATE + INSERT。没有事务的情况下，如果 INSERT 在 UPDATE 成功后失败，数据库处于不一致状态——一半好友关系存在。

PostgreSQL 事务包装多条语句为 all-or-nothing 单元。`defer tx.Rollback(ctx)` 确保事务一定会被清理，即使函数 panic。

**生产实践**：永远不要假设两次顺序写是安全的。始终问："如果第二次失败了会怎样？"如果答案是"数据不一致"，就需要事务。

## 数据库：游标分页用于实时数据

Offset 分页对频繁变化的数据有问题：新消息到达导致行位移，翻页出现重复或遗漏。

游标分页：`WHERE created_at < $cursor ORDER BY created_at DESC LIMIT n`。用上一页最后一条的时间戳作为下一页的起点。新消息不影响已有页面。

**取舍**：游标分页不能跳转到任意页——只有"上一页""下一页"。对聊天记录这是正确行为：用户是滚动，不是输入"第 5 页"。

## API 设计：错误码映射

领域错误（Go `error` 值）不应知道 HTTP 状态码——这会把业务逻辑耦合到 HTTP。我们的方式：
- Repository/Service 返回 `AppError{Err: sentinel, Message: "..."}`
- Handler 通过 `errorStatus()` 将 sentinel 映射为 HTTP 状态码
- 新增错误类型只需更新 `errorStatus()`

**生产实践**：这是"错误信封"模式。保持传输层（HTTP、gRPC 等）独立于领域逻辑。

## 项目结构：组合根

`cmd/server/main.go` 在一个地方组装所有依赖。每个组件通过构造函数接收它需要的。没有包级别全局变量，没有 `init()` 函数，没有 service locator。

**为什么重要**：可以读 `main.go` 就理解整个系统的依赖图。测试时可以构造 Service + mock Repository——不需要 monkey-patching。

## WebSocket：HTTP 如何变为长连接

1. 客户端发送 `GET /ws` 携带 `Upgrade: websocket` 和 `Connection: Upgrade` 头
2. 服务器回复 `101 Switching Protocols`——TCP 连接不再是 HTTP
3. 同一 TCP socket 现在承载 WebSocket 帧，不是 HTTP 请求

升级后 HTTP 服务器（`http.Server`、Gin）不再管理此连接。`ReadTimeout`/`WriteTimeout` 不再适用。连接的生命周期完全在应用手中——因此需要 readPump/writePump goroutine。

**为什么 JWT 在查询参数而非请求头**：WebSocket JavaScript API（`new WebSocket(url)`）只接受 URL，无法设置自定义请求头。查询参数简单、通用，所有 WebSocket 客户端都支持。

## WebSocket：为什么禁止并发写

gorilla/websocket 明确文档说明连接最多支持一个并发 reader 和一个并发 writer。两个 goroutine 同时调用 `WriteMessage` 会损坏 WebSocket 帧流。

**解决方案**：专用写 goroutine（writePump）从 channel 读取。任何想发数据的地方写入 channel，不写 WebSocket。channel 序列化所有写操作。

这是 **fan-in 模式**：多个发送方 → channel → 单消费者。

## Redis：TTL 过期

Refresh token 存 Redis 使用 `SET key value EX <seconds>`。TTL 过期后 Redis 自动删除 key：
- 不需要 cron job 清理过期 token
- Token 过期免费——只要不续期
- Redis 重启 token 丢失（可接受——用户重新登录即可）

在线状态同理：`presence:<userID>` key 有 60s TTL，作为崩溃安全网。正常断开时 Hub 主动 DEL，崩溃时 TTL 自动清除僵尸 key。

## 设计：服务端生成 vs 客户端生成 ID

**服务端生成（我们的方式）**：
- 服务端是唯一真相源
- 不需要冲突解决
- 客户端不能伪造时间戳或 ID
- 取舍：客户端等 ACK 才知道 ID

**客户端生成（部分 P2P 系统）**：
- 乐观 UI：客户端立刻知道 ID
- 需要冲突解决
- 服务端必须校验唯一性

对于客户端-服务器 IM 系统，服务端生成 ID 是标准选择。~5ms ACK 延迟用户感知不到。

## 设计：何时使用有缓冲 vs 无缓冲 Channel

| Channel | 缓冲 | 原因 |
|---------|------|------|
| `Hub.register` | 64 | Handler 不应阻塞等待 Hub |
| `Hub.unregister` | 64 | readPump 已在关闭路径中，不能停顿 |
| `Client.send` | 256 | 解耦 Hub 路由和网络 I/O 速度 |

**经验法则**：当发送方和接收方速度不同且发送方不应等待时，用缓冲。当你需要发送方阻塞作为反压时，不缓冲。

## Docker：多阶段构建

Go 的最终产物是一个静态二进制——不需要 Go SDK 来运行。Docker 多阶段构建利用这一点：

- **Stage 1 (build)**：完整 Go 工具链，编译二进制。这一层的膨胀（~800MB）在最终镜像中被丢弃。
- **Stage 2 (run)**：只拷贝二进制 + ca-certificates + tzdata。最终镜像 ~15MB。

关键标志：
- `CGO_ENABLED=0`：纯 Go 静态链接，不依赖 glibc。可以在 Alpine (musl libc) 或 scratch 上运行。
- `-ldflags="-s -w"`：strip 调试信息，二进制缩小 ~30%。代价是 panic 时没有文件:行号——生产环境通过日志追踪，可接受。
- `USER appuser`：容器内非 root。即使应用被 RCE，攻击者也拿不到容器 root 权限。

## Docker Compose：profiles 实现可选服务

Kafka 不是必须的——Phase 4 设计保证了 Kafka 故障不影响消息收发。用 `profiles: ["kafka"]` 让 Kafka+Zookeeper 默认不启动：

```yaml
kafka:
  profiles: ["kafka"]
```

`docker compose up -d` 启动 PG + Redis + server。
`docker compose --profile kafka up -d` 再加 Kafka。

这比维护两个 docker-compose 文件更干净——基础服务始终相同，只是选择性添加 profile 服务。

## CI/CD：Go 项目的 GitHub Actions

Go CI 的流水线简单到只有三步：

1. **Lint**：`golangci-lint` 聚合了多个 linter (govet, staticcheck, errcheck…)。发现问题在 CI 跑之前人工修复。
2. **Test**：`go test -race -shuffle=on`。`-race` 检测数据竞争（慢 ~10x 但在 CI 值得），`-shuffle=on` 随机化测试顺序暴露隐式依赖。
3. **Build**：`go build` 验证编译，不产生 artifact（CI 环境不需要二进制）。

## Go：优雅关闭的关闭顺序

关闭一个多组件 Go 服务不是随手 `defer` 那么简单。我们学到了三个原则：

**1. 顺序很重要**。Hub 必须在 HTTP server 之前停——HTTP server 关闭时可能还有未完成的 WebSocket 关闭帧要写，如果 Hub 已经停了，send channel 已关闭，写会 panic。

**2. Defer 是 LIFO**。Go 的 defer 栈是后进先出。所以正确的 defer 顺序是：`pool.Close() → redis.Close() → broker.Close() → hubCancel()`。执行时反转：hubCancel (先) → ... → pool.Close (后)。

**3. 救命超时**。如果 `pool.Close()` 因为网络分区卡住 5 分钟，Kubernetes 最终会 SIGKILL。但在本地开发时进程就 hang 了。30 秒的 `select { case <-done: ... case <-time.After(30s): os.Exit(1) }` 是低成本保险。

## Go：defer 与显式调用的配合

`runAll()` 同时使用了 `defer hubCancel()` 和显式 `hubCancel()`。这看起来重复，但各有用途：

- **defer**：保证函数提前返回时（如服务器启动失败）清理执行
- **显式调用**：在优雅关闭中精确控制时机

对 Context 的 cancel 函数调用多次是安全的（no-op after first call），所以两者不冲突。

## Go：配置校验的模式

配置校验采用 "warnings not errors" 原则：

- 用默认 JWT secret 启动？打印警告但不阻止——开发环境 OK。
- Access secret 和 refresh secret 相同？打印警告——生产环境应该不同。
- Secret 太短（<16 字符）？打印警告——容易被暴力破解。

**为什么不用 error 阻止启动？** 阻止启动意味着配置不完美的实例无法运行。在生产环境，配置由运维团队管理，错误阻止启动是正确的。在开发环境，开发者只想快速跑起来——warnings 提供可见性而不阻碍。

---

# 专题笔记

以下是从 `docs/learning-note/` 合并进来的专题笔记。

## Go Context — 超时、取消、值传递

### Context 是什么

每个请求的"通行证"，做三件事：

| 能力 | 方法 | 场景 |
|------|------|------|
| 超时 | `WithTimeout` / `WithDeadline` | DB 查询最多等 2s |
| 取消 | `WithCancel` | 用户断连，级联停止 |
| 传值 | `WithValue` | trace ID, user ID |

### 超时控制

```go
// hub.go — 用 1 秒超时保护事件循环
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()   // 用完必须释放
err := h.presence.SetOnline(ctx, userID)
```

**为什么重要**：如果 Redis 挂了，`SetOnline` 会永久卡住。Hub 的事件循环是单 goroutine，卡住意味着所有用户无法注册/注销。1s 超时保证了"挂不可怕，但不能拖累别人"。

### 取消传播

```go
// main.go
hubCtx, hubCancel := context.WithCancel(context.Background())
// hubCancel() → 所有 goroutine 级联退出
```

### 关键规则

- **永远第一个参数**：`func A(ctx context.Context, ...)`
- **不要存 struct 里**：context 是一次请求的，不是服务的
- **用完 cancel**：`WithTimeout`/`WithCancel` 不 defer cancel() 会泄漏 goroutine
- **不传 nil**：至少传 `context.Background()` 或 `context.TODO()`

## Go Defer / Panic / Recover

### 三者关系

```
panic("出事了")
    │
    ├── 没人 recover → 程序崩溃（stack trace）
    └── 有人 recover → defer func() { recover() } 捕获，继续运行
```

### Defer — LIFO 栈

```go
defer A()   // 第3个执行
defer B()   // 第2个执行
defer C()   // 第1个执行
```

### 三个经典模式

**资源清理**：
```go
pool, _ := postgres.NewPool(ctx, dsn)
defer pool.Close()
```

**事务回滚**（最经典的 Go 模式）：
```go
tx, _ := pool.Begin(ctx)
defer tx.Rollback(ctx)   // 先注册回滚
if err := fn(tx); err != nil { return err }
return tx.Commit(ctx)     // Rollback 在已提交的事务上是 no-op
```

**defer + recover**：
```go
defer func() {
    if r := recover(); r != nil {
        slog.Warn("send on closed channel (recovered)")
    }
}()
c.send <- data   // 如果 channel 已关闭会 panic，recover 兜底
```

### Panic 使用规则

| 用 panic | 用 error |
|----------|----------|
| 程序员的 bug | 运行时条件（网络超时） |
| 启动时 / 不可恢复 | 可恢复 |

### 面试重点

- `defer` 的参数在 defer 语句执行时就求值
- 命名返回值 + defer 可以修改返回值
- panic 只用于不可恢复的编程错误
- recover 只捕获当前 goroutine 的 panic

## Go Error Handling — 不是异常，是返回值

### 核心理念

Go 没有 try-catch。error 就是一个普通返回值：

```go
val, err := someFunc()
if err != nil { /* 你决定怎么处理 */ }
```

### `%w` vs `%v`

```go
err1 := errors.New("database connection refused")
err2 := fmt.Errorf("query friend: %w", err1)  // %w 保留链
err3 := fmt.Errorf("query friend: %v", err1)  // %v 打断链
errors.Is(err2, err1)  // true
errors.Is(err3, err1)  // false
```

**规则**：向上抛时用 `%w`；只打印日志时用 `%v`。

### 项目中的错误设计

```go
var (
    ErrNotFound     = errors.New("resource not found")
    ErrInvalidInput = errors.New("invalid input")
    ErrForbidden    = errors.New("forbidden")
)

type AppError struct {
    Err     error   // 哨兵，机器判断用
    Message string  // 给人看的
}
```

### 面试要点

- error 是值，不是异常
- %w 保留错误链，errors.Is/As 能沿着链找
- 哨兵用 errors.Is，自定义类型用 errors.As
- 不要吞错误，除非明确不重要

## Go Interface — 隐式满足 & 接口隔离

### 核心概念

Go 的接口是**隐式满足**的：不需要声明 `implements`，有方法就自动实现。

### 项目例子：MessageRouter

`service/routing.go` 定义接口，`gateway.Hub` 隐式满足：

```go
type MessageRouter interface {
    SendToUser(userID string, env *ws.Envelope)
    IsOnline(userID string) bool
}
```

**好处**：`service` 包不需要 import `gateway` 包。

### 接口隔离原则 (ISP)

`service/message.go` 定义了 4 个小接口，而不是 1 个大接口。调用方不依赖不需要的方法。

| 接口 | 生产实现 | 测试 fake |
|------|---------|-----------|
| `messageStore` | `*postgres.MessageRepo` | `fakeMessageStore` |
| `friendChecker` | `*postgres.FriendRepo` | `fakeFriendChecker` |
| `MessageRouter` | `*gateway.Hub` | `fakeRouter` |

### 面试要点

- 隐式满足：不改上游代码就能给已有类型实现新接口
- 接口要小：Go 社区建议 1-3 个方法
- 用接口做依赖注入：参数用接口，测试传 fake

## Go Testing — 标准库 + 接口 = 可测试性

### Table-Driven Tests（Go 标志模式）

```go
func TestValidate(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{{"empty", "", true}, {"valid", "hello", false}}
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { /* ... */ })
    }
}
```

加新用例就是加一行 struct。

### Fake 替代真实依赖

```go
type fakeMessageStore struct {
    messages map[string]*model.Message
}
func (f *fakeMessageStore) Insert(_ ctx, msg) error { f.messages[msg.ID] = msg; return nil }
```

### 关键命令

```bash
go test ./...                  # 全跑
go test ./internal/service/ -v # 一个包
go test -run TestXxx -v        # 单个测试
go test -cover                 # 覆盖率
```
