package rag

import (
	"log/slog"
	"time"
)

// SmartLoader wraps AutoLoader and optionally prepends a CLI-based parser.
// When a CLI parser is configured, SmartLoader tries it first and silently
// falls back to the internal AutoLoader if the CLI tool fails.
type SmartLoader struct {
	cliLoader      Loader // nil when parser type is "internal"
	fallbackLoader Loader // AutoLoader (always present)
}

// NewSmartLoader creates a SmartLoader based on the parser configuration.
// extractImages and assetsDir are forwarded to the underlying AutoLoader.
func NewSmartLoader(parserCfg ParserConfig, extractImages bool, assetsDir string) *SmartLoader {
	fallback := NewAutoLoader(extractImages, assetsDir)

	if parserCfg.Type != "cli" || len(parserCfg.Command) == 0 {
		return &SmartLoader{fallbackLoader: fallback}
	}

	timeout, _ := time.ParseDuration(parserCfg.Timeout)
	cli := NewCLILoader(parserCfg.Command, timeout)

	slog.Info("[SmartLoader] CLI parser enabled", "command", parserCfg.Command, "timeout", timeout)

	return &SmartLoader{
		cliLoader:      cli,
		fallbackLoader: fallback,
	}
}

// Load implements the Loader interface.
// If a CLI parser is configured, it tries that first. On failure, it logs
// a warning and falls back to the internal AutoLoader.
func (l *SmartLoader) Load(path string) (*Document, error) {
	if l.cliLoader == nil {
		return l.fallbackLoader.Load(path)
	}

	doc, err := l.cliLoader.Load(path)
	if err == nil && doc != nil && doc.Text != "" {
		return doc, nil
	}

	slog.Warn("[SmartLoader] CLI parser failed, falling back to internal",
		"path", path,
		"error", err,
		"hint", "check command argument order: options (e.g. --format, -q) must come before {{file_path}}",
	)
	return l.fallbackLoader.Load(path)
}
