package rag

import (
	"bytes"
	"fmt"

	"github.com/ledongthuc/pdf"
)

// PDFLoader handles extraction of text from PDF files.
type PDFLoader struct{}

func NewPDFLoader() *PDFLoader {
	return &PDFLoader{}
}

func (l *PDFLoader) Load(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer

	// r.NumPage() returns total number of pages
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		// Extract plain text from page
		text, err := p.GetPlainText(nil)
		if err != nil {
			// Log error but continue with other pages?
			// For now return error if a page fails.
			return "", fmt.Errorf("failed to extract text from page %d: %w", pageIndex, err)
		}

		buf.WriteString(text)
		// Add a newline between pages to separate them semantically
		buf.WriteString("\n")
	}

	return buf.String(), nil
}
