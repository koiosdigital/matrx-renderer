# Redis Cache Implementation

This document describes the Redis-based caching implementation for the MATRX renderer service.

## Architecture

The Redis cache uses a shared instance pattern with contextual wrappers to provide key scoping while maintaining efficient connection usage.

### Key Components

1. **RedisCache**: Main Redis client wrapper that manages the connection
2. **ContextualRedisCache**: Per-request wrapper that provides app/device scoped caching
3. **Cache Interface**: Implements the runtime.Cache interface for Pixlet compatibility

## Usage

### Server Initialization

```go
// Create shared Redis cache instance
redisCache := pixlet.NewRedisCache(&config.RedisConfig{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
})
defer redisCache.Close()

// Create processor with shared Redis instance
processor := pixlet.NewProcessorWithRedis(redisCache)
```

### Per-Request Usage

```go
// Create contextual cache for specific app and device
cache := redisCache.WithContext(appID, deviceID)

// Use cache in Pixlet app
result, err := processor.RenderAppWithCache(appPath, cache)
```

## Key Scoping

All cache keys are automatically scoped using the pattern:

```
/{app_id}/{device_id}/{original_key}
```

### Examples

- App: `clock`, Device: `device123`, Key: `weather_data`
- Redis Key: `/clock/device123/weather_data`

- App: `stocks`, Device: `device456`, Key: `portfolio/AAPL`
- Redis Key: `/stocks/device456/portfolio_AAPL`

## Key Normalization

Special characters in keys are automatically cleaned:

- Forward slashes (`/`) are replaced with underscores (`_`)
- This prevents Redis key parsing issues while maintaining readability

## Cache Operations

### Basic Operations

```go
// Set value with TTL (in seconds)
err := cache.Set(thread, "my_key", []byte("my_value"), 3600)

// Get value
value, found, err := cache.Get(thread, "my_key")
```

### Bulk Operations

```go
// Flush all keys for current app/device
err := cache.FlushApp(ctx)

// Flush all keys for current device (across all apps)
err := cache.FlushDevice(ctx)

// Get statistics for current app/device
count, err := cache.Stats(ctx)
```

## Configuration

Redis connection is configured via environment variables:

```bash
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0
```

## Testing

Tests require a running Redis instance. They will be skipped if Redis is not available:

```bash
# Start Redis (using Docker)
docker run -d -p 6379:6379 redis:alpine

# Run tests
go test ./internal/pixlet/...
```

## Performance Considerations

### Connection Efficiency

- Single Redis client instance is shared across all requests
- Connection pooling is handled automatically by the Redis client
- No connection overhead per request

### Key Isolation

- Each app/device combination gets its own key namespace
- Prevents cache collisions between different contexts
- Enables targeted cache invalidation

### Memory Management

- TTL values prevent indefinite memory growth
- Bulk flush operations for cleanup
- Pattern-based scanning for targeted operations

## Error Handling

The cache implementation gracefully handles:

- Redis connection failures (falls back to no caching)
- Key not found conditions (returns found=false)
- Network timeouts and reconnections
- Invalid TTL values

## Security

- Keys are scoped to prevent cross-tenant data access
- No sensitive data is logged in error messages
- Redis AUTH is supported via configuration

## Starlark Integration

The cache integrates seamlessly with Pixlet's Starlark runtime:

```python
# In your .star files
load("cache.star", "cache")

def main():
    # Check cache first
    cached_data = cache.get("weather_data")
    if cached_data:
        return render_with_data(cached_data)

    # Fetch new data
    data = fetch_weather_data()

    # Cache for 5 minutes
    cache.set("weather_data", data, ttl_seconds=300)

    return render_with_data(data)
```

## Docker Compose Example

```yaml
version: "3.8"
services:
  redis:
    image: redis:alpine
    ports:
      - "6379:6379"
    command: redis-server --appendonly yes
    volumes:
      - redis_data:/data

  matrx-renderer:
    build: .
    environment:
      - REDIS_ADDR=redis:6379
      - REDIS_DB=0
    depends_on:
      - redis

volumes:
  redis_data:
```

## Migration from InMemoryCache

The RedisCache implements the same `runtime.Cache` interface as InMemoryCache, so migration is seamless:

1. Add Redis configuration to your environment
2. The system automatically creates a shared Redis instance
3. Each request gets a contextual cache wrapper
4. No code changes required for existing Pixlet apps

## Advanced Usage

### Manual Cache Creation

```go
// Create Redis client directly
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
    DB:   0,
})

// Create shared cache from client
sharedCache := pixlet.NewRedisCacheFromClient(client)

// Create contextual caches
cache1 := sharedCache.WithContext("app1", "device1")
cache2 := sharedCache.WithContext("app2", "device2")
```

### Cache Statistics

```go
// Get cache statistics for specific app/device
count, err := cache.Stats(context.Background())
if err != nil {
    log.Printf("Failed to get stats: %v", err)
} else {
    log.Printf("Cache contains %d keys for this app/device", count)
}
```

### Targeted Cache Clearing

```go
ctx := context.Background()

// Clear all cache entries for specific app/device
err := cache.FlushApp(ctx)

// Clear all cache entries for a device across all apps
err := cache.FlushDevice(ctx)
```

This architecture provides optimal performance with proper isolation and resource management.
