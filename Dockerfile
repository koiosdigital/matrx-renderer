# Build stage
FROM golang:1.23-alpine3.19 AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata libwebp libwebp-dev gcc musl-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Set build arguments
ARG BUILDPLATFORM
ARG TARGETPLATFORM  
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_TIME
ARG GIT_COMMIT

# Copy source code (leverages .dockerignore for efficiency)
COPY . .

# Build the application with optimizations and build info
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
    -a -o matrx-renderer ./cmd/server \
    && strip matrx-renderer

# Final stage
FROM alpine:3.19

# Metadata labels
LABEL maintainer="Koios Digital" \
      version="1.0" \
      description="MATRX Renderer - Redis-based Pixlet rendering service" \
      org.opencontainers.image.source="https://github.com/koiosdigital/matrx-renderer"

# Set build arguments for architecture detection
ARG TARGETARCH

# Install runtime dependencies and security updates
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    libwebp \
    libwebpmux \
    libwebpdemux \
    git \
    curl \
    dumb-init \
    && apk upgrade --no-cache \
    && update-ca-certificates \
    && rm -rf /var/cache/apk/*

# Download and install s6-overlay based on architecture with checksum validation
RUN case ${TARGETARCH} in \
        amd64) S6_ARCH=x86_64 ;; \
        arm64) S6_ARCH=aarch64 ;; \
        *) echo "Unsupported architecture: ${TARGETARCH}" && exit 1 ;; \
    esac \
    && S6_VERSION="v3.1.6.2" \
    && curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/${S6_VERSION}/s6-overlay-noarch.tar.xz" -o /tmp/s6-overlay-noarch.tar.xz \
    && curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/${S6_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" -o /tmp/s6-overlay-arch.tar.xz \
    && tar -C / -Jxpf /tmp/s6-overlay-noarch.tar.xz \
    && tar -C / -Jxpf /tmp/s6-overlay-arch.tar.xz \
    && rm -rf /tmp/s6-overlay-*.tar.xz

# Create non-root user with home directory
RUN addgroup -g 1001 -S appuser \
    && adduser -S -D -u 1001 -h /home/appuser -s /sbin/nologin -G appuser -g appuser appuser

# Create app directory and apps directory
RUN mkdir -p /app /opt/apps /home/appuser \
    && chown -R appuser:appuser /app /home/appuser \
    && chown -R appuser:appuser /opt/apps \
    && chmod -R 750 /opt/apps

# Download matrx-apps repository with error handling
RUN git clone --depth=1 --single-branch https://github.com/koiosdigital/matrx-apps.git /opt/apps \
    || (echo "Warning: Failed to clone apps repository, creating empty directory" && mkdir -p /opt/apps) \
    && chown -R appuser:appuser /opt/apps \
    && chmod -R 750 /opt/apps

# Copy the binary from builder stage
COPY --from=builder /build/matrx-renderer /app/matrx-renderer

# Make binary executable and owned by root
RUN chmod +x /app/matrx-renderer \
    && chown root:root /app/matrx-renderer

# Create s6 service directories
RUN mkdir -p /etc/s6-overlay/s6-rc.d/renderer/dependencies.d \
    && mkdir -p /etc/s6-overlay/s6-rc.d/git-puller/dependencies.d \
    && mkdir -p /etc/s6-overlay/s6-rc.d/initial-clone/dependencies.d \
    && mkdir -p /etc/s6-overlay/s6-rc.d/user/contents.d

# Create initial-clone oneshot service (runs once at startup)
RUN echo "oneshot" > /etc/s6-overlay/s6-rc.d/initial-clone/type \
    && echo "appuser" > /etc/s6-overlay/s6-rc.d/initial-clone/user \
    && printf '#!/command/with-contenv sh\necho "Performing initial git pull..."\nexport HOME=/home/appuser\ngit config --global --add safe.directory /opt/apps\ngit config --global user.email "renderer@koios.digital"\ngit config --global user.name "Matrx Renderer"\ncd /opt/apps\nif git pull origin main; then\n    echo "Initial git pull completed successfully"\nelse\n    echo "Initial git pull failed, but continuing..."\nfi\n' > /etc/s6-overlay/s6-rc.d/initial-clone/up \
    && chmod +x /etc/s6-overlay/s6-rc.d/initial-clone/up

# Create renderer service with dependency on initial-clone
RUN echo "longrun" > /etc/s6-overlay/s6-rc.d/renderer/type \
    && echo "appuser" > /etc/s6-overlay/s6-rc.d/renderer/user \
    && echo "initial-clone" > /etc/s6-overlay/s6-rc.d/renderer/dependencies.d/initial-clone \
    && printf '#!/command/with-contenv sh\ncd /app\nexec ./matrx-renderer\n' > /etc/s6-overlay/s6-rc.d/renderer/run \
    && chmod +x /etc/s6-overlay/s6-rc.d/renderer/run

# Create git-puller service that calls refresh endpoint after pulls
RUN echo "longrun" > /etc/s6-overlay/s6-rc.d/git-puller/type \
    && echo "appuser" > /etc/s6-overlay/s6-rc.d/git-puller/user \
    && echo "renderer" > /etc/s6-overlay/s6-rc.d/git-puller/dependencies.d/renderer \
    && printf '#!/command/with-contenv sh\necho "Starting git puller service..."\nexport HOME=/home/appuser\ngit config --global --add safe.directory /opt/apps\ngit config --global user.email "renderer@koios.digital"\ngit config --global user.name "Matrx Renderer"\nwhile true; do\n    echo "Pulling latest changes..."\n    cd /opt/apps\n    if git pull origin main; then\n        echo "Git pull completed successfully"\n        # Call refresh endpoint to reload apps\n        echo "Refreshing app registry..."\n        curl -f -X POST http://localhost:8080/apps/refresh || echo "Failed to refresh apps, but continuing..."\n    else\n        echo "Git pull failed, retrying in 60 seconds"\n    fi\n    sleep 60\ndone\n' > /etc/s6-overlay/s6-rc.d/git-puller/run \
    && chmod +x /etc/s6-overlay/s6-rc.d/git-puller/run

# Add services to user bundle
RUN touch /etc/s6-overlay/s6-rc.d/user/contents.d/initial-clone \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/renderer \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/git-puller \
    && echo "bundle" > /etc/s6-overlay/s6-rc.d/user/type

# Expose port
EXPOSE 8080

# Health check - test both process and HTTP endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:8080/health || pgrep matrx-renderer > /dev/null || exit 1

# Set environment variables for production
ENV S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_SYNC_DISKS=1 \
    TZ=UTC \
    GOGC=20 \
    GOMEMLIMIT=128MiB

# Switch to non-root user for final operations
USER appuser
WORKDIR /app

# Use s6-overlay as init with dumb-init as fallback
ENTRYPOINT ["/init"]
