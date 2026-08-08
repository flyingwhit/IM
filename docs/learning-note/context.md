# Go Context — 超时、取消、值传递

## Context 是什么

每个请求的"通行证"，做三件事：

| 能力 | 方法 | 场景 |
|------|------|------|
| 超时 | `WithTimeout` / `WithDeadline` | DB 查询最多等 2s |
| 取消 | `WithCancel` | 用户断连，级联停止 |
| 传值 | `WithValue` | trace ID, user ID |

## 超时控制

```go
// hub.go — 用 1 秒超时保护事件循环
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()   // 用完必须释放
err := h.presence.SetOnline(ctx, userID)

// Redis 在 1s 内返回 → OK
// Redis 超时 → "context deadline exceeded"
```

**为什么重要**：如果 Redis 挂了，`SetOnline` 会永久卡住。Hub 的事件循环是单 goroutine，卡住意味着所有用户无法注册/注销。1s 超时保证了"挂不可怕，但不能拖累别人"。

## 取消传播

```go
// main.go
hubCtx, hubCancel := context.WithCancel(context.Background())

// broker.go
go func() {
    for {
        select {
        case <-ctx.Done():   // hubCancel() 调用后立即触发
            return
        case msg := <-ch:
            handle(msg)
        }
    }
}()

// 关闭时：hubCancel() → 所有 goroutine 级联退出
```

## 关键规则

- **永远第一个参数**：`func A(ctx context.Context, ...)`
- **不要存 struct 里**：context 是一次请求的，不是服务的
- **用完 cancel**：`WithTimeout`/`WithCancel` 不 defer cancel() 会泄漏 goroutine
- **不传 nil**：至少传 `context.Background()` 或 `context.TODO()`
- **Background vs TODO**：Background 是请求链起点，TODO 是占位
