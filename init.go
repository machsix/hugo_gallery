package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"text/template"
)

// Worker input job
type folderJob struct {
	path string
}

func InitScanFolders(config Config, db *sql.DB, tmpl *template.Template) {
	log.Println("Initializing markdown posts by scanning watched folders (parallel)...")
	// 1. Gather all folders first
	var folders []string
	err := filepath.Walk(config.WatchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != config.WatchDir {
			folders = append(folders, path)
		}
		return nil
	})
	if err != nil {
		log.Printf("Error during folder initialization scan: %v", err)
		return
	}
	os.MkdirAll(filepath.Join(config.ContentDir, "tags"), 0755)

	// 2. Prepare worker pool
	numWorkers := runtime.NumCPU()
	jobs := make(chan folderJob, numWorkers)
	var wg sync.WaitGroup

	// 3. Worker function
	worker := func() {
		for job := range jobs {
			images := listImages(job.path, config.PhotoExts)
			videos := listImages(job.path, config.VideoExts)
			if len(images)+len(videos) < 4 {
				continue
			}
			folderSHA := sha1Hex(job.path)
			existingPath := GetRelPath(db, folderSHA)
			newNFile := len(images) + len(videos)
			nFile := GetNFile(db, folderSHA)
			if existingPath != "" && nFile == newNFile {
				continue // Already indexed
			}
			log.Printf("Folder used for post: %s (%d images + %d videos)", job.path, len(images), len(videos))
			if existingPath == "" {
				handleNewFolderWithTemplate(job.path, config, db, tmpl)
			} else {
				updatePost(db, job.path, images, videos, config, tmpl)
			}
		}
		wg.Done()
	}

	// 4. Start workers
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go worker()
	}

	// 5. Send jobs
	for _, path := range folders {
		jobs <- folderJob{path: path}
	}
	close(jobs)

	// 6. Wait for all to finish
	wg.Wait()
}
