package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/koios/matrx-renderer/internal/amqp"
	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/internal/handlers"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize AMQP connection
	amqpConn, err := amqp.NewConnection(cfg.AMQP, logger)
	if err != nil {
		logger.Fatal("Failed to create AMQP connection", zap.Error(err))
	}
	defer amqpConn.Close()

	// Initialize event handler
	eventHandler := handlers.NewEventHandler(logger, cfg)

	// Initialize consumer
	consumer := amqp.NewConsumer(amqpConn, eventHandler, logger)

	// Start consuming messages
	go func() {
		if err := consumer.Start(ctx, cfg.AMQP.QueueName); err != nil {
			logger.Error("Consumer failed", zap.Error(err))
			cancel()
		}
	}()

	logger.Info("Server started",
		zap.String("queue", cfg.AMQP.QueueName),
		zap.String("exchange", cfg.AMQP.Exchange))

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Give outstanding requests a deadline for completion
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

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
