package rag

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// DocxLoader extracts text from .docx files preserving basic structure (Headers).
type DocxLoader struct{}

func NewDocxLoader() *DocxLoader {
	return &DocxLoader{}
}

func (l *DocxLoader) Load(path string) (*Document, error) {
	// 1. Open the .docx file as a ZIP archive
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open docx zip: %w", err)
	}
	defer r.Close()

	// 2. Find "word/document.xml"
	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}

	if docFile == nil {
		return nil, fmt.Errorf("invalid docx: word/document.xml not found")
	}

	// 3. Open the XML file
	rc, err := docFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// 4. Parse XML manually to extract text and styles
	text, err := parseDocxXML(rc)
	if err != nil {
		return nil, err
	}

	// 5. Wrap in the Document struct
	// Note: We leave Images as nil for now.
	return &Document{
		Text:   text,
		Images: nil,
	}, nil
}

// parseDocxXML streams the XML and converts Paragraphs to Markdown-like text
func parseDocxXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var result strings.Builder

	// State variables
	var currentParaText strings.Builder
	var currentStyle string

	inParagraph := false
	inTextNode := false // Are we inside a <w:t> tag?

	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "p" {
				// Paragraph Start
				inParagraph = true
				currentParaText.Reset()
				currentStyle = ""
			} else if se.Name.Local == "pStyle" {
				// Style (e.g. Heading1)
				for _, attr := range se.Attr {
					if attr.Name.Local == "val" {
						currentStyle = attr.Value
					}
				}
			} else if se.Name.Local == "t" {
				// Text Start
				inTextNode = true
			}

		case xml.CharData:
			// Text Content
			if inParagraph && inTextNode {
				currentParaText.Write(se)
			}

		case xml.EndElement:
			if se.Name.Local == "t" {
				inTextNode = false
			} else if se.Name.Local == "p" {
				// Paragraph End: Flush
				if inParagraph {
					text := currentParaText.String()
					if strings.TrimSpace(text) != "" {
						// Debug Log (Remove after active dev)
						// log.Printf("[DOCX DEBUG] Found Para: style='%s' text='%.20s...'", currentStyle, text)

						prefix := ""
						// Heading Recognition (improved for case-insensitive or variants)
						// Word often uses "Heading1", "Heading 2", etc.
						if strings.Contains(currentStyle, "Heading") || strings.Contains(currentStyle, "heading") {
							if strings.Contains(currentStyle, "1") {
								prefix = "# "
							} else if strings.Contains(currentStyle, "2") {
								prefix = "## "
							} else if strings.Contains(currentStyle, "3") {
								prefix = "### "
							}
						}
						result.WriteString(prefix + text + "\n\n")
					}
				}
				inParagraph = false
			}
		}
	}

	finalText := result.String()
	slog.Info("[DOCX] Extracted chars", "count", len(finalText))
	return finalText, nil
}
