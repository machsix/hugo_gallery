package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

func WatchFolders(config Config, db *sql.DB) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	var wg sync.WaitGroup

	addWatchersRecursive := func(dir string) {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if err := watcher.Add(path); err != nil {
					log.Printf("Failed to watch %s: %v", path, err)
				}
			}
			return nil
		})
	}

	addWatchersRecursive(config.WatchDir)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Folder created
				if event.Op&fsnotify.Create == fsnotify.Create {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						addWatchersRecursive(event.Name)
						handleNewFolder(event.Name, config, db)
					}
				}
				// Folder deleted
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					if _, err := os.Stat(event.Name); os.IsNotExist(err) {
						handleDeletedFolder(event.Name, config, db)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("Watcher error:", err)
			}
		}
	}()
	wg.Wait()
}

// Handle new folder creation
func handleNewFolder(path string, config Config, db *sql.DB) {
	images := listImages(path, config.PhotoExts)
	if len(images) < 4 {
		return
	}
	categories := getCategories(config.WatchDir, path)
	folderSHA := sha1Hex(path)
	postFile := folderSHA + ".md"
	postDir := filepath.Join(config.ContentDir, filepath.Join(categories...))
	postPath := filepath.Join(postDir, postFile)
	if err := os.MkdirAll(postDir, 0755); err != nil {
		log.Printf("Error creating post directory: %v", err)
		return
	}
	mdContent := generateMarkdown(config.Archetype, images, path, categories)
	err := os.WriteFile(postPath, []byte(mdContent), 0644)
	if err != nil {
		log.Println("Error writing markdown:", err)
		return
	}
	AddPost(db, folderSHA, postFile, strings.Join(categories, "/"))
	rebuildHugo(config)
}

// Handle folder deletion
func handleDeletedFolder(path string, config Config, db *sql.DB) {
	folderSHA := sha1Hex(path)
	var postFile, category string
	row := db.QueryRow("SELECT post_filename, category FROM posts WHERE folder_sha = ?", folderSHA)
	row.Scan(&postFile, &category)
	if postFile != "" && category != "" {
		postPath := filepath.Join(config.ContentDir, category, postFile)
		os.Remove(postPath)
		RemovePost(db, folderSHA)
		rebuildHugo(config)
	}
}

// List images in folder sorted human-friendly
func listImages(folder string, exts []string) []string {
	entries, _ := os.ReadDir(folder)
	var imgs []string
	for _, e := range entries {
		if !e.IsDir() {
			for _, ext := range exts {
				if strings.HasSuffix(strings.ToLower(e.Name()), ext) {
					imgs = append(imgs, e.Name())
				}
			}
		}
	}
	sort.Slice(imgs, func(i, j int) bool {
		return naturalLess(imgs[i], imgs[j])
	})
	return imgs
}

// Helper for human natural sorting (simple, replace with better version if desired)
func naturalLess(a, b string) bool {
	return a < b // Replace with more sophisticated natural sort if needed
}

func sha1Hex(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func getCategories(root, folder string) []string {
	rel, _ := filepath.Rel(root, folder)
	rel = filepath.Dir(rel)
	if rel == "." || rel == "" {
		return []string{}
	}
	return strings.Split(rel, string(os.PathSeparator))
}

func rebuildHugo(config Config) {
	cmd := exec.Command(config.HugoPath, "--source", ".", "--destination", config.HugoOutDir)
	cmd.Run()
}