package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/pkg/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Connection wraps the AMQP connection and channel
type Connection struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	config  config.AMQPConfig
	logger  *zap.Logger
}

// NewConnection creates a new AMQP connection
func NewConnection(cfg config.AMQPConfig, logger *zap.Logger) (*Connection, error) {
	c := &Connection{
		config: cfg,
		logger: logger,
	}

	if err := c.connect(); err != nil {
		return nil, err
	}

	return c, nil
}

// connect establishes the AMQP connection and channel
func (c *Connection) connect() error {
	// Close existing connections if any
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}

	conn, err := amqp.Dial(c.config.URL)
	if err != nil {
		return fmt.Errorf("failed to connect to AMQP: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}

	// Set QoS for fair distribution across multiple consumers
	// This ensures each consumer gets only the configured number of unacknowledged messages
	err = ch.Qos(
		c.config.PrefetchCount, // prefetch count (number of messages to prefetch)
		0,                      // prefetch size (0 = no limit on message size)
		false,                  // global (false = apply to current consumer only)
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	// Declare input queue
	_, err = ch.QueueDeclare(
		c.config.QueueName, // name (e.g., matrx.render_requests)
		false,              // durable
		true,               // delete when unused
		false,              // exclusive
		false,              // no-wait
		nil,                // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("failed to declare input queue: %w", err)
	}

	// Update connection and channel
	c.conn = conn
	c.channel = ch

	c.logger.Info("AMQP connection established",
		zap.String("queue", c.config.QueueName))

	return nil
}

// EnsureConnection checks if the connection is healthy and reconnects if needed
func (c *Connection) EnsureConnection() error {
	// Check if connection or channel is nil or closed
	if c.conn == nil || c.conn.IsClosed() || c.channel == nil || c.isChannelUnusable() {
		c.logger.Warn("AMQP connection/channel lost, attempting to reconnect")

		// Try to reconnect up to 3 times with immediate retries
		var lastErr error
		for i := 0; i < 3; i++ {
			if err := c.connect(); err != nil {
				lastErr = err
				c.logger.Warn("Reconnection attempt failed",
					zap.Int("attempt", i+1),
					zap.Error(err))

				// Small delay between retries
				time.Sleep(time.Millisecond * 100)
				continue
			}
			return nil
		}
		return fmt.Errorf("failed to reconnect after 3 attempts: %w", lastErr)
	}
	return nil
}

// isChannelUnusable checks if the channel is closed or unusable
func (c *Connection) isChannelUnusable() bool {
	if c.channel == nil {
		return true
	}

	// Try a simple QoS operation to test if channel is usable
	// This will return an error if the channel is closed
	err := c.channel.Qos(c.config.PrefetchCount, 0, false)
	return err != nil
}

// Close closes the AMQP connection and channel
func (c *Connection) Close() error {
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// forceClose forcibly closes the connection and sets references to nil
// This ensures the next EnsureConnection call will create new connections
func (c *Connection) forceClose() {
	if c.channel != nil {
		c.channel.Close()
		c.channel = nil
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.logger.Debug("Forcibly closed AMQP connection")
}

// PublishResult publishes a result message to the device-specific queue
func (c *Connection) PublishResult(ctx context.Context, result *models.RenderResult) error {
	// Ensure we have a valid connection
	if err := c.EnsureConnection(); err != nil {
		return fmt.Errorf("failed to ensure connection: %w", err)
	}

	// Create device-specific queue name: matrx.{DEVICE_ID}
	deviceQueue := fmt.Sprintf("matrx.%s", result.DeviceID)

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	// Publish directly to the device queue (no exchange)
	err = c.channel.PublishWithContext(
		ctx,
		"",          // exchange (empty string means default exchange)
		deviceQueue, // routing key (queue name for direct publishing)
		true,        // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		// If publish fails, force close connection to ensure reconnection
		c.logger.Warn("Failed to publish result, forcing reconnection", zap.Error(err))
		c.forceClose()
		return fmt.Errorf("failed to publish result: %w", err)
	}

	c.logger.Debug("Published result to device queue",
		zap.String("device_id", result.DeviceID),
		zap.String("app_id", result.AppID),
		zap.String("queue", deviceQueue))
	return nil
}
