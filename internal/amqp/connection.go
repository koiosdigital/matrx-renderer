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

	// Declare exchange
	err = ch.ExchangeDeclare(
		cfg.Exchange, // name
		"topic",      // type
		true,         // durable
		false,        // auto-deleted
		false,        // internal
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Declare input queue
	_, err = ch.QueueDeclare(
		cfg.QueueName, // name
		true,          // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	// Note: Result queues will be declared dynamically per device in PublishResult

	// Bind queue to exchange
	err = ch.QueueBind(
		cfg.QueueName,  // queue name
		cfg.RoutingKey, // routing key
		cfg.Exchange,   // exchange
		false,          // no-wait
		nil,            // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

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

	// Declare the device-specific queue (idempotent operation)
	_, err := c.channel.QueueDeclare(
		deviceQueue, // name
		true,        // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare device queue %s: %w", deviceQueue, err)
	}

	// Bind the device queue to the exchange with device ID as routing key
	err = c.channel.QueueBind(
		deviceQueue,       // queue name
		result.DeviceID,   // routing key (device ID)
		c.config.Exchange, // exchange
		false,             // no-wait
		nil,               // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to bind device queue %s: %w", deviceQueue, err)
	}

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	err = c.channel.PublishWithContext(
		ctx,
		c.config.Exchange, // exchange
		result.DeviceID,   // routing key (device ID)
		false,             // mandatory
		false,             // immediate
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
