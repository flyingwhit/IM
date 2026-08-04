# Learning Notes

## Go Concurrency: Graceful Shutdown Pattern

**Concept**: When a Go HTTP server needs to shut down cleanly (close DB connections, flush logs), you can't just `os.Exit()`. You need to:
1. Catch OS signals (`signal.Notify`)
2. Call `srv.Shutdown(ctx)` to stop accepting new connections while letting in-flight requests finish
3. Use `defer` to close resources in reverse order of creation

**Key insight**: `log.Fatalf` calls `os.Exit(1)` which skips all defers. Never use it inside goroutines that need cleanup. Use channels to propagate errors to the main goroutine instead.

**Production practice**: Always set a shutdown timeout (`context.WithTimeout`). If connections don't drain within the timeout, the server forces shutdown — better than hanging indefinitely.

## Go Concurrency: Select Statement for Multi-Source Blocking

`select` blocks until ONE case is ready. Used in `main.go` to wait for either:
- A server error (listen failure) → immediate shutdown with error
- An OS signal (Ctrl+C) → graceful shutdown

This is cleaner than two goroutines coordinating through shared state.

## Database: Why Transactions Matter

`AcceptRequest` does two writes: UPDATE + INSERT. Without a transaction, if the INSERT fails after a successful UPDATE, the database is in an inconsistent state — half the friendship exists.

PostgreSQL transactions wrap multiple statements in an all-or-nothing unit. `BEGIN` ... work ... `COMMIT` (or `ROLLBACK` on error). The `defer tx.Rollback(ctx)` pattern ensures the transaction is always cleaned up, even if the function panics.

**Production practice**: Never assume two sequential writes are safe. Always ask: "What happens if the second one fails?" If the answer is "inconsistent data," you need a transaction.

## Security: Token Replay Attacks

A refresh token should be single-use. Without atomicity, two concurrent requests could both validate the same token and both issue new ones — a replay attack.

Redis `GETDEL` solves this: it's an atomic "get the value AND delete the key" operation. The first caller gets the value; the second gets `nil`. No race window.

**Production practice**: For any "check-then-consume" operation on a shared resource, use an atomic primitive. Databases have `SELECT ... FOR UPDATE`. Redis has `GETDEL`, `SET NX`, and Lua scripts.

## Security: User Enumeration

When login returns "user not found" vs "wrong password," an attacker can enumerate valid usernames. Our login returns the same message for both cases: "invalid username or password."

Trade-off: Worse UX for legitimate users who typo their username. Production systems sometimes accept this trade-off or mitigate with rate limiting.

## API Design: Error Code Mapping

Domain errors (Go `error` values) shouldn't know about HTTP status codes — that couples business logic to HTTP. Our approach:
- Repository/Service returns `AppError{Err: sentinel, Message: "..."}`
- Handler maps sentinel → HTTP status via `errorStatus()`
- If a new error type is added, only `errorStatus()` needs updating

**Production practice**: This is the "error envelope" pattern. It keeps the transport layer (HTTP, gRPC, etc.) independent of domain logic.

## Project Structure: Composition Root

`cmd/server/main.go` wires ALL dependencies in one place. Every component receives exactly what it needs through its constructor. No package-level globals, no `init()` functions, no service locator.

**Why this matters**: You can read `main.go` and understand the entire system's dependency graph. When testing, you can construct a Service with a mock Repository — no need for monkey-patching.

## Redis: TTL-Based Expiry

Refresh tokens stored in Redis use `SET key value EX <seconds>`. When the TTL expires, Redis automatically deletes the key. This means:
- No cron job needed to clean up expired tokens
- Token expiry is free — just don't extend the TTL
- If Redis restarts, tokens are gone (acceptable — users just re-login)

**Production practice**: Always set TTL on cached data. Redis memory is finite; unbounded growth leads to eviction or OOM.

## WebSocket: How HTTP Becomes a Long-Lived Connection

**Concept**: WebSocket starts as an HTTP request and upgrades to a bidirectional TCP socket.

1. Client sends `GET /ws` with `Upgrade: websocket` and `Connection: Upgrade` headers
2. Server responds `101 Switching Protocols` — the TCP connection is no longer HTTP
3. The same TCP socket now carries WebSocket frames, not HTTP requests
4. Either side can send a message at any time — full duplex

**Key insight**: After the upgrade, the HTTP server (`http.Server`, Gin) no longer manages this connection. `ReadTimeout` and `WriteTimeout` don't apply. The connection's lifecycle is entirely in the application's hands — hence the need for readPump/writePump goroutines.

**Why JWT in query parameter, not header**: WebSocket's JavaScript API (`new WebSocket(url)`) only accepts a URL. No way to set custom headers. Alternatives like `Sec-WebSocket-Protocol` hack exist but are not idiomatic. Query parameter is simple, universal, and works with every WebSocket client.

## Go Concurrency: Hub Goroutine Pattern

**Concept**: A single goroutine owns mutable state. All mutations arrive through channels and are processed sequentially — no locks needed for mutation.

This is the "communicate by sharing memory" inversion: instead of multiple goroutines sharing a map protected by a mutex, a single goroutine owns the map and others communicate with it through channels.

```
Handler goroutine:     Hub goroutine:           Handler goroutine:
     │                      │                        │
     │── register ─────────▶│                        │
     │                      │  clients["A"]["1"]=c   │
     │                      │                        │
     │                      │◀── unregister ─────────│
     │                      │  delete from map       │
```

**Why channels for mutation but RWMutex for reads?** Find/IsOnline need to return immediately — they can't wait for a channel round-trip. Since reads vastly outnumber writes and don't conflict with each other, RWMutex is ideal: many concurrent readers, exclusive writer.

This is a common Go pattern: **channel for ownership transfer, mutex for read-heavy shared state**.

## Go Concurrency: Channel-as-Signal (Close to Broadcast)

**Concept**: In Go, closing a channel is a broadcast. Every goroutine blocked on `<-ch` or `range ch` unblocks immediately.

Our connection shutdown uses this:

```
readPump (detecting disconnect):
    close(c.send)   ← broadcasts "connection is done"

writePump (blocked on <-c.send):
    message, ok := <-c.send
    ok == false     ← channel closed, exit loop
```

**Why not a done channel?** A separate `done chan struct{}` would require writePump to select on both `send` and `done` — more complex. Closing the send channel itself kills two birds with one stone: it unblocks writePump AND prevents new messages from being sent.

**The drain pattern**: writePump's defer does `for range c.send {}`. After the channel is closed, this drains any leftover buffered messages. Without this drain, readPump's `close(c.send)` could block if writePump hasn't consumed everything yet — the channel can only be closed when no sends are pending. Since readPump already sent `unregister` before closing, and the Hub might still be routing messages to this client, the drain prevents a deadlock.

## Go Concurrency: Non-Blocking Channel Send

**Concept**: A `select` with a `default` case makes a channel send non-blocking.

```go
select {
case c.send <- data:
    // Message enqueued
default:
    // Buffer full — drop message
}
```

**Why drop messages instead of blocking?** If writePump is slow (network congestion, slow client), blocking the sender (Hub.SendToUser) would block the entire message routing pipeline. One slow client would delay messages to ALL clients. Dropping is safe because the message is already persisted in PostgreSQL — the receiver can retrieve it later.

**Production practice**: Back-pressure is the alternative. Instead of dropping, the sender blocks and the slowdown propagates backward — eventually the client's send rate is throttled. This is TCP's approach. For IM, dropping + DB fallback is simpler and more user-friendly (other conversations aren't affected by one slow recipient).

## WebSocket: Why Concurrent Writes Are Forbidden

gorilla/websocket explicitly documents that connections support at most one concurrent reader and one concurrent writer. Two goroutines calling `WriteMessage` simultaneously will corrupt the WebSocket frame stream.

**The solution**: A dedicated write goroutine (writePump) reads from a channel. Anyone wanting to send data writes to the channel, not the WebSocket. The channel serializes all writes.

This is an instance of the **fan-in pattern**: multiple senders (Hub routing, ACKs, pings) → channel → single consumer (writePump → WebSocket).

## Design: When to Use Buffered vs Unbuffered Channels

| Channel | Buffer | Reason |
|---------|--------|--------|
| `Hub.register` | 64 | Handler shouldn't block waiting for Hub — fire and continue |
| `Hub.unregister` | 64 | Same — readPump is already in shutdown path, can't stall |
| `Client.send` | 256 | Decouples Hub routing from network I/O speed. A burst of messages queues in memory, not in Hub's routing loop |

**Rule of thumb**: Buffer when the sender and receiver operate at different speeds and the sender shouldn't wait. Don't buffer when you need the sender to block as back-pressure.

The register/unregister channels were originally unbuffered. Buffering them (64 slots) prevents a deadlock scenario: if Hub.Run() is slow processing a message, multiple concurrent connections or disconnections would block the Gin handler goroutines. With 64-slot buffers, up to 64 connections can register or disconnect before the Hub needs to catch up.

## Go Design: Breaking Circular Imports with Interfaces and Callbacks

**Problem**: `gateway` imports `service` (for AuthService), but `service` needs `gateway.Hub` (for routing). Go forbids circular package imports.

**Solution — two complementary patterns**:

1. **Interface in consumer package**: `service` defines `MessageRouter` interface with the methods it needs (`SendToUser`, `IsOnline`). `gateway.Hub` satisfies this interface through Go's structural typing — it doesn't need to know the interface exists. This breaks the cycle in one direction.

2. **Function pointer callbacks**: For the reverse direction (Hub needs to call service), Hub stores function pointers (`OnMessage func(...)`, `OnConnect func(...)`) rather than importing the service type. The composition root (`main.go`) sets these pointers. Hub has zero knowledge of what they do — it just calls them.

This two-pattern combination is clean and idiomatic: interfaces where you consume, callbacks where you're consumed.

**Key insight**: Go's structural typing means you can define an interface in the package that USES it, and any type from any other package automatically satisfies it if the methods match. This is the opposite of Java/C# where types must explicitly declare `implements`.

## Design: Message Routing with Callbacks

The Hub callback pattern is a lightweight alternative to event buses or message brokers for intra-process communication:

```go
// Hub — provides but doesn't consume
type Hub struct {
    OnMessage func(userID string, raw []byte)
    OnConnect func(userID string)
}

// MessageService — consumes but doesn't provide
func (s *MessageService) HandleIncomingMessage(userID string, raw []byte) { ... }

// main.go — the only place that knows both
hub.OnMessage = messageService.HandleIncomingMessage
```

This is simpler than:
- **Channels**: Would need a dedicated goroutine to read and dispatch
- **Interfaces**: Would create circular imports (service defines interface, gateway implements it, gateway imports service for other things)
- **Event bus**: Overkill for a single-process application

**When to use callbacks vs interfaces**:
- Interface: when the consumer defines what it needs and many implementations exist
- Callback: when one component needs to notify another without knowing its type

## Design: Server-Generated vs Client-Generated IDs

In messaging systems, who generates the message ID determines the trust model:

**Server-generated (our approach)**:
- Server is the single source of truth
- No conflict resolution needed
- Client can't forge timestamps or IDs
- Trade-off: client doesn't know ID until ACK arrives

**Client-generated (some P2P systems)**:
- Optimistic UI: client knows ID immediately
- Conflict resolution needed: what if two clients use the same ID?
- Server must validate uniqueness

For a client-server IM system, server-generated IDs are the standard choice. The ~5ms ACK delay is imperceptible to users — the UI shows the message locally and updates it with the server ID on ACK.

## Database: Cursor-Based Pagination for Real-Time Data

Offset-based pagination (`OFFSET 20 LIMIT 10`) is problematic for data that changes frequently:

```
Page 1: SELECT ... ORDER BY created_at DESC LIMIT 10 OFFSET 0  → messages #1-10
[3 new messages arrive]
Page 2: SELECT ... ORDER BY created_at DESC LIMIT 10 OFFSET 10 → messages #11-20
                                                                    ↑ misses #11-13 (now shifted)
                                                                    ↑ duplicates #8-10
```

Cursor-based pagination avoids this:
```
Page 1: SELECT ... WHERE created_at < NOW() ORDER BY created_at DESC LIMIT 10
        → messages #1-10, cursor = created_at of #10
[3 new messages arrive]
Page 2: SELECT ... WHERE created_at < cursor ORDER BY created_at DESC LIMIT 10
        → messages #11-20 (correct, no duplicates or gaps)
```

**Trade-off**: Cursor pagination can't jump to arbitrary pages — only "next page" and "previous page." For chat history, this is the right behavior; users scroll, they don't type "page 5."
