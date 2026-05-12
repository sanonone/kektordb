//go:build !rust

package embeddings

import "fmt"

// NewLocalEmbedder returns an error when compiled without the 'rust' build tag.
// To enable local embedding, rebuild with: go build -tags rust ./cmd/kektordb
func NewLocalEmbedder(modelPath, tokenizerPath string) (Embedder, error) {
	return nil, fmt.Errorf(
		"local embedder not available: compiled without -tags rust.\n" +
			"Options:\n" +
			"  1. Rebuild with: go build -tags rust ./cmd/kektordb\n" +
			"  2. Or install Ollama: ollama pull nomic-embed-text && ollama serve",
	)
}
