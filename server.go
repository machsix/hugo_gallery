package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/disintegration/imaging"
)

// ImageCache represents cached image metadata
type ImageCache struct {
	OriginalPath string
	CachedPath   string
	CreatedAt    time.Time
}

// ResizeRequest represents a pending image resize request
type ResizeRequest struct {
	OriginalPath string
	Width        int
	Context      context.Context
	Cancel       context.CancelFunc
}

// Add this type to handle background jobs
type ResizeJob struct {
	OriginalPath string
	Width        int
	CacheKey     string
	Done         bool
	Error        error
	mutex        sync.RWMutex
}

// ImageProcessor handles image resizing and caching operations
type ImageProcessor struct {
	cacheDir      string
	expiration    time.Duration
	cache         map[string]ImageCache
	mutex         sync.RWMutex
	cleanupTick   time.Duration
	cacheFile     string
	lastSaved     time.Time
	saveInterval  time.Duration
	resizeLimit   chan struct{} // Replace resizeSemaphore
	maxConcurrent int64

	// New fields for active resizes
	activeResizes   map[string]*ResizeRequest
	activeResizeMux sync.RWMutex

	// Add these fields to ImageProcessor struct
	pendingResizes map[string]*ResizeJob
	pendingMux     sync.RWMutex
}

// NewImageProcessor creates a new ImageProcessor instance
func NewImageProcessor(cacheDir string, expiration time.Duration) (*ImageProcessor, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	const maxConcurrentResizes = 10 // Limit concurrent resizes

	processor := &ImageProcessor{
		cacheDir:       cacheDir,
		expiration:     expiration,
		cache:          make(map[string]ImageCache),
		cleanupTick:    10 * time.Minute,
		cacheFile:      path.Join(cacheDir, "cache.json"),
		saveInterval:   10 * time.Minute,
		lastSaved:      time.Now(),
		resizeLimit:    make(chan struct{}, maxConcurrentResizes),
		maxConcurrent:  maxConcurrentResizes,
		activeResizes:  make(map[string]*ResizeRequest),
		pendingResizes: make(map[string]*ResizeJob),
	}

	// Load cache from file
	if err := processor.loadCache(); err != nil {
		log.Printf("Warning: Could not load cache file: %v", err)
	}

	// Start cleanup routine
	go processor.startCleanup()

	return processor, nil
}

func (p *ImageProcessor) startCleanup() {
	ticker := time.NewTicker(p.cleanupTick)
	for range ticker.C {
		p.cleanup()
	}
}

func (p *ImageProcessor) cleanup() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	now := time.Now()

	// Clean up in-memory cache and corresponding files
	for key, cache := range p.cache {
		if now.Sub(cache.CreatedAt) > p.expiration {
			os.Remove(cache.CachedPath)
			delete(p.cache, key)
		}
	}

	// Clean up orphaned files in cache directory
	entries, err := os.ReadDir(p.cacheDir)
	if err != nil {
		log.Printf("Failed to read cache directory: %v", err)
		return
	}

	for _, entry := range entries {
		// Skip the cache.json file
		if entry.Name() == filepath.Base(p.cacheFile) {
			continue
		}

		filePath := filepath.Join(p.cacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			log.Printf("Failed to get file info for %s: %v", filePath, err)
			continue
		}

		// Remove files older than expiration
		if now.Sub(info.ModTime()) > p.expiration {
			if err := os.Remove(filePath); err != nil {
				log.Printf("Failed to remove old cache file %s: %v", filePath, err)
			}
		}
	}

	// Save cache if enough time has passed
	if now.Sub(p.lastSaved) >= p.saveInterval {
		if err := p.saveCache(); err != nil {
			log.Printf("Failed to save cache: %v", err)
		}
	}
}

func (p *ImageProcessor) getCacheKey(path string, width int) string {
	dir := filepath.Dir(path)
	filename := filepath.Base(path)

	// Create FNV-1a hash of the directory path
	hasher := fnv.New64a()
	hasher.Write([]byte(dir))
	dirHash := strconv.FormatUint(hasher.Sum64(), 16)
	return fmt.Sprintf("%s_%s_%d", dirHash, filename, width)
}

func (p *ImageProcessor) saveCache() error {
	p.mutex.RLock()
	data, err := json.Marshal(p.cache)
	p.mutex.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	tempFile := p.cacheFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	if err := os.Rename(tempFile, p.cacheFile); err != nil {
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	p.lastSaved = time.Now()
	return nil
}

func (p *ImageProcessor) loadCache() error {
	data, err := os.ReadFile(p.cacheFile)
	if err != nil {
		return err
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	return json.Unmarshal(data, &p.cache)
}

func (p *ImageProcessor) startBackgroundResize(originalPath string, width int, cacheKey string) *ResizeJob {
	p.pendingMux.Lock()
	if job, exists := p.pendingResizes[cacheKey]; exists {
		p.pendingMux.Unlock()
		return job
	}

	job := &ResizeJob{
		OriginalPath: originalPath,
		Width:        width,
		CacheKey:     cacheKey,
	}
	p.pendingResizes[cacheKey] = job
	p.pendingMux.Unlock()

	go func() {
		// Wait for a resize slot
		p.resizeLimit <- struct{}{}
		defer func() { <-p.resizeLimit }()

		src, err := imaging.Open(originalPath)
		if err != nil {
			job.mutex.Lock()
			job.Error = err
			job.Done = true
			job.mutex.Unlock()
			return
		}

		bounds := src.Bounds()
		if width >= bounds.Dx() {
			job.mutex.Lock()
			job.Done = true
			job.mutex.Unlock()
			return
		}

		resized := imaging.Resize(src, width, 0, imaging.Lanczos)
		cachedPath := filepath.Join(p.cacheDir, cacheKey+filepath.Ext(originalPath))

		if err := imaging.Save(resized, cachedPath); err != nil {
			job.mutex.Lock()
			job.Error = err
			job.Done = true
			job.mutex.Unlock()
			return
		}

		p.mutex.Lock()
		p.cache[cacheKey] = ImageCache{
			OriginalPath: originalPath,
			CachedPath:   cachedPath,
			CreatedAt:    time.Now(),
		}
		p.mutex.Unlock()

		job.mutex.Lock()
		job.Done = true
		job.mutex.Unlock()

		// Cleanup after some time
		time.AfterFunc(time.Minute, func() {
			p.pendingMux.Lock()
			delete(p.pendingResizes, cacheKey)
			p.pendingMux.Unlock()
		})
	}()

	return job
}

// GetResizedImage retrieves the resized image path, performing the resize operation if necessary
func (p *ImageProcessor) GetResizedImage(ctx context.Context, originalPath string, width int) (string, bool, error) {
	if width == 0 {
		return originalPath, true, nil
	}

	cacheKey := p.getCacheKey(originalPath, width)
	cachedPath := filepath.Join(p.cacheDir, cacheKey+filepath.Ext(originalPath))

	// 1. First check in-memory cache
	p.mutex.RLock()
	if cache, exists := p.cache[cacheKey]; exists {
		p.mutex.RUnlock()
		if time.Since(cache.CreatedAt) < p.expiration {
			if _, err := os.Stat(cache.CachedPath); err == nil {
				return cache.CachedPath, true, nil
			}
			// File doesn't exist, remove from cache
			p.mutex.Lock()
			delete(p.cache, cacheKey)
			p.mutex.Unlock()
		}
	} else {
		p.mutex.RUnlock()
	}

	// 2. Check if file exists on disk (but wasn't in memory cache)
	if _, err := os.Stat(cachedPath); err == nil {
		p.mutex.Lock()
		p.cache[cacheKey] = ImageCache{
			OriginalPath: originalPath,
			CachedPath:   cachedPath,
			CreatedAt:    time.Now(),
		}
		p.mutex.Unlock()
		return cachedPath, true, nil
	}

	// 3. Try to acquire resize slot with timeout
	select {
	case p.resizeLimit <- struct{}{}:
		defer func() { <-p.resizeLimit }() // Release on return
		// Do immediate resize

		// 4. Do the actual resize
		src, err := imaging.Open(originalPath)
		if err != nil {
			return "", false, fmt.Errorf("failed to open image: %w", err)
		}

		bounds := src.Bounds()
		if width >= bounds.Dx() {
			return originalPath, true, nil
		}

		resized := imaging.Resize(src, width, 0, imaging.Lanczos)
		if err := imaging.Save(resized, cachedPath); err != nil {
			return "", false, fmt.Errorf("failed to save resized image: %w", err)
		}

		// 5. Add to cache
		p.mutex.Lock()
		p.cache[cacheKey] = ImageCache{
			OriginalPath: originalPath,
			CachedPath:   cachedPath,
			CreatedAt:    time.Now(),
		}
		p.mutex.Unlock()

		return cachedPath, true, nil
	default:
		// Start background resize
		job := p.startBackgroundResize(originalPath, width, cacheKey)

		// Check if it's already done
		job.mutex.RLock()
		if job.Done {
			job.mutex.RUnlock()
			if job.Error != nil {
				return "", false, job.Error
			}
			return cachedPath, true, nil
		}
		job.mutex.RUnlock()

		// Return original path and 429 status
		return originalPath, false, fmt.Errorf("too many concurrent resizes")
	}
}

func ServeHugo(config Config, db *sql.DB) error {
	expiration := time.Duration(config.ImageCacheExpirationMinutes) * time.Minute
	imageProcessor, err := NewImageProcessor(config.ImageCacheDir, expiration)
	if err != nil {
		return fmt.Errorf("failed to initialize image processor: %w", err)
	}

	http.Handle("/", http.FileServer(http.Dir(config.HugoOutDir)))
	http.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/images/"), "/", 2)
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}

		// Parse width parameter
		widthStr := r.URL.Query().Get("w")
		var width int
		if widthStr != "" {
			var err error
			width, err = strconv.Atoi(widthStr)
			if err != nil || width < 0 {
				http.Error(w, "Invalid width parameter", http.StatusBadRequest)
				return
			}
		}

		folderSHA, file := parts[0], parts[1]
		realFile, _ := url.QueryUnescape(file)
		realFolder := GetRealPath(db, folderSHA)

		if config.Verbose {
			log.Printf("Looking for %s/%s with width %d", realFolder, realFile, width)
		}
		if realFolder == "" {
			http.NotFound(w, r)
			return
		}

		imgPath := filepath.Join(realFolder, realFile)
		if _, err := os.Stat(imgPath); err != nil {
			http.NotFound(w, r)
			return
		}

		// Get resized or original image path
		servedPath, _, err := imageProcessor.GetResizedImage(r.Context(), imgPath, width)
		if err != nil {
			if strings.Contains(err.Error(), "too many concurrent resizes") {
				w.Header().Set("Retry-After", "5")
				http.Error(w, "Image is being processed, please try again later", http.StatusTooManyRequests)
				return
			}
			log.Printf("Error processing image: %v", err)
			http.Error(w, "Failed to process image", http.StatusInternalServerError)
			return
		}

		http.ServeFile(w, r, servedPath)
	})

	log.Printf("Serving Hugo site at http://localhost:%s/", config.ServerPort)
	log.Printf("Serving images from mapped folders at /images/{sha1}/...")
	return http.ListenAndServe(":"+config.ServerPort, nil)
}
