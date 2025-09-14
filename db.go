package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

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
	return db
}

func AddPost(db *sql.DB, folderSHA, postFile, tags, realPath string, nFile int) error {
	// info, _ := os.Stat(realPath)
	// modTime := info.ModTime()
	_, err := db.Exec(
		"INSERT OR REPLACE INTO posts (folder_sha, post_filename, tags, rel_path, created_at, n_file) VALUES (?, ?, ?, ?, ?, ?)",
		folderSHA, postFile, tags, realPath, time.Now().Format(time.RFC3339), nFile,
	)
	return err
}

func RemovePost(db *sql.DB, folderSHA string) error {
	_, err := db.Exec("DELETE FROM posts WHERE folder_sha = ?", folderSHA)
	return err
}

func GetRelPath(db *sql.DB, folderSHA string) string {
	var relPath string
	row := db.QueryRow("SELECT rel_path FROM posts WHERE folder_sha = ?", folderSHA)
	row.Scan(&relPath)
	return relPath
}

func UpdateNFile(db *sql.DB, folderSHA string, realPath string, nFile int) error {
	info, _ := os.Stat(realPath)
	modTime := info.ModTime()
	_, err := db.Exec(`
		UPDATE posts
		SET n_file = ?
    SET created_at = ?
		WHERE folder_sha = ?
	`, nFile, modTime.Format(time.RFC3339), folderSHA)
	return err
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
