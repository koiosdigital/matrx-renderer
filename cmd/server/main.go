package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/internal/handlers"
	"github.com/koios/matrx-renderer/internal/redis"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	// Create context for graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Redis client
	redisClient, err := redis.NewClient(cfg.Redis, logger)
	if err != nil {
		logger.Fatal("Failed to create Redis client", zap.Error(err))
	}
	defer redisClient.Close()

	// Initialize event handler
	eventHandler := handlers.NewEventHandler(logger, cfg)

	// Initialize Redis consumer
	consumer := redis.NewConsumer(redisClient, eventHandler, logger)

	// Create HTTP server for app management API
	mux := http.NewServeMux()
	appHandler := handlers.NewAppHandler(eventHandler.GetProcessor(), logger)
	appHandler.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
	}

	// Start HTTP server
	go func() {
		logger.Info("Starting HTTP server", zap.Int("port", cfg.Server.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", zap.Error(err))
			cancel()
		}
	}()

	// Start consuming messages from Redis
	go func() {
		if err := consumer.Start(); err != nil {
			logger.Error("Redis consumer failed", zap.Error(err))
			cancel()
		}
	}()

	logger.Info("Server started",
		zap.String("redis_addr", cfg.Redis.Addr),
		zap.String("input_channel", "matrx:render_requests"),
		zap.String("output_channel_pattern", "device:{device_id}"))

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Give outstanding requests a deadline for completion
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown failed", zap.Error(err))
	}

	// Stop Redis consumer
	consumer.Stop()

	// Cancel the main context to stop all operations
	cancel()

	// Wait for shutdown to complete or timeout
	select {
	case <-shutdownCtx.Done():
		logger.Warn("Shutdown timeout exceeded")
	case <-time.After(2 * time.Second):
		logger.Info("Server shutdown complete")
	}
}
