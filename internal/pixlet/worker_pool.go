package pixlet

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/koios/matrx-renderer/pkg/models"
	"go.uber.org/zap"

	"tidbyt.dev/pixlet/encode"
	"tidbyt.dev/pixlet/globals"
	"tidbyt.dev/pixlet/render"
	"tidbyt.dev/pixlet/runtime"
	"tidbyt.dev/pixlet/tools"
)

// RenderJob represents a render request to be processed by a worker
type RenderJob struct {
	AppID  string
	Params map[string]interface{}
	Device models.Device
	Result chan *RenderResult
}

// RenderResult contains the result of a render job
type RenderResult struct {
	Screens *encode.Screens
	Error   error
}

// WorkerPool manages a pool of render workers for concurrent processing
type WorkerPool struct {
	workers     int
	jobQueue    chan *RenderJob
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *zap.Logger
	appRegistry *models.AppRegistry
	cache       runtime.Cache
	redisCache  *RedisCache
	secretKey   runtime.SecretDecryptionKey
	timeout     int // timeout in seconds
}

// NewWorkerPool creates a new worker pool with the specified number of workers
func NewWorkerPool(
	workers int,
	logger *zap.Logger,
	appRegistry *models.AppRegistry,
	cache runtime.Cache,
	redisCache *RedisCache,
	secretKey runtime.SecretDecryptionKey,
	timeout int,
) *WorkerPool {
	if workers <= 0 {
		workers = 4 // default to 4 workers
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		workers:     workers,
		jobQueue:    make(chan *RenderJob, workers*2), // buffer for 2x workers
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		appRegistry: appRegistry,
		cache:       cache,
		redisCache:  redisCache,
		secretKey:   secretKey,
		timeout:     timeout,
	}

	return pool
}

// Start launches all worker goroutines
func (wp *WorkerPool) Start() {
	wp.logger.Info("Starting render worker pool",
		zap.Int("workers", wp.workers),
		zap.Int("queue_size", cap(wp.jobQueue)))

	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
}

// Stop gracefully shuts down the worker pool
func (wp *WorkerPool) Stop() {
	wp.logger.Info("Stopping render worker pool")
	wp.cancel()
	close(wp.jobQueue)
	wp.wg.Wait()
	wp.logger.Info("Render worker pool stopped")
}

// UpdateAppRegistry updates the app registry used by workers
func (wp *WorkerPool) UpdateAppRegistry(registry *models.AppRegistry) {
	wp.appRegistry = registry
	wp.logger.Info("Worker pool app registry updated")
}

// Submit submits a render job to the pool and returns the result channel
func (wp *WorkerPool) Submit(ctx context.Context, appID string, params map[string]interface{}, device models.Device) (*encode.Screens, error) {
	resultChan := make(chan *RenderResult, 1)

	job := &RenderJob{
		AppID:  appID,
		Params: params,
		Device: device,
		Result: resultChan,
	}

	select {
	case wp.jobQueue <- job:
		// Job submitted
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wp.ctx.Done():
		return nil, fmt.Errorf("worker pool is shutting down")
	}

	// Wait for result
	select {
	case result := <-resultChan:
		return result.Screens, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wp.ctx.Done():
		return nil, fmt.Errorf("worker pool is shutting down")
	}
}

// worker is the main loop for a single worker
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	wp.logger.Debug("Render worker started", zap.Int("worker_id", id))

	for {
		select {
		case job, ok := <-wp.jobQueue:
			if !ok {
				wp.logger.Debug("Render worker stopping (queue closed)", zap.Int("worker_id", id))
				return
			}
			wp.processJob(id, job)
		case <-wp.ctx.Done():
			wp.logger.Debug("Render worker stopping (context cancelled)", zap.Int("worker_id", id))
			return
		}
	}
}

// processJob handles a single render job
func (wp *WorkerPool) processJob(workerID int, job *RenderJob) {
	wp.logger.Debug("Worker processing job",
		zap.Int("worker_id", workerID),
		zap.String("app_id", job.AppID))

	screens, err := wp.renderScreens(job.AppID, job.Params, job.Device)

	job.Result <- &RenderResult{
		Screens: screens,
		Error:   err,
	}
	close(job.Result)

	if err != nil {
		wp.logger.Debug("Worker completed job with error",
			zap.Int("worker_id", workerID),
			zap.String("app_id", job.AppID),
			zap.Error(err))
	} else {
		wp.logger.Debug("Worker completed job successfully",
			zap.Int("worker_id", workerID),
			zap.String("app_id", job.AppID))
	}
}

// renderScreens performs the actual rendering (called by workers)
func (wp *WorkerPool) renderScreens(appID string, params map[string]interface{}, device models.Device) (*encode.Screens, error) {
	if strings.Contains(appID, "..") || strings.Contains(appID, "/") {
		return nil, fmt.Errorf("invalid app ID: %s", appID)
	}

	var requestCache runtime.Cache
	if wp.redisCache != nil {
		requestCache = wp.redisCache
	} else {
		requestCache = wp.cache
	}

	runtime.InitHTTP(requestCache)
	runtime.InitCache(requestCache)

	app, exists := wp.appRegistry.GetApp(appID)
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
		runtime.WithSecretDecryptionKey(&wp.secretKey),
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

	ctx, cancel := context.WithTimeout(wp.ctx, secondsToDuration(wp.timeout))
	defer cancel()

	// Lock mutex to protect global dimension variables during render.
	// The globals and render package variables are not thread-safe.
	renderMu.Lock()
	globals.Width = width
	globals.Height = height
	// Also set render.FrameWidth/FrameHeight directly because pixlet's Paint()
	// only updates them when globals differ from defaults (64x32). This causes
	// stale dimensions when switching from non-default to default sizes.
	render.FrameWidth = width
	render.FrameHeight = height

	roots, err := applet.RunWithConfig(ctx, config)
	if err != nil {
		renderMu.Unlock()
		return nil, fmt.Errorf("error running applet: %w", err)
	}

	screens := encode.ScreensFromRoots(roots)
	renderMu.Unlock()

	return screens, nil
}

func secondsToDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}
