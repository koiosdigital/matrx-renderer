package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koios/matrx-renderer/internal/pixlet"
	"go.uber.org/zap"
	"tidbyt.dev/pixlet/schema"
)

// ValidationError represents a validation error for a specific field
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// Validator coordinates schema-driven config validation and dynamic field resolution.
type Validator struct {
	processor *pixlet.Processor
	logger    *zap.Logger
}

// NewValidator creates a validator instance bound to the Pixlet processor.
func NewValidator(processor *pixlet.Processor, logger *zap.Logger) *Validator {
	return &Validator{processor: processor, logger: logger}
}

// ValidateConfig validates a configuration map against an app schema and returns normalized values.
func (v *Validator) ValidateConfig(ctx context.Context, appID string, config map[string]interface{}, appSchema *schema.Schema) (map[string]interface{}, []ValidationError, error) {
	if config == nil {
		config = make(map[string]interface{})
	}

	normalizedConfig := make(map[string]interface{})
	for key, value := range config {
		normalizedConfig[key] = value
	}

	var errors []ValidationError

	schemaFields := make(map[string]schema.SchemaField)
	for _, field := range appSchema.Fields {
		schemaFields[field.ID] = field
	}

	effectiveFields := make([]schema.SchemaField, 0, len(appSchema.Fields))
	for _, field := range appSchema.Fields {
		if field.Type == "generated" {
			generatedFields, err := v.resolveGeneratedFields(ctx, appID, field, config, schemaFields)
			if err != nil {
				return nil, nil, err
			}
			for _, gf := range generatedFields {
				effectiveFields = append(effectiveFields, gf)
				schemaFields[gf.ID] = gf
			}
			continue
		}

		effectiveFields = append(effectiveFields, field)
	}

	for _, field := range effectiveFields {
		value, exists := config[field.ID]
		defaultDefined := strings.TrimSpace(field.Default) != ""

		if !exists {
			if defaultDefined {
				normalizedConfig[field.ID] = v.coerceDefaultValue(field)
			} else if v.fieldRequiresExplicitValue(field) {
				errors = append(errors, ValidationError{
					Field:   field.ID,
					Message: fmt.Sprintf("Field '%s' is required", field.Name),
					Code:    "required",
				})
			}
			continue
		}

		normalizedConfig[field.ID] = value
		fieldErrors := v.validateFieldValue(field, value)
		errors = append(errors, fieldErrors...)
	}

	for key := range config {
		if _, exists := schemaFields[key]; !exists {
			errors = append(errors, ValidationError{
				Field:   key,
				Message: fmt.Sprintf("Unknown field '%s'", key),
				Code:    "unknown_field",
			})
		}
	}

	return normalizedConfig, errors, nil
}

func (v *Validator) resolveGeneratedFields(ctx context.Context, appID string, generatedField schema.SchemaField, config map[string]interface{}, schemaFields map[string]schema.SchemaField) ([]schema.SchemaField, error) {
	v.logger.Debug("Resolving generated field",
		zap.String("field_id", generatedField.ID),
		zap.String("handler", generatedField.Handler),
		zap.String("source", generatedField.Source))

	if generatedField.Handler == "" {
		v.logger.Warn("Generated field missing handler", zap.String("field_id", generatedField.ID))
		return nil, nil
	}

	sourceField, ok := schemaFields[generatedField.Source]
	if !ok {
		v.logger.Warn("Generated field references unknown source",
			zap.String("field_id", generatedField.ID),
			zap.String("source_id", generatedField.Source))
		return nil, nil
	}

	sourceValue, exists := config[sourceField.ID]
	if !exists {
		if sourceField.Default == "" {
			v.logger.Debug("Generated field source has no value and no default",
				zap.String("field_id", generatedField.ID),
				zap.String("source_id", sourceField.ID))
			return nil, nil
		}
		sourceValue = sourceField.Default
		v.logger.Debug("Using default value for generated field source",
			zap.String("field_id", generatedField.ID),
			zap.String("source_id", sourceField.ID),
			zap.Any("default", sourceField.Default))
	}

	parameter, err := stringifyValue(sourceValue)
	if err != nil {
		return nil, fmt.Errorf("failed to encode source value for generated field %s: %w", generatedField.ID, err)
	}

	if parameter == "" {
		v.logger.Debug("Generated field source value is empty after stringification",
			zap.String("field_id", generatedField.ID),
			zap.String("source_id", sourceField.ID))
		return nil, nil
	}

	v.logger.Debug("Calling schema handler for generated field",
		zap.String("field_id", generatedField.ID),
		zap.String("handler", generatedField.Handler),
		zap.String("parameter", parameter))

	result, err := v.processor.CallSchemaHandler(ctx, appID, generatedField.Handler, parameter)
	if err != nil {
		return nil, fmt.Errorf("generated handler call failed for %s: %w", generatedField.ID, err)
	}

	if result == "" {
		v.logger.Debug("Generated field handler returned empty result",
			zap.String("field_id", generatedField.ID),
			zap.String("handler", generatedField.Handler))
		return nil, nil
	}

	v.logger.Debug("Generated field handler returned result",
		zap.String("field_id", generatedField.ID),
		zap.Int("result_len", len(result)))

	var generatedSchema schema.Schema
	if err := json.Unmarshal([]byte(result), &generatedSchema); err != nil {
		return nil, fmt.Errorf("failed to decode generated schema for %s: %w", generatedField.ID, err)
	}

	v.logger.Debug("Parsed generated schema",
		zap.String("field_id", generatedField.ID),
		zap.Int("num_fields", len(generatedSchema.Fields)))

	fields := make([]schema.SchemaField, 0, len(generatedSchema.Fields))
	for _, field := range generatedSchema.Fields {
		if field.Type == "generated" {
			v.logger.Warn("Nested generated schema ignored",
				zap.String("parent_field", generatedField.ID),
				zap.String("child_field", field.ID))
			continue
		}
		v.logger.Debug("Adding generated field to schema",
			zap.String("parent_id", generatedField.ID),
			zap.String("child_id", field.ID),
			zap.String("child_type", field.Type))
		fields = append(fields, field)
	}

	v.logger.Debug("Resolved generated fields",
		zap.String("field_id", generatedField.ID),
		zap.Int("num_resolved", len(fields)))

	return fields, nil
}

func (v *Validator) validateFieldValue(field schema.SchemaField, value interface{}) []ValidationError {
	var errors []ValidationError

	strValue, err := stringifyValue(value)
	if err != nil {
		errors = append(errors, ValidationError{
			Field:   field.ID,
			Message: fmt.Sprintf("Invalid value type for field '%s'", field.Name),
			Code:    "invalid_type",
		})
		return errors
	}

	switch field.Type {
	case "text":
		if _, ok := value.(string); !ok {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a string", field.Name),
				Code:    "invalid_type",
			})
		}

	case "color":
		if !isValidColor(strValue) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid color (e.g., #FF0000)", field.Name),
				Code:    "invalid_color",
			})
		}

	case "onoff", "toggle":
		if strings.TrimSpace(strValue) == "" {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be true or false", field.Name),
				Code:    "invalid_bool",
			})
			break
		}
		if _, err := strconv.ParseBool(strValue); err != nil {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a boolean", field.Name),
				Code:    "invalid_bool",
			})
		}

	case "dropdown", "radio":
		if !isValidOption(strValue, field.Options) {
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
		if strValue != "" && !isValidDateTime(strValue) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid datetime", field.Name),
				Code:    "invalid_datetime",
			})
		}

	case "location":
		if !isValidLocation(value) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid location object with lat and lng", field.Name),
				Code:    "invalid_location",
			})
		}

	case "locationbased":
		if !isValidOptionSelection(value) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must contain a valid location option", field.Name),
				Code:    "invalid_location_option",
			})
		}

	case "typeahead":
		if !isValidOptionSelection(value) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must contain a valid selection", field.Name),
				Code:    "invalid_selection",
			})
		}

	case "oauth2":
		if strings.TrimSpace(strValue) == "" {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must contain authorization data", field.Name),
				Code:    "missing_credentials",
			})
		}

	case "png":
		if strings.TrimSpace(strValue) == "" {
			break
		}
		if !isValidBase64Image(strValue) {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' must be a valid base64 encoded image", field.Name),
				Code:    "invalid_image",
			})
		}

	case "notification":
		if strings.TrimSpace(strValue) == "" {
			errors = append(errors, ValidationError{
				Field:   field.ID,
				Message: fmt.Sprintf("Field '%s' cannot be empty", field.Name),
				Code:    "invalid_notification",
			})
		}
	}

	return errors
}

func (v *Validator) fieldRequiresExplicitValue(field schema.SchemaField) bool {
	switch field.Type {
	case "dropdown", "onoff", "radio", "toggle", "oauth2":
		return true
	default:
		return false
	}
}

func (v *Validator) coerceDefaultValue(field schema.SchemaField) interface{} {
	trimmed := strings.TrimSpace(field.Default)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var obj interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			return obj
		}
	}

	switch field.Type {
	case "onoff", "toggle":
		if b, err := strconv.ParseBool(trimmed); err == nil {
			return b
		}
	}

	if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f
	}

	return field.Default
}

func isValidColor(color string) bool {
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

func isValidOption(value string, options []schema.SchemaOption) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func isValidDateTime(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return true
	}
	if _, err := time.Parse("2006-01-02T15:04", value); err == nil {
		return true
	}
	if _, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		return true
	}
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return true
	}
	return false
}

func isValidLocation(value interface{}) bool {
	obj, err := decodeJSONObject(value)
	if err != nil {
		return false
	}
	latRaw, hasLat := obj["lat"]
	lngRaw, hasLng := obj["lng"]
	if !hasLat || !hasLng {
		return false
	}
	latStr, err := stringifyValue(latRaw)
	if err != nil || strings.TrimSpace(latStr) == "" {
		return false
	}
	if _, err := strconv.ParseFloat(latStr, 64); err != nil {
		return false
	}
	lngStr, err := stringifyValue(lngRaw)
	if err != nil || strings.TrimSpace(lngStr) == "" {
		return false
	}
	if _, err := strconv.ParseFloat(lngStr, 64); err != nil {
		return false
	}
	return true
}

func isValidOptionSelection(value interface{}) bool {
	obj, err := decodeJSONObject(value)
	if err != nil {
		return false
	}
	optionValue, ok := obj["value"]
	if !ok {
		return false
	}
	valueStr, err := stringifyValue(optionValue)
	if err != nil || strings.TrimSpace(valueStr) == "" {
		return false
	}
	if display, ok := obj["display"]; ok {
		displayStr, err := stringifyValue(display)
		if err != nil {
			return false
		}
		if strings.TrimSpace(displayStr) == "" {
			return false
		}
	}
	return true
}

func decodeJSONObject(value interface{}) (map[string]interface{}, error) {
	switch v := value.(type) {
	case map[string]interface{}:
		return v, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("empty string")
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(v), &obj); err != nil {
			return nil, err
		}
		return obj, nil
	case json.RawMessage:
		var obj map[string]interface{}
		if err := json.Unmarshal(v, &obj); err != nil {
			return nil, err
		}
		return obj, nil
	case nil:
		return nil, fmt.Errorf("empty value")
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(bytes, &obj); err != nil {
			return nil, err
		}
		return obj, nil
	}
}

func isValidBase64Image(data string) bool {
	clean := sanitizeBase64Payload(data)
	if clean == "" {
		return false
	}
	if _, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return true
	}
	if _, err := base64.RawStdEncoding.DecodeString(clean); err == nil {
		return true
	}
	return false
}

func sanitizeBase64Payload(data string) string {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "data:") {
		if idx := strings.Index(trimmed, ","); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
	}
	trimmed = strings.ReplaceAll(trimmed, "\n", "")
	trimmed = strings.ReplaceAll(trimmed, "\r", "")
	return trimmed
}

// stringifyValue normalizes arbitrary config values into a string for downstream Pixlet handlers
func stringifyValue(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 64), nil
	case int:
		return strconv.Itoa(v), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case json.Number:
		return v.String(), nil
	case nil:
		return "", nil
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(bytes), nil
	}
}
