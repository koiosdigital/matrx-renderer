package pixlet

import (
	"context"
	"fmt"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/redis/go-redis/v9"
	"go.starlark.net/starlark"
)

// RedisCache implements the runtime.Cache interface using Redis
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a new shared Redis cache instance
func NewRedisCache(cfg *config.RedisConfig) *RedisCache {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	return &RedisCache{
		client: rdb,
	}
}

// NewRedisCacheFromClient creates a new Redis cache instance from an existing client
func NewRedisCacheFromClient(client *redis.Client) *RedisCache {
	return &RedisCache{
		client: client,
	}
}

// Close closes the Redis connection
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Ping tests the Redis connection
func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Get retrieves a value from the Redis cache
func (c *RedisCache) Get(thread *starlark.Thread, key string) ([]byte, bool, error) {
	ctx := context.Background()
	if thread != nil {
		// Use thread context if available
		if threadCtx := thread.Local("context"); threadCtx != nil {
			if threadContext, ok := threadCtx.(context.Context); ok {
				ctx = threadContext
			}
		}
	}

	result, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			// Key doesn't exist
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get key %s from Redis: %w", key, err)
	}

	return []byte(result), true, nil
}

// Set stores a value in the Redis cache with the specified TTL
func (c *RedisCache) Set(thread *starlark.Thread, key string, value []byte, ttl int64) error {
	ctx := context.Background()
	if thread != nil {
		// Use thread context if available
		if threadCtx := thread.Local("context"); threadCtx != nil {
			if threadContext, ok := threadCtx.(context.Context); ok {
				ctx = threadContext
			}
		}
	}

	expiration := time.Duration(ttl) * time.Second

	err := c.client.Set(ctx, key, value, expiration).Err()
	if err != nil {
		return fmt.Errorf("failed to set key %s in Redis: %w", key, err)
	}

	return nil
}
