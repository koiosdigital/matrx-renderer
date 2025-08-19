package pixlet

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"

	"tidbyt.dev/pixlet/encode"
	"tidbyt.dev/pixlet/globals"
	"tidbyt.dev/pixlet/runtime"
	"tidbyt.dev/pixlet/schema"
	"tidbyt.dev/pixlet/tools"
)

// NoOpAEAD is a dummy implementation of tink.AEAD that transparently returns the underlying data
// without any encryption/decryption. Used for development/testing purposes.
type NoOpAEAD struct{}

// Encrypt simply returns the plaintext as-is, ignoring associatedData
func (n *NoOpAEAD) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	return plaintext, nil
}

// Decrypt simply returns the ciphertext as-is, ignoring associatedData
func (n *NoOpAEAD) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	return ciphertext, nil
}

// Processor handles Pixlet app processing with a persistent runtime
type Processor struct {
	config              *config.PixletConfig
	redisConfig         *config.RedisConfig
	logger              *zap.Logger
	cache               runtime.Cache
	redisCache          *RedisCache // Shared Redis cache instance
	timeout             time.Duration
	appRegistry         *models.AppRegistry         // App registry for manifest-based loading
	secretDecryptionKey runtime.SecretDecryptionKey // Key for decrypting secrets in Pixlet apps
}

// NewProcessor creates a new Pixlet processor with persistent runtime using InMemory cache
func NewProcessor(cfg *config.PixletConfig, logger *zap.Logger) *Processor {
	cache := runtime.NewInMemoryCache()
	runtime.InitHTTP(cache)
	runtime.InitCache(cache)

	// Create app registry and load apps
	appRegistry := models.NewAppRegistry()
	if err := appRegistry.LoadApps(cfg.AppsPath); err != nil {
		logger.Error("Failed to load apps", zap.Error(err))
	}

	decodedKeyset, err := base64.StdEncoding.DecodeString(cfg.PlaintextSecretKeysetB64)
	if err != nil {
		logger.Error("Failed to decode secret keyset", zap.Error(err))
	}

	secretDecryptionKey := &runtime.SecretDecryptionKey{
		EncryptedKeysetJSON: decodedKeyset,
		KeyEncryptionKey:    &NoOpAEAD{}, // Use NoOp AEAD for transparent key handling
	}

	return &Processor{
		config:              cfg,
		logger:              logger,
		cache:               cache,
		timeout:             30 * time.Second, // Default timeout
		appRegistry:         appRegistry,
		secretDecryptionKey: *secretDecryptionKey,
	}
}

// NewProcessorWithRedis creates a new Pixlet processor with Redis cache support
func NewProcessorWithRedis(cfg *config.PixletConfig, redisConfig *config.RedisConfig, logger *zap.Logger) *Processor {
	// Create shared Redis cache instance
	redisCache := NewRedisCache(redisConfig)

	// For initialization, we use an in-memory cache as fallback
	cache := runtime.NewInMemoryCache()
	runtime.InitHTTP(cache)
	runtime.InitCache(cache)

	// Create app registry and load apps
	appRegistry := models.NewAppRegistry()
	if err := appRegistry.LoadApps(cfg.AppsPath); err != nil {
		logger.Error("Failed to load apps", zap.Error(err))
	}

	decodedKeyset, err := base64.StdEncoding.DecodeString(cfg.PlaintextSecretKeysetB64)
	if err != nil {
		logger.Error("Failed to decode secret keyset", zap.Error(err))
	}

	secretDecryptionKey := &runtime.SecretDecryptionKey{
		EncryptedKeysetJSON: decodedKeyset,
		KeyEncryptionKey:    &NoOpAEAD{}, // Use NoOp AEAD for transparent key handling
	}

	return &Processor{
		config:              cfg,
		redisConfig:         redisConfig,
		logger:              logger,
		cache:               cache,
		redisCache:          redisCache,
		timeout:             30 * time.Second, // Default timeout
		appRegistry:         appRegistry,
		secretDecryptionKey: *secretDecryptionKey,
	}
}

// RenderApp renders a Pixlet app with the given configuration using the runtime
func (p *Processor) RenderApp(ctx context.Context, request *models.RenderRequest) (*models.RenderResult, error) {
	// Validate app ID (security: prevent path traversal)
	if strings.Contains(request.AppID, "..") || strings.Contains(request.AppID, "/") {
		return nil, fmt.Errorf("invalid app ID: %s", request.AppID)
	}

	// Set up cache for this request
	var requestCache runtime.Cache
	if p.redisCache != nil {
		requestCache = p.redisCache
	} else {
		requestCache = p.cache
	}

	// Initialize runtime with the request-specific cache
	runtime.InitHTTP(requestCache)
	runtime.InitCache(requestCache)

	// Get app from registry
	app, exists := p.appRegistry.GetApp(request.AppID)
	if !exists {
		return nil, fmt.Errorf("app not found: %s", request.AppID)
	}

	// Use the star file path from the manifest
	appPath := app.StarFilePath

	// Set device dimensions in globals
	globals.Width = request.Device.Width
	globals.Height = request.Device.Height

	// Create context with timeout
	renderCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Set up filesystem for the app
	var appFS fs.FS
	info, err := os.Stat(appPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat app path: %w", err)
	}

	if info.IsDir() {
		appFS = os.DirFS(appPath)
	} else {
		if !strings.HasSuffix(appPath, ".star") {
			return nil, fmt.Errorf("app file must have suffix .star: %s", appPath)
		}
		appFS = tools.NewSingleFileFS(appPath)
	}

	// Create applet with silent output (no print statements)
	opts := []runtime.AppletOption{
		runtime.WithPrintDisabled(),
		runtime.WithSecretDecryptionKey(&p.secretDecryptionKey),
	}

	applet, err := runtime.NewAppletFromFS(request.AppID, appFS, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load applet: %w", err)
	}

	// Prepare config with device dimensions and request params
	config := make(map[string]string)

	// Add request params first
	for key, value := range request.Params {
		config[key] = value
	}

	// Set device dimensions, allowing them to override request params if specified
	config["display_width"] = fmt.Sprintf("%d", request.Device.Width)
	config["display_height"] = fmt.Sprintf("%d", request.Device.Height) // Run the applet with configuration
	roots, err := applet.RunWithConfig(renderCtx, config)
	if err != nil {
		return nil, fmt.Errorf("error running applet: %w", err)
	}

	// Convert to screens and encode as WebP
	screens := encode.ScreensFromRoots(roots)

	// Use default filter (no magnification)
	filter := func(input image.Image) (image.Image, error) {
		return input, nil
	}

	// Set max duration for animations (15 seconds default)
	maxDuration := 15000
	if screens.ShowFullAnimation {
		maxDuration = 0
	}

	// Encode as WebP
	webpData, err := screens.EncodeWebP(maxDuration, filter)
	if err != nil {
		return nil, fmt.Errorf("error encoding WebP: %w", err)
	}

	// Encode to base64
	base64Output := base64.StdEncoding.EncodeToString(webpData)

	p.logger.Debug("Pixlet render completed",
		zap.String("app_id", request.AppID),
		zap.String("device_id", request.Device.ID),
		zap.Int("output_size", len(webpData)))

	return &models.RenderResult{
		Type:         "render_result",
		UUID:         request.UUID,
		DeviceID:     request.Device.ID,
		AppID:        request.AppID,
		RenderOutput: base64Output,
		ProcessedAt:  time.Now(),
	}, nil
}

// ListApps returns a list of available Pixlet apps from the registry
func (p *Processor) ListApps() ([]*models.PixletApp, error) {
	var apps []*models.PixletApp

	// Get all apps from the registry
	manifests := p.appRegistry.GetAppsList()

	for _, manifest := range manifests {
		app := &models.PixletApp{
			ID:   manifest.ID,
			Name: manifest.Name,
			Path: manifest.StarFilePath,
		}
		apps = append(apps, app)
	}

	return apps, nil
}

// GetAppRegistry returns the app registry for HTTP endpoints
func (p *Processor) GetAppRegistry() *models.AppRegistry {
	return p.appRegistry
}

// GetAppSchema returns the schema for a specific app
func (p *Processor) GetAppSchema(ctx context.Context, appID string) (*schema.Schema, error) {
	// Validate app ID (security: prevent path traversal)
	if strings.Contains(appID, "..") || strings.Contains(appID, "/") {
		return nil, fmt.Errorf("invalid app ID: %s", appID)
	}

	// Get app from registry
	app, exists := p.appRegistry.GetApp(appID)
	if !exists {
		return nil, fmt.Errorf("app not found: %s", appID)
	}

	// Use the star file path from the manifest
	appPath := app.StarFilePath

	// Set up filesystem for the app
	var appFS fs.FS
	info, err := os.Stat(appPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat app path: %w", err)
	}

	if info.IsDir() {
		appFS = os.DirFS(appPath)
	} else {
		if !strings.HasSuffix(appPath, ".star") {
			return nil, fmt.Errorf("app file must have suffix .star: %s", appPath)
		}
		appFS = tools.NewSingleFileFS(appPath)
	}

	// Create applet with silent output (no print statements)
	opts := []runtime.AppletOption{
		runtime.WithPrintDisabled(),
		runtime.WithSecretDecryptionKey(&p.secretDecryptionKey),
	}

	applet, err := runtime.NewAppletFromFS(appID, appFS, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load applet: %w", err)
	}

	// Return the schema from the applet
	if applet.Schema == nil {
		return nil, fmt.Errorf("app does not define a schema")
	}

	return applet.Schema, nil
}

// CallSchemaHandler calls a schema handler for a specific app
func (p *Processor) CallSchemaHandler(ctx context.Context, appID, handlerName, parameter string) (string, error) {
	// Validate app ID (security: prevent path traversal)
	if strings.Contains(appID, "..") || strings.Contains(appID, "/") {
		return "", fmt.Errorf("invalid app ID: %s", appID)
	}

	// Get app from registry
	app, exists := p.appRegistry.GetApp(appID)
	if !exists {
		return "", fmt.Errorf("app not found: %s", appID)
	}

	// Use the star file path from the manifest
	appPath := app.StarFilePath

	// Set up filesystem for the app
	var appFS fs.FS
	info, err := os.Stat(appPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat app path: %w", err)
	}

	if info.IsDir() {
		appFS = os.DirFS(appPath)
	} else {
		if !strings.HasSuffix(appPath, ".star") {
			return "", fmt.Errorf("app file must have suffix .star: %s", appPath)
		}
		appFS = tools.NewSingleFileFS(appPath)
	}

	// Create applet with silent output (no print statements)
	opts := []runtime.AppletOption{
		runtime.WithPrintDisabled(),
		runtime.WithSecretDecryptionKey(&p.secretDecryptionKey),
	}

	applet, err := runtime.NewAppletFromFS(appID, appFS, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to load applet: %w", err)
	}

	// Check if the applet has a schema
	if applet.Schema == nil {
		return "", fmt.Errorf("app does not define a schema")
	}

	// Call the schema handler
	result, err := applet.CallSchemaHandler(ctx, handlerName, parameter)
	if err != nil {
		return "", fmt.Errorf("error calling schema handler %s: %w", handlerName, err)
	}

	return result, nil
}

// Close closes the processor and any associated resources
func (p *Processor) Close() error {
	if p.redisCache != nil {
		return p.redisCache.Close()
	}
	return nil
}
