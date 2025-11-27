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

// consumeMessages handles the actual message consumption
func (c *Consumer) consumeMessages() error {
	pubsub, ch, err := c.client.SubscribeToRenderRequests()
	if err != nil {
		return err
	}
	defer pubsub.Close()

	c.logger.Info("Started consuming Redis messages")

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case msg := <-ch:
			if msg == nil {
				continue
			}

			c.handleMessage(msg)
		case <-time.After(30 * time.Second):
			// Periodic health check to ensure subscription is still active
			if !c.client.IsHealthy() {
				return fmt.Errorf("Redis connection unhealthy, will reconnect")
			}
		}
	}
}

// handleMessage processes a single Redis message
func (c *Consumer) handleMessage(msg *redis.Message) {
	c.logger.Debug("Received render request",
		zap.String("channel", msg.Channel),
		zap.String("payload_length", fmt.Sprintf("%d", len(msg.Payload))))

	var request models.RenderRequest
	if err := json.Unmarshal([]byte(msg.Payload), &request); err != nil {
		c.logger.Error("Failed to unmarshal render request",
			zap.Error(err),
			zap.String("payload", msg.Payload))
		return
	}

	// Handle the request
	result, err := c.handler.Handle(c.ctx, &request)
	if err != nil {
		c.logger.Error("Failed to handle render request",
			zap.Error(err),
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

	// Publish the result to device-specific channel
	if err := c.client.PublishRenderResult(result); err != nil {
		c.logger.Error("Failed to publish render result",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))
	}
}
