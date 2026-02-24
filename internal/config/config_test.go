package config

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		os.Setenv("TEST_GET_ENV_KEY", "myvalue")
		defer os.Unsetenv("TEST_GET_ENV_KEY")

		if got := getEnv("TEST_GET_ENV_KEY", "default"); got != "myvalue" {
			t.Errorf("got %q, want myvalue", got)
		}
	})

	t.Run("returns default when unset", func(t *testing.T) {
		os.Unsetenv("TEST_GET_ENV_KEY_MISSING")
		if got := getEnv("TEST_GET_ENV_KEY_MISSING", "fallback"); got != "fallback" {
			t.Errorf("got %q, want fallback", got)
		}
	})
}

func TestGetEnvAsInt(t *testing.T) {
	t.Run("valid int", func(t *testing.T) {
		os.Setenv("TEST_INT", "42")
		defer os.Unsetenv("TEST_INT")

		if got := getEnvAsInt("TEST_INT", 10); got != 42 {
			t.Errorf("got %d, want 42", got)
		}
	})

	t.Run("invalid int returns default", func(t *testing.T) {
		os.Setenv("TEST_INT_BAD", "not_a_number")
		defer os.Unsetenv("TEST_INT_BAD")

		if got := getEnvAsInt("TEST_INT_BAD", 99); got != 99 {
			t.Errorf("got %d, want 99", got)
		}
	})

	t.Run("unset returns default", func(t *testing.T) {
		os.Unsetenv("TEST_INT_MISSING")
		if got := getEnvAsInt("TEST_INT_MISSING", 7); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
}

func TestGetRedisAddr(t *testing.T) {
	// Save and clear all redis env vars
	origURL := os.Getenv("REDIS_URL")
	origAddr := os.Getenv("REDIS_ADDR")
	defer func() {
		setOrUnset("REDIS_URL", origURL)
		setOrUnset("REDIS_ADDR", origAddr)
	}()

	t.Run("REDIS_URL with redis:// prefix", func(t *testing.T) {
		os.Setenv("REDIS_URL", "redis://myhost:6380")
		os.Unsetenv("REDIS_ADDR")

		if got := getRedisAddr(); got != "myhost:6380" {
			t.Errorf("got %q, want myhost:6380", got)
		}
	})

	t.Run("REDIS_URL without prefix", func(t *testing.T) {
		os.Setenv("REDIS_URL", "otherhost:1234")
		os.Unsetenv("REDIS_ADDR")

		if got := getRedisAddr(); got != "otherhost:1234" {
			t.Errorf("got %q, want otherhost:1234", got)
		}
	})

	t.Run("REDIS_ADDR fallback", func(t *testing.T) {
		os.Unsetenv("REDIS_URL")
		os.Setenv("REDIS_ADDR", "addr-host:9999")

		if got := getRedisAddr(); got != "addr-host:9999" {
			t.Errorf("got %q, want addr-host:9999", got)
		}
	})

	t.Run("default when nothing set", func(t *testing.T) {
		os.Unsetenv("REDIS_URL")
		os.Unsetenv("REDIS_ADDR")

		if got := getRedisAddr(); got != "localhost:6379" {
			t.Errorf("got %q, want localhost:6379", got)
		}
	})
}

func setOrUnset(key, val string) {
	if val == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, val)
	}
}
