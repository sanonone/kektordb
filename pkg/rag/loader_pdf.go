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

	// r.NumPage() restituisce il numero totale di pagine
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		// Estrai il testo puro dalla pagina
		text, err := p.GetPlainText(nil)
		if err != nil {
			// Logghiamo l'errore ma continuiamo con le altre pagine?
			// Per ora ritorniamo errore se una pagina fallisce.
			return "", fmt.Errorf("failed to extract text from page %d: %w", pageIndex, err)
		}

		buf.WriteString(text)
		// Aggiungiamo un newline tra le pagine per separarle semanticamente
		buf.WriteString("\n")
	}

	return buf.String(), nil
}
