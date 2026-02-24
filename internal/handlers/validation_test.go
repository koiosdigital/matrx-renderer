package handlers

import (
	"encoding/json"
	"testing"

	"tidbyt.dev/pixlet/schema"
	"go.uber.org/zap"
)

// --- isValidColor ---

func TestIsValidColor(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"#FF0000", true},
		{"#000000", true},
		{"#ffffff", true},
		{"#aAbBcC", true},
		{"#123456", true},
		{"FF0000", false},    // missing #
		{"#FFF", false},      // too short
		{"#GGGGGG", false},   // invalid hex
		{"#12345", false},    // too short
		{"#1234567", false},  // too long
		{"", false},
		{"red", false},
	}
	for _, tt := range tests {
		if got := isValidColor(tt.input); got != tt.want {
			t.Errorf("isValidColor(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- isValidDateTime ---

func TestIsValidDateTime(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"2024-01-15T10:30:00Z", true},               // RFC3339
		{"2024-01-15T10:30:00+05:00", true},           // RFC3339 with tz
		{"2024-01-15T10:30", true},                     // ISO without seconds
		{"2024-01-15T10:30:00", true},                  // ISO with seconds
		{"2024-01-15", true},                           // date only
		{"", false},
		{"not-a-date", false},
		{"2024-13-01", false},                          // invalid month
		{"2024-01-15 10:30:00", false},                 // space instead of T
		{"  ", false},
	}
	for _, tt := range tests {
		if got := isValidDateTime(tt.input); got != tt.want {
			t.Errorf("isValidDateTime(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- isValidLocation ---

func TestIsValidLocation(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  bool
	}{
		{"valid map", map[string]interface{}{"lat": 40.7, "lng": -74.0}, true},
		{"string lat/lng", map[string]interface{}{"lat": "40.7", "lng": "-74.0"}, true},
		{"json string", `{"lat": 40.7, "lng": -74.0}`, true},
		{"missing lat", map[string]interface{}{"lng": -74.0}, false},
		{"missing lng", map[string]interface{}{"lat": 40.7}, false},
		{"empty map", map[string]interface{}{}, false},
		{"non-numeric lat", map[string]interface{}{"lat": "abc", "lng": -74.0}, false},
		{"nil", nil, false},
		{"string non-json", "not json", false},
		{"empty string", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidLocation(tt.input); got != tt.want {
				t.Errorf("isValidLocation(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- isValidOptionSelection ---

func TestIsValidOptionSelection(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  bool
	}{
		{"valid", map[string]interface{}{"display": "Foo", "value": "foo"}, true},
		{"value only", map[string]interface{}{"value": "foo"}, true},
		{"missing value", map[string]interface{}{"display": "Foo"}, false},
		{"empty value", map[string]interface{}{"value": ""}, false},
		{"empty display", map[string]interface{}{"display": "", "value": "foo"}, false},
		{"json string", `{"display":"Foo","value":"foo"}`, true},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidOptionSelection(tt.input); got != tt.want {
				t.Errorf("isValidOptionSelection(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- isValidOption ---

func TestIsValidOption(t *testing.T) {
	options := []schema.SchemaOption{
		{Display: "A", Value: "a"},
		{Display: "B", Value: "b"},
		{Display: "C", Value: "c"},
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"a", true},
		{"b", true},
		{"c", true},
		{"d", false},
		{"", false},
		{"A", false}, // case sensitive
	}
	for _, tt := range tests {
		if got := isValidOption(tt.input, options); got != tt.want {
			t.Errorf("isValidOption(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- stringifyValue ---

func TestStringifyValue(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"string", "hello", "hello"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"float64", 3.14, "3.14"},
		{"int", 42, "42"},
		{"int64", int64(99), "99"},
		{"json.Number", json.Number("123"), "123"},
		{"nil", nil, ""},
		{"map", map[string]string{"a": "b"}, `{"a":"b"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stringifyValue(tt.input)
			if err != nil {
				t.Fatalf("stringifyValue(%v) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("stringifyValue(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- toStringMap ---

func TestToStringMap(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := toStringMap(nil); got != nil {
			t.Errorf("toStringMap(nil) = %v, want nil", got)
		}
	})

	t.Run("mixed types", func(t *testing.T) {
		input := map[string]interface{}{
			"str":  "hello",
			"num":  42,
			"bool": true,
		}
		got := toStringMap(input)
		expected := map[string]string{"str": "hello", "num": "42", "bool": "true"}
		for k, v := range expected {
			if got[k] != v {
				t.Errorf("toStringMap[%q] = %q, want %q", k, got[k], v)
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		got := toStringMap(map[string]interface{}{})
		if len(got) != 0 {
			t.Errorf("toStringMap(empty) = %v, want empty", got)
		}
	})
}

// --- toFloat64 ---

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    float64
		wantErr bool
	}{
		{"float64", 3.14, 3.14, false},
		{"int", 42, 42.0, false},
		{"int64", int64(99), 99.0, false},
		{"json.Number", json.Number("1.5"), 1.5, false},
		{"string", "nope", 0, true},
		{"bool", true, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("toFloat64(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- sanitizeBase64Payload ---

func TestSanitizeBase64Payload(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "aGVsbG8=", "aGVsbG8="},
		{"data uri", "data:image/png;base64,aGVsbG8=", "aGVsbG8="},
		{"with newlines", "aGVs\nbG8=\r\n", "aGVsbG8="},
		{"empty", "", ""},
		{"whitespace", "  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeBase64Payload(tt.input); got != tt.want {
				t.Errorf("sanitizeBase64Payload(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- isValidBase64Image ---

func TestIsValidBase64Image(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid base64", "aGVsbG8=", true},
		{"data uri", "data:image/png;base64,aGVsbG8=", true},
		{"invalid", "not!!base64@@", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidBase64Image(tt.input); got != tt.want {
				t.Errorf("isValidBase64Image(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- decodeJSONObject ---

func TestDecodeJSONObject(t *testing.T) {
	t.Run("map passthrough", func(t *testing.T) {
		input := map[string]interface{}{"key": "val"}
		got, err := decodeJSONObject(input)
		if err != nil {
			t.Fatal(err)
		}
		if got["key"] != "val" {
			t.Errorf("expected key=val, got %v", got)
		}
	})

	t.Run("json string", func(t *testing.T) {
		got, err := decodeJSONObject(`{"a":1}`)
		if err != nil {
			t.Fatal(err)
		}
		if got["a"] == nil {
			t.Error("expected key 'a'")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := decodeJSONObject("")
		if err == nil {
			t.Error("expected error for empty string")
		}
	})

	t.Run("nil", func(t *testing.T) {
		_, err := decodeJSONObject(nil)
		if err == nil {
			t.Error("expected error for nil")
		}
	})

	t.Run("invalid json string", func(t *testing.T) {
		_, err := decodeJSONObject("not json")
		if err == nil {
			t.Error("expected error for invalid json")
		}
	})
}

// --- validatePolygonCoordinates ---

func TestValidatePolygonCoordinates(t *testing.T) {
	t.Run("valid closed polygon", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{
				[]interface{}{
					[]interface{}{0.0, 0.0},
					[]interface{}{1.0, 0.0},
					[]interface{}{1.0, 1.0},
					[]interface{}{0.0, 0.0},
				},
			},
		}
		if err := validatePolygonCoordinates(geo); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("not closed", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{
				[]interface{}{
					[]interface{}{0.0, 0.0},
					[]interface{}{1.0, 0.0},
					[]interface{}{1.0, 1.0},
					[]interface{}{0.5, 0.5},
				},
			},
		}
		if err := validatePolygonCoordinates(geo); err == nil {
			t.Error("expected error for unclosed ring")
		}
	})

	t.Run("too few points", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{
				[]interface{}{
					[]interface{}{0.0, 0.0},
					[]interface{}{1.0, 0.0},
					[]interface{}{0.0, 0.0},
				},
			},
		}
		if err := validatePolygonCoordinates(geo); err == nil {
			t.Error("expected error for < 4 points")
		}
	})

	t.Run("missing coordinates", func(t *testing.T) {
		if err := validatePolygonCoordinates(map[string]interface{}{}); err == nil {
			t.Error("expected error for missing coordinates")
		}
	})
}

// --- validatePointCoordinates ---

func TestValidatePointCoordinates(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{-74.0, 40.7},
		}
		if err := validatePointCoordinates(geo); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing coordinates", func(t *testing.T) {
		if err := validatePointCoordinates(map[string]interface{}{}); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("too few coords", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{-74.0},
		}
		if err := validatePointCoordinates(geo); err == nil {
			t.Error("expected error for < 2 coordinates")
		}
	})

	t.Run("non-numeric coords", func(t *testing.T) {
		geo := map[string]interface{}{
			"coordinates": []interface{}{"not", "numbers"},
		}
		if err := validatePointCoordinates(geo); err == nil {
			t.Error("expected error for non-numeric")
		}
	})
}

// --- coerceDefaultValue ---

func TestCoerceDefaultValue(t *testing.T) {
	v := NewValidator(nil, zap.NewNop())

	tests := []struct {
		name     string
		field    schema.SchemaField
		expected interface{}
	}{
		{
			"empty default",
			schema.SchemaField{Default: ""},
			"",
		},
		{
			"string default",
			schema.SchemaField{Default: "hello"},
			"hello",
		},
		{
			"bool toggle true",
			schema.SchemaField{Type: "toggle", Default: "true"},
			true,
		},
		{
			"bool onoff false",
			schema.SchemaField{Type: "onoff", Default: "false"},
			false,
		},
		{
			"integer",
			schema.SchemaField{Default: "42"},
			int64(42),
		},
		{
			"float",
			schema.SchemaField{Default: "3.14"},
			3.14,
		},
		{
			"json object",
			schema.SchemaField{Default: `{"lat":40.7,"lng":-74}`},
			map[string]interface{}{"lat": 40.7, "lng": float64(-74)},
		},
		{
			"json array",
			schema.SchemaField{Default: `["a","b"]`},
			[]interface{}{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.coerceDefaultValue(tt.field)
			gotJSON, _ := json.Marshal(got)
			expectedJSON, _ := json.Marshal(tt.expected)
			if string(gotJSON) != string(expectedJSON) {
				t.Errorf("coerceDefaultValue() = %s, want %s", gotJSON, expectedJSON)
			}
		})
	}
}

// --- ValidateOAuth2HandlerCall ---

func TestValidateOAuth2HandlerCall(t *testing.T) {
	v := NewValidator(nil, zap.NewNop())

	t.Run("valid basic", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2"}
		errs := v.ValidateOAuth2HandlerCall(field, `{"client_id":"abc"}`)
		if len(errs) != 0 {
			t.Errorf("expected no errors, got %v", errs)
		}
	})

	t.Run("missing client_id", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2"}
		errs := v.ValidateOAuth2HandlerCall(field, `{}`)
		if len(errs) != 1 || errs[0].Code != "missing_client_id" {
			t.Errorf("expected missing_client_id, got %v", errs)
		}
	})

	t.Run("pkce requires code_verifier", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2", PKCE: true}
		errs := v.ValidateOAuth2HandlerCall(field, `{"client_id":"abc"}`)
		if len(errs) != 1 || errs[0].Code != "missing_code_verifier" {
			t.Errorf("expected missing_code_verifier, got %v", errs)
		}
	})

	t.Run("pkce with code_verifier", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2", PKCE: true}
		errs := v.ValidateOAuth2HandlerCall(field, `{"client_id":"abc","code_verifier":"xyz"}`)
		if len(errs) != 0 {
			t.Errorf("expected no errors, got %v", errs)
		}
	})

	t.Run("user_defined_client requires secret", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2", UserDefinedClient: true}
		errs := v.ValidateOAuth2HandlerCall(field, `{"client_id":"abc"}`)
		if len(errs) != 1 || errs[0].Code != "missing_client_secret" {
			t.Errorf("expected missing_client_secret, got %v", errs)
		}
	})

	t.Run("invalid json data", func(t *testing.T) {
		field := schema.SchemaField{ID: "oauth", Type: "oauth2"}
		errs := v.ValidateOAuth2HandlerCall(field, `not json`)
		if len(errs) != 1 || errs[0].Code != "invalid_handler_data" {
			t.Errorf("expected invalid_handler_data, got %v", errs)
		}
	})
}

// --- FindFieldByHandler ---

func TestFindFieldByHandler(t *testing.T) {
	v := NewValidator(nil, zap.NewNop())
	s := &schema.Schema{
		Fields: []schema.SchemaField{
			{ID: "f1", Handler: "handler_a"},
			{ID: "f2", Handler: "handler_b"},
			{ID: "f3", Handler: ""},
		},
	}

	t.Run("found", func(t *testing.T) {
		f := v.FindFieldByHandler("handler_b", s)
		if f == nil || f.ID != "f2" {
			t.Errorf("expected f2, got %v", f)
		}
	})

	t.Run("not found", func(t *testing.T) {
		f := v.FindFieldByHandler("handler_c", s)
		if f != nil {
			t.Errorf("expected nil, got %v", f)
		}
	})
}

// --- fieldRequiresExplicitValue ---

func TestFieldRequiresExplicitValue(t *testing.T) {
	v := NewValidator(nil, zap.NewNop())

	requiresValue := []string{"dropdown", "onoff", "radio", "toggle", "oauth2"}
	for _, typ := range requiresValue {
		if !v.fieldRequiresExplicitValue(schema.SchemaField{Type: typ}) {
			t.Errorf("expected type %q to require explicit value", typ)
		}
	}

	doesNotRequire := []string{"text", "color", "datetime", "location", "png", "geojson", "notification"}
	for _, typ := range doesNotRequire {
		if v.fieldRequiresExplicitValue(schema.SchemaField{Type: typ}) {
			t.Errorf("expected type %q to NOT require explicit value", typ)
		}
	}
}
