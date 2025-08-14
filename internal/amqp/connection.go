package amqp

import (
	"context"
	"encoding/json"
	"fmt"

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
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to AMQP: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Set QoS for fair distribution across multiple consumers
	// This ensures each consumer gets only the configured number of unacknowledged messages
	err = ch.Qos(
		cfg.PrefetchCount, // prefetch count (number of messages to prefetch)
		0,                 // prefetch size (0 = no limit on message size)
		false,             // global (false = apply to current consumer only)
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to set QoS: %w", err)
	}

	// Declare input queue
	_, err = ch.QueueDeclare(
		cfg.QueueName, // name (e.g., matrx.render_requests)
		false,         // durable
		true,          // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare input queue: %w", err)
	}

	// Note: Output queues will be declared dynamically per device in PublishResult

	return &Connection{
		conn:    conn,
		channel: ch,
		config:  cfg,
		logger:  logger,
	}, nil
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

// PublishResult publishes a result message to the device-specific queue
func (c *Connection) PublishResult(ctx context.Context, result *models.RenderResult) error {
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
		return fmt.Errorf("failed to publish result: %w", err)
	}

	c.logger.Debug("Published result to device queue",
		zap.String("device_id", result.DeviceID),
		zap.String("app_id", result.AppID),
		zap.String("queue", deviceQueue))
	return nil
}
