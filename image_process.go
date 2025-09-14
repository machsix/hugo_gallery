package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
)

func cache_image_hash(originalPath string, width int) string {
	dir := filepath.Dir(originalPath)
	dir_hash_hex := md5.Sum([]byte(dir))
	dir_hash := hex.EncodeToString(dir_hash_hex[:])[:16]

	file_name_without_ext := strings.TrimSuffix(filepath.Base(originalPath), filepath.Ext(originalPath))
	hash := fmt.Sprintf("%s_%s_%d", dir_hash, file_name_without_ext, width)
	return hash
}

func cache_image_path(originalPath string, cacheDir string, width int) string {
	if width <= 0 {
		return originalPath
	}
	hash := cache_image_hash(originalPath, width)
	ext := strings.ToLower(filepath.Ext(originalPath))
	return filepath.Join(cacheDir, fmt.Sprintf("%s%s", hash, ext))
}

type ImageProcessor struct {
	cacheDir      string
	resourceDir   string
	expiration    time.Duration
	maxConcurrent int
	processMux    sync.RWMutex    // protects cache operations
	jobSemaphore  chan struct{}   // limits total concurrent jobs
	activeJobs    map[string]*Job // tracks jobs by unique key
	jobsMux       sync.RWMutex    // protects activeJobs map
}

type Job struct {
	Done  chan struct{} // signals job completion
	Path  string        // resulting cached path
	Error error         // any error during processing
}

func NewImageProcessor(cacheDir, resourceDir string, expiration time.Duration, maxConcurrent int) *ImageProcessor {
	return &ImageProcessor{
		cacheDir:      cacheDir,
		resourceDir:   resourceDir,
		expiration:    expiration,
		maxConcurrent: maxConcurrent,
		jobSemaphore:  make(chan struct{}, maxConcurrent),
		activeJobs:    make(map[string]*Job),
	}
}

func (ip *ImageProcessor) ProcessImage(srcRelPath string, width int) (string, error) {
	srcPath := filepath.Join(ip.resourceDir, srcRelPath)
	if width <= 0 {
		return srcPath, nil
	}

	cachedPath := cache_image_path(srcRelPath, ip.cacheDir, width)

	// Quick check if already cached
	if _, err := os.Stat(cachedPath); err == nil {
		return cachedPath, nil
	}

	// Create unique job key
	jobKey := fmt.Sprintf("%s_%d", srcRelPath, width)

	// Check for existing job or create new one
	ip.jobsMux.Lock()
	job, exists := ip.activeJobs[jobKey]
	if exists {
		ip.jobsMux.Unlock()
		// Wait for existing job
		<-job.Done
		return job.Path, job.Error
	}

	// Create new job
	job = &Job{Done: make(chan struct{})}
	ip.activeJobs[jobKey] = job
	ip.jobsMux.Unlock()

	// Try to acquire processing slot immediately
	select {
	case ip.jobSemaphore <- struct{}{}:
		// Got slot immediately, process normally
	default:
		// No slot available, start background job and return 429
		go func() {
			// Wait for a slot
			ip.jobSemaphore <- struct{}{}
			defer func() { <-ip.jobSemaphore }()

			// Process image
			srcPath := filepath.Join(ip.resourceDir, srcRelPath)
			if err := ip.resizeImage(srcPath, cachedPath, width); err != nil {
				job.Error = err
			} else {
				job.Path = cachedPath
			}

			// Clean up
			close(job.Done)
			ip.jobsMux.Lock()
			delete(ip.activeJobs, jobKey)
			ip.jobsMux.Unlock()
		}()

		return srcPath, fmt.Errorf("too many concurrent resizes")
	}
	defer func() { <-ip.jobSemaphore }()

	// Process image immediately since we got a slot

	if err := ip.resizeImage(srcPath, cachedPath, width); err != nil {
		job.Error = err
		close(job.Done)
		return srcPath, err
	}

	job.Path = cachedPath
	close(job.Done)
	return cachedPath, nil
}

func (ip *ImageProcessor) resizeImage(srcPath, destPath string, width int) error {
	src, err := imaging.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source image: %w", err)
	}

	// Create cache directory if needed
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	dst := imaging.Resize(src, width, 0, imaging.Lanczos)
	if err := imaging.Save(dst, destPath); err != nil {
		return fmt.Errorf("failed to save resized image: %w", err)
	}

	return nil
}

func (ip *ImageProcessor) CleanCache() {
	// Use write lock to prevent concurrent processing
	ip.processMux.Lock()
	defer ip.processMux.Unlock()

	files, err := filepath.Glob(filepath.Join(ip.cacheDir, "*"))
	if err != nil {
		fmt.Printf("Error reading cache directory: %v\n", err)
		return
	}
	now := time.Now()
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			fmt.Printf("Error stating file %s: %v\n", file, err)
			continue
		}
		if now.Sub(info.ModTime()) > ip.expiration {
			err := os.Remove(file)
			if err != nil {
				fmt.Printf("Error removing file %s: %v\n", file, err)
			} else {
				fmt.Printf("Removed expired cache file: %s\n", file)
			}
		}
	}
}

func (ip *ImageProcessor) ServeProcessedImage(srcRelPath string, width int) (string, error) {
	return ip.ProcessImage(srcRelPath, width)
}

func (ip *ImageProcessor) StartCleanupRoutine(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			ip.CleanCache()
		}
	}()
}
