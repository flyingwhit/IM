# Go Interface — 隐式满足 & 接口隔离

## 核心概念

Go 的接口是**隐式满足**的：一个类型不需要声明 `implements InterfaceX`，只要它有接口要求的全部方法，就自动实现了该接口。

```go
// Java/C#: 显式声明
class Dog implements Animal { ... }

// Go: 不声明，自动满足
type Dog struct { ... }
func (d Dog) Speak() string { return "woof" }
// Dog 自动实现了 interface { Speak() string }
```

## 项目例子1：MessageRouter

`service/routing.go` 定义了一个只有 2 个方法的接口：

```go
type MessageRouter interface {
    SendToUser(userID string, env *ws.Envelope)
    IsOnline(userID string) bool
}
```

`gateway.Hub` **没有写** `implements MessageRouter`，但它有这两个方法，所以隐式满足：

```go
// main.go
hub := gateway.NewHub(presenceRepo, msgBroker)
messageService := service.NewMessageService(..., hub, ...)
// hub 的类型是 *gateway.Hub，但函数要的是 MessageRouter 接口
// Go 编译器自动检查：Hub 有没有 SendToUser + IsOnline？有 → OK
```

**好处**：`service` 包不需要 import `gateway` 包。两个包完全解耦。

## 项目例子2：接口隔离 (ISP)

`service/message.go` 定义了 4 个小接口，而不是 1 个大接口：

```go
type messageStore interface {      // 私信存储：7个方法
    Insert, FindByID, FindConversation, ...
}
type friendChecker interface {     // 好友检查：1个方法
    FindByUserAndFriend(...)
}
type groupMemberStore interface {  // 群成员：2个方法
    IsMember, ListMembers
}
type groupMessageStore interface { // 群消息：1个方法
    Insert
}
```

对比：如果写成大接口：
```go
// ❌ 不好：调用方依赖了不需要的方法
type BigRepo interface {
    // 私信
    Insert(...)
    FindByID(...)
    // 好友
    FindByUserAndFriend(...)
    // 群聊
    IsMember(...)
    ListMembers(...)
    // group msg
    InsertGroupMsg(...)
}
```

遵循 **Interface Segregation Principle**：调用方不应依赖它不需要的方法。测试 `SendMessage` 时只需 mock 私信相关方法，不需要管群聊。

## 生产 vs 测试

| 接口 | 生产实现 | 测试 fake |
|------|---------|-----------|
| `messageStore` | `*postgres.MessageRepo` (写 PostgreSQL) | `fakeMessageStore` (写内存 map) |
| `friendChecker` | `*postgres.FriendRepo` | `fakeFriendChecker` |
| `MessageRouter` | `*gateway.Hub` (连 WebSocket) | `fakeRouter` (记录到 slice) |

所有实现者都没写 `implements`，只是因为刚好有那些方法。

## 面试要点

- **隐式满足** vs **显式声明**：Go 让你在不改上游代码的情况下给已有类型实现新接口
- **接口要小**：Go 社区建议 1-3 个方法的接口最常见（如 `io.Reader` 只有 `Read`）
- **用接口做依赖注入**：函数参数用接口类型，调用方传具体实现，测试传 fake
- **`var _ Interface = (*Type)(nil)`**：编译期验证类型是否满足接口
