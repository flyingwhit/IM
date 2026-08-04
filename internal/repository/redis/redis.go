package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/ciel/im/internal/config"
)

// NewClient creates a Redis client and verifies connectivity.
func NewClient(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr(),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return rdb, nil
}
