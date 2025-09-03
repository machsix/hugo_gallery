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
	"golang.org/x/sync/semaphore"
)

// ImageCache represents cached image metadata
type ImageCache struct {
	OriginalPath string
	CachedPath   string
	CreatedAt    time.Time
}

// ImageProcessor handles image resizing and caching operations
type ImageProcessor struct {
	cacheDir        string
	expiration      time.Duration
	cache           map[string]ImageCache
	mutex           sync.RWMutex
	cleanupTick     time.Duration
	cacheFile       string
	lastSaved       time.Time
	saveInterval    time.Duration
	resizeSemaphore *semaphore.Weighted
	maxConcurrent   int64
}

// NewImageProcessor creates a new ImageProcessor instance
func NewImageProcessor(cacheDir string, expiration time.Duration) (*ImageProcessor, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	const maxConcurrentResizes = 10 // Limit concurrent resizes

	processor := &ImageProcessor{
		cacheDir:        cacheDir,
		expiration:      expiration,
		cache:           make(map[string]ImageCache),
		cleanupTick:     10 * time.Minute,
		cacheFile:       path.Join(cacheDir, "cache.json"),
		saveInterval:    10 * time.Minute,
		lastSaved:       time.Now(),
		resizeSemaphore: semaphore.NewWeighted(maxConcurrentResizes),
		maxConcurrent:   maxConcurrentResizes,
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

func (p *ImageProcessor) GetResizedImage(originalPath string, width int) (string, error) {
	if width == 0 {
		return originalPath, nil
	}

	cacheKey := p.getCacheKey(originalPath, width)

	// Check cache first
	p.mutex.RLock()
	if cache, exists := p.cache[cacheKey]; exists {
		p.mutex.RUnlock()
		if time.Since(cache.CreatedAt) < p.expiration {
			// Verify the cached file still exists
			if _, err := os.Stat(cache.CachedPath); err == nil {
				return cache.CachedPath, nil
			}
			// File doesn't exist, remove from cache
			p.mutex.Lock()
			delete(p.cache, cacheKey)
			p.mutex.Unlock()
		}
	} else {
		p.mutex.RUnlock()
	}

	// Check if resized file already exists
	cachedPath := filepath.Join(p.cacheDir, cacheKey+filepath.Ext(originalPath))
	if _, err := os.Stat(cachedPath); err == nil {
		// File exists, add to cache and return
		p.mutex.Lock()
		p.cache[cacheKey] = ImageCache{
			OriginalPath: originalPath,
			CachedPath:   cachedPath,
			CreatedAt:    time.Now(),
		}
		p.mutex.Unlock()
		return cachedPath, nil
	}

	// Load original image before acquiring semaphore
	src, err := imaging.Open(originalPath)
	if err != nil {
		return "", fmt.Errorf("failed to open image: %w", err)
	}

	// Check dimensions before acquiring semaphore
	bounds := src.Bounds()
	if width >= bounds.Dx() {
		return originalPath, nil
	}

	// Only acquire semaphore when we actually need to resize
	ctx := context.Background()
	if err := p.resizeSemaphore.Acquire(ctx, 1); err != nil {
		return "", fmt.Errorf("failed to acquire resize semaphore: %w", err)
	}
	defer p.resizeSemaphore.Release(1)

	// Resize image maintaining aspect ratio
	resized := imaging.Resize(src, width, 0, imaging.Lanczos)

	// Save resized image
	if err := imaging.Save(resized, cachedPath); err != nil {
		return "", fmt.Errorf("failed to save resized image: %w", err)
	}

	// Store in cache
	p.mutex.Lock()
	p.cache[cacheKey] = ImageCache{
		OriginalPath: originalPath,
		CachedPath:   cachedPath,
		CreatedAt:    time.Now(),
	}
	p.mutex.Unlock()

	return cachedPath, nil
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
		servedPath, err := imageProcessor.GetResizedImage(imgPath, width)
		if err != nil {
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
