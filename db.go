package main

import (
	"database/sql"
	"log"
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
		category TEXT,
		created_at TEXT
	)`)
	if err != nil {
		log.Fatalf("Error creating table: %v", err)
	}
	return db
}

func AddPost(db *sql.DB, folderSHA, postFile, category string) error {
	_, err := db.Exec(
		"INSERT INTO posts (folder_sha, post_filename, category, created_at) VALUES (?, ?, ?, ?)",
		folderSHA, postFile, category, time.Now().Format(time.RFC3339),
	)
	return err
}

func RemovePost(db *sql.DB, folderSHA string) error {
	_, err := db.Exec("DELETE FROM posts WHERE folder_sha = ?", folderSHA)
	return err
}