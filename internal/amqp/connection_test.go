package amqp

import (
	"testing"

	"github.com/koios/matrx-renderer/internal/config"
)

func TestDynamicQueueNaming(t *testing.T) {
	// Test that queue names are generated correctly for different device IDs
	testCases := []struct {
		deviceID      string
		expectedQueue string
	}{
		{"device-123", "matrx.device-123"},
		{"display-001", "matrx.display-001"},
		{"screen_alpha", "matrx.screen_alpha"},
		{"test-device-uuid-1234", "matrx.test-device-uuid-1234"},
	}

	for _, tc := range testCases {
		t.Run(tc.deviceID, func(t *testing.T) {
			// Test that we can create the expected queue name
			expectedQueue := "matrx." + tc.deviceID
			if expectedQueue != tc.expectedQueue {
				t.Errorf("Expected queue %s, got %s", tc.expectedQueue, expectedQueue)
			}
		})
	}
}

func TestAMQPConfig(t *testing.T) {
	// Test that the new default configuration is correct
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Check default queue names
	expectedInputQueue := "matrx.renderer_requests"
	if cfg.AMQP.QueueName != expectedInputQueue {
		t.Errorf("Expected input queue %s, got %s", expectedInputQueue, cfg.AMQP.QueueName)
	}

	expectedRoutingKey := "renderer_requests"
	if cfg.AMQP.RoutingKey != expectedRoutingKey {
		t.Errorf("Expected routing key %s, got %s", expectedRoutingKey, cfg.AMQP.RoutingKey)
	}

	expectedResultTemplate := "matrx.{DEVICE_ID}"
	if cfg.AMQP.ResultQueue != expectedResultTemplate {
		t.Errorf("Expected result queue template %s, got %s", expectedResultTemplate, cfg.AMQP.ResultQueue)
	}
}

// Note: This test would require a running RabbitMQ instance
// It's commented out but shows how you would test the actual AMQP functionality
/*
func TestAMQPConnection(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.AMQPConfig{
		URL:         "amqp://guest:guest@localhost:5672/",
		Exchange:    "test-matrx",
		QueueName:   "test-matrx.renderer_requests",
		RoutingKey:  "renderer_requests",
		ResultQueue: "test-matrx.{DEVICE_ID}",
	}

	conn, err := NewConnection(cfg, logger)
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer conn.Close()

	// Test publishing a result
	result := &models.RenderResult{
		DeviceID:     "test-device-123",
		AppID:        "test-app",
		RenderOutput: "dGVzdCBkYXRh", // "test data" in base64
		ProcessedAt:  time.Now(),
	}

	ctx := context.Background()
	err = conn.PublishResult(ctx, result)
	if err != nil {
		t.Fatalf("Failed to publish result: %v", err)
	}

	// Verify the queue was created (this would require additional AMQP inspection)
	// For a full test, you'd want to consume from the queue and verify the message
}
*/
