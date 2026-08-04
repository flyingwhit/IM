# Engineering Decisions

## 1. Layered Architecture over Hexagonal/Clean Architecture

**Decision**: Handler → Service → Repository layers with concrete dependencies.

**Alternative**: Port/Adapter pattern with interfaces at every boundary.

**Why**: Phase 1 has three entities (User, Friend, Token) with straightforward CRUD + auth logic. Hexagonal architecture at this stage would mean 6+ interfaces, doubling the file count with no benefit. The layering is intentional — when we need to swap a repository implementation (e.g., PostgreSQL → gRPC in Phase 4), the Service layer's dependency is already abstractable behind an interface.

**When to revisit**: Phase 2 WebSocket testing demands mockable services → introduce interfaces then.

## 2. UUID over SERIAL Primary Keys

**Decision**: All tables use `UUID DEFAULT gen_random_uuid()`.

**Why now, not later**: Migrating from SERIAL to UUID after deployment is painful (every foreign key breaks). Starting with UUID costs a few percent in index performance but saves a migration disaster later.

**Real cost**: UUID indexes are ~2x larger than BIGINT. For a million users, that's ~32 MB vs ~16 MB for the index — negligible on modern hardware.

## 3. Dual-Record Friend Model

**Decision**: Store `(A, B)` and `(B, A)` as separate rows when friends.

**Alternative**: Single row with `user_id < friend_id` canonical ordering.

**Why dual-record**: Query `WHERE user_id = ? AND status = 'accepted'` is a simple indexed lookup. The single-row approach requires `WHERE (user_id = ? OR friend_id = ?)` which forces a sequential scan or bitmap index scan — at least 2x slower for friend list queries, and friend list is the most frequent read in the system.

**Trade-off**: Double storage for friendships. A friendship uses ~64 bytes; 1000 friends = 64 KB. Acceptable.

## 4. JWT Access Token + Opaque Refresh Token

**Decision**: Access tokens are JWTs (stateless). Refresh tokens are random hex strings (stateful, stored in Redis).

**Alternative**: Both JWTs, or both opaque, or pure session-based auth.

**Why this split**:
- Access token is validated on EVERY request → must be fast. JWT validation is a local HMAC check, no network call.
- Refresh token is used infrequently (every 15 min) → a Redis lookup is acceptable.
- Making the refresh token opaque means it carries no claims — an attacker who steals one learns nothing. Storing only its SHA-256 hash in Redis limits damage if Redis is compromised.

**Token rotation**: Each refresh invalidates the old token and issues a new pair. If an attacker steals a refresh token and uses it, the legitimate user's next refresh will fail (old token already consumed), alerting them to the theft.

## 5. bcrypt over Argon2

**Decision**: Use bcrypt with DefaultCost (10).

**Why not Argon2**: `golang.org/x/crypto/bcrypt` is in the standard library extended ecosystem. Argon2 requires a third-party library. For a learning project, bcrypt's 100ms hash time per password is more than adequate — an attacker can test ~10 passwords/second/CPU core. With an 8-char random password, that's decades to crack.

**When to revisit**: If this becomes a real production system with regulatory requirements, switch to Argon2id.

## 6. go-redis GETDEL for Token Refresh

**Decision**: Use Redis `GETDEL` command (atomic get-and-delete) instead of separate `GET` + `DEL` calls.

**Problem it solves**: Without atomicity, two concurrent refresh requests could both pass the `GET` check before either `DEL` executes, both issuing new tokens from the same old refresh token.

`GETDEL` transforms this from a check-then-act race condition into a single atomic operation — the second caller sees `nil` and knows the token was already consumed.

**Trade-off**: `GETDEL` requires Redis 6.2.0+. Our docker-compose uses Redis 7, so this is fine.

## 7. Database Transactions for Friend Operations

**Decision**: `AcceptRequest` and `RemoveFriend` execute inside PostgreSQL transactions (`BEGIN`/`COMMIT`).

**Without transactions**: Accepting a friend request means (1) UPDATE existing row status, (2) INSERT reverse row. If step 2 fails, the database is inconsistent — A is in B's friend list but not vice versa.

**Implementation**: `postgres.RunTx` helper wraps the pgx transaction lifecycle. Service layer orchestrates the transaction boundary; repository provides tx-aware methods (`*Tx` suffix).

**Trade-off**: The `*Tx` suffix convention creates code duplication (each method has a pool version and a tx version). In Phase 2, introduce a `DB` interface that both `*pgxpool.Pool` and `pgx.Tx` satisfy, eliminating the duplication.

## 8. Composition Root Pattern (Manual DI)

**Decision**: `cmd/server/main.go` manually constructs all dependencies and wires them together. No DI framework.

**Why no wire/dig**: For 10-15 components, manual DI is simpler than learning a framework's semantics. The dependency graph is visible in one file. If the constructor list grows beyond ~20 lines in Phase 4, `google/wire` (compile-time DI) is the natural upgrade path.

## 9. config.Load() Returns Error, Not Panic

**Decision (revised)**: `requireEnv` returns `(string, error)` instead of panicking.

**Why changed**: The original panic-based approach was justified as "fail fast at startup," but it had a real problem: `Load()` signed a contract (`*Config, error`) that it didn't honor. The caller's `if err != nil` was dead code. When startup fails, the error now propagates through `main()` which calls `log.Fatalf` — same fail-fast behavior, honest API.

## 10. Health Check Outside API Versioning

**Decision**: `GET /health` (not `/api/v1/health`).

**Why**: Infrastructure endpoints and business APIs have different lifecycles. Load balancers, Kubernetes probes, and monitoring tools hit `/health` — changing its path with API versions would break infrastructure tooling. Business APIs evolve with `/api/v1` → `/api/v2`; health checks should be stable forever.

## 11. Hub + Client Goroutine Model over Actor Framework

**Decision**: Use the gorilla/websocket chat example pattern — a Hub goroutine with channel-based registration and per-connection readPump/writePump goroutines.

**Alternative**: Actor framework (e.g. ergo), or a single goroutine polling all connections.

**Why this pattern**:
- It's the most widely deployed Go WebSocket pattern — battle-tested in thousands of production systems
- Each connection's read and write are independent goroutines that communicate through a buffered channel — this is Go's native concurrency primitive, not a framework
- The Hub's single-goroutine event loop eliminates lock contention during mutation: register and unregister are serialized through channels
- Readers don't block writers and vice versa — if readPump is processing a slow message, writePump can still send pings

**Trade-off**: Two goroutines per connection (~8 KB stack each). At 10K connections, that's ~80 MB of goroutine overhead. Acceptable for Phase 2-4; shard the Hub when approaching 100K connections.

## 12. Channel-Based Lifecycle over Mutex-Based State Machine

**Decision**: Connection shutdown is signaled by closing the `send` channel, not by setting a `closed` flag with a mutex.

**Why**: Closing a channel broadcasts to all receivers. writePump blocks on `<-c.send` — when the channel closes, it unblocks immediately with `ok == false`. No polling, no condition variables, no timed retries.

This is a Go idiom: "close a channel to signal completion." The readPump's defer does `close(c.send)` → writePump wakes → writes close frame → exits → defer drains remaining messages → goroutine exits cleanly.

**Without this pattern**: A `sync.Mutex` + `bool` flag requires writePump to periodically check the flag between writes. A `sync.Cond` is more complex and error-prone. Channels are the simplest correct solution.

## 13. Store-and-Forward Message Delivery (Persist-First)

**Decision**: Messages are written to PostgreSQL before being routed to the receiver's WebSocket.

**Alternative**: Push to receiver first, persist asynchronously (WhatsApp/微信 model).

**Why persist-first for Phase 2**:
- Zero message loss: if the server crashes between receive and delivery, the message is already in the database
- Offline messages are free: if the receiver is offline, the message is already persisted — just load it when they reconnect
- Simpler implementation: no retry queue, no acknowledgement protocol, no reconciliation

**Trade-off**: Every online message pays a ~1-5ms database write latency. High-performance IM systems avoid this by delivering first and persisting asynchronously — but that requires an acknowledgement protocol (sender → server → receiver → ACK chain) to detect lost messages. This complexity is deferred to Phase 4.

## 14. JSON over Protobuf for Message Protocol

**Decision**: WebSocket messages use JSON encoding (`ws.Envelope` with `json.RawMessage` payload).

**Why not protobuf/msgpack**: JSON is human-readable — invaluable for debugging during development. Protobuf requires schema compilation, code generation, and opaque wire formats that need specialized tools to inspect.

**When to switch**: Phase 4 scalability. Protobuf is ~3-5x smaller on the wire and ~5-10x faster to encode/decode. The `ws.Envelope` type abstracts the encoding — switching to protobuf means changing the marshal/unmarshal calls, not the API.

## 15. Device Limit at HTTP Layer, Not WebSocket Layer

**Decision**: Check `maxDevice` per user in `gateway.Handler.Handle()` before the WebSocket upgrade, not in Hub.Run() after the upgrade.

**Why before upgrade**:
- Failing early means the client gets a clean JSON error (`429 Too Many Requests`) rather than a WebSocket close frame — easier to handle in client code
- Avoids wasting the HTTP upgrade handshake and goroutine creation for connections that will be rejected immediately
- Keeps Hub.Run()'s responsibility narrow: register/unregister. Admission control is a handler concern, not a registry concern

**Trade-off**: There's a TOCTOU race between the `Find` check and the `register` channel send — another connection could register in between. For a 3-device limit, this race is benign (user might get 4 connections instead of 3). For stricter limits, use an atomic counter in Redis.

## 16. Server-Side Heartbeat over Client-Side Ping

**Decision**: Server sends WebSocket ping frames every 54 seconds; browsers automatically reply with pong.

**Why server-initiated**:
- Browser WebSocket API does not expose ping/pong control frames to JavaScript — only the browser's internal WebSocket implementation can respond to pings
- The server is the party that needs to know if the connection is dead (to clean up Hub state and free resources)
- gorilla/websocket's `SetReadDeadline` + `PongHandler` pattern makes this trivial: pong refreshes the deadline, missed pong triggers ReadMessage error → readPump exits → cleanup

**Constants**: `pongWait = 60s` (deadline), `pingPeriod = 54s` (pongWait × 0.9). The 10% margin accommodates network jitter.

## 17. Server-Generated Message IDs

**Decision**: Message IDs are generated by the server (`UUID DEFAULT gen_random_uuid()`), not the client.

**Why server-side**: The server is the single source of truth for message ordering and existence. Client-generated IDs would require conflict resolution ("what if two clients send the same ID?"), deduplication ("is this a retry or a duplicate?"), and trust ("does the client-generated timestamp reflect reality?").

**Trade-off**: The sender doesn't know the message ID until the ACK arrives (~5-10ms). For an IM client, this is imperceptible — the UI can show an "optimistic" version locally and replace it with the confirmed message on ACK.

## 18. Persist-First Message Delivery

**Decision**: Messages are written to PostgreSQL BEFORE being routed to the receiver's WebSocket.

**Why persist-first**: Zero message loss. If the server crashes between receive and delivery, the message is already in the database. Offline messages are free — no separate code path needed. Simpler than the alternative (deliver-first + async persist + reconciliation).

**Trade-off**: Every online message pays ~1-5ms database write latency. High-performance IM systems (WhatsApp, 微信) deliver first and persist asynchronously, but this requires an acknowledgement chain (sender→server→receiver→ACK) to detect lost messages. Deferred to Phase 4.

## 19. Callback Pattern for Bidirectional Package Dependencies

**Decision**: `gateway.Hub` uses function pointer callbacks (`OnMessage`, `OnConnect`) instead of importing `service.MessageService` directly.

**Why callbacks over interfaces**: The `gateway` package already imports `service` (for `AuthService`). If `service` also imports `gateway` (for `Hub`), we have a circular dependency. Go's solution: define interfaces in the consumer package (service defines `MessageRouter` for routing) and use function callbacks for the reverse direction (Hub stores callbacks set by main.go).

**This is the Composition Root pattern in action**: `main.go` is the only file that knows about both packages, so it's the natural place to wire callbacks together.

## 20. Repository Interfaces in Service Package

**Decision**: `MessageService` stores repos as narrow unexported interfaces (`messageStore`, `friendChecker`) rather than concrete `*postgres.XxxRepo` types.

**Why**: Testability without a database. The service can be tested with fakes that implement these interfaces. The interfaces are unexported because no external package should depend on them — they're an implementation detail of the service package.

**Trade-off**: Two layers of interfaces (unexported in service, exported concrete type in postgres). The exported `NewMessageService` constructor still accepts concrete types, so callers (main.go) don't know about the unexported interfaces. This is a pragmatic middle ground: loose coupling where needed, concrete types where sufficient.

## 21. Friendship Check on Every Message

**Decision**: The sender must be an accepted friend of the receiver to send a message. Self-messaging is exempt.

**Why**: This makes the friend system meaningful — friendship is the basic social contract. Without it, any user could spam any other user. Self-messaging is useful for "Saved Messages" / "Notes to Self" patterns.

**Performance consideration**: Each message requires a `FindByUserAndFriend` query (indexed lookup on `friends` table). In Phase 4, this can be cached in Redis to reduce DB load.

## 22. No Conversations Table for 1-on-1 Messages

**Decision**: Conversations are derived from the messages table via SQL, not stored in a separate table.

**Why not now**: A conversations table adds write-path complexity (every `INSERT INTO messages` must also `UPSERT INTO conversations`). The read-path benefit (a single query for the conversation list) doesn't justify this overhead for 1-on-1 messaging.

**When to revisit**: Phase 3 group chat. Group conversations have metadata (group name, avatar, member count) that doesn't belong on the messages table. At that point, a conversations table becomes necessary.
