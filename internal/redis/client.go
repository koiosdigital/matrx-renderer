package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/pkg/models"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Client wraps the Redis client for pub/sub operations
type Client struct {
	client *redis.Client
	config config.RedisConfig
	logger *zap.Logger
	ctx    context.Context
}

// NewClient creates a new Redis client
func NewClient(cfg config.RedisConfig, logger *zap.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
		PoolTimeout:  30 * time.Second,
	})

	ctx := context.Background()

	// Test the connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	client := &Client{
		client: rdb,
		config: cfg,
		logger: logger,
		ctx:    ctx,
	}

	logger.Info("Connected to Redis", zap.String("addr", cfg.Addr))
	return client, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.client.Close()
}

// PublishRenderResult publishes a render result to the device-specific channel
func (c *Client) PublishRenderResult(result *models.RenderResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal render result: %w", err)
	}

	channel := fmt.Sprintf("device:%s", result.DeviceID)

	if err := c.client.Publish(c.ctx, channel, body).Err(); err != nil {
		return fmt.Errorf("failed to publish to Redis channel %s: %w", channel, err)
	}

	c.logger.Debug("Published render result",
		zap.String("channel", channel),
		zap.String("device_id", result.DeviceID),
		zap.String("app_id", result.AppID),
		zap.String("uuid", result.UUID))

	return nil
}

// SubscribeToRenderRequests subscribes to the render requests channel
func (c *Client) SubscribeToRenderRequests() (*redis.PubSub, <-chan *redis.Message, error) {
	pubsub := c.client.Subscribe(c.ctx, "matrx:render_requests")

	// Wait for confirmation that subscription is created
	if _, err := pubsub.Receive(c.ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to subscribe to render requests: %w", err)
	}

	c.logger.Info("Subscribed to render requests channel", zap.String("channel", "matrx:render_requests"))

	// Return the pubsub and message channel
	ch := pubsub.Channel()
	return pubsub, ch, nil
}

// IsHealthy checks if Redis connection is healthy
func (c *Client) IsHealthy() bool {
	return c.client.Ping(c.ctx).Err() == nil
}
