package main

import (
    "bytes"
    "text/template"
    "log"
    "path/filepath"
    "time"
    "net/url"
)

type MarkdownData struct {
    FolderName string
    FolderSHA  string
    ImagesURL  []string
    Images     []string
    VideosURL  []string
    Videos     []string
    Tags []string
    Date string
}

func generateMarkdownWithTemplate(tmpl *template.Template, images []string, videos []string, folderName, folderSHA string, tags []string, date time.Time) string {
  encodedVideos := make([]string, len(videos))
  encodedImages := make([]string, len(images))
  for i, v := range videos {
    encodedVideos[i] = url.QueryEscape(v)
  }
  for i, v := range images {
    encodedImages[i] = url.QueryEscape(v)
  }
	data := MarkdownData{
    FolderName: folderName,
    FolderSHA:  folderSHA,
    ImagesURL:     encodedImages,
    Images: images,
    VideosURL:     encodedVideos,
    Videos: videos,
    Tags: tags,
    Date: date.Format("2006-01-02T15:04:05-07:00"),
	}
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, filepath.Base(tmpl.Name()), data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		return ""
	}
	return buf.String()
}
