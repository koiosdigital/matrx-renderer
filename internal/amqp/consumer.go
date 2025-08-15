package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/koios/matrx-renderer/pkg/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// EventHandler defines the interface for handling events
type EventHandler interface {
	Handle(ctx context.Context, event *models.RenderRequest) (*models.RenderResult, error)
}

// Consumer handles consuming messages from AMQP
type Consumer struct {
	conn    *Connection
	handler EventHandler
	logger  *zap.Logger
}

// NewConsumer creates a new consumer
func NewConsumer(conn *Connection, handler EventHandler, logger *zap.Logger) *Consumer {
	return &Consumer{
		conn:    conn,
		handler: handler,
		logger:  logger,
	}
}

// Start starts consuming messages from the specified queue
func (c *Consumer) Start(ctx context.Context, queueName string) error {
	// Generate unique consumer tag for this instance
	hostname, _ := os.Hostname()
	consumerTag := fmt.Sprintf("matrx-renderer-%s-%d", hostname, time.Now().Unix())

	msgs, err := c.conn.channel.Consume(
		queueName,   // queue
		consumerTag, // consumer tag (unique identifier for this consumer)
		false,       // auto-ack (disabled for manual acknowledgment)
		false,       // exclusive (allow multiple consumers)
		false,       // no-local
		false,       // no-wait
		nil,         // args
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	c.logger.Info("Started consuming messages",
		zap.String("queue", queueName),
		zap.String("consumer_tag", consumerTag))

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Consumer context cancelled, stopping")
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				c.logger.Warn("Message channel closed")
				return fmt.Errorf("message channel closed")
			}

			go c.handleMessage(ctx, msg)
		}
	}
}

// handleMessage processes a single message
func (c *Consumer) handleMessage(ctx context.Context, msg amqp.Delivery) {
	c.logger.Debug("Received message",
		zap.String("routing_key", msg.RoutingKey),
		zap.String("correlation_id", msg.CorrelationId))

	// Parse the message
	var request models.RenderRequest
	if err := json.Unmarshal(msg.Body, &request); err != nil {
		c.logger.Error("Failed to unmarshal message",
			zap.Error(err),
			zap.String("correlation_id", msg.CorrelationId))
		msg.Nack(false, false)
		return
	}

	// Handle the event
	result, err := c.handler.Handle(ctx, &request)
	if err != nil {
		// As requested: log error but don't send AMQP response on error
		c.logger.Error("Failed to handle event",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		// Acknowledge the message to remove it from queue
		if ackErr := msg.Ack(false); ackErr != nil {
			c.logger.Error("Failed to acknowledge message after error",
				zap.Error(ackErr),
				zap.String("app_id", request.AppID),
				zap.String("device_id", request.Device.ID))
		}

		//publish result, with empty output
		result = &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "", // Empty output on error
			ProcessedAt:  time.Now(),
		}
	}

	// Publish successful result
	if err := c.conn.PublishResult(ctx, result); err != nil {
		c.logger.Error("Failed to publish result",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))
		msg.Nack(false, true) // Requeue the message
		return
	}

	// Acknowledge the message
	if err := msg.Ack(false); err != nil {
		c.logger.Error("Failed to acknowledge message",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))
	}
}
