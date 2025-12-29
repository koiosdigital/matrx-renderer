package pixlet

import (
	"context"
	"encoding/base64"
	"errors"
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
	"tidbyt.dev/pixlet/runtime"
	"tidbyt.dev/pixlet/schema"
	"tidbyt.dev/pixlet/tools"

	"github.com/google/tink/go/testing/fakekms"
)


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
	workerPool          *WorkerPool                 // Worker pool for concurrent rendering
}

// ErrSchemaNotDefined indicates that an app does not expose a Pixlet schema.
var ErrSchemaNotDefined = errors.New("app does not define a schema")

func GetSecretDecryptionKey(cfg *config.PixletConfig, logger *zap.Logger) (*runtime.SecretDecryptionKey, error) {
	defaultKey := &runtime.SecretDecryptionKey{}
	if cfg == nil {
		return defaultKey, fmt.Errorf("pixlet config is required")
	}

	if strings.TrimSpace(cfg.KeyEncryptionKeyB64) == "" || strings.TrimSpace(cfg.SecretEncryptionKeyB64) == "" {
		logger.Debug("Secret encryption keys not configured; using default decryptor")
		return defaultKey, nil
	}

	client, err := fakekms.NewClient(cfg.KeyEncryptionKeyB64)
	if err != nil {
		return defaultKey, fmt.Errorf("failed to create KMS client: %w", err)
	}
	kekAEAD, err := client.GetAEAD(cfg.KeyEncryptionKeyB64)
	if err != nil {
		return defaultKey, fmt.Errorf("failed to get AEAD: %w", err)
	}

	decodedKeyset, err := base64.StdEncoding.DecodeString(cfg.SecretEncryptionKeyB64)
	if err != nil {
		return defaultKey, fmt.Errorf("failed to decode secret keyset: %w", err)
	}

	secretDecryptionKey := &runtime.SecretDecryptionKey{
		EncryptedKeysetJSON: decodedKeyset,
		KeyEncryptionKey:    kekAEAD,
	}

	return secretDecryptionKey, nil
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

	secretDecryptionKey, err := GetSecretDecryptionKey(cfg, logger)
	if err != nil {
		logger.Error("Failed to get secret decryption key", zap.Error(err))
	}

	timeout := cfg.RenderTimeout
	if timeout <= 0 {
		timeout = 30
	}

	// Create worker pool for concurrent rendering
	workerPool := NewWorkerPool(
		cfg.RenderWorkers,
		logger,
		appRegistry,
		cache,
		nil, // no Redis cache
		*secretDecryptionKey,
		timeout,
	)
	workerPool.Start()

	return &Processor{
		config:              cfg,
		logger:              logger,
		cache:               cache,
		timeout:             time.Duration(timeout) * time.Second,
		appRegistry:         appRegistry,
		secretDecryptionKey: *secretDecryptionKey,
		workerPool:          workerPool,
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

	secretDecryptionKey, err := GetSecretDecryptionKey(cfg, logger)
	if err != nil {
		logger.Error("Failed to get secret decryption key", zap.Error(err))
	}

	timeout := cfg.RenderTimeout
	if timeout <= 0 {
		timeout = 30
	}

	// Create worker pool for concurrent rendering
	workerPool := NewWorkerPool(
		cfg.RenderWorkers,
		logger,
		appRegistry,
		cache,
		redisCache,
		*secretDecryptionKey,
		timeout,
	)
	workerPool.Start()

	return &Processor{
		config:              cfg,
		redisConfig:         redisConfig,
		logger:              logger,
		cache:               cache,
		redisCache:          redisCache,
		timeout:             time.Duration(timeout) * time.Second,
		appRegistry:         appRegistry,
		secretDecryptionKey: *secretDecryptionKey,
		workerPool:          workerPool,
	}
}

// RenderApp renders a Pixlet app with the given configuration using the runtime
func (p *Processor) RenderApp(ctx context.Context, request *models.RenderRequest) (*models.RenderResult, error) {
	screens, err := p.renderScreens(ctx, request.AppID, request.Params, request.Device)
	if err != nil {
		// Render failed (e.g., fail() called in starlark) - return empty result with error flag
		return &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "",
			Error:        true,
			ProcessedAt:  time.Now(),
		}, err
	}

	// Check if app returned empty screens (e.g., return [] in starlark)
	if screens.Empty() {
		p.logger.Debug("Pixlet render returned empty screens (skipped)",
			zap.String("app_id", request.AppID),
			zap.String("device_id", request.Device.ID))

		return &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "",
			Error:        false,
			ProcessedAt:  time.Now(),
		}, nil
	}

	filter := func(input image.Image) (image.Image, error) {
		return input, nil
	}

	maxDuration := 15000
	if screens.ShowFullAnimation {
		maxDuration = 0
	}

	webpData, err := screens.EncodeWebP(maxDuration, filter)
	if err != nil {
		// Encoding failed - return empty result with error flag
		return &models.RenderResult{
			Type:         "render_result",
			UUID:         request.UUID,
			DeviceID:     request.Device.ID,
			AppID:        request.AppID,
			RenderOutput: "",
			Error:        true,
			ProcessedAt:  time.Now(),
		}, fmt.Errorf("error encoding WebP: %w", err)
	}

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
		Error:        false,
		ProcessedAt:  time.Now(),
	}, nil
}

// RenderPreview renders an app configuration and returns raw image bytes in the requested format.
func (p *Processor) RenderPreview(ctx context.Context, appID string, params map[string]interface{}, device models.Device, format string) ([]byte, error) {
	screens, err := p.renderScreens(ctx, appID, params, device)
	if err != nil {
		return nil, err
	}

	filter := func(input image.Image) (image.Image, error) {
		return input, nil
	}

	maxDuration := 15000
	if screens.ShowFullAnimation {
		maxDuration = 0
	}

	if strings.ToLower(format) != "webp" {
		return nil, fmt.Errorf("unsupported format: %s (only webp is supported)", format)
	}

	webpData, err := screens.EncodeWebP(maxDuration, filter)
	if err != nil {
		return nil, fmt.Errorf("error encoding WebP: %w", err)
	}
	p.logger.Debug("Pixlet preview rendered",
		zap.String("app_id", appID),
		zap.Int("output_size", len(webpData)))
	return webpData, nil
}

func (p *Processor) renderScreens(ctx context.Context, appID string, params map[string]interface{}, device models.Device) (*encode.Screens, error) {
	// Delegate rendering to the worker pool for concurrent processing
	return p.workerPool.Submit(ctx, appID, params, device)
}

// renderScreensDirect performs rendering directly without the worker pool (used for schema operations)
func (p *Processor) renderScreensDirect(ctx context.Context, appID string, params map[string]interface{}, device models.Device) (*encode.Screens, error) {
	if strings.Contains(appID, "..") || strings.Contains(appID, "/") {
		return nil, fmt.Errorf("invalid app ID: %s", appID)
	}

	var requestCache runtime.Cache
	if p.redisCache != nil {
		requestCache = p.redisCache
	} else {
		requestCache = p.cache
	}

	runtime.InitHTTP(requestCache)
	runtime.InitCache(requestCache)

	app, exists := p.appRegistry.GetApp(appID)
	if !exists {
		return nil, fmt.Errorf("app not found: %s", appID)
	}

	appPath := app.StarFilePath

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

	opts := []runtime.AppletOption{
		runtime.WithPrintDisabled(),
		runtime.WithSecretDecryptionKey(&p.secretDecryptionKey),
	}

	applet, err := runtime.NewAppletFromFS(appID, appFS, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load applet: %w", err)
	}

	config := make(map[string]string)
	for key, value := range params {
		switch v := value.(type) {
		case string:
			config[key] = v
		case nil:
			config[key] = ""
		default:
			config[key] = fmt.Sprintf("%v", v)
		}
	}

	width := device.Width
	if width <= 0 {
		width = 64
	}
	height := device.Height
	if height <= 0 {
		height = 32
	}

	config["display_width"] = fmt.Sprintf("%d", width)
	config["display_height"] = fmt.Sprintf("%d", height)

	renderCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Use RunWithConfigAndDimensions to embed dimensions in roots for thread-safe rendering
	roots, err := applet.RunWithConfigAndDimensions(renderCtx, config, width, height)
	if err != nil {
		return nil, fmt.Errorf("error running applet: %w", err)
	}

	screens := encode.ScreensFromRoots(roots)
	return screens, nil
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

// RefreshAppRegistry reloads apps from the filesystem
func (p *Processor) RefreshAppRegistry() error {
	p.logger.Info("Refreshing app registry from filesystem",
		zap.String("apps_path", p.config.AppsPath))

	// Create a new registry and load apps
	newRegistry := models.NewAppRegistry()
	if err := newRegistry.LoadApps(p.config.AppsPath); err != nil {
		return fmt.Errorf("failed to load apps: %w", err)
	}

	// Replace the current registry
	p.appRegistry = newRegistry

	// Update the worker pool's registry as well
	if p.workerPool != nil {
		p.workerPool.UpdateAppRegistry(newRegistry)
	}

	apps := newRegistry.GetAppsList()
	p.logger.Info("App registry refreshed successfully",
		zap.Int("app_count", len(apps)))

	return nil
}

// Stop gracefully shuts down the processor and its worker pool
func (p *Processor) Stop() {
	if p.workerPool != nil {
		p.workerPool.Stop()
	}
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

	// Return the schema from the applet (empty schema is valid)
	if applet.Schema == nil {
		return &schema.Schema{}, nil
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
		return "", ErrSchemaNotDefined
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
