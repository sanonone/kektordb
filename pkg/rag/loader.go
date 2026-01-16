package rag

// "os"

// Image represents a visual asset extracted from a document.
type Image struct {
	ID      string
	Data    []byte // Raw binary data
	Ext     string // e.g., "png", "jpg"
	Page    int    // Page number where it was found
	URLPath string // e.g. /assets/image_24892.png
}

// Document represents the extracted content of a file.
type Document struct {
	Text   string
	Images []Image
}

// Loader defines the contract for reading a file.
// It returns a *Document struct containing text and optional images.
type Loader interface {
	Load(path string) (*Document, error)
}

/*
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
*/
