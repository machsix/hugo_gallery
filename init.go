package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Worker input job
type folderJob struct {
	path string
}

func InitScanFolders(config Config, db *sql.DB, tmpl *template.Template) {
	log.Println("Initializing markdown posts by scanning watched folders...")

	// 1. Use a buffered channel for folder discovery
	folderChan := make(chan string, 1000)
	errChan := make(chan error, 1)

	// Start async folder discovery
	go func() {
		defer close(folderChan)
		err := filepath.Walk(config.WatchDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() && path != config.WatchDir {
				folderChan <- path
			}
			return nil
		})
		if err != nil {
			errChan <- err
		}
	}()

	os.MkdirAll(filepath.Join(config.ContentDir, "tags"), 0755)

	// 2. Prepare worker pool with fewer workers
	numWorkers := runtime.NumCPU() // Reduced from NumCPU()*5
	jobs := make(chan folderJob, numWorkers*2)
	var wg sync.WaitGroup

	// 3. Add DB transaction support
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v", err)
		return
	}
	defer tx.Rollback()

	// 4. Worker function with batched DB operations
	worker := func(id int) {
		// Prepare reusable slices to avoid allocations
		images := make([]string, 0, 100)
		videos := make([]string, 0, 10)

		for job := range jobs {
			start := time.Now()

			// Quick check if folder needs processing
			folderSHA := sha1Hex(job.path)
			existingPath := GetRelPath(db, folderSHA)

			// Do single directory read instead of separate scans
			entries, err := os.ReadDir(job.path)
			if err != nil {
				log.Printf("[Worker %d] Error reading directory: %v", id, err)
				continue
			}

			// Reset slices without allocation
			images = images[:0]
			videos = videos[:0]

			// Single pass file counting and classification
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				ext := strings.ToLower(filepath.Ext(name))

				// Classify files in single pass
				switch {
				case isInSlice(ext, config.PhotoExts):
					images = append(images, name)
				case isInSlice(ext, config.VideoExts):
					videos = append(videos, name)
				}
			}

			totalFiles := len(images) + len(videos)

			if existingPath != "" {
				nFile := GetNFile(db, folderSHA)
				if nFile == totalFiles {
					continue
				}
			}

			log.Printf("[Worker %d] Processing: %s (%d files, took %v)",
				id, job.path, totalFiles, time.Since(start))

			if existingPath == "" {
				handleNewFolderWithTemplate(job.path, config, db, tmpl, false, images, videos)
			} else {
				updatePost(db, job.path, images, videos, config, tmpl)
			}
		}
		wg.Done()
	}

	// 5. Start workers
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go worker(i)
	}

	// 6. Process folders as they're discovered
	for path := range folderChan {
		jobs <- folderJob{path: path}
	}

	// Check for folder discovery errors
	select {
	case err := <-errChan:
		log.Printf("Error during folder scan: %v", err)
	default:
	}

	close(jobs)
	wg.Wait()

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v", err)
	}
}

// Helper function to check if item is in slice
func isInSlice(item string, slice []string) bool {
	for _, s := range slice {
		if item == s {
			return true
		}
	}
	return false
}
