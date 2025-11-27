package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/koios/matrx-renderer/internal/handlers"
	"github.com/koios/matrx-renderer/pkg/models"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Consumer handles Redis pub/sub message consumption for render requests
type Consumer struct {
	client  *Client
	handler *handlers.EventHandler
	logger  *zap.Logger
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewConsumer creates a new Redis consumer
func NewConsumer(client *Client, handler *handlers.EventHandler, logger *zap.Logger) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		client:  client,
		handler: handler,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start starts consuming messages from the render requests channel
func (c *Consumer) Start() error {
	c.logger.Info("Starting Redis consumer for render requests")

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Info("Redis consumer stopped")
			return nil
		default:
			if err := c.consumeMessages(); err != nil {
				c.logger.Error("Error consuming messages, will retry",
					zap.Error(err),
					zap.Duration("retry_delay", 5*time.Second))
				time.Sleep(5 * time.Second)
				continue
			}
		}
	}
}

// Stop stops the consumer
func (c *Consumer) Stop() {
	c.logger.Info("Stopping Redis consumer")
	c.cancel()
}

// consumeMessages handles the actual message consumption from Redis Streams
func (c *Consumer) consumeMessages() error {
	c.logger.Info("Started consuming Redis stream messages")

	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
			// Read messages from stream with blocking timeout
			streams, err := c.client.ReadFromStream(c.ctx, 10, 5*time.Second)
			if err != nil {
				// Check if connection is healthy
				if !c.client.IsHealthy() {
					return fmt.Errorf("Redis connection unhealthy, will reconnect")
				}
				c.logger.Error("Error reading from stream", zap.Error(err))
				time.Sleep(1 * time.Second)
				continue
			}

			// Process messages from the stream
			for _, stream := range streams {
				for _, message := range stream.Messages {
					c.handleStreamMessage(message)
				}
			}
		}
	}
}

// handleStreamMessage processes a single Redis Stream message
func (c *Consumer) handleStreamMessage(msg redis.XMessage) {
	c.logger.Debug("Received render request from stream",
		zap.String("message_id", msg.ID),
		zap.Int("fields_count", len(msg.Values)))

	// Extract the payload from the stream message
	payload, ok := msg.Values["payload"].(string)
	if !ok {
		c.logger.Error("Failed to extract payload from stream message",
			zap.String("message_id", msg.ID))
		// Acknowledge the message anyway to prevent reprocessing
		_ = c.client.AcknowledgeMessage(c.ctx, msg.ID)
		return
	}

	var request models.RenderRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		c.logger.Error("Failed to unmarshal render request",
			zap.Error(err),
			zap.String("message_id", msg.ID),
			zap.String("payload", payload))
		// Acknowledge the message to prevent reprocessing bad data
		_ = c.client.AcknowledgeMessage(c.ctx, msg.ID)
		return
	}

	// Handle the request
	result, err := c.handler.Handle(c.ctx, &request)
	if err != nil {
		c.logger.Error("Failed to handle render request",
			zap.Error(err),
			zap.String("message_id", msg.ID),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		// Create error result with empty output
		result = &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "", // Empty output on error
			ProcessedAt:  time.Now(),
		}
	}

	// Publish the result to device-specific pub/sub channel
	if err := c.client.PublishRenderResult(result); err != nil {
		c.logger.Error("Failed to publish render result",
			zap.Error(err),
			zap.String("message_id", msg.ID),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))
		// Don't acknowledge if we failed to publish - allow retry
		return
	}

	// Acknowledge the message after successful processing and publishing
	if err := c.client.AcknowledgeMessage(c.ctx, msg.ID); err != nil {
		c.logger.Error("Failed to acknowledge message",
			zap.Error(err),
			zap.String("message_id", msg.ID))
	} else {
		c.logger.Debug("Message processed and acknowledged",
			zap.String("message_id", msg.ID),
			zap.String("device_id", request.Device.ID),
			zap.String("app_id", request.AppID))
	}
}
