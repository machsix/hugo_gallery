package main

import (
	"database/sql"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

func ServeHugo(config Config, imageProcessor *ImageProcessor, db *sql.DB) error {
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
		fileName, _ := url.QueryUnescape(file)
		fileDir := GetRelPath(db, folderSHA)
		relPath := filepath.Join(fileDir, fileName)
		servedPath := filepath.Join(config.WatchDir, relPath)
		fileExt := strings.ToLower(filepath.Ext(fileName))

		for _, ext := range config.PhotoExts {
			if fileExt == ext {
				var err error
				servedPath, err = imageProcessor.ProcessImage(relPath, width)
				if err != nil {
					if strings.Contains(err.Error(), "short Huffman data") {
						break // Corrupted JPEG, serve original
					}
					if strings.Contains(err.Error(), "too many concurrent resizes") {
						w.Header().Set("Retry-After", "5")
						http.Error(w, "Server busy, try again later", http.StatusTooManyRequests)
					} else {
						http.Error(w, "Error processing image", http.StatusInternalServerError)
					}
					log.Printf("[ERROR] Image processing error: %v", err)
					return
				}
				break
			}
		}

		if config.Verbose {
			log.Printf("[DEBUG] Serving image: %s (width=%d) -> %s", r.URL.Path, width, servedPath)
		}

		http.ServeFile(w, r, servedPath)
	})

	log.Printf("Serving Hugo site at http://localhost:%s/", config.ServerPort)
	log.Printf("Serving images from mapped folders at /images/{sha1}/...")
	return http.ListenAndServe(":"+config.ServerPort, nil)
}
