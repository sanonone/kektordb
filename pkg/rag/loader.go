package rag

import (
	"os"
)

// Loader defines the contract for reading a file and extracting its text content.
type Loader interface {
	// Load reads the file at the given path and returns its text content.
	Load(path string) (string, error)
}

// TextLoader is a generic loader for plain text files (txt, md, code, json).
type TextLoader struct{}

func NewTextLoader() *TextLoader {
	return &TextLoader{}
}

func (l *TextLoader) Load(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
