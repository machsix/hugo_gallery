package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/template"
	"time"
)

var folderMap = make(map[string]string)

func loadTemplate(templatePath string) *template.Template {
	t, err := template.New(filepath.Base(templatePath)).Funcs(template.FuncMap{
		"urlquery": template.URLQueryEscaper,
		"now":      func() string { return time.Now().Format("2006-01-02T15:04:05Z07:00") },
	}).ParseFiles(templatePath)
	if err != nil {
		log.Fatalf("Error loading template: %v", err)
	}
	return t
}

func main() {
	config := LoadConfig("config.ini")

	// Check if database needs initialization
	dbNeedsInit := true
	if _, err := os.Stat(config.SqlitePath); os.IsNotExist(err) {
		dbNeedsInit = true
	}

	db := InitDB(config.SqlitePath)
	defer db.Close()

	// Load template only once
	tmpl := loadTemplate(config.Archetype)

	// Initialization: scan folders and generate posts if DB is new
	if dbNeedsInit {
		log.Println("SQLite DB does not exist. Running initial scan of folders to create markdowns and DB records.")
		InitScanFolders(config, db, tmpl)
	}

	// Rebuild map from SQLite for image serving
	folderMap = LoadFolderMap(db)
	log.Printf("Loaded %d folder mappings from SQLite", len(folderMap))

	// Build Hugo site after markdowns are ready
	rebuildHugo(config)

	// Create image processor
	imageProcessor := NewImageProcessor(config.ImageCacheDir, config.ImageRoot, time.Duration(config.ImageCacheExpirationMinutes)*time.Minute, 5)

	// Initialize and start server and folder watcher
	go ServeHugo(config, imageProcessor, db)
	go WatchFolders(config, db, tmpl)

	// Start image cache cleanup routine
	imageProcessor.StartCleanupRoutine(time.Hour * 7 * 24)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down...")
}
