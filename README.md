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
WebSocket API → Redis: "matrx:render_requests" → MATRX Renderer → Pixlet → Redis: "device:{device_id}:responses" → WebSocket API
```

The service:

1. Subscribes to render requests from the `matrx:render_requests` Redis channel
2. Validates and processes requests using Pixlet
3. Publishes results to device-specific Redis channels: `device:{device_id}:responses`
4. Handles errors gracefully with proper logging and empty result responses

## Configuration

All configuration is done via environment variables:

### Redis Settings

- `REDIS_URL`: Redis connection string (default: `redis://localhost:6379`)
- `REDIS_ADDR`: Alternative Redis address format (default: `localhost:6379`)
- `REDIS_PASSWORD`: Redis password (default: empty)
- `REDIS_DB`: Redis database number (default: `0`)

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

### Render Request

```json
{
  "type": "render_request",
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

### Render Result

```json
{
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

### Monitoring

Monitor these metrics for scaling decisions:

- Queue depth in `matrx.renderer_requests`
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
