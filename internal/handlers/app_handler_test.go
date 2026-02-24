package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/internal/pixlet"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"
)

// setupTestHandler creates an AppHandler backed by a real processor with a test app
// that defines schema handlers. The app has a single-param handler ("get_options")
// and a two-param handler ("get_options_with_config") that reads from the config.
func setupTestHandler(t *testing.T) *AppHandler {
	t.Helper()

	tempDir := t.TempDir()
	appDir := filepath.Join(tempDir, "test-app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("Failed to create app directory: %v", err)
	}

	appContent := `
load("render.star", "render")
load("schema.star", "schema")

def main(config):
    return render.Root(
        child=render.Text("Hello"),
    )

def get_options(param):
    return [
        schema.Option(display="Option " + param, value=param),
    ]

def get_options_with_config(param, config):
    user = config.get("user_id", "anonymous")
    return [
        schema.Option(display="Option for " + user, value=param),
    ]

def get_schema():
    return schema.Schema(
        version = "1",
        fields = [
            schema.Text(
                id = "user_id",
                name = "User ID",
                desc = "Your user ID",
                icon = "user",
            ),
            schema.Typeahead(
                id = "options",
                name = "Options",
                desc = "Pick an option",
                icon = "list",
                handler = get_options,
            ),
            schema.Typeahead(
                id = "options_cfg",
                name = "Options With Config",
                desc = "Pick an option (config-aware)",
                icon = "list",
                handler = get_options_with_config,
            ),
        ],
    )
`
	if err := os.WriteFile(filepath.Join(appDir, "test-app.star"), []byte(appContent), 0644); err != nil {
		t.Fatalf("Failed to create app file: %v", err)
	}

	manifest := fmt.Sprintf(`id: test-app
name: test-app
summary: Test app
desc: Test app with schema handlers
author: Test Suite
fileName: test-app.star
packageName: apps.test-app
`)
	if err := os.WriteFile(filepath.Join(appDir, "manifest.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	cfg := &config.PixletConfig{
		AppsPath: tempDir,
	}
	logger := zap.NewNop()
	processor := pixlet.NewProcessor(cfg, logger)

	return NewAppHandler(processor, logger)
}

func callHandler(handler *AppHandler, appID string, body interface{}) *httptest.ResponseRecorder {
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/apps/"+appID+"/call_handler", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.handleAppDetails(w, req)
	return w
}

// --- Health endpoint ---

func TestHealth(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Errorf("Expected status=healthy, got %v", resp["status"])
	}
}

func TestHealth_WrongMethod(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	h.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Code)
	}
}

// --- Apps list endpoint ---

func TestApps(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/apps", nil)
	w := httptest.NewRecorder()
	h.handleApps(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var apps []interface{}
	if err := json.NewDecoder(w.Body).Decode(&apps); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if len(apps) != 1 {
		t.Errorf("Expected 1 app, got %d", len(apps))
	}
}

func TestApps_WrongMethod(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/apps", nil)
	w := httptest.NewRecorder()
	h.handleApps(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Code)
	}
}

// --- App details endpoint ---

func TestAppDetails(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/apps/test-app", nil)
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var app map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&app); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if app["id"] != "test-app" {
		t.Errorf("Expected id=test-app, got %v", app["id"])
	}
}

func TestAppDetails_NotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/apps/nonexistent", nil)
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
}

// --- Schema endpoint ---

func TestAppSchema(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/apps/test-app/schema", nil)
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var schema map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&schema); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if schema["version"] != "1" {
		t.Errorf("Expected schema version 1, got %v", schema["version"])
	}
}

// --- Validate schema endpoint ---

func TestValidateSchema_ValidConfig(t *testing.T) {
	h := setupTestHandler(t)

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": "hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/apps/test-app/schema", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ValidateSchemaResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if !resp.Valid {
		t.Errorf("Expected valid=true, got errors: %v", resp.Errors)
	}
}

func TestValidateSchema_UnknownField(t *testing.T) {
	h := setupTestHandler(t)

	body, _ := json.Marshal(map[string]interface{}{
		"totally_unknown": "value",
	})
	req := httptest.NewRequest(http.MethodPost, "/apps/test-app/schema", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ValidateSchemaResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if resp.Valid {
		t.Error("Expected valid=false for unknown field")
	}
	found := false
	for _, e := range resp.Errors {
		if e.Code == "unknown_field" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected unknown_field error, got: %v", resp.Errors)
	}
}

// --- Apps refresh endpoint ---

func TestAppsRefresh(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/apps/refresh", nil)
	w := httptest.NewRecorder()
	h.handleAppsRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	if resp["status"] != "success" {
		t.Errorf("Expected status=success, got %v", resp["status"])
	}
}

func TestAppsRefresh_WrongMethod(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/apps/refresh", nil)
	w := httptest.NewRecorder()
	h.handleAppsRefresh(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Code)
	}
}

// --- parseDimension ---

func TestParseDimension(t *testing.T) {
	tests := []struct {
		raw     string
		def     int
		want    int
		wantErr bool
	}{
		{"", 64, 64, false},
		{"  ", 64, 64, false},
		{"128", 64, 128, false},
		{"0", 64, 0, true},
		{"-1", 64, 0, true},
		{"abc", 64, 0, true},
	}
	for _, tt := range tests {
		got, err := parseDimension(tt.raw, tt.def)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseDimension(%q, %d) error = %v, wantErr %v", tt.raw, tt.def, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseDimension(%q, %d) = %d, want %d", tt.raw, tt.def, got, tt.want)
		}
	}
}

// --- addDisplayDimensions ---

func TestAddDisplayDimensions(t *testing.T) {
	config := map[string]interface{}{"key": "val"}
	device := models.Device{Width: 128, Height: 64}
	result := addDisplayDimensions(config, device)

	if result["display_width"] != 128 {
		t.Errorf("Expected display_width=128, got %v", result["display_width"])
	}
	if result["display_height"] != 64 {
		t.Errorf("Expected display_height=64, got %v", result["display_height"])
	}
	if result["key"] != "val" {
		t.Error("Expected original key preserved")
	}
	// Original should not be mutated
	if _, ok := config["display_width"]; ok {
		t.Error("Original config should not be mutated")
	}
}

// --- Call handler tests ---

func TestCallHandler_MissingHandlerName(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "test-app", map[string]interface{}{
		"data":   "test",
		"config": map[string]string{},
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCallHandler_MissingConfig(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "test-app", map[string]interface{}{
		"handler_name": "options$get_options",
		"data":         "test",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCallHandler_InvalidJSON(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/apps/test-app/call_handler", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleAppDetails(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCallHandler_AppNotFound(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "nonexistent-app", map[string]interface{}{
		"handler_name": "options$get_options",
		"data":         "test",
		"config":       map[string]string{},
	})

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCallHandler_SingleParamHandler(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "test-app", map[string]interface{}{
		"handler_name": "options$get_options",
		"data":         "hello",
		"config":       map[string]string{},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CallHandlerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Result == "" {
		t.Error("Expected non-empty result")
	}
}

func TestCallHandler_TwoParamHandler_WithConfig(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "test-app", map[string]interface{}{
		"handler_name": "options_cfg$get_options_with_config",
		"data":         "val1",
		"config":       map[string]string{"user_id": "test_user_42"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CallHandlerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Result == "" {
		t.Error("Expected non-empty result")
	}
	// The handler returns options with "Option for test_user_42"
	if !bytes.Contains([]byte(resp.Result), []byte("test_user_42")) {
		t.Errorf("Expected result to contain config value 'test_user_42', got: %s", resp.Result)
	}
}

func TestCallHandler_TwoParamHandler_EmptyConfig(t *testing.T) {
	h := setupTestHandler(t)

	w := callHandler(h, "test-app", map[string]interface{}{
		"handler_name": "options_cfg$get_options_with_config",
		"data":         "val1",
		"config":       map[string]string{},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CallHandlerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	// With empty config, handler falls back to "anonymous"
	if !bytes.Contains([]byte(resp.Result), []byte("anonymous")) {
		t.Errorf("Expected result to contain fallback 'anonymous', got: %s", resp.Result)
	}
}
