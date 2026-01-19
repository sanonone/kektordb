package rag

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	//"regexp"
	"sort"
	//"strconv"
	"crypto/sha256"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDFAdvancedLoader extracts both text (via ledongthuc/pdf) and images (via pdfcpu).
// It uses a temporary directory to buffer images extracted by pdfcpu.
type PDFAdvancedLoader struct {
	extractImages   bool
	assetsOutputDir string
}

func NewPDFAdvancedLoader(extractImages bool, assetsOutputDir string) *PDFAdvancedLoader {
	return &PDFAdvancedLoader{
		extractImages:   extractImages,
		assetsOutputDir: assetsOutputDir,
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
		imgs, err := l.extractAndSaveImages(path)
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

// extractAndSaveImages usa pdfcpu, salva su disco permanente e prepara struct Image
func (l *PDFAdvancedLoader) extractAndSaveImages(sourcePath string) ([]Image, error) {
	slog.Debug("[PDF] Extracting images", "file", filepath.Base(sourcePath))

	// 1. Usa cartella temp per estrazione raw
	tempDir, err := os.MkdirTemp("", "kektor_img_extract_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir) // Pulizia temp

	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.EXTRACTIMAGES
	conf.ValidationMode = model.ValidationRelaxed

	err = api.ExtractImagesFile(sourcePath, tempDir, nil, conf)
	if err != nil {
		return nil, fmt.Errorf("pdfcpu extraction failed: %w", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, err
	}

	// 2. Processa, Hasha e Salva in Asset Dir
	var images []Image

	// Verifica che la dir di output esista (sicurezza)
	if l.assetsOutputDir != "" {
		if err := os.MkdirAll(l.assetsOutputDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to ensure assets dir: %w", err)
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		tempFilePath := filepath.Join(tempDir, entry.Name())
		imgData, err := os.ReadFile(tempFilePath)
		if err != nil {
			continue
		}

		ext := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")

		// 3. Generazione ID Univoco (Hash del contenuto)
		// Questo evita duplicati se la stessa icona è su 100 pagine.
		hash := sha256.Sum256(imgData)
		fileName := fmt.Sprintf("%x.%s", hash[:8], ext) // Primi 16 char dell'hash sono sufficienti

		targetPath := ""
		publicURL := ""

		if l.assetsOutputDir != "" {
			targetPath = filepath.Join(l.assetsOutputDir, fileName)
			// Verifica se esiste già per non sovrascrivere inutilmente
			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				if err := os.WriteFile(targetPath, imgData, 0o644); err != nil {
					slog.Warn("[PDF] Failed to save asset", "file", fileName, "error", err)
					continue
				}
			}
			// Costruiamo il path relativo per il server HTTP
			publicURL = "/assets/" + fileName
		}

		images = append(images, Image{
			ID:      fileName,
			Data:    imgData,
			Ext:     ext,
			URLPath: publicURL, // <--- Ecco il link per il Markdown!
		})
	}

	// Ordina per stabilità
	sort.Slice(images, func(i, j int) bool {
		return images[i].ID < images[j].ID
	})

	if len(images) > 0 {
		slog.Debug("[PDF] Images extracted", "count", len(images))
	}

	return images, nil
}

/*
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
*/
