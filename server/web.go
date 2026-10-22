package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed web
var webFiles embed.FS

// webRoutes serves the page and the Material Icons font it uses.
func webRoutes(mux *http.ServeMux) {
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// The files are built into the binary, so always check for a newer version.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /assets/MaterialIcons-Regular.ttf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "font/ttf")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		if _, err := w.Write(materialIconsTTF); err != nil {
			log.Printf("write font: %v", err)
		}
	})
}
