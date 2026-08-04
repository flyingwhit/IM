# Architecture

## Overview

IM backend follows a **layered architecture** with four tiers:

```
HTTP Handler  →  Service  →  Repository  →  Database/Cache
   (Gin)         (logic)      (data)       (PostgreSQL/Redis)
```

## Why Layered Architecture

- **Learnability**: Each layer has a single responsibility, making it easy to trace a request end-to-end
- **Evolvability**: When Phase 4 introduces service decomposition, layer boundaries already exist — splitting repos or services across processes is mechanical
- **Avoids over-engineering**: Phase 1 logic is simple enough that Hexagonal/Clean Architecture would add ceremony without benefit

Trade-off: Concrete dependencies between layers (no interfaces yet) make unit testing harder. Accepted for Phase 1 — interfaces add indirection cost, and the test pyramid today leans toward integration tests. Phase 2 WebSocket tests work around this by testing Hub and Client directly without real connections.

## Module Responsibilities

| Module | Responsibility | Must NOT |
|--------|---------------|----------|
| `handler` | Bind HTTP params, call service, format response | Business logic, direct DB access |
| `service` | Business rules, orchestration, token management | HTTP concerns, SQL |
| `repository` | Data access, SQL/Redis commands | Business rules, HTTP concerns |
| `middleware` | Cross-cutting: auth, CORS, logging | Business logic |
| `model` | Shared types, domain errors | Any I/O or logic |
| `config` | Environment variable loading | Business defaults |
| `router` | Route registration, middleware binding | Handler logic |
| `gateway` | WebSocket upgrade, connection lifecycle, message routing | Business logic, persistence |
| `ws` | WebSocket message protocol types and encoding | I/O or business logic |

## Request Flow

```
Client → Gin Router → Middleware (JWT) → Handler → Service → Repository → DB/Redis
                                                      ↑
                                              Domain errors (AppError)
                                              propagate back through layers
                                              mapped to HTTP status in handler
```

Key design decisions in this flow:
- **Context propagation**: `c.Request.Context()` is passed through all layers. If the client disconnects, the context cancels, and in-flight PostgreSQL queries are aborted automatically.
- **Error convention**: Repository returns `AppError{Err: sentinel, Message: human}`. Service either propagates it or wraps it. Handler maps sentinel → HTTP status via `errorStatus()`.

## WebSocket Architecture (Phase 2.1)

Unlike HTTP's request-response model, WebSocket connections are long-lived. This required a different concurrency model while reusing the existing auth layer.

### Dual-Path Architecture

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
                                  (JWT validation
                                   reused from HTTP)
```

**Why not put WebSocket in handler/?** HTTP handlers have a request-response lifecycle measured in milliseconds. WebSocket connections last minutes or hours. Mixing them in the same package would blur two fundamentally different execution models: short-lived goroutines per request vs. long-lived goroutines per connection.

### Hub + Client Goroutine Model

The gateway package implements the canonical Go WebSocket pattern (from gorilla/websocket's chat example):

**Hub** — a single goroutine that owns the connection registry. All register/unregister mutations arrive through channels and are processed sequentially. No locks needed during mutation.

**Client** — each WebSocket connection gets two goroutines:
- **readPump**: blocking read on the WebSocket → dispatch to handler. On error (disconnect), signals unregister and closes the send channel.
- **writePump**: reads from a buffered channel → writes to the WebSocket. Also sends periodic pings. The channel is the ONLY writer — gorilla/websocket forbids concurrent writes.

### Connection Lifecycle

```
  Connect                  Active                     Disconnect
  ─────────               ─────────                 ────────────
  GET /ws?token=           readPump:                 readPump:
  ├─ JWT validated          ReadMessage() loop        ReadMessage error
  ├─ Device limit check     │                         │
  ├─ HTTP 101 Upgrade       ├─ receive message.send   defer:
  ├─ Client created         │  → MessageService       ├─ close(send)
  ├─ register → Hub         │                         ├─ conn.Close()
  ├─ go writePump()         writePump:                └─ unregister → Hub
  └─ go readPump()          ├─ ← send channel
                             ├─ ticker → ping         writePump:
                             └─ WriteMessage()        ← channel closed
                                                      WriteCloseMessage()
                                                      return
```

**Key insight**: The HTTP handler (`Handle`) returns immediately after launching the two goroutines. The connection's entire remaining lifetime — minutes or hours — is managed by readPump and writePump. Gin's timeouts no longer apply; the connection has been hijacked from HTTP.

### Message Routing

```
Sender's readPump → (future: MessageService → DB) → Hub.SendToUser(receiverID)
                                                          │
                                                    Hub.Find(receiverID)
                                                    → all receiver's Clients
                                                    → each Client's send channel
                                                    → receiver's writePump
```

`Hub.SendToUser` marshals the message envelope **once** and pushes the same `[]byte` to all connections belonging to that user. This avoids redundant JSON encoding when a user has multiple devices.

### Concurrency Safety

| Operation | Mechanism | Why |
|-----------|-----------|-----|
| Register/unregister | Channel → single goroutine | Serialized map mutation |
| Find/IsOnline | RWMutex read lock | Many concurrent readers, no blocking |
| WebSocket write | Single goroutine per connection | gorilla/websocket not concurrent-write safe |
| Send to connection | Buffered channel (256) + non-blocking send | Prevents slow clients from blocking Hub |

## Dependency Injection

`cmd/server/main.go` is the **Composition Root** — the single place where all concrete types are wired together. No DI framework. No global state. Every component receives its dependencies through constructors.

This makes the dependency graph explicit and testable: swap a real `UserRepo` for a stub by passing a different implementation.

## Evolution Path

The architecture is designed to evolve without rewrites:

- **Phase 2.1 (WebSocket Foundation)**: ✅ Complete. `gateway` package for WebSocket connections. Hub+Client goroutine model. Heartbeat via server-side pings. Device limit at HTTP upgrade layer. Existing auth service reused via `ValidateAccessToken`.
- **Phase 2.2 (Online Status)**: ✅ Complete. Redis-based presence tracking integrated with Hub. First/last connection detection in Hub.Run() event loop.
- **Phase 3 (Messaging System)**: ✅ Complete. Message model, repository, service — connecting readPump's dispatch through Hub callbacks to persistence and routing. See [Messaging Architecture](#messaging-architecture-phase-3) below.
- **Phase 4 (Service Decomposition)**: Repository layer can be split into separate services with HTTP/gRPC boundaries. Service layer stays the same — only the repository implementation changes.
- **Phase 5 (Observability)**: Middleware layer is the natural place to add metrics, tracing, and structured logging — injected without touching business logic.

## Messaging Architecture (Phase 3)

### Message Flow

```
  Client A                         Server                          Client B
     │                                │                                │
     │── message.send ──────────────▶│                                │
     │                                │  readPump → Hub.OnMessage     │
     │                                │  → MessageService             │
     │                                │     ├─ Parse + Validate       │
     │                                │     ├─ Check friendship       │
     │                                │     ├─ INSERT INTO messages   │
     │                                │     ├─ Hub.Find(B)            │
     │                                │     └─ route:                 │
     │                                │        online → message.new ──▶│
     │                                │        offline → skip          │
     │◀── message.ack ───────────────│                                │
```

### Dependency Graph

```
 gateway.Handler ──→ service.AuthService (existing)
 gateway.Client  ──→ hub.OnMessage callback ──→ service.MessageService
 gateway.Hub     ──→ hub.OnConnect callback ──→ service.MessageService
 service.MessageService ──→ messageStore interface ──→ postgres.MessageRepo
 service.MessageService ──→ friendChecker interface ──→ postgres.FriendRepo
 service.MessageService ──→ MessageRouter interface ──→ gateway.Hub
```

### Avoiding Circular Dependencies

The `gateway` and `service` packages have a bidirectional relationship:
- `gateway` imports `service` for `AuthService` (Handler) and `MessageService` (callbacks)
- `service` needs `gateway.Hub` for routing

This is resolved through interfaces defined in the service package:
- `MessageRouter` (SendToUser, IsOnline) — implemented by Hub via structural typing
- `messageStore` / `friendChecker` — unexported interfaces for test injection

Hub never imports service directly — it stores function pointers (`OnMessage`, `OnConnect`)
set by main.go. The composition root pattern makes this wiring explicit.

### Callback Pattern

Hub exposes two callback fields for extensibility without adding type dependencies:

| Callback | Trigger | Set to |
|----------|---------|--------|
| `OnMessage(userID, raw)` | Client sends `message.send` frame | `MessageService.HandleIncomingMessage` |
| `OnConnect(userID)` | New WebSocket connection registered | `MessageService.DeliverOfflineMessages` |

This pattern is extensible. Future features (typing indicators, read receipts) can add
their own callbacks without modifying Hub or Client.

### Message Status Lifecycle

```
  sent ─────────→ delivered ─────────→ read (Phase 3.x)
  (persisted)     (WebSocket write)   (client ACK)
```

- **sent**: Default on INSERT. Message is in DB but receiver is offline.
- **delivered**: Hub found at least one connection and pushed the message.
- **read**: Reserved. Will use `message.read` WebSocket frame from client.

### Offline Message Delivery

On WebSocket connect (`OnConnect` → `DeliverOfflineMessages`):
1. Query `WHERE receiver_id=$1 AND status='sent' ORDER BY created_at ASC`
2. Push each message via `Hub.SendToUser` as `message.new`
3. Update status to `delivered`

Race condition: `OnConnect` runs in a goroutine, Hub may not have processed
register yet. This is benign — if delivery fails, messages stay as `sent` and
will be retried on the next connect.
