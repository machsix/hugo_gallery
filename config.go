package main

import (
	"log"

	"gopkg.in/ini.v1"
)

type Config struct {
	WatchDir                    string   // Directory of photos/videos to watch
	ImageRoot                   string   // Root directory for image URLs
	ImageCacheDir               string   // Directory to store cached resized images
	ImageCacheExpirationMinutes int      // Minutes before cached images expire
	HugoOutDir                  string   // Directory where Hugo outputs the static site
	PhotoExts                   []string // Supported photo file extensions
	VideoExts                   []string // Supported video file extensions
	ServerPort                  string   // Port for the HTTP server
	SqlitePath                  string   // Path to the SQLite database file
	HugoPath                    string   // Path to the Hugo binary
	Archetype                   string   // Path to the Hugo archetype template
	ContentDir                  string   // Path to the Hugo content directory relative to HugoOutDir
	Verbose                     bool     // Verbose logging
	IdleSecond                  int      // Seconds to wait for folder to be idle before processing new files
}

func LoadConfig(path string) Config {
	cfg, err := ini.Load(path)
	if err != nil {
		log.Fatalf("Fail to read file: %v", err)
	}
	return Config{
		WatchDir:                    cfg.Section("main").Key("watched_folder").String(),
		ImageRoot:                   cfg.Section("main").Key("watched_folder").String(),
		ImageCacheDir:               cfg.Section("main").Key("image_cache_folder").String(),
		ImageCacheExpirationMinutes: cfg.Section("main").Key("image_cache_expiration_minutes").MustInt(60),
		HugoOutDir:                  cfg.Section("main").Key("hugo_built_out_folder").String(),
		PhotoExts:                   cfg.Section("main").Key("photo_extensions").Strings(","),
		VideoExts:                   cfg.Section("main").Key("video_extensions").Strings(","),
		ServerPort:                  cfg.Section("main").Key("http_port").MustString("8080"),
		SqlitePath:                  cfg.Section("main").Key("sqlite_db_path").String(),
		HugoPath:                    cfg.Section("main").Key("hugo_bin_path").String(),
		Archetype:                   cfg.Section("main").Key("hugo_archetype").String(),
		ContentDir:                  cfg.Section("main").Key("hugo_content_dir").MustString("content"),
		Verbose:                     cfg.Section("main").Key("verbose").MustBool(false),
		IdleSecond:                  cfg.Section("main").Key("idle_second").MustInt(10),
	}
}
