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

# Set build arguments for architecture detection
ARG TARGETARCH

# Install runtime dependencies including git for periodic pulls
RUN apk add --no-cache ca-certificates tzdata libwebp libwebpmux libwebpdemux git curl \
    && update-ca-certificates

# Download and install s6-overlay based on architecture
RUN case ${TARGETARCH} in \
        amd64) S6_ARCH=x86_64 ;; \
        arm64) S6_ARCH=aarch64 ;; \
        *) echo "Unsupported architecture: ${TARGETARCH}" && exit 1 ;; \
    esac \
    && curl -L "https://github.com/just-containers/s6-overlay/releases/download/v3.1.6.2/s6-overlay-noarch.tar.xz" -o /tmp/s6-overlay-noarch.tar.xz \
    && curl -L "https://github.com/just-containers/s6-overlay/releases/download/v3.1.6.2/s6-overlay-${S6_ARCH}.tar.xz" -o /tmp/s6-overlay-arch.tar.xz \
    && tar -C / -Jxpf /tmp/s6-overlay-noarch.tar.xz \
    && tar -C / -Jxpf /tmp/s6-overlay-arch.tar.xz \
    && rm -f /tmp/s6-overlay-*.tar.xz

# Create non-root user with home directory
RUN addgroup -g 1001 -S appuser \
    && adduser -S -D -u 1001 -h /home/appuser -s /sbin/nologin -G appuser -g appuser appuser

# Create app directory and apps directory
RUN mkdir -p /app /opt/apps /home/appuser \
    && chown -R appuser:appuser /app /home/appuser \
    && chown -R appuser:appuser /opt/apps \
    && chmod -R 750 /opt/apps

# Download matrx-apps repository with git credentials for future pulls
RUN git clone https://github.com/koiosdigital/matrx-apps.git /opt/apps \
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
    && mkdir -p /etc/s6-overlay/s6-rc.d/user/contents.d

# Create renderer service with shell script that inherits environment
RUN echo "longrun" > /etc/s6-overlay/s6-rc.d/renderer/type \
    && echo "appuser" > /etc/s6-overlay/s6-rc.d/renderer/user \
    && printf '#!/command/with-contenv sh\ncd /app\nexec ./matrx-renderer\n' > /etc/s6-overlay/s6-rc.d/renderer/run \
    && chmod +x /etc/s6-overlay/s6-rc.d/renderer/run

# Create git-puller service with shell script that inherits environment
RUN echo "longrun" > /etc/s6-overlay/s6-rc.d/git-puller/type \
    && echo "appuser" > /etc/s6-overlay/s6-rc.d/git-puller/user \
    && printf '#!/command/with-contenv sh\necho "Starting git puller service..."\n# Set environment variables\nexport HOME=/home/appuser\n# Configure git safe directory at runtime\ngit config --global --add safe.directory /opt/apps\ngit config --global user.email "renderer@koios.digital"\ngit config --global user.name "Matrx Renderer"\nwhile true; do\n    echo "Pulling latest changes..."\n    cd /opt/apps\n    if git pull origin main; then\n        echo "Git pull completed successfully"\n    else\n        echo "Git pull failed, retrying in 60 seconds"\n    fi\n    sleep 60\ndone\n' > /etc/s6-overlay/s6-rc.d/git-puller/run \
    && chmod +x /etc/s6-overlay/s6-rc.d/git-puller/run

# Add services to user bundle
RUN touch /etc/s6-overlay/s6-rc.d/user/contents.d/renderer \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/git-puller \
    && echo "bundle" > /etc/s6-overlay/s6-rc.d/user/type

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep matrx-renderer > /dev/null || exit 1

# Set s6 environment
ENV S6_BEHAVIOUR_IF_STAGE2_FAILS=2

# Use s6-overlay as init
ENTRYPOINT ["/init"]
