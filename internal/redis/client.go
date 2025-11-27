package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	// Generate consumer name if not provided
	if cfg.ConsumerName == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "unknown"
		}
		cfg.ConsumerName = fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano())
	}

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

	logger.Info("Connected to Redis",
		zap.String("addr", cfg.Addr),
		zap.String("consumer_group", cfg.ConsumerGroup),
		zap.String("consumer_name", cfg.ConsumerName))

	// Initialize consumer group for the stream
	if err := client.initializeConsumerGroup(); err != nil {
		logger.Warn("Failed to initialize consumer group (may already exist)", zap.Error(err))
	}

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

// initializeConsumerGroup creates the consumer group for the render requests stream
func (c *Client) initializeConsumerGroup() error {
	const streamKey = "matrx:render_requests"

	// Create consumer group if it doesn't exist
	// Using "0" as the ID means start from the beginning
	// Using "$" would mean start from new messages only
	err := c.client.XGroupCreateMkStream(c.ctx, streamKey, c.config.ConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("failed to create consumer group: %w", err)
	}

	c.logger.Info("Consumer group initialized",
		zap.String("stream", streamKey),
		zap.String("group", c.config.ConsumerGroup))

	return nil
}

// ReadFromStream reads messages from the render requests stream using consumer group
func (c *Client) ReadFromStream(ctx context.Context, count int64, block time.Duration) ([]redis.XStream, error) {
	const streamKey = "matrx:render_requests"

	// Read from stream using consumer group
	// ">" means only new messages not yet delivered to other consumers
	streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.config.ConsumerGroup,
		Consumer: c.config.ConsumerName,
		Streams:  []string{streamKey, ">"},
		Count:    count,
		Block:    block,
		NoAck:    false, // We want to explicitly acknowledge messages
	}).Result()

	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to read from stream: %w", err)
	}

	return streams, nil
}

// AcknowledgeMessage acknowledges a message from the stream
func (c *Client) AcknowledgeMessage(ctx context.Context, messageID string) error {
	const streamKey = "matrx:render_requests"

	err := c.client.XAck(ctx, streamKey, c.config.ConsumerGroup, messageID).Err()
	if err != nil {
		return fmt.Errorf("failed to acknowledge message %s: %w", messageID, err)
	}

	return nil
}

// IsHealthy checks if Redis connection is healthy
func (c *Client) IsHealthy() bool {
	return c.client.Ping(c.ctx).Err() == nil
}
