package rag

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// SmartLoader wraps AutoLoader and optionally prepends a CLI-based parser.
// When a CLI parser is configured, SmartLoader tries it first and silently
// falls back to the internal AutoLoader if the CLI tool fails.
//
// If CLI parsing succeeds and image extraction is enabled, SmartLoader also
// extracts images from PDF files using the internal PDFAdvancedLoader to
// ensure image support even when using external CLI tools for text parsing.
type SmartLoader struct {
	cliLoader      Loader // nil when parser type is "internal"
	fallbackLoader Loader // AutoLoader (always present)

	// Image extraction for CLI mode
	extractImages     bool
	assetsOutputDir   string
	pdfImageExtractor *PDFAdvancedLoader // nil if not PDF or images disabled
}

// NewSmartLoader creates a SmartLoader based on the parser configuration.
// extractImages and assetsDir are forwarded to the underlying AutoLoader.
func NewSmartLoader(parserCfg ParserConfig, extractImages bool, assetsDir string) *SmartLoader {
	fallback := NewAutoLoader(extractImages, assetsDir)

	sl := &SmartLoader{
		fallbackLoader:  fallback,
		extractImages:   extractImages,
		assetsOutputDir: assetsDir,
	}

	if parserCfg.Type == "cli" && len(parserCfg.Command) > 0 {
		timeout, _ := time.ParseDuration(parserCfg.Timeout)
		if timeout <= 0 {
			timeout = defaultCLITimeout
		}
		sl.cliLoader = NewCLILoader(parserCfg.Command, timeout)

		// Create PDF image extractor if image extraction is enabled
		if extractImages {
			sl.pdfImageExtractor = NewPDFAdvancedLoader(true, assetsDir)
		}

		slog.Info("[SmartLoader] CLI parser enabled", "command", parserCfg.Command, "timeout", timeout)
	}

	return sl
}

// Load implements the Loader interface.
// If a CLI parser is configured, it tries that first. On failure, it logs
// a warning and falls back to the internal AutoLoader.
//
// When CLI parsing succeeds and image extraction is enabled, it also extracts
// images from PDF files using the internal PDFAdvancedLoader.
func (l *SmartLoader) Load(path string) (*Document, error) {
	if l.cliLoader == nil {
		return l.fallbackLoader.Load(path)
	}

	doc, err := l.cliLoader.Load(path)
	if err == nil && doc != nil && doc.Text != "" {
		// CLI succeeded - now extract images if enabled and file is PDF
		if l.extractImages && l.isPDF(path) && l.pdfImageExtractor != nil {
			if imgDoc, imgErr := l.pdfImageExtractor.Load(path); imgErr == nil && len(imgDoc.Images) > 0 {
				doc.Images = imgDoc.Images
				slog.Debug("[SmartLoader] Extracted images from PDF via CLI", "path", filepath.Base(path), "image_count", len(doc.Images))
			}
		}
		return doc, nil
	}

	slog.Warn("[SmartLoader] CLI parser failed, falling back to internal",
		"path", path,
		"error", err,
		"hint", "check command argument order: options (e.g. --format, -q) must come before {{file_path}}",
	)
	return l.fallbackLoader.Load(path)
}

// isPDF returns true if the file extension indicates a PDF file.
func (l *SmartLoader) isPDF(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".pdf"
}
