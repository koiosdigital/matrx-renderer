# MATRX Renderer

A Go-based Redis pub/sub event processor for rendering Tidbyt Pixlet applications. This service receives render requests via Redis channels, processes them using Pixlet, and returns results through device-specific Redis channels.

## Features

- **Redis Pub/Sub**: Consumes render requests and publishes results via Redis (lightweight, high-performance)
- **Pixlet Processing**: Renders Tidbyt applications using the Pixlet engine
- **Redis Caching**: Distributed caching layer with app/device scoped keys
- **12-Factor App**: Environment-based configuration following 12-factor principles
- **Security**: Non-root container user with read-only filesystem access
- **Graceful Shutdown**: Proper signal handling and cleanup
- **Structured Logging**: JSON-structured logging with Zap
- **Health Checks**: Container and service health monitoring

## Architecture

```
API → Redis Stream: "matrx:render_requests" → [Consumer Group] → MATRX Renderer (scalable) → Pixlet → Redis Pub/Sub: "device:{device_id}" → Device
```

The service uses a hybrid Redis architecture optimized for both work distribution and real-time delivery:

### Redis Streams (Input - Work Queue Pattern)

- **Stream**: `matrx:render_requests`
- **Consumer Group**: Enables horizontal scaling with automatic load balancing
- **Features**:
  - Multiple render workers can consume from the same stream
  - Consumer groups ensure each message is processed exactly once
  - Messages persist until explicitly acknowledged (XACK)
  - Failed messages can be retried or moved to dead letter queue
  - Perfect for distributed work processing

### Redis Pub/Sub (Output - Real-time Delivery)

- **Channels**: `device:{device_id}` (per-device channels)
- **Features**:
  - Instant delivery to subscribed devices
  - Simple, fast, ephemeral messages
  - No message backlog or cleanup needed
  - Perfect for real-time device control

### Processing Flow

1. Device/API publishes render request to `matrx:render_requests` stream
2. Render worker consumes message from stream using consumer group
3. Worker validates and processes request using Pixlet
4. Worker publishes result to device-specific `device:{device_id}` pub/sub channel
5. Device receives result instantly via pub/sub subscription
6. Worker acknowledges message in stream (XACK)
7. Handles errors gracefully with proper logging and empty result responses

## HTTP API

Alongside the Redis worker pipeline, the renderer exposes a lightweight HTTP API that mirrors the same schema-driven workflows:

- `GET /health` – simple service heartbeat.
- `GET /apps` and `GET /apps/{id}` – enumerate loaded Pixlet apps from the registry.
- `GET /apps/{id}/schema` – retrieve the Pixlet app schema; `POST /apps/{id}/schema` validates a configuration. **Send the configuration object at the JSON root** (no nested `config` wrapper). The response includes normalized defaults plus structured field errors.
- `POST /apps/{id}/render` – validates the provided configuration and returns a JSON payload containing the base64-encoded WebP render output along with the normalized config. Optional query parameters `width`, `height`, and `device_id` control rendering dimensions (defaults 64×32) and logging metadata.
- `GET /apps/{id}/preview.webp` / `GET /apps/{id}/preview.gif` – render previews using schema defaults (no request body) and stream the binary WebP or GIF response. Use the optional `width` and `height` query parameters to override device dimensions.

These HTTP utilities are ideal for local testing, schema validation, or generating previews without publishing into Redis.

## Configuration

All configuration is done via environment variables:

### Redis Settings

- `REDIS_URL`: Redis connection string (default: `redis://localhost:6379`)
- `REDIS_ADDR`: Alternative Redis address format (default: `localhost:6379`)
- `REDIS_PASSWORD`: Redis password (default: empty)
- `REDIS_DB`: Redis database number (default: `0`)
- `REDIS_CONSUMER_GROUP`: Consumer group name for streams (default: `matrx-renderer-group`)
- `REDIS_CONSUMER_NAME`: Consumer name (auto-generated if not provided: `{hostname}-{timestamp}`)

### Server Settings

- `SERVER_PORT`: HTTP port for health checks (default: `8080`)
- `SERVER_READ_TIMEOUT`: Read timeout in seconds (default: `10`)
- `SERVER_WRITE_TIMEOUT`: Write timeout in seconds (default: `10`)

### Pixlet Settings

- `PIXLET_APPS_PATH`: Path to Pixlet apps directory (default: `/opt/apps`)

**App Directory Structure**: Apps are organized in nested directories as `/opt/apps/{app_id}/{app_id}.star`. The Docker build automatically downloads apps from the [matrx-apps repository](https://github.com/koiosdigital/matrx-apps).

### Redis Settings (Optional)

- `REDIS_ADDR`: Redis server address (default: `localhost:6379`)
- `REDIS_PASSWORD`: Redis password (optional)
- `REDIS_DB`: Redis database number (default: `0`)

### Logging

- `LOG_LEVEL`: Log level (default: `info`)

## Caching

The renderer supports both in-memory and Redis-based caching:

- **In-Memory Cache**: Used by default when no Redis configuration is provided
- **Redis Cache**: Automatically enabled when `REDIS_ADDR` is configured
- **Cache Scoping**: Keys are scoped as `/{applet_id}/{device_id}/{key_name}`
- **TTL Support**: Configurable time-to-live for cached values

For detailed Redis cache configuration and usage, see [REDIS_CACHE.md](REDIS_CACHE.md).

## App Structure

Pixlet apps are organized in a nested directory structure within the apps path:

```
/opt/apps/
├── clock/
│   └── clock.star
├── weather/
│   └── weather.star
└── news/
    └── news.star
```

Each app must:

- Be in its own directory named after the app ID
- Contain a `.star` file with the same name as the directory
- Follow the Pixlet app structure and conventions

The Docker build process automatically downloads apps from the [koiosdigital/matrx-apps](https://github.com/koiosdigital/matrx-apps) repository during image creation.

## Message Format

### Publishing Render Requests

To send a render request, publish to the `matrx:render_requests` Redis Stream:

**Using Redis CLI:**

```bash
redis-cli XADD matrx:render_requests * payload '{"type":"render_request","uuid":"req-123","app_id":"clock","device":{"id":"CN","width":64,"height":32},"params":{"timezone":"America/New_York"}}'
```

**Using redis-py (Python):**

```python
import redis
import json

r = redis.Redis(host='localhost', port=6379, db=0)

request = {
    "type": "render_request",
    "uuid": "req-123",
    "app_id": "clock",
    "device": {
        "id": "CN",
        "width": 64,
        "height": 32
    },
    "params": {
        "timezone": "America/New_York"
    }
}

r.xadd('matrx:render_requests', {'payload': json.dumps(request)})
```

**Using go-redis (Go):**

```go
import (
    "encoding/json"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

request := map[string]interface{}{
    "type": "render_request",
    "uuid": "req-123",
    "app_id": "clock",
    "device": map[string]interface{}{
        "id": "CN",
        "width": 64,
        "height": 32,
    },
    "params": map[string]string{
        "timezone": "America/New_York",
    },
}

payload, _ := json.Marshal(request)
rdb.XAdd(ctx, &redis.XAddArgs{
    Stream: "matrx:render_requests",
    Values: map[string]interface{}{"payload": string(payload)},
})
```

### Render Request Format

```json
{
  "type": "render_request",
  "uuid": "unique-request-id",
  "app_id": "clock",
  "device": {
    "id": "device-uuid-or-string",
    "width": 64,
    "height": 32
  },
  "params": {
    "timezone": "America/New_York",
    "format": "12h"
  }
}
```

### Render Result Format

Results are published to device-specific pub/sub channels: `device:{device_id}`

**Subscribe to results:**

```bash
# Redis CLI
redis-cli SUBSCRIBE device:CN

# Python
import redis
r = redis.Redis()
pubsub = r.pubsub()
pubsub.subscribe('device:CN')
for message in pubsub.listen():
    print(message)
```

**Result payload:**

```json
{
  "type": "render_result",
  "uuid": "unique-request-id",
  "device_id": "device-uuid-or-string",
  "app_id": "clock",
  "render_output": "base64-encoded-webp-data",
  "processed_at": "2025-08-12T10:30:05Z"
}
```

**Note**: On error, the service logs the error to console.

## Queue Routing

The service uses a dynamic queue routing system:

### Input

- **Queue**: `matrx.renderer_requests`
- **Routing Key**: `renderer_requests`
- All render requests are sent to this single queue

### Output

- **Queue**: `matrx.{DEVICE_ID}` (e.g., `matrx.device-123`)
- **Routing Key**: `{DEVICE_ID}` (e.g., `device-123`)
- Each device gets its own result queue for isolation
- Queues are created automatically when the first result is published

This design allows:

- Multiple renderer instances to consume from the same input queue
- Device-specific result routing for proper message isolation
- Automatic scaling based on queue depth

## Horizontal Scaling

The MATRX renderer is designed for horizontal scaling with multiple instances:

### Key Features

1. **Fair Load Distribution**: Each instance processes only one message at a time.
2. **Message Safety**: Manual acknowledgment ensures messages are only removed after successful processing
3. **Automatic Failover**: Failed messages are requeued for other instances to process
4. **Instance Identification**: Each consumer has a unique tag for monitoring and debugging

### Scaling Guidelines

1. **Start with 1-2 instances** and monitor queue depth
2. **Scale up** when average queue depth consistently exceeds desired latency
3. **Monitor CPU/memory** usage per instance - rendering is CPU-intensive
4. **Use container orchestration** (Kubernetes, Docker Swarm) for automatic scaling

### Example: Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: matrx-renderer
spec:
  replicas: 3 # Start with 3 instances
  selector:
    matchLabels:
      app: matrx-renderer
  template:
    metadata:
      labels:
        app: matrx-renderer
    spec:
      containers:
        - name: renderer
          image: matrx-renderer:latest
          resources:
            requests:
              cpu: 500m
              memory: 512Mi
            limits:
              cpu: 1000m
              memory: 1Gi
```

### Horizontal Scaling

The renderer is designed for horizontal scaling using Redis Streams consumer groups:

1. **Automatic Load Balancing**: Redis consumer groups distribute messages across all instances
2. **No Configuration Changes**: Simply increase replica count - each instance auto-registers
3. **Exactly-Once Processing**: Consumer groups ensure each message is processed by only one worker
4. **Fault Tolerance**: Failed instances don't lose messages - they can be reassigned to healthy workers

**Scaling Example:**

```bash
# Scale to 5 instances
kubectl scale deployment matrx-renderer --replicas=5

# Scale based on CPU usage
kubectl autoscale deployment matrx-renderer --cpu-percent=70 --min=3 --max=10
```

### Monitoring

Monitor these metrics for scaling decisions:

- Stream pending messages: `XPENDING matrx:render_requests`
- Consumer group lag: Messages not yet acknowledged
- Message processing rate per instance
- CPU/Memory usage per instance
- Error rates and failed message counts

## Development

### Prerequisites

- Go 1.21+
- Docker and Docker Compose
- Pixlet CLI tool

### Local Development

1. Clone the repository
2. Copy environment file: `cp .env.example .env`
3. Install dependencies: `go mod download`
4. Run RabbitMQ: `docker-compose up -d rabbitmq`
5. Create apps directory: `mkdir -p apps`
6. Add your Pixlet `.star` files to the `apps` directory
7. Run the service: `go run cmd/server/main.go`

### Docker Development

1. Build and run with Docker Compose:

   ```bash
   docker-compose up --build
   ```

2. Access RabbitMQ Management UI at http://localhost:15672 (guest/guest)

### Testing

Run tests with:

```bash
go test ./...
```

## Deployment

### Docker

Build the image:

```bash
docker build -t matrx-renderer .
```

### Kubernetes

The application is designed to work well in Kubernetes with:

- ConfigMaps for configuration
- Secrets for sensitive data
- ReadOnlyRootFilesystem security context
- Resource limits and requests
- Health check endpoints

## Security Features

- **Non-root user**: Container runs as user ID 1001
- **Read-only filesystem**: Apps directory mounted read-only
- **Path validation**: Prevents directory traversal attacks
- **Input sanitization**: Validates configuration parameters
- **Minimal attack surface**: Alpine-based minimal container
- **No shell access**: User has no shell (`/sbin/nologin`)

## Monitoring

The service provides:

- Health check endpoint for container orchestration
- Structured JSON logging for log aggregation
- Error tracking with correlation IDs
- Performance metrics through logging

## License

MIT License - see LICENSE file for details
