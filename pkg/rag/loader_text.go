package rag

import "os"

// TextLoader is a generic loader for plain text files.
type TextLoader struct{}

func NewTextLoader() *TextLoader {
	return &TextLoader{}
}

func (l *TextLoader) Load(path string) (*Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Return document with empty images list
	return &Document{
		Text:   string(content),
		Images: nil,
	}, nil
}
