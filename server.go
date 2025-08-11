package main

import (
	"log"
	"net/http"
)

func ServeHugo(port, outDir string) {
	fs := http.FileServer(http.Dir(outDir))
	http.Handle("/", fs)
	log.Printf("Serving Hugo site at http://localhost:%s/", port)
	http.ListenAndServe(":"+port, nil)
}