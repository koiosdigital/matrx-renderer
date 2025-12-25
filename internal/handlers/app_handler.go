package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/koios/matrx-renderer/internal/pixlet"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"
)

const (
	defaultDeviceWidth  = 64
	defaultDeviceHeight = 32
)

// AppHandler handles HTTP requests for app management
type AppHandler struct {
	processor *pixlet.Processor
	validator *Validator
	logger    *zap.Logger
}

// NewAppHandler creates a new app handler
func NewAppHandler(processor *pixlet.Processor, logger *zap.Logger) *AppHandler {
	return &AppHandler{
		processor: processor,
		validator: NewValidator(processor, logger),
		logger:    logger,
	}
}

// RegisterRoutes registers the app management routes
func (h *AppHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/apps", h.handleApps)
	mux.HandleFunc("/apps/refresh", h.handleAppsRefresh)
	mux.HandleFunc("/apps/", h.handleAppDetails)
	mux.HandleFunc("/swagger.json", h.handleSwagger)
}

// handleHealth handles GET /health - returns service health status
func (h *AppHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Simple health check - just return OK if we can respond
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"service": "matrx-renderer",
		"version": "1.0.0",
	})
}

// handleApps handles GET /apps - returns list of all apps
func (h *AppHandler) handleApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	registry := h.processor.GetAppRegistry()
	apps := registry.GetAppsList()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(apps); err != nil {
		h.logger.Error("Failed to encode apps response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Debug("Served apps list", zap.Int("count", len(apps)))
}

// handleAppsRefresh handles POST /apps/refresh - reloads the app registry
func (h *AppHandler) handleAppsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.logger.Info("Refreshing app registry...")

	// Reload the app registry from the filesystem
	if err := h.processor.RefreshAppRegistry(); err != nil {
		h.logger.Error("Failed to refresh app registry", zap.Error(err))
		http.Error(w, "Failed to refresh apps", http.StatusInternalServerError)
		return
	}

	registry := h.processor.GetAppRegistry()
	apps := registry.GetAppsList()

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"status":    "success",
		"message":   "App registry refreshed successfully",
		"app_count": len(apps),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode refresh response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("App registry refreshed successfully", zap.Int("app_count", len(apps)))
}

// handleAppDetails handles:
// - GET /apps/{id} - returns specific app or 404
// - GET /apps/{id}/schema - returns the app's schema
// - POST /apps/{id}/call_handler - calls a schema handler
func (h *AppHandler) handleAppDetails(w http.ResponseWriter, r *http.Request) {
	// Parse the path: /apps/{id} or /apps/{id}/schema or /apps/{id}/call_handler
	path := strings.TrimPrefix(r.URL.Path, "/apps/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) == 0 || pathParts[0] == "" {
		http.Error(w, "App ID required", http.StatusBadRequest)
		return
	}

	appID := pathParts[0]
	registry := h.processor.GetAppRegistry()
	app, exists := registry.GetApp(appID)

	if !exists {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	if len(pathParts) > 1 {
		switch pathParts[1] {
		case "schema":
			if r.Method == http.MethodGet {
				h.handleAppSchema(w, r, appID, app)
				return
			} else if r.Method == http.MethodPost {
				h.handleValidateSchema(w, r, appID)
				return
			}
		case "call_handler":
			if r.Method == http.MethodPost {
				h.handleCallSchemaHandler(w, r, appID)
				return
			}
		case "render":
			if r.Method == http.MethodPost {
				h.handleAppRender(w, r, appID)
				return
			}
		default:
			if strings.HasPrefix(pathParts[1], "preview.") {
				if r.Method != http.MethodGet {
					http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}
				format := strings.TrimPrefix(pathParts[1], "preview.")
				h.handleAppPreview(w, r, appID, format)
				return
			}
		}
	}

	// Handle GET /apps/{id} - return app details
	if r.Method == http.MethodGet && len(pathParts) == 1 {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(app); err != nil {
			h.logger.Error("Failed to encode app response", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		h.logger.Debug("Served app details", zap.String("app_id", appID))
		return
	}

	// If none of the above matched, return method not allowed or not found
	if len(pathParts) > 1 {
		http.Error(w, "Endpoint not found", http.StatusNotFound)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleAppSchema handles GET /apps/{id}/schema - returns the app's schema as JSON
func (h *AppHandler) handleAppSchema(w http.ResponseWriter, r *http.Request, appID string, app interface{}) {
	// Get the schema for the app using the processor
	schema, err := h.processor.GetAppSchema(r.Context(), appID)
	if err != nil {
		h.logger.Error("Failed to get app schema",
			zap.String("app_id", appID),
			zap.Error(err))

		// Handle specific errors
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "does not define a schema") {
			http.Error(w, "App does not define a schema", http.StatusNotFound)
			return
		}

		http.Error(w, "Failed to get app schema", http.StatusInternalServerError)
		return
	}

	// Return the schema as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(schema); err != nil {
		h.logger.Error("Failed to encode schema response",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Debug("Served app schema", zap.String("app_id", appID))
}

// CallHandlerRequest represents the request body for calling a schema handler
type CallHandlerRequest struct {
	HandlerName string `json:"handler_name"`
	Data        string `json:"data"`
}

// CallHandlerResponse represents the response from calling a schema handler
type CallHandlerResponse struct {
	Result string `json:"result"`
}

// handleCallSchemaHandler handles POST /apps/{id}/call_handler - calls a schema handler
func (h *AppHandler) handleCallSchemaHandler(w http.ResponseWriter, r *http.Request, appID string) {
	// Parse the request body
	var request CallHandlerRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		h.logger.Error("Failed to decode call handler request",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if request.HandlerName == "" {
		http.Error(w, "handler_name is required", http.StatusBadRequest)
		return
	}

	// Call the schema handler using the processor
	result, err := h.processor.CallSchemaHandler(r.Context(), appID, request.HandlerName, request.Data)
	if err != nil {
		h.logger.Error("Failed to call schema handler",
			zap.String("app_id", appID),
			zap.String("handler_name", request.HandlerName),
			zap.Error(err))

		// Handle specific errors
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "does not define a schema") {
			http.Error(w, "App does not define a schema", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "handler") {
			http.Error(w, "Schema handler error: "+err.Error(), http.StatusBadRequest)
			return
		}

		http.Error(w, "Failed to call schema handler", http.StatusInternalServerError)
		return
	}

	// Return the result as JSON
	response := CallHandlerResponse{
		Result: result,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode call handler response",
			zap.String("app_id", appID),
			zap.String("handler_name", request.HandlerName),
			zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Debug("Called schema handler successfully",
		zap.String("app_id", appID),
		zap.String("handler_name", request.HandlerName))
}

// ValidateSchemaResponse represents the response from schema validation
type ValidateSchemaResponse struct {
	Valid            bool                   `json:"valid"`
	Errors           []ValidationError      `json:"errors,omitempty"`
	NormalizedConfig map[string]interface{} `json:"normalized_config,omitempty"`
}

// RenderResponse represents the response from the HTTP render endpoint
type RenderResponse struct {
	Result           *models.RenderResult   `json:"result"`
	NormalizedConfig map[string]interface{} `json:"normalized_config"`
}

// handleValidateSchema handles POST /apps/{id}/schema - validates config against schema
func (h *AppHandler) handleValidateSchema(w http.ResponseWriter, r *http.Request, appID string) {
	config, err := decodeConfigBody(r)
	if err != nil {
		h.logger.Error("Failed to decode validate schema request",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	appSchema, err := h.processor.GetAppSchema(r.Context(), appID)
	if err != nil {
		h.logger.Error("Failed to get app schema for validation",
			zap.String("app_id", appID),
			zap.Error(err))
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "does not define a schema") {
			http.Error(w, "App does not define a schema", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get app schema", http.StatusInternalServerError)
		return
	}

	normalizedConfig, validationErrors, err := h.validator.ValidateConfig(r.Context(), appID, config, appSchema)
	if err != nil {
		h.logger.Error("Failed to validate schema",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Failed to validate config", http.StatusInternalServerError)
		return
	}

	response := ValidateSchemaResponse{
		Valid:            len(validationErrors) == 0,
		Errors:           validationErrors,
		NormalizedConfig: normalizedConfig,
	}

	h.writeJSON(w, http.StatusOK, response)

	h.logger.Debug("Validated schema",
		zap.String("app_id", appID),
		zap.Bool("valid", response.Valid),
		zap.Int("error_count", len(validationErrors)))
}

// handleAppRender handles POST /apps/{id}/render - renders an app with the provided configuration
func (h *AppHandler) handleAppRender(w http.ResponseWriter, r *http.Request, appID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	config, err := decodeConfigBody(r)
	if err != nil {
		h.logger.Error("Failed to decode render request body",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	appSchema, err := h.processor.GetAppSchema(r.Context(), appID)
	if err != nil {
		h.logger.Error("Failed to get app schema for render",
			zap.String("app_id", appID),
			zap.Error(err))
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "does not define a schema") {
			http.Error(w, "App does not define a schema", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get app schema", http.StatusInternalServerError)
		return
	}

	normalizedConfig, validationErrors, err := h.validator.ValidateConfig(r.Context(), appID, config, appSchema)
	if err != nil {
		h.logger.Error("Failed to validate render config",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Failed to validate config", http.StatusInternalServerError)
		return
	}
	if len(validationErrors) > 0 {
		h.respondValidationFailure(w, normalizedConfig, validationErrors)
		return
	}

	device, err := h.parseDevice(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if device.ID == "" {
		device.ID = "http-render"
	}

	renderParams := addDisplayDimensions(normalizedConfig, device)

	request := &models.RenderRequest{
		Type:   "render_request",
		UUID:   fmt.Sprintf("http-%d", time.Now().UnixNano()),
		AppID:  appID,
		Device: device,
		Params: renderParams,
	}

	result, err := h.processor.RenderApp(r.Context(), request)
	if err != nil {
		h.logger.Error("Failed to render app",
			zap.String("app_id", appID),
			zap.String("device_id", device.ID),
			zap.Error(err))
		http.Error(w, "Failed to render app", http.StatusInternalServerError)
		return
	}

	response := RenderResponse{
		Result:           result,
		NormalizedConfig: normalizedConfig,
	}

	h.writeJSON(w, http.StatusOK, response)

	h.logger.Info("Rendered app via HTTP",
		zap.String("app_id", appID),
		zap.String("device_id", device.ID))
}

// handleAppPreview handles GET /apps/{id}/preview.{webp|gif} - renders and streams binary data using defaults
func (h *AppHandler) handleAppPreview(w http.ResponseWriter, r *http.Request, appID, format string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	format = strings.ToLower(strings.TrimSpace(format))
	if format != "webp" && format != "gif" {
		http.Error(w, "Unsupported preview format", http.StatusNotFound)
		return
	}

	appSchema, err := h.processor.GetAppSchema(r.Context(), appID)
	if err != nil {
		h.logger.Error("Failed to get app schema for preview",
			zap.String("app_id", appID),
			zap.Error(err))
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "does not define a schema") {
			http.Error(w, "App does not define a schema", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get app schema", http.StatusInternalServerError)
		return
	}

	normalizedConfig, _, err := h.validator.ValidateConfig(r.Context(), appID, nil, appSchema)
	if err != nil {
		h.logger.Error("Failed to validate preview config",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Failed to validate config", http.StatusInternalServerError)
		return
	}

	device, err := h.parseDevice(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if device.ID == "" {
		device.ID = fmt.Sprintf("preview-%s", format)
	}

	previewParams := addDisplayDimensions(normalizedConfig, device)

	previewBytes, err := h.processor.RenderPreview(r.Context(), appID, previewParams, device, format)
	if err != nil {
		h.logger.Error("Failed to render preview",
			zap.String("app_id", appID),
			zap.String("format", format),
			zap.Error(err))
		http.Error(w, "Failed to render preview", http.StatusInternalServerError)
		return
	}

	if format == "webp" {
		w.Header().Set("Content-Type", "image/webp")
	} else {
		w.Header().Set("Content-Type", "image/gif")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(previewBytes); err != nil {
		h.logger.Error("Failed to write preview response",
			zap.String("app_id", appID),
			zap.Error(err))
	}

	h.logger.Info("Rendered preview via HTTP",
		zap.String("app_id", appID),
		zap.String("format", format),
		zap.String("device_id", device.ID))
}

func (h *AppHandler) respondValidationFailure(w http.ResponseWriter, normalizedConfig map[string]interface{}, validationErrors []ValidationError) {
	response := ValidateSchemaResponse{
		Valid:            false,
		Errors:           validationErrors,
		NormalizedConfig: normalizedConfig,
	}
	h.writeJSON(w, http.StatusUnprocessableEntity, response)
}

func addDisplayDimensions(config map[string]interface{}, device models.Device) map[string]interface{} {
	result := make(map[string]interface{}, len(config)+2)
	for key, value := range config {
		result[key] = value
	}
	result["display_width"] = device.Width
	result["display_height"] = device.Height
	return result
}

func (h *AppHandler) parseDevice(r *http.Request) (models.Device, error) {
	query := r.URL.Query()
	width, err := parseDimension(query.Get("width"), defaultDeviceWidth)
	if err != nil {
		return models.Device{}, fmt.Errorf("invalid width: %w", err)
	}
	height, err := parseDimension(query.Get("height"), defaultDeviceHeight)
	if err != nil {
		return models.Device{}, fmt.Errorf("invalid height: %w", err)
	}

	return models.Device{
		ID:     query.Get("device_id"),
		Width:  width,
		Height: height,
	}, nil
}

func parseDimension(raw string, defaultVal int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultVal, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return value, nil
}

func decodeConfigBody(r *http.Request) (map[string]interface{}, error) {
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var config map[string]interface{}
	if err := decoder.Decode(&config); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("request body required")
		}
		return nil, err
	}

	if err := ensureSingleJSONObject(decoder); err != nil {
		return nil, err
	}

	if config == nil {
		config = make(map[string]interface{})
	}

	return config, nil
}

func ensureSingleJSONObject(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain a single JSON object")
		}
		return err
	}
	return nil
}

func (h *AppHandler) writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("Failed to encode JSON response",
			zap.Int("status", status),
			zap.Error(err))
	}
}

// handleSwagger handles GET /swagger.json - returns OpenAPI specification
func (h *AppHandler) handleSwagger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the swagger.json file from the api directory
	swaggerPath := "api/swagger.json"
	swaggerData, err := os.ReadFile(swaggerPath)
	if err != nil {
		h.logger.Error("Failed to read swagger.json", zap.Error(err))
		http.Error(w, "Swagger specification not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(swaggerData)
}
