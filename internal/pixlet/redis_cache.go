package pixlet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/redis/go-redis/v9"
	"go.starlark.net/starlark"
)

// RedisCache implements the runtime.Cache interface using Redis
type RedisCache struct {
	client *redis.Client
}

// ContextualRedisCache wraps RedisCache with app/device context for key prefixing
type ContextualRedisCache struct {
	cache    *RedisCache
	appID    string
	deviceID string
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

// WithContext creates a contextual cache wrapper with app/device prefixing
func (r *RedisCache) WithContext(appID, deviceID string) *ContextualRedisCache {
	return &ContextualRedisCache{
		cache:    r,
		appID:    appID,
		deviceID: deviceID,
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

// buildKey creates a scoped cache key with app/device context
func (c *ContextualRedisCache) buildKey(key string) string {
	// Clean key to remove any potential path separators for security
	cleanKey := strings.ReplaceAll(key, "/", "_")
	return fmt.Sprintf("%s/%s/%s", c.appID, c.deviceID, cleanKey)
}

// Get retrieves a value from the Redis cache
func (c *ContextualRedisCache) Get(thread *starlark.Thread, key string) ([]byte, bool, error) {
	ctx := context.Background()
	if thread != nil {
		// Use thread context if available
		if threadCtx := thread.Local("context"); threadCtx != nil {
			if threadContext, ok := threadCtx.(context.Context); ok {
				ctx = threadContext
			}
		}
	}

	cacheKey := c.buildKey(key)

	result, err := c.cache.client.Get(ctx, cacheKey).Result()
	if err != nil {
		if err == redis.Nil {
			// Key doesn't exist
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get key %s from Redis: %w", cacheKey, err)
	}

	return []byte(result), true, nil
}

// Set stores a value in the Redis cache with the specified TTL
func (c *ContextualRedisCache) Set(thread *starlark.Thread, key string, value []byte, ttl int64) error {
	ctx := context.Background()
	if thread != nil {
		// Use thread context if available
		if threadCtx := thread.Local("context"); threadCtx != nil {
			if threadContext, ok := threadCtx.(context.Context); ok {
				ctx = threadContext
			}
		}
	}

	cacheKey := c.buildKey(key)
	expiration := time.Duration(ttl) * time.Second

	err := c.cache.client.Set(ctx, cacheKey, value, expiration).Err()
	if err != nil {
		return fmt.Errorf("failed to set key %s in Redis: %w", cacheKey, err)
	}

	return nil
}

// FlushApp removes all cache entries for the current app and device
func (c *ContextualRedisCache) FlushApp(ctx context.Context) error {
	pattern := fmt.Sprintf("%s/%s/*", c.appID, c.deviceID)

	iter := c.cache.client.Scan(ctx, 0, pattern, 0).Iterator()
	var keys []string

	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan for keys with pattern %s: %w", pattern, err)
	}

	if len(keys) > 0 {
		err := c.cache.client.Del(ctx, keys...).Err()
		if err != nil {
			return fmt.Errorf("failed to delete keys: %w", err)
		}
	}

	return nil
}

// FlushDevice removes all cache entries for the current device (across all apps)
func (c *ContextualRedisCache) FlushDevice(ctx context.Context) error {
	pattern := fmt.Sprintf("*/%s/*", c.deviceID)

	iter := c.cache.client.Scan(ctx, 0, pattern, 0).Iterator()
	var keys []string

	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan for keys with pattern %s: %w", pattern, err)
	}

	if len(keys) > 0 {
		err := c.cache.client.Del(ctx, keys...).Err()
		if err != nil {
			return fmt.Errorf("failed to delete keys: %w", err)
		}
	}

	return nil
}

// Stats returns cache statistics for the current app and device
func (c *ContextualRedisCache) Stats(ctx context.Context) (int64, error) {
	pattern := fmt.Sprintf("%s/%s/*", c.appID, c.deviceID)

	var count int64
	iter := c.cache.client.Scan(ctx, 0, pattern, 0).Iterator()

	for iter.Next(ctx) {
		count++
	}

	if err := iter.Err(); err != nil {
		return 0, fmt.Errorf("failed to count keys with pattern %s: %w", pattern, err)
	}

	return count, nil
}
