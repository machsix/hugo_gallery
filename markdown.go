package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"path/filepath"
)

type MarkdownData struct {
	FolderName string
	Images     []string
	Categories []string
}

// templatePath: path to Hugo archetype/template file
// images: list of image file paths (relative to Hugo content)
// folderName: name of the folder
// categories: slice of category strings (multi-level)
func generateMarkdown(templatePath string, images []string, folderName string, categories []string) string {
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		return ""
	}
	data := MarkdownData{
		FolderName: filepath.Base(folderName),
		Images:     images,
		Categories: categories,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Error executing template: %v", err)
		return ""
	}
	return buf.String()
}