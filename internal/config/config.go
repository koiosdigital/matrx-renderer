package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application
type Config struct {
	AMQP     AMQPConfig
	Server   ServerConfig
	Pixlet   PixletConfig
	Redis    RedisConfig
	LogLevel string
}

// AMQPConfig holds AMQP-related configuration
type AMQPConfig struct {
	URL           string
	Exchange      string
	QueueName     string
	RoutingKey    string
	ResultQueue   string
	PrefetchCount int // QoS prefetch count for load balancing
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port         int
	ReadTimeout  int
	WriteTimeout int
}

// PixletConfig holds Pixlet-related configuration
type PixletConfig struct {
	AppsPath               string
	SecretEncryptionKeyB64 string // Base64 encoded secret keyset for Pixlet
	KeyEncryptionKeyB64    string // Base64 encoded key encryption key for Pixlet
}

// RedisConfig holds Redis-related configuration
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (optional)
	_ = godotenv.Load()

	cfg := &Config{
		AMQP: AMQPConfig{
			URL:           getEnv("AMQP_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange:      getEnv("AMQP_EXCHANGE", "matrx"),
			QueueName:     getEnv("AMQP_QUEUE", "matrx.render_requests"),
			RoutingKey:    getEnv("AMQP_ROUTING_KEY", "renderer_requests"),
			ResultQueue:   getEnv("AMQP_RESULT_QUEUE", "matrx.{DEVICE_ID}"), // Template - will be replaced with actual device ID
			PrefetchCount: getEnvAsInt("AMQP_PREFETCH_COUNT", 1),            // Default to 1 for fair distribution
		},
		Server: ServerConfig{
			Port:         getEnvAsInt("SERVER_PORT", 8080),
			ReadTimeout:  getEnvAsInt("SERVER_READ_TIMEOUT", 10),
			WriteTimeout: getEnvAsInt("SERVER_WRITE_TIMEOUT", 10),
		},
		Pixlet: PixletConfig{
			AppsPath:               getEnv("PIXLET_APPS_PATH", "/opt/apps"),
			SecretEncryptionKeyB64: getEnv("PIXLET_SECRET_KEYSET_B64", ""),
			KeyEncryptionKeyB64:    getEnv("PIXLET_KEY_ENCRYPTION_KEY_B64", ""),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvAsInt("REDIS_DB", 0),
		},
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt gets an environment variable as int or returns a default value
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
