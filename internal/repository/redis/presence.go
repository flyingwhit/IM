package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// presenceTTL is how long a presence key lives without refresh.
	// After this duration, the key auto-expires and the user appears offline.
	// 60s matches pongWait — the same timeout that detects dead connections.
	presenceTTL = 60 * time.Second

	// presenceOpTimeout bounds Redis operations in the Hub event loop.
	// A slow/broken Redis must not stall register/unregister processing.
	presenceOpTimeout = 1 * time.Second
)

// PresenceRepo manages online/offline status in Redis.
//
// It stores presence:<userID> → "1" keys with a TTL. Keys are actively
// deleted on disconnect; TTL handles the crash/reconnect edge case
// (zombie keys expire harmlessly after 60s).
type PresenceRepo struct {
	client *redis.Client
}

// NewPresenceRepo creates a PresenceRepo backed by the given Redis client.
func NewPresenceRepo(client *redis.Client) *PresenceRepo {
	return &PresenceRepo{client: client}
}

func (r *PresenceRepo) key(userID string) string {
	return "presence:" + userID
}

// Refresh extends the TTL for an active user's presence key.
func (r *PresenceRepo) Refresh(ctx context.Context, userID string) error {
	return r.client.Set(ctx, r.key(userID), "1", presenceTTL).Err()
}

// SetOnline marks a user as online with a TTL.
func (r *PresenceRepo) SetOnline(ctx context.Context, userID string) error {
	return r.client.Set(ctx, r.key(userID), "1", presenceTTL).Err()
}

// SetOffline removes the online marker for a user.
func (r *PresenceRepo) SetOffline(ctx context.Context, userID string) error {
	return r.client.Del(ctx, r.key(userID)).Err()
}

// IsOnline checks whether a user is currently online.
func (r *PresenceRepo) IsOnline(ctx context.Context, userID string) (bool, error) {
	n, err := r.client.Exists(ctx, r.key(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
