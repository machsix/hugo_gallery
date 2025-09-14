package main

import (
	"database/sql"
	"log"
	"os"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var dbMutex sync.Mutex

func InitDB(dbPath string) *sql.DB {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Error opening db: %v", err)
	}
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS posts (
		folder_sha TEXT PRIMARY KEY,
		post_filename TEXT,
		tags TEXT,
		rel_path TEXT,
		created_at TEXT,
    n_file INTEGER
	)`)
	if err != nil {
		log.Fatalf("Error creating table: %v", err)
	}

	// Add WAL mode for better concurrency
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		log.Printf("Warning: Could not enable WAL mode: %v", err)
	}

	// Set busy timeout
	_, err = db.Exec("PRAGMA busy_timeout = 5000")
	if err != nil {
		log.Printf("Warning: Could not set busy timeout: %v", err)
	}

	return db
}

func AddPost(db *sql.DB, folderSHA, postFile, tags, realPath string, nFile int) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT OR REPLACE INTO posts (folder_sha, post_filename, tags, rel_path, created_at, n_file) VALUES (?, ?, ?, ?, ?, ?)",
		folderSHA, postFile, tags, realPath, time.Now().Format(time.RFC3339), nFile,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func RemovePost(db *sql.DB, folderSHA string) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM posts WHERE folder_sha = ?", folderSHA)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func GetRelPath(db *sql.DB, folderSHA string) string {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	var relPath string
	row := db.QueryRow("SELECT rel_path FROM posts WHERE folder_sha = ?", folderSHA)
	row.Scan(&relPath)
	return relPath
}

func UpdateNFile(db *sql.DB, folderSHA string, realPath string, nFile int) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	info, _ := os.Stat(realPath)
	modTime := info.ModTime()
	_, err = tx.Exec(`
		UPDATE posts
		SET n_file = ?,
			created_at = ?
		WHERE folder_sha = ?`,
		nFile, modTime.Format(time.RFC3339), folderSHA)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func GetNFile(db *sql.DB, folderSHA string) int {
	var nFile int
	row := db.QueryRow("SELECT n_file FROM posts WHERE folder_sha = ?", folderSHA)
	row.Scan(&nFile)
	return nFile
}

// Load all mappings from SQLite
func LoadFolderMap(db *sql.DB) map[string]string {
	fmap := make(map[string]string)
	rows, err := db.Query("SELECT folder_sha, rel_path FROM posts")
	if err != nil {
		log.Println("Error loading folder map:", err)
		return fmap
	}
	defer rows.Close()
	for rows.Next() {
		var sha, path string
		if err := rows.Scan(&sha, &path); err == nil {
			fmap[sha] = path
		}
	}
	return fmap
}
