package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application
type Config struct {
	Server   ServerConfig
	Pixlet   PixletConfig
	Redis    RedisConfig
	LogLevel string
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
	Addr          string
	Password      string
	DB            int
	ConsumerGroup string // Consumer group name for streams
	ConsumerName  string // Consumer name (unique per instance)
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (optional)
	_ = godotenv.Load()

	cfg := &Config{
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
			Addr:          getRedisAddr(),
			Password:      getEnv("REDIS_PASSWORD", ""),
			DB:            getEnvAsInt("REDIS_DB", 0),
			ConsumerGroup: getEnv("REDIS_CONSUMER_GROUP", "matrx-renderer-group"),
			ConsumerName:  getEnv("REDIS_CONSUMER_NAME", ""),
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

// getRedisAddr gets Redis address, supporting both REDIS_URL and REDIS_ADDR formats
func getRedisAddr() string {
	// Check for REDIS_URL first (format: redis://host:port)
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		// Parse redis://host:port to host:port
		if redisURL[:8] == "redis://" {
			return redisURL[8:]
		}
		return redisURL
	}

	// Fall back to REDIS_ADDR
	return getEnv("REDIS_ADDR", "localhost:6379")
}
