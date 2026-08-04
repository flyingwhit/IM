# API Design

## Conventions

### URL Pattern
```
/api/v1/<resource>/<action>
```
Version prefix allows breaking changes in `/api/v2` without affecting existing clients.

### Authentication

- **Access Token** (JWT, 15 min TTL): Passed as `Authorization: Bearer <token>`. Short-lived so revocation isn't needed — it expires before an attacker can do much damage.
- **Refresh Token** (opaque, 7 day TTL): Used only at `/auth/refresh`. Long-lived but stored server-side in Redis, so it can be revoked. Implements token rotation: each refresh invalidates the old token and issues a new pair.

Why not sessions? JWT is stateless — any gateway instance can validate it without a shared session store. Important for Phase 4's multi-gateway architecture.

### Error Format

Every error response follows:
```json
{"error": "<human-readable message>"}
```

HTTP status codes are determined by domain error type, not by the error message:
- `ErrUnauthorized` → 401
- `ErrNotFound` → 404
- `ErrDuplicate` → 409
- `ErrForbidden` → 403
- `ErrInvalidInput` → 400
- Everything else → 500

### Request/Response

All bodies are JSON. Request validation uses Gin's `binding` tags. Response fields use `json` tags with `omitempty` for optional fields.

## Endpoints

### Auth (public)

| Method | Path | Body | Response | Notes |
|--------|------|------|----------|-------|
| POST | `/api/v1/auth/register` | `{username, email, password}` | `{id, username, email}` (201) | Passwords: bcrypt, min 8 chars |
| POST | `/api/v1/auth/login` | `{username, password}` | `{access_token, refresh_token, expires_in}` | Returns token pair |
| POST | `/api/v1/auth/refresh` | `{refresh_token}` | `{access_token, refresh_token, expires_in}` | Rotates old token |
| POST | `/api/v1/auth/logout` | `{refresh_token}` | `{message}` | Deletes refresh token |

Login and register return the same error message for wrong username and wrong password: "invalid username or password". This prevents user enumeration — an attacker can't distinguish "user doesn't exist" from "wrong password".

### User (auth required)

| Method | Path | Body | Response |
|--------|------|------|----------|
| GET | `/api/v1/users/me` | — | Full user object |
| PUT | `/api/v1/users/me` | `{nickname?, avatar_url?}` | Updated user object |

`PUT /users/me` uses pointers for optional fields: `nil` means "don't change", non-nil means "update to this value". This avoids the "was the field omitted or set to empty?" ambiguity.

### Friends (auth required)

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/friends/requests` | Send friend request |
| GET | `/api/v1/friends` | List accepted friends |
| GET | `/api/v1/friends/requests` | List pending incoming requests |
| PUT | `/api/v1/friends/requests/:id/accept` | Accept a request |
| PUT | `/api/v1/friends/requests/:id/reject` | Reject a request |
| DELETE | `/api/v1/friends/:id` | Remove a friend |

Accept creates a bidirectional relationship (two DB rows in one transaction). Reject deletes the pending request. Remove deletes both directional rows in one transaction.

### Health (public)

| Method | Path | Response |
|--------|------|----------|
| GET | `/health` | `{"status": "ok"}` |

Outside `/api/v1` — this is an infrastructure endpoint, not a business API.
