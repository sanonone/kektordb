// Package embeddings provides automatic embedder selection.
// This file is always compiled (no build tags).
package embeddings

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EmbedderConfig configures the embedder selection.
type EmbedderConfig struct {
	// Mode: "auto", "ollama", "openai", "local"
	// "auto" (default): auto-detect Ollama → local → error with guidance
	Mode string

	// Ollama settings
	OllamaURL   string // default "http://localhost:11434/api/embeddings"
	OllamaModel string // default "nomic-embed-text"

	// OpenAI settings
	OpenAIURL   string
	OpenAIModel string
	OpenAIKey   string

	// Local ONNX settings
	ModelDir string // path to directory containing .onnx and tokenizer.json
}

// SelectEmbedder selects the best available embedder based on configuration
// and runtime detection. dataDir is used for model storage (download + cache).
func SelectEmbedder(cfg EmbedderConfig, dataDir string) (Embedder, error) {
	// 1. Explicit mode from flag/config
	switch cfg.Mode {
	case "openai":
		url := cfg.OpenAIURL
		if url == "" {
			url = "https://api.openai.com/v1/embeddings"
		}
		return NewOpenAIEmbedder(url, cfg.OpenAIModel, cfg.OpenAIKey, 30*time.Second), nil

	case "ollama":
		url := cfg.OllamaURL
		if url == "" {
			url = "http://localhost:11434/api/embeddings"
		}
		model := cfg.OllamaModel
		if model == "" {
			model = "nomic-embed-text"
		}
		return NewOllamaEmbedder(url, model, 30*time.Second), nil

	case "local":
		return tryLocalEmbedder(dataDir, cfg.ModelDir)
	}

	// 2. Auto-detect: Ollama already running?
	ollamaURL := cfg.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434/api/embeddings"
	}
	if isOllamaRunning(ollamaURL) {
		model := cfg.OllamaModel
		if model == "" {
			model = "nomic-embed-text"
		}
		slog.Info("Embedder: Ollama detected", "url", ollamaURL, "model", model)
		return NewOllamaEmbedder(ollamaURL, model, 30*time.Second), nil
	}

	// 3. Auto-detect: local embedder available?
	emb, err := tryLocalEmbedder(dataDir, cfg.ModelDir)
	if err == nil {
		slog.Info("Embedder: local built-in", "model", "all-MiniLM-L6-v2", "dim", 384)
		return emb, nil
	}

	// 4. Nothing available — error with guidance
	return nil, fmt.Errorf(
		"no embedder available.\n\n"+
			"  Option A (recommended): Install Ollama\n"+
			"    curl -fsSL https://ollama.com/install.sh | sh\n"+
			"    ollama pull nomic-embed-text && ollama serve\n\n"+
			"  Option B: Rebuild with built-in embedding\n"+
			"    go build -tags rust ./cmd/kektordb\n",
	)
}

// NoopEmbedder is a fallback that returns an error on Embed.
// Used when no real embedder is available but the caller wants graceful degradation.
type NoopEmbedder struct{}

func (NoopEmbedder) Embed(text string) ([]float32, error) {
	return nil, fmt.Errorf("no embedder configured — install Ollama or rebuild with -tags rust")
}

// tryLocalEmbedder attempts to create a local embedder.
// It avoids downloading the model if the local embedder runtime isn't available
// (e.g., pure Go build where NewLocalEmbedder always returns an error).
func tryLocalEmbedder(dataDir, modelDir string) (Embedder, error) {
	// First, check if the model files already exist without downloading.
	// This prevents 87MB download in pure Go builds where the embedder can't be used.
	modelPath, tokenizerPath := modelPaths(dataDir, modelDir)
	modelExists := fileExists(modelPath) && fileExists(tokenizerPath)

	if modelExists {
		emb, err := NewLocalEmbedder(modelPath, tokenizerPath)
		if err == nil {
			return emb, nil
		}
		// Model exists but embedder can't load it (e.g., pure Go build).
		// Fall through to download attempt below — but only if the error
		// suggests it's worth retrying (Rust build with corrupted model).
	}

	// Model doesn't exist yet. Try a no-op probe to check if local embedder
	// is available at all (without downloading).
	if !modelExists {
		probe, probeErr := NewLocalEmbedder("/nonexistent/model.onnx", "/nonexistent/tokenizer.json")
		if probeErr != nil {
			// Local embedder is fundamentally unavailable (e.g., pure Go build).
			// Don't download — model would be useless.
			return nil, fmt.Errorf("local embedder not available: %w", probeErr)
		}
		_ = probe // dropped, we just needed to check availability
	}

	// Local embedder IS available but model is missing. Download now.
	dlModelPath, dlTokenizerPath, err := ensureModel(dataDir, modelDir)
	if err != nil {
		return nil, fmt.Errorf("model download failed: %w", err)
	}
	return NewLocalEmbedder(dlModelPath, dlTokenizerPath)
}

// modelPaths returns the expected paths for model and tokenizer files.
func modelPaths(dataDir, modelDir string) (modelPath, tokenizerPath string) {
	base := dataDir
	if modelDir != "" {
		base = modelDir
	}
	dir := filepath.Join(base, "models")
	modelPath = filepath.Join(dir, defaultModelName+".onnx")
	tokenizerPath = filepath.Join(dir, defaultModelName+"-tokenizer.json")
	return
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isOllamaRunning checks if Ollama is running at the given base URL.
func isOllamaRunning(embeddingsURL string) bool {
	baseURL := strings.TrimSuffix(embeddingsURL, "/api/embeddings")
	baseURL = strings.TrimSuffix(baseURL, "/api/embedding")
	if !strings.HasPrefix(baseURL, "http") {
		return false
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
