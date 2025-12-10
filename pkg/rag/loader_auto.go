package rag

import (
	"path/filepath"
	"strings"
)

// AutoLoader automatically selects the correct loader based on file extension.
type AutoLoader struct {
	textLoader Loader
	pdfLoader  Loader
}

func NewAutoLoader() *AutoLoader {
	return &AutoLoader{
		textLoader: NewTextLoader(),
		pdfLoader:  NewPDFLoader(),
	}
}

func (l *AutoLoader) Load(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".pdf":
		return l.pdfLoader.Load(path)
	// Lista estensioni testuali note
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".go", ".py", ".js", ".ts", ".html", ".css", ".csv":
		return l.textLoader.Load(path)
	default:
		// Fallback: proviamo a leggerlo come testo.
		// Se è un binario (es. immagine), os.ReadFile leggerà spazzatura,
		// ma per ora è meglio che fallire.
		// Alternativa: ritornare errore per estensioni sconosciute.
		// return "", fmt.Errorf("unsupported file extension: %s", ext)
		return l.textLoader.Load(path)
	}
}
