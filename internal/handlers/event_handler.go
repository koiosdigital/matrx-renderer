package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/internal/pixlet"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"
)

type EventHandler struct {
	pixletProcessor *pixlet.Processor
	logger          *zap.Logger
	config          *config.Config
}

// NewEventHandler creates a new event handler
func NewEventHandler(logger *zap.Logger, cfg *config.Config) *EventHandler {
	var pixletProcessor *pixlet.Processor

	// Check if Redis is configured
	if cfg.Redis.Addr != "" {
		logger.Info("Initializing Pixlet processor with Redis cache",
			zap.String("redis_addr", cfg.Redis.Addr),
			zap.Int("redis_db", cfg.Redis.DB))
		pixletProcessor = pixlet.NewProcessorWithRedis(&cfg.Pixlet, &cfg.Redis, logger)
	} else {
		logger.Info("Initializing Pixlet processor with in-memory cache")
		pixletProcessor = pixlet.NewProcessor(&cfg.Pixlet, logger)
	}

	return &EventHandler{
		pixletProcessor: pixletProcessor,
		logger:          logger,
		config:          cfg,
	}
}

// Handle processes a render request event
func (h *EventHandler) Handle(ctx context.Context, request *models.RenderRequest) (*models.RenderResult, error) {
	h.logger.Info("Processing render request",
		zap.String("app_id", request.AppID),
		zap.String("device_id", request.Device.ID),
		zap.String("type", request.Type))

	// Validate request
	if request.Type != "render_request" {
		h.logger.Error("Invalid request type", zap.String("type", request.Type))
		return nil, fmt.Errorf("invalid request type: %s", request.Type)
	}

	if request.AppID == "" {
		h.logger.Error("Missing app_id")
		return nil, fmt.Errorf("app_id is required")
	}

	if request.Device.ID == "" {
		h.logger.Error("Missing device ID")
		return nil, fmt.Errorf("device.id is required")
	}

	result, err := h.pixletProcessor.RenderApp(ctx, request)
	if err != nil {
		h.logger.Error("Render request failed",
			zap.Error(err),
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		// Create result with empty output on error
		result2 := &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "", // Empty output on error
			ProcessedAt:  time.Now(),
		}
		return result2, err
	}

	h.logger.Info("Render request completed successfully",
		zap.String("app_id", request.AppID),
		zap.String("device_id", request.Device.ID))

	return result, nil
}

// GetProcessor returns the pixlet processor for HTTP handlers
func (h *EventHandler) GetProcessor() *pixlet.Processor {
	return h.pixletProcessor
}
