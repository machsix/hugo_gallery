package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode/utf8"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/fsnotify/fsnotify"
	"github.com/yanyiwu/gojieba"
)

var (
	n_current      int
	mu             sync.Mutex
	jiebaSingleton *gojieba.Jieba
	jiebaOnce      sync.Once
)

func WatchFolders(config Config, db *sql.DB, tmpl *template.Template) {
	watcher, err := fsnotify.NewWatcher()
	watched_folder := mapset.NewSet[string]()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	var wg sync.WaitGroup

	addWatchersRecursive := func(dir string) {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Printf("WalkDir error on %s: %v", path, err)
				return nil // continue walking
			}
			if d.IsDir() {
				if watched_folder.Contains(path) {
					return nil
				}
				if err := watcher.Add(path); err != nil {
					log.Printf("Failed to watch %s: %v", path, err)
				} else {
					log.Printf("Watching: %s", path)
				}
			}
			return nil
		})
	}

	n_current = 0
	addWatchersRecursive(config.WatchDir)
	// exts := append(config.PhotoExts, config.VideoExts...)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Handle rename/move events specially
				if event.Op&fsnotify.Rename != 0 {
					// For renames, handle the deletion of old path
					log.Printf("[DEBUG] Rename detected: %s", event.Name)
					handleDeletedFolder(event.Name, config, db)

					// Give the OS time to complete the rename
					time.Sleep(100 * time.Millisecond)
					go func() {
						time.Sleep(time.Minute)
						houseKeeping(config, db)
						rebuildHugo(config)
					}()
				} else if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
					// Handle normal create/write events
					go func(path string) {
						info, err := os.Stat(path)
						if err != nil {
							if !os.IsNotExist(err) {
								log.Printf("Error stating %s: %v", path, err)
							}
							return
						}

						if info.IsDir() {
							if config.Verbose {
								log.Printf("[DEBUG] New directory detected: %s", path)
							}
							addWatchersRecursive(path)
							handleNewFolderWithTemplate(path, config, db, tmpl, true, nil, nil)
						}
					}(event.Name)
				}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					if _, err := os.Stat(event.Name); os.IsNotExist(err) {
						log.Printf("Deletion of directory detected: %s", event.Name)
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

func handleNewFolderWithTemplate(path string, config Config, db *sql.DB, tmpl *template.Template, rebuild bool, images []string, videos []string) {
	rel_path, err := filepath.Rel(config.WatchDir, path)
	if err != nil {
		log.Printf("Error getting relative path: %v", err)
		return
	}

	// Single directory scan
	files, err := os.ReadDir(path)
	if err != nil {
		log.Printf("Error reading folder %s: %v", path, err)
		return
	}

	// Process files in one pass
	if len(images) == 0 {
		images = make([]string, 0, len(files))
		videos = make([]string, 0, len(files))
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			name := file.Name()
			ext := strings.ToLower(filepath.Ext(name))

			if isInSlice(ext, config.PhotoExts) {
				images = append(images, name)
			} else if isInSlice(ext, config.VideoExts) {
				videos = append(videos, name)
			}
		}

	}

	totalFiles := len(images) + len(videos)
	if totalFiles == 0 {
		log.Printf("No media files found in %s, skipping.", path)
		return
	}

	postname := filepath.Base(path)
	categories := getCategories(rel_path)
	tags := getTags(categories, postname)
	folderSHA := sha1Hex(path)

	postFile := folderSHA + ".md"
	postDir := filepath.Join(config.ContentDir, "post")
	postPath := filepath.Join(postDir, postFile)

	if err := os.MkdirAll(postDir, 0755); err != nil {
		log.Printf("Error creating post directory: %v", err)
		return
	}

	// Use file stat directly instead of separate call
	fileInfo, err := os.Stat(path)
	date := time.Now()
	// set date to folder mod time if available
	if err == nil {
		date = fileInfo.ModTime()
	}

	log.Printf("Generating post %s.md for %s", folderSHA, path)
	mdContent := generateMarkdownWithTemplate(tmpl, images, videos, postname, folderSHA, tags, date)

	if err := os.WriteFile(postPath, []byte(mdContent), 0644); err != nil {
		log.Printf("Error writing markdown: %v", err)
		return
	}

	AddPost(db, folderSHA, postFile, strings.Join(categories, "/"), rel_path, totalFiles)
	folderMap[folderSHA] = path

	if rebuild {
		rebuildHugo(config)
	}
}

func updatePost(db *sql.DB, path string, images []string, videos []string, config Config, tmpl *template.Template) {
	folderSHA := sha1Hex(path)
	newNFile := len(images) + len(images)
	rel_path, _ := filepath.Rel(config.WatchDir, path)
	categories := getCategories(rel_path)
	postname := filepath.Base(path)
	tags := getTags(categories, postname)
	postFile := folderSHA + ".md"
	// postDir := filepath.Join(config.ContentDir, filepath.Join(categories...))
	postDir := filepath.Join(config.ContentDir, "post")
	postPath := filepath.Join(postDir, postFile)
	if err := os.MkdirAll(postDir, 0755); err != nil {
		log.Printf("Error creating post directory: %v", err)
		return
	}

	date := time.Now()
	{
		info, err := os.Stat(path)
		if err == nil {
			date = info.ModTime()
		}
	}

	UpdateNFile(db, folderSHA, path, newNFile)

	if newNFile == 0 {
		os.Remove(postPath)
		RemovePost(db, folderSHA)
		log.Printf("No media files left in %s, removed post and database record.", path)
		return
	}
	mdContent := generateMarkdownWithTemplate(tmpl, images, videos, filepath.Base(path), folderSHA, tags, date)
	err := os.WriteFile(postPath, []byte(mdContent), 0644)
	if err != nil {
		log.Println("Error writing markdown:", err)
		return
	}
}

// Handle folder deletion
func handleDeletedFolder(path string, config Config, db *sql.DB) {
	folderSHA := sha1Hex(path)
	var postFile, category string
	row := db.QueryRow("SELECT post_filename, category FROM posts WHERE folder_sha = ?", folderSHA)
	row.Scan(&postFile, &category)
	delete(folderMap, folderSHA)
	postPath := filepath.Join(config.ContentDir, "post", postFile)
	// check if file exists before removing
	if postFile != "" {
		if _, err := os.Stat(postPath); err == nil {
			log.Printf("[DEBUG] Removing post file: %s", postPath)
			os.Remove(postPath)
		} else {
			log.Printf("[DEBUG] Post file %s does not exist, skipping removal.", postPath)
		}
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
		return imgs[i] < imgs[j] // simple ASCII sort
	})
	return imgs
}

func sha1Hex(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func getCategories(rel string) []string {
	rel = filepath.Dir(rel)
	if rel == "." || rel == "" {
		return []string{}
	}
	return strings.Split(rel, string(os.PathSeparator))
}

// Get or create Jieba instance
func getJieba() *gojieba.Jieba {
	jiebaOnce.Do(func() {
		jiebaSingleton = gojieba.NewJieba()
		jiebaSingleton.AddWord("夏夏子")
	})
	return jiebaSingleton
}

func getTags(categories []string, postname string) []string {
	filtered := make([]string, 0, len(categories))
	for _, c := range categories {
		if len(c) <= 20 && !strings.ContainsAny(c, " \t\n\r") {
			filtered = append(filtered, c)
		}
	}

	if utf8.RuneCountInString(postname) > 3 {
		jb := getJieba() // Use singleton instance
		words := jb.Cut(postname, true)
		// log.Printf("Jieba cut for %s: %v", postname, strings.Join(words, "/"))

		asciiSymbols := `!"#$%&'()*+,-./:;<=>?@[\]^_{|}~`
		reStartWithNumber := regexp.MustCompile(`^P?\d+V?`)
		reStartWithPart := regexp.MustCompile(`^part`)
		skipWords := []string{"MB", "GB", "作品", "写真", "写真集", "原创", "原創", "订阅"}
		skipSet := make(map[string]struct{}, len(skipWords))
		for _, w := range skipWords {
			skipSet[w] = struct{}{}
		}
		for _, c := range words {
			if _, skip := skipSet[c]; skip {
				continue
			}
			if reStartWithNumber.MatchString(c) || reStartWithPart.MatchString(c) {
				continue
			}
			if utf8.RuneCountInString(c) > 1 &&
				!strings.ContainsAny(c, " []()\t\n\r") &&
				!strings.ContainsAny(c, asciiSymbols) {
				filtered = append(filtered, c)
			}
		}
	}
	// log.Printf("Tags for %s: %v", postname, strings.Join(filtered, "/"))
	// remove duplicates keeping order
	seen := make(map[string]struct{}, len(filtered))
	result := make([]string, 0, len(filtered))
	for _, tag := range filtered {
		if _, ok := seen[tag]; !ok {
			seen[tag] = struct{}{}
			result = append(result, tag)
		}
	}

	return filtered
}

func rebuildHugo(config Config) {
	mu.Lock()
	n_current++
	my := n_current
	mu.Unlock()

	if my != 1 {
		mu.Lock()
		n_current--
		mu.Unlock()
	} else {
		for {
			mu.Lock()
			if n_current <= 1 {
				mu.Unlock()
				break
			}
			mu.Unlock()
			time.Sleep(5 * time.Second)
		}
		log.Printf("Start building at %v", time.Now())
		cmd := exec.Command(config.HugoPath, "--source", ".", "--destination", config.HugoOutDir)
		cmd.Run()

		mu.Lock()
		n_current--
		mu.Unlock()
	}

}

func cleanupJieba() {
	if jiebaSingleton != nil {
		jiebaSingleton.Free()
	}
}

func houseKeeping(config Config, db *sql.DB) {
	// Initialize the map
	records := make(map[string]string)

	rows, err := db.Query("SELECT folder_sha, rel_path FROM posts")
	if err != nil {
		log.Printf("Error querying posts: %v", err)
		return
	}
	defer rows.Close()

	// Populate the map
	for rows.Next() {
		var postID, relPath string
		if err := rows.Scan(&postID, &relPath); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		absPath := filepath.Join(config.WatchDir, relPath)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			// folder does not exist, remove from db
			log.Printf("Folder %s does not exist, removing from db", absPath)
			err := RemovePost(db, postID)
			if err != nil {
				log.Printf("Error removing post %s: %v", postID, err)
			}
		} else {
			records[postID] = relPath
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("Row iteration error: %v", err)
		return
	}

	// Delete orphaned post files
	postDir := filepath.Join(config.ContentDir, "post")
	err = filepath.Walk(postDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error walking path %s: %v", path, err)
			return nil
		}
		if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			postID := strings.TrimSuffix(info.Name(), ".md")
			if _, exists := records[postID]; !exists {
				// post_id not in db, delete the file
				log.Printf("Removing orphaned post file: %s", path)
				os.Remove(path)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("Error walking post directory: %v", err)
	}
}

func startHouseKeeping(config Config, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			log.Println("Starting housekeeping...")
			houseKeeping(config, db)
			log.Println("Housekeeping completed.")
		}
	}()
}
