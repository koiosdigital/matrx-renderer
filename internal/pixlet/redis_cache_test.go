package pixlet

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/redis/go-redis/v9"
	"go.starlark.net/starlark"
)

func TestRedisCache(t *testing.T) {
	// This test requires a running Redis instance
	// Skip if Redis is not available
	cfg := &config.RedisConfig{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1, // Use a test database
	}

	sharedCache := NewRedisCache(cfg)
	defer sharedCache.Close()

	// Test ping
	ctx := context.Background()
	if err := sharedCache.Ping(ctx); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Create contextual cache for testing
	cache := sharedCache.WithContext("test-app", "test-device")

	// Clean up any existing test data
	cache.FlushApp(ctx)

	// Create a mock thread
	thread := &starlark.Thread{Name: "test"}

	t.Run("Set and Get", func(t *testing.T) {
		key := "test-key"
		value := []byte("test-value")
		ttl := int64(60)

		// Set value
		err := cache.Set(thread, key, value, ttl)
		if err != nil {
			t.Fatalf("Failed to set value: %v", err)
		}

		// Get value
		retrieved, found, err := cache.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value: %v", err)
		}

		if !found {
			t.Fatal("Value not found")
		}

		if string(retrieved) != string(value) {
			t.Fatalf("Expected %s, got %s", value, retrieved)
		}
	})

	t.Run("Key Scoping", func(t *testing.T) {
		// Test that different apps/devices have isolated key spaces
		cache1 := sharedCache.WithContext("app1", "device1")
		cache2 := sharedCache.WithContext("app2", "device2")

		key := "same-key"
		value1 := []byte("value1")
		value2 := []byte("value2")

		// Set same key in both caches
		err := cache1.Set(thread, key, value1, 60)
		if err != nil {
			t.Fatalf("Failed to set value in cache1: %v", err)
		}

		err = cache2.Set(thread, key, value2, 60)
		if err != nil {
			t.Fatalf("Failed to set value in cache2: %v", err)
		}

		// Get from both caches - should be different
		retrieved1, found1, err := cache1.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value from cache1: %v", err)
		}
		if !found1 {
			t.Fatal("Value not found in cache1")
		}

		retrieved2, found2, err := cache2.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value from cache2: %v", err)
		}
		if !found2 {
			t.Fatal("Value not found in cache2")
		}

		if string(retrieved1) != string(value1) {
			t.Fatalf("Cache1 expected %s, got %s", value1, retrieved1)
		}
		if string(retrieved2) != string(value2) {
			t.Fatalf("Cache2 expected %s, got %s", value2, retrieved2)
		}

		// Clean up
		cache1.FlushApp(ctx)
		cache2.FlushApp(ctx)
	})

	t.Run("Key Cleaning", func(t *testing.T) {
		// Test that keys with special characters work
		key := "key/with/slashes"
		value := []byte("test-value")

		err := cache.Set(thread, key, value, 60)
		if err != nil {
			t.Fatalf("Failed to set value with special characters: %v", err)
		}

		retrieved, found, err := cache.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value with special characters: %v", err)
		}
		if !found {
			t.Fatal("Value with special characters not found")
		}

		if string(retrieved) != string(value) {
			t.Fatalf("Expected %s, got %s", value, retrieved)
		}
	})

	t.Run("TTL Expiration", func(t *testing.T) {
		key := "ttl-key"
		value := []byte("ttl-value")
		ttl := int64(1) // 1 second

		// Set value with short TTL
		err := cache.Set(thread, key, value, ttl)
		if err != nil {
			t.Fatalf("Failed to set value: %v", err)
		}

		// Value should exist immediately
		_, found, err := cache.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value: %v", err)
		}
		if !found {
			t.Fatal("Value should be found immediately")
		}

		// Wait for expiration
		time.Sleep(2 * time.Second)

		// Value should be expired
		_, found, err = cache.Get(thread, key)
		if err != nil {
			t.Fatalf("Failed to get value: %v", err)
		}
		if found {
			t.Fatal("Value should be expired")
		}
	})

	t.Run("FlushApp", func(t *testing.T) {
		// Set multiple keys
		keys := []string{"flush-key-1", "flush-key-2", "flush-key-3"}
		value := []byte("flush-value")

		for _, key := range keys {
			err := cache.Set(thread, key, value, 60)
			if err != nil {
				t.Fatalf("Failed to set value for key %s: %v", key, err)
			}
		}

		// Verify all keys exist
		for _, key := range keys {
			_, found, err := cache.Get(thread, key)
			if err != nil {
				t.Fatalf("Failed to get value for key %s: %v", key, err)
			}
			if !found {
				t.Fatalf("Key %s should exist", key)
			}
		}

		// Flush all keys for this app/device
		err := cache.FlushApp(ctx)
		if err != nil {
			t.Fatalf("Failed to flush app cache: %v", err)
		}

		// Verify all keys are gone
		for _, key := range keys {
			_, found, err := cache.Get(thread, key)
			if err != nil {
				t.Fatalf("Failed to get value for key %s: %v", key, err)
			}
			if found {
				t.Fatalf("Key %s should be flushed", key)
			}
		}
	})

	t.Run("Stats", func(t *testing.T) {
		// Clean slate
		cache.FlushApp(ctx)

		// Check initial count
		count, err := cache.Stats(ctx)
		if err != nil {
			t.Fatalf("Failed to get stats: %v", err)
		}
		if count != 0 {
			t.Fatalf("Expected 0 keys, got %d", count)
		}

		// Add some keys
		numKeys := 5
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("stats-key-%d", i)
			err := cache.Set(thread, key, []byte("value"), 60)
			if err != nil {
				t.Fatalf("Failed to set key %s: %v", key, err)
			}
		}

		// Check count
		count, err = cache.Stats(ctx)
		if err != nil {
			t.Fatalf("Failed to get stats: %v", err)
		}
		if count != int64(numKeys) {
			t.Fatalf("Expected %d keys, got %d", numKeys, count)
		}
	})
}

func TestRedisCacheFromClient(t *testing.T) {
	// Create a mock Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})
	defer client.Close()

	// Test ping first
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	sharedCache := NewRedisCacheFromClient(client)
	cache := sharedCache.WithContext("client-app", "client-device")

	// Clean up any existing test data
	cache.FlushApp(ctx)

	// Create a mock thread
	thread := &starlark.Thread{Name: "test"}
	key := "test-key"
	value := []byte("test-value")

	err := cache.Set(thread, key, value, 60)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	retrieved, found, err := cache.Get(thread, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !found {
		t.Fatal("Key not found")
	}
	if string(retrieved) != string(value) {
		t.Fatalf("Expected %s, got %s", string(value), string(retrieved))
	}

	// Clean up
	cache.FlushApp(ctx)
}
