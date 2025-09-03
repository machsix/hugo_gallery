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
	"sort"
	"strings"
	"sync"
  "text/template"
  "unicode/utf8"
  "regexp"
  "time"
   mapset "github.com/deckarep/golang-set/v2"

	"github.com/fsnotify/fsnotify"
  "github.com/yanyiwu/gojieba"
)

var (
	n_current int
	mu      sync.Mutex
)

func WatchFolders(config Config, db *sql.DB, tmpl *template.Template ) {
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
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
					// Delay to ensure the dir is fully created before adding watchers
					go func(path string) {
						info, err := os.Stat(path)
						if err != nil {
							log.Printf("Error stating %s: %v", path, err)
							return
						}

						if info.IsDir() {
              if config.Verbose {
  							log.Printf("[DEBUG] New directory detected: %s", path)
              }
							addWatchersRecursive(path)
							handleNewFolderWithTemplate(path, config, db, tmpl)
            }
            // else {
            //   folder := filepath.Dir(path)
            //   for _, ext := range exts {
            //     if strings.HasSuffix(strings.ToLower(info.Name()), ext) {
						//       log.Printf("Modified directory detected: %s", path)
            //       handleNewFolderWithTemplate(folder, config, db, tmpl)
            //       break
            //     }
            //   }
            // }

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


func handleNewFolderWithTemplate(path string, config Config, db *sql.DB, tmpl *template.Template) {
  idleThreshold := time.Duration(config.IdleSecond) * time.Second
	var lastCount int
	lastChange := time.Now()

	for {
		// Count number of files in the folder (non-recursive)
		files, err := os.ReadDir(path)
		if err != nil {
			log.Printf("Error reading folder %s: %v", path, err)
			return
		}

		count := len(files)
		if count != lastCount {
			lastCount = count
			lastChange = time.Now()
		}

		// If no changes for idleThreshold, break
		if time.Since(lastChange) >= idleThreshold {
      if config.Verbose {
        log.Printf("[DEBUG] Folder [%s] has %d files added", path, count)
      }
			break
		}
	}

	images := listImages(path, config.PhotoExts)
	videos := listImages(path, config.VideoExts)
	if len(images) + len(videos) < 4 {
		return
	}
  postname := filepath.Base(path)
	categories := getCategories(config.WatchDir, path)
  tags := getTags(categories, postname)
	folderSHA := sha1Hex(path)

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
  log.Printf("Generating post %s.md for %s", folderSHA, path)
	mdContent := generateMarkdownWithTemplate(tmpl, images, videos, filepath.Base(path), folderSHA, tags, date)
	err := os.WriteFile(postPath, []byte(mdContent), 0644)
	if err != nil {
		log.Println("Error writing markdown:", err)
		return
	}
  nFile := len(images) + len(videos)
	AddPost(db, folderSHA, postFile, strings.Join(categories, "/"), path, nFile)
	folderMap[folderSHA] = path
  rebuildHugo(config)
}

func updatePost(db *sql.DB, path string,images []string, videos []string, config Config, tmpl *template.Template) {
	folderSHA := sha1Hex(path)
  newNFile := len(images) + len(images)
	categories := getCategories(config.WatchDir, path)
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
		return imgs[i] < imgs[j] // simple ASCII sort
	})
	return imgs
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

func getTags(categories []string, postname string) [] string {
    filtered := make([]string, 0, len(categories))
    for _, c := range categories{
        if len(c) <= 8 && !strings.ContainsAny(c, " \t\n\r") {
            filtered = append(filtered, c)
        }
    }
    if utf8.RuneCountInString(postname) > 3 {
      jb := gojieba.NewJieba()
      jb.AddWord("夏夏子")
      defer jb.Free()
      words := jb.Cut(postname, true)
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
        !strings.ContainsAny(c, asciiSymbols)  {
          filtered = append(filtered, c)
        }
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
