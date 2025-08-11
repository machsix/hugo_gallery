package main

import (
	"log"
	"strings"

	"gopkg.in/ini.v1"
)

type Config struct {
	WatchDir    string
	HugoOutDir  string
	PhotoExts   []string
	ServerPort  string
	SqlitePath  string
	HugoPath    string
	Archetype   string
	ContentDir  string
}

func LoadConfig(path string) Config {
	cfg, err := ini.Load(path)
	if err != nil {
		log.Fatalf("Fail to read file: %v", err)
	}
	return Config{
		WatchDir:    cfg.Section("main").Key("watched_folder").String(),
		HugoOutDir:  cfg.Section("main").Key("hugo_built_out_folder").String(),
		PhotoExts:   cfg.Section("main").Key("photo_extensions").Strings(","),
		ServerPort:  cfg.Section("main").Key("http_port").String(),
		SqlitePath:  cfg.Section("main").Key("sqlite_db_path").String(),
		HugoPath:    cfg.Section("main").Key("hugo_bin_path").String(),
		Archetype:   cfg.Section("main").Key("hugo_archetype").String(),
		ContentDir:  cfg.Section("main").Key("hugo_content_dir").MustString("content"),
	}
}