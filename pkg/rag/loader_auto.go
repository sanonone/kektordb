package rag

import (
	"path/filepath"
	"strings"
)

// AutoLoader automatically selects the correct loader based on file extension.
type AutoLoader struct {
	textLoader Loader
	pdfLoader  Loader
	docxLoader Loader
}

func NewAutoLoader() *AutoLoader {
	return &AutoLoader{
		textLoader: NewTextLoader(),
		pdfLoader:  NewPDFLoader(),
		docxLoader: NewDocxLoader(),
	}
}

func (l *AutoLoader) Load(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".pdf":
		return l.pdfLoader.Load(path)
	case ".docx":
		return l.docxLoader.Load(path)
	// List of known text extensions
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".go", ".py", ".js", ".ts", ".html", ".css", ".csv":
		return l.textLoader.Load(path)
	default:
		// Fallback: try reading it as text.
		// If it is binary (e.g. image), os.ReadFile will read garbage,
		// but for now is better than failing.
		// Alternative: return error for unknown extensions.
		// return "", fmt.Errorf("unsupported file extension: %s", ext)
		return l.textLoader.Load(path)
	}
}
