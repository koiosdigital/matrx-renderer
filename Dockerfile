# Build stage
FROM golang:alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata libwebp libwebp-dev gcc musl-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -o matrx-renderer ./cmd/server

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata libwebp libwebpmux libwebpdemux \
    && update-ca-certificates

# Create non-root user
RUN addgroup -g 1001 -S appuser \
    && adduser -S -D -H -u 1001 -h /app -s /sbin/nologin -G appuser -g appuser appuser

# Create app directory and apps directory
RUN mkdir -p /app /opt/apps \
    && chown -R appuser:appuser /app \
    && chown -R root:appuser /opt/apps \
    && chmod -R 750 /opt/apps

# Download matrx-apps repository
RUN apk add --no-cache git \
    && git clone https://github.com/koiosdigital/matrx-apps.git /tmp/matrx-apps \
    && cp -r /tmp/matrx-apps/* /opt/apps/ \
    && rm -rf /tmp/matrx-apps \
    && chown -R root:appuser /opt/apps \
    && chmod -R 750 /opt/apps \
    && apk del git

# Copy the binary from builder stage
COPY --from=builder /build/matrx-renderer /app/matrx-renderer

# Make binary executable and owned by root
RUN chmod +x /app/matrx-renderer \
    && chown root:root /app/matrx-renderer

# Switch to non-root user
USER appuser

# Set working directory
WORKDIR /app

# Expose port (if needed for health checks)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep matrx-renderer > /dev/null || exit 1

# Run the application
CMD ["./matrx-renderer"]
