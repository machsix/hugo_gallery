package main

import (
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	config := LoadConfig("config.ini")
	db := InitDB(config.SqlitePath)
	defer db.Close()

	// Start Hugo server
	go ServeHugo(config.ServerPort, config.HugoOutDir)

	// Start watcher
	go WatchFolders(config, db)

	// Wait for exit signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down...")
}