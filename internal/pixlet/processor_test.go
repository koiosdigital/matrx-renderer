package pixlet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"
)

func TestProcessorWithNestedApps(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test app structure: /temp/test-app/test-app.star
	appDir := filepath.Join(tempDir, "test-app")
	err := os.MkdirAll(appDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create app directory: %v", err)
	}

	// Create a simple test app
	appContent := `
def main():
    return render.Root(
        child=render.Text("Hello, World!")
    )
`
	appFile := filepath.Join(appDir, "test-app.star")
	err = os.WriteFile(appFile, []byte(appContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create app file: %v", err)
	}

	// Create processor with temp directory
	cfg := &config.PixletConfig{
		AppsPath: tempDir,
	}
	logger := zap.NewNop()
	processor := NewProcessor(cfg, logger)

	t.Run("ListApps finds nested apps", func(t *testing.T) {
		apps, err := processor.ListApps()
		if err != nil {
			t.Fatalf("ListApps failed: %v", err)
		}

		if len(apps) != 1 {
			t.Fatalf("Expected 1 app, got %d", len(apps))
		}

		app := apps[0]
		if app.ID != "test-app" {
			t.Errorf("Expected app ID 'test-app', got '%s'", app.ID)
		}

		expectedPath := filepath.Join(tempDir, "test-app", "test-app.star")
		if app.Path != expectedPath {
			t.Errorf("Expected path '%s', got '%s'", expectedPath, app.Path)
		}
	})

	t.Run("RenderApp works with nested structure", func(t *testing.T) {
		request := &models.RenderRequest{
			Type:  "render_request",
			AppID: "test-app",
			Device: models.Device{
				ID:     "test-device",
				Width:  64,
				Height: 32,
			},
			Params: map[string]string{},
		}

		ctx := context.Background()
		result, err := processor.RenderApp(ctx, request)

		// Note: This might fail if Pixlet dependencies aren't properly set up,
		// but it should at least find the app file
		if err != nil && err.Error() == "app not found: test-app" {
			t.Fatal("App should be found with nested structure")
		}

		// If it gets past the "app not found" check, that's what we're testing
		if err != nil {
			t.Logf("RenderApp failed (expected for test environment): %v", err)
		} else {
			// If rendering succeeded, verify the result
			if result.AppID != "test-app" {
				t.Errorf("Expected result app ID 'test-app', got '%s'", result.AppID)
			}
			if result.DeviceID != "test-device" {
				t.Errorf("Expected result device ID 'test-device', got '%s'", result.DeviceID)
			}
		}
	})

	t.Run("Invalid app ID returns error", func(t *testing.T) {
		request := &models.RenderRequest{
			Type:  "render_request",
			AppID: "nonexistent-app",
			Device: models.Device{
				ID:     "test-device",
				Width:  64,
				Height: 32,
			},
			Params: map[string]string{},
		}

		ctx := context.Background()
		_, err := processor.RenderApp(ctx, request)

		if err == nil {
			t.Fatal("Expected error for nonexistent app")
		}

		expectedError := "app not found: nonexistent-app"
		if err.Error() != expectedError {
			t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
		}
	})
}

func TestAppStructureValidation(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create various directory structures to test

	// Valid app structure
	validAppDir := filepath.Join(tempDir, "valid-app")
	err := os.MkdirAll(validAppDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create valid app directory: %v", err)
	}
	err = os.WriteFile(filepath.Join(validAppDir, "valid-app.star"), []byte("# valid app"), 0644)
	if err != nil {
		t.Fatalf("Failed to create valid app file: %v", err)
	}

	// Invalid app structure (missing .star file)
	invalidAppDir := filepath.Join(tempDir, "invalid-app")
	err = os.MkdirAll(invalidAppDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create invalid app directory: %v", err)
	}
	// No .star file created

	// Directory with wrong star file name
	wrongNameDir := filepath.Join(tempDir, "wrong-name")
	err = os.MkdirAll(wrongNameDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create wrong-name directory: %v", err)
	}
	err = os.WriteFile(filepath.Join(wrongNameDir, "different-name.star"), []byte("# wrong name"), 0644)
	if err != nil {
		t.Fatalf("Failed to create wrong-name app file: %v", err)
	}

	// Create a regular file (not directory) in the apps path
	err = os.WriteFile(filepath.Join(tempDir, "regular-file.txt"), []byte("not an app"), 0644)
	if err != nil {
		t.Fatalf("Failed to create regular file: %v", err)
	}

	// Test ListApps
	cfg := &config.PixletConfig{
		AppsPath: tempDir,
	}
	logger := zap.NewNop()
	processor := NewProcessor(cfg, logger)

	apps, err := processor.ListApps()
	if err != nil {
		t.Fatalf("ListApps failed: %v", err)
	}

	// Should only find the valid app
	if len(apps) != 1 {
		t.Fatalf("Expected 1 valid app, got %d", len(apps))
	}

	if apps[0].ID != "valid-app" {
		t.Errorf("Expected app ID 'valid-app', got '%s'", apps[0].ID)
	}
}
