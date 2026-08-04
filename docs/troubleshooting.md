# Troubleshooting

## Bug: `make run` exits with Error 1 on Ctrl+C

**Symptom**: Pressing Ctrl+C during `make run` shows `make: *** [run] Error 1` even though the server shuts down.

**Root cause**: `go run` compiles the server to a temp binary and runs it as a child process. When Ctrl+C sends SIGINT to the process group, `go run` itself receives the signal and exits with code 143 (killed by signal), even though our server binary handles it gracefully (exit 0). `make` sees `go run`'s exit code, not our server's.

**Fix**: Changed `Makefile` `run` target from `go run ./cmd/server` to `go build -o ./tmp/server ./cmd/server && ./tmp/server`. The binary receives SIGINT directly and exits with code 0.

**Lesson**: `go run` is convenient for development but not suitable for testing signal handling. Always test graceful shutdown with a compiled binary.

## Bug: Server goroutine `log.Fatalf` prevents cleanup

**Symptom**: When the HTTP server fails to start (port in use, etc.), PostgreSQL and Redis connections are leaked — `defer pool.Close()` never runs.

**Root cause**: `log.Fatalf` calls `os.Exit(1)` which terminates the process immediately without running deferred functions from other goroutines. The server goroutine's `log.Fatalf` was killing the process before `main()`'s defers executed.

**Fix**: Replaced `log.Fatalf` with an error channel (`serverErr`). The server goroutine sends errors to the channel; `main()` receives them via `select` and returns normally, allowing defers to run before exiting.

**Lesson**: Never use `os.Exit` or `log.Fatal` in goroutines. Always propagate errors back to the main goroutine through channels.

## Bug: Shutdown timeout treated as fatal error

**Symptom**: `server error: context deadline exceeded` printed on Ctrl+C.

**Root cause**: `srv.Shutdown(ctx)` returns `context.DeadlineExceeded` if in-flight requests don't complete within the timeout. `run()` returned this error, and `main()` called `log.Fatalf` on it — treating a normal shutdown condition as a crash.

**Fix**: `run()` now logs shutdown errors (`log.Printf`) and returns `nil`. The process exits with code 0 whether shutdown was clean or timed out. Resources are still cleaned up by defers.

**Lesson**: Not all errors are fatal. Distinguish startup errors (can't listen — truly fatal) from shutdown errors (timed out waiting for connections — expected, log and move on).

## Bug: Friend AcceptRequest race condition (data inconsistency)

**Symptom**: After accepting a friend request, one user sees the friendship but the other doesn't.

**Root cause**: `AcceptRequest` did two separate database writes (UPDATE status, INSERT reverse record) outside a transaction. If the INSERT failed (network blip, constraint violation), the UPDATE had already committed — the database was half-updated.

**Fix**: Wrapped both writes in a PostgreSQL transaction using `RunTx` helper. Now either both writes succeed or both roll back.

**Lesson**: Multi-statement mutations are the most common source of data corruption. Always ask "what if the second statement fails?" and use transactions when the answer is "data becomes inconsistent."

## Bug: Refresh token replay vulnerability

**Symptom**: The same refresh token could be used multiple times concurrently to get new access tokens.

**Root cause**: `GET` (check token exists) + `DEL` (invalidate) is not atomic. Two requests could both pass the `GET` check before either executed `DEL`, both receiving new token pairs.

**Fix**: Replaced `GET` + `DEL` with Redis `GETDEL` — an atomic command that gets the value and deletes the key in one operation. Only the first caller gets the value.

**Lesson**: Check-then-act patterns on shared state are always race conditions. Use atomic primitives (`GETDEL`, `SET NX`, `SELECT FOR UPDATE`) or distributed locks.

## Bug: `config.Load()` API dishonesty

**Symptom**: `Load()` returned `(*Config, error)` but actually panicked on missing env vars. Callers wrote `if err != nil` error handling that never executed.

**Root cause**: `requireEnv` used `panic()` for missing environment variables. While "fail fast" is correct, panicking violated the function's contract — the `error` return was misleading.

**Fix**: Changed `requireEnv` to return `(string, error)`. `Load()` now propagates the first missing variable as an error. `main()` receives the error and calls `log.Fatalf` — same fail-fast behavior, honest API.

**Lesson**: A function's signature IS its contract. If you return `error`, callers will check it. If you panic instead, be explicit about it (name it `MustLoad()`, not `Load()`).
