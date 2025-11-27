package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/koios/matrx-renderer/internal/pixlet"
	"go.uber.org/zap"
	"tidbyt.dev/pixlet/schema"
)

// AppHandler handles HTTP requests for app management
type AppHandler struct {
	processor *pixlet.Processor
	logger    *zap.Logger
}

// NewAppHandler creates a new app handler
func NewAppHandler(processor *pixlet.Processor, logger *zap.Logger) *AppHandler {
	return &AppHandler{
		processor: processor,
		logger:    logger,
	}
}

// RegisterRoutes registers the app management routes
func (h *AppHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/apps", h.handleApps)
	mux.HandleFunc("/apps/refresh", h.handleAppsRefresh)
	mux.HandleFunc("/apps/", h.handleAppDetails)
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

	// Check if this is a schema request (GET or POST)
	if len(pathParts) > 1 && pathParts[1] == "schema" {
		if r.Method == http.MethodGet {
			h.handleAppSchema(w, r, appID, app)
			return
		} else if r.Method == http.MethodPost {
			h.handleValidateSchema(w, r, appID)
			return
		}
	}

	// Check if this is a call_handler request (POST)
	if len(pathParts) > 1 && pathParts[1] == "call_handler" && r.Method == http.MethodPost {
		h.handleCallSchemaHandler(w, r, appID)
		return
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

// ValidateSchemaRequest represents the request body for schema validation
type ValidateSchemaRequest struct {
	Config map[string]interface{} `json:"config"`
}

// ValidationError represents a validation error for a specific field
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// ValidateSchemaResponse represents the response from schema validation
type ValidateSchemaResponse struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// handleValidateSchema handles POST /apps/{id}/schema - validates config against schema
func (h *AppHandler) handleValidateSchema(w http.ResponseWriter, r *http.Request, appID string) {
	// Parse the request body
	var request ValidateSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		h.logger.Error("Failed to decode validate schema request",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Get the app schema
	schema, err := h.processor.GetAppSchema(r.Context(), appID)
	if err != nil {
		h.logger.Error("Failed to get app schema for validation",
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

	// Validate the config against the schema
	errors := h.validateConfigAgainstSchema(request.Config, schema)

	// Prepare response
	response := ValidateSchemaResponse{
		Valid:  len(errors) == 0,
		Errors: errors,
	}

	// Return the validation result as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode validate schema response",
			zap.String("app_id", appID),
			zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Debug("Validated schema",
		zap.String("app_id", appID),
		zap.Bool("valid", response.Valid),
		zap.Int("error_count", len(errors)))
}

// validateConfigAgainstSchema validates a configuration map against a schema
func (h *AppHandler) validateConfigAgainstSchema(config map[string]interface{}, appSchema *schema.Schema) []ValidationError {
	var errors []ValidationError

	// Create a map of schema fields by ID for quick lookup
	schemaFields := make(map[string]schema.SchemaField)
	for _, field := range appSchema.Fields {
		schemaFields[field.ID] = field
	}

	// Check for required fields and validate each provided field
	for _, field := range appSchema.Fields {
		value, exists := config[field.ID]

		// Check if required field is missing
		if !exists {
			// Check if this field has a default value
			if field.Default == "" {
				// Only some field types are required to have defaults, others might be optional
				switch field.Type {
				case "dropdown", "onoff", "radio":
					// These types require defaults according to validation tags, so if no value provided and no default, it's an error
					if field.Default == "" {
						errors = append(errors, ValidationError{
							Field:   field.ID,
							Message: fmt.Sprintf("Field '%s' is required", field.Name),
							Code:    "required",
						})
					}
				case "text", "color", "datetime", "location":
					// These might be optional depending on the app, skip for now
					continue
				}
			}
			continue
		}

		// Validate the field value based on its type
		fieldErrors := h.validateFieldValue(field, value)
		errors = append(errors, fieldErrors...)
	}

	// Check for unexpected fields
	for key := range config {
		if _, exists := schemaFields[key]; !exists {
			errors = append(errors, ValidationError{
				Field:   key,
				Message: fmt.Sprintf("Unknown field '%s'", key),
				Code:    "unknown_field",
			})
		}
	}

	return errors
}

// validateFieldValue validates a single field value based on its schema definition
func (h *AppHandler) validateFieldValue(field schema.SchemaField, value interface{}) []ValidationError {
	var errors []ValidationError

	// Convert value to string for validation (most schema validations work with strings)
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	case float64:
		strValue = strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		strValue = strconv.Itoa(v)
	case bool:
		strValue = strconv.FormatBool(v)
	case nil:
		strValue = ""
	default:
		// For complex objects, convert to JSON string
		if jsonBytes, err := json.Marshal(v); err == nil {
			strValue = string(jsonBytes)
		} else {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Invalid value type for field '%s'", field.Name),
				Code:    "invalid_type",
			})
			return errors
		}
	}

	// Validate based on field type
	switch field.Type {
	case "text":
		// Text fields accept any string value
		if reflect.TypeOf(value).Kind() != reflect.String {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a string", field.Name),
				Code:    "invalid_type",
			})
		}

	case "color":
		// Color fields should be hex color codes
		if !h.isValidColor(strValue) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid color (e.g., #FF0000)", field.Name),
				Code:    "invalid_color",
			})
		}

	case "onoff", "toggle":
		// Boolean fields
		if reflect.TypeOf(value).Kind() != reflect.Bool {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a boolean", field.Name),
				Code:    "invalid_type",
			})
		}

	case "dropdown", "radio":
		// Validate against available options
		if !h.isValidOption(strValue, field.Options) {
			validOptions := make([]string, len(field.Options))
			for i, opt := range field.Options {
				validOptions[i] = opt.Value
			}
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be one of: %s", field.Name, strings.Join(validOptions, ", ")),
				Code:    "invalid_option",
			})
		}

	case "datetime":
		// Basic datetime validation (you might want to use a proper time parser)
		if strValue != "" && !h.isValidDateTime(strValue) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid datetime", field.Name),
				Code:    "invalid_datetime",
			})
		}

	case "location":
		// Location should be an object with latitude and longitude
		if !h.isValidLocation(value) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid location object with lat and lng", field.Name),
				Code:    "invalid_location",
			})
		}
	}

	return errors
}

// Helper validation functions
func (h *AppHandler) isValidColor(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := color[i]
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (h *AppHandler) isValidOption(value string, options []schema.SchemaOption) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func (h *AppHandler) isValidDateTime(value string) bool {
	// Basic validation - you might want to use time.Parse for more robust validation
	return len(value) > 0 && strings.Contains(value, "T")
}

func (h *AppHandler) isValidLocation(value interface{}) bool {
	if obj, ok := value.(map[string]interface{}); ok {
		_, hasLat := obj["lat"]
		_, hasLng := obj["lng"]
		return hasLat && hasLng
	}
	return false
}
