package rag

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDFAdvancedLoader extracts both text (via ledongthuc/pdf) and images (via pdfcpu).
// It uses a temporary directory to buffer images extracted by pdfcpu.
type PDFAdvancedLoader struct {
	extractImages bool
}

func NewPDFAdvancedLoader(extractImages bool) *PDFAdvancedLoader {
	return &PDFAdvancedLoader{
		extractImages: extractImages,
	}
}

// Load implements the Loader interface.
func (l *PDFAdvancedLoader) Load(path string) (*Document, error) {
	// 1. Extract Text
	text, err := l.extractText(path)
	if err != nil {
		// We log but continue, as the file might be an image-only PDF
		slog.Warn("[PDF] Text extraction warning", "path", path, "error", err)
	}

	var images []Image

	// 2. Extract Images (if enabled)
	if l.extractImages {
		imgs, err := l.extractImagesFromPDF(path)
		if err != nil {
			slog.Warn("[PDF] Image extraction warning", "path", path, "error", err)
		} else {
			images = imgs
		}
	}

	return &Document{
		Text:   text,
		Images: images,
	}, nil
}

// extractText uses ledongthuc/pdf for reliable plain text extraction.
func (l *PDFAdvancedLoader) extractText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}
		t, _ := p.GetPlainText(nil)
		buf.WriteString(t)
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

// extractImagesFromPDF uses pdfcpu to extract images to a temp dir, then reads them back.
func (l *PDFAdvancedLoader) extractImagesFromPDF(path string) ([]Image, error) {
	slog.Info("[PDF] Starting image extraction", "filename", filepath.Base(path))
	// Create a temporary directory for extraction
	tempDir, err := os.MkdirTemp("", "kektor_img_extract_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	// Ensure cleanup happens when we are done
	defer os.RemoveAll(tempDir)

	// Configure pdfcpu to be quiet
	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.EXTRACTIMAGES
	conf.ValidationMode = model.ValidationRelaxed // Be tolerant with malformed PDFs

	// Execute extraction (writes files to tempDir)
	// Passing nil for selectedPages extracts from all pages.
	err = api.ExtractImagesFile(path, tempDir, nil, conf)
	if err != nil {
		slog.Error("[PDF] pdfcpu error", "error", err)
		return nil, fmt.Errorf("pdfcpu extraction failed: %w", err)
	}

	// Read files back from tempDir
	var images []Image

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, err
	}

	slog.Info("[PDF] pdfcpu extracted files", "count", len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Filename usually contains page number info, useful for debugging
		// e.g. "im0-1.png" (image 0 on page 1)
		fullPath := filepath.Join(tempDir, entry.Name())

		imgData, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		// Simple heuristic to guess page number from filename if needed
		// For now we just store the extension
		ext := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")

		images = append(images, Image{
			Data: imgData,
			Ext:  ext,
			Name: entry.Name(),
			// Page: ... parsing filename logic could go here
		})
	}

	// Sort images by filename numbers (Page-Index) to maintain context order
	// pdfcpu typically outputs "im<Page>-<Index>" (e.g. im1-0.png)
	reNum := regexp.MustCompile(`\d+`)

	sort.Slice(images, func(i, j int) bool {
		// Extract all numbers from filenames
		umsI := reNum.FindAllString(images[i].Name, -1)
		umsJ := reNum.FindAllString(images[j].Name, -1)

		// Compare number by number
		for k := 0; k < len(umsI) && k < len(umsJ); k++ {
			nI, _ := strconv.Atoi(umsI[k])
			nJ, _ := strconv.Atoi(umsJ[k])
			if nI != nJ {
				return nI < nJ
			}
		}
		// Fallback to string comparison if numbers equal
		return images[i].Name < images[j].Name
	})

	return images, nil
}
