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

// Start starts consuming messages from the specified queue with automatic reconnection
func (c *Consumer) Start(ctx context.Context, queueName string) error {
	retryDelay := time.Second
	maxRetryDelay := 30 * time.Second
	retryCount := 0

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Consumer context cancelled, stopping")
			return ctx.Err()
		default:
			// Attempt to start consuming messages
			if err := c.startConsuming(ctx, queueName); err != nil {
				retryCount++
				c.logger.Error("Consumer failed, will retry after delay",
					zap.Error(err),
					zap.String("queue", queueName),
					zap.Int("retry_count", retryCount),
					zap.Duration("retry_delay", retryDelay))

				// Wait before retrying with exponential backoff, but respect context cancellation
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay):
					// Exponential backoff with jitter
					retryDelay = time.Duration(float64(retryDelay) * 1.5)
					if retryDelay > maxRetryDelay {
						retryDelay = maxRetryDelay
					}
					continue
				}
			} else {
				// Reset retry delay on successful connection
				retryDelay = time.Second
				retryCount = 0
			}
		}
	}
}

// startConsuming handles a single consumption session
func (c *Consumer) startConsuming(ctx context.Context, queueName string) error {
	// Ensure we have a valid connection
	if err := c.conn.EnsureConnection(); err != nil {
		return fmt.Errorf("failed to ensure connection: %w", err)
	}

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
		// If consume fails, force a reconnection on next attempt
		c.logger.Warn("Failed to register consumer, forcing reconnection",
			zap.Error(err),
			zap.String("queue", queueName))

		// Force close current connection to ensure reconnection
		c.conn.forceClose()

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
				c.logger.Warn("Message channel closed, will reconnect")
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
		// Log error but create a result with empty output
		c.logger.Error("Failed to handle event",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		// Create result with empty output on error
		result = &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "", // Empty output on error
			ProcessedAt:  time.Now(),
		}
	}

	// Always publish result (successful or error)
	if publishErr := c.conn.PublishResult(ctx, result); publishErr != nil {
		c.logger.Error("Failed to publish result",
			zap.Error(publishErr),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		// Only requeue if it was a successful render that failed to publish
		// For error results, we still want to ack to avoid infinite retry loops
		if err == nil {
			msg.Nack(false, true) // Requeue the message only for successful renders
		} else {
			// For error results, acknowledge anyway to prevent infinite retries
			if ackErr := msg.Ack(false); ackErr != nil {
				c.logger.Error("Failed to acknowledge message after publish error",
					zap.Error(ackErr),
					zap.String("app_id", request.AppID),
					zap.String("device_id", request.Device.ID))
			}
		}
		return
	}

	// Acknowledge the message on successful publish
	if ackErr := msg.Ack(false); ackErr != nil {
		c.logger.Error("Failed to acknowledge message",
			zap.Error(ackErr),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))
	}
}
