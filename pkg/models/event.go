package models

import "time"

// Device represents the target device configuration
type Device struct {
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// RenderRequest represents a request to render a Pixlet app
type RenderRequest struct {
	Type   string            `json:"type"`
	AppID  string            `json:"app_id"`
	Device Device            `json:"device"`
	Params map[string]string `json:"params"`
}

// RenderResult represents the result of a render operation
type RenderResult struct {
	DeviceID     string    `json:"device_id"`
	AppID        string    `json:"app_id"`
	RenderOutput string    `json:"render_output"` // base64 encoded WebP
	ProcessedAt  time.Time `json:"processed_at"`
}

// PixletApp represents metadata about a Pixlet app
type PixletApp struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Description string            `json:"description"`
	Config      map[string]string `json:"config"`
}
