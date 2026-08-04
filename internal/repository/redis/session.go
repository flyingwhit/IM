package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionRepo manages refresh token state in Redis.
// It stores token hash → user_id mappings with TTL matching the token expiry.
type SessionRepo struct {
	client *redis.Client
}

func NewSessionRepo(client *redis.Client) *SessionRepo {
	return &SessionRepo{client: client}
}

// key returns the Redis key for a token hash.
func (r *SessionRepo) key(tokenHash string) string {
	return "refresh:" + tokenHash
}

// Store saves a refresh token hash → user_id mapping with a TTL.
// After TTL expires, the token is automatically invalidated.
func (r *SessionRepo) Store(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	return r.client.Set(ctx, r.key(tokenHash), userID, ttl).Err()
}

// GetUserID retrieves the user ID associated with a refresh token hash.
// Returns empty string if the token is not found or expired.
func (r *SessionRepo) GetUserID(ctx context.Context, tokenHash string) (string, error) {
	val, err := r.client.Get(ctx, r.key(tokenHash)).Result()
	if err != nil {
		return "", err
	}
	return val, nil
}

// GetAndDelete atomically retrieves and deletes a refresh token.
// Uses Redis GETDEL: if two concurrent requests race on the same token,
// only one gets the value; the other sees the key already gone.
// Returns ("", redis.Nil) when the token does not exist.
func (r *SessionRepo) GetAndDelete(ctx context.Context, tokenHash string) (string, error) {
	val, err := r.client.GetDel(ctx, r.key(tokenHash)).Result()
	if err != nil {
		return "", err
	}
	return val, nil
}

// Delete removes a single refresh token (used on logout).
func (r *SessionRepo) Delete(ctx context.Context, tokenHash string) error {
	return r.client.Del(ctx, r.key(tokenHash)).Err()
}
