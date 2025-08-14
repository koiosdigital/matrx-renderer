package pixlet

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/koios/matrx-renderer/internal/config"
	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"

	"tidbyt.dev/pixlet/encode"
	"tidbyt.dev/pixlet/globals"
	"tidbyt.dev/pixlet/runtime"
	"tidbyt.dev/pixlet/tools"
)

// Processor handles Pixlet app processing with a persistent runtime
type Processor struct {
	config      *config.PixletConfig
	redisConfig *config.RedisConfig
	logger      *zap.Logger
	cache       runtime.Cache
	redisCache  *RedisCache // Shared Redis cache instance
	timeout     time.Duration
}

// NewProcessor creates a new Pixlet processor with persistent runtime using InMemory cache
func NewProcessor(cfg *config.PixletConfig, logger *zap.Logger) *Processor {
	cache := runtime.NewInMemoryCache()
	runtime.InitHTTP(cache)
	runtime.InitCache(cache)

	return &Processor{
		config:  cfg,
		logger:  logger,
		cache:   cache,
		timeout: 30 * time.Second, // Default timeout
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

	return &Processor{
		config:      cfg,
		redisConfig: redisConfig,
		logger:      logger,
		cache:       cache,
		redisCache:  redisCache,
		timeout:     30 * time.Second, // Default timeout
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
		// Use Redis cache with app/device scoped keys
		requestCache = p.redisCache.WithContext(request.AppID, request.Device.ID)
	} else {
		// Use the default in-memory cache
		requestCache = p.cache
	}

	// Initialize runtime with the request-specific cache
	runtime.InitHTTP(requestCache)
	runtime.InitCache(requestCache)

	// Construct app path - use app_id to find the .star file in nested directory
	appPath := filepath.Join(p.config.AppsPath, request.AppID, request.AppID+".star")

	// Check if app exists
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("app not found: %s", request.AppID)
	}

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
	}

	applet, err := runtime.NewAppletFromFS(request.AppID, appFS, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load applet: %w", err)
	}

	// Run the applet with configuration
	roots, err := applet.RunWithConfig(renderCtx, request.Params)
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

// ListApps returns a list of available Pixlet apps
func (p *Processor) ListApps() ([]*models.PixletApp, error) {
	var apps []*models.PixletApp

	// Read the apps subdirectory within the apps path
	// Structure: /opt/apps/apps/{app_id}/{app_id}.star
	appsDir := filepath.Join(p.config.AppsPath, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read apps directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		appID := entry.Name()
		expectedStarFile := filepath.Join(p.config.AppsPath, "apps", appID, appID+".star")

		// Check if the expected .star file exists
		if _, err := os.Stat(expectedStarFile); err == nil {
			app := &models.PixletApp{
				ID:   appID,
				Name: appID,
				Path: expectedStarFile,
			}
			apps = append(apps, app)
		}
	}

	return apps, nil
}

// Close closes the processor and any associated resources
func (p *Processor) Close() error {
	if p.redisCache != nil {
		return p.redisCache.Close()
	}
	return nil
}
