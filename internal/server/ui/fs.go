package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var content embed.FS

// GetHandler returns an http.Handler that serves the static UI files.
// It automatically strips the "static" prefix from the embedded filesystem.
func GetHandler() http.Handler {
	// Subdir creates a view of the filesystem starting at "static".
	// This means we can access "index.html" directly instead of "static/index.html".
	fsys, err := fs.Sub(content, "static")
	if err != nil {
		panic(err) // Should never happen with embed
	}
	return http.FileServer(http.FS(fsys))
}
