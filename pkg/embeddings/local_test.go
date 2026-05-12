//go:build rust

package embeddings

import (
	"testing"
)

// TestLocalEmbedderSmoke verifies the Go CGO wrapper can call the Rust ONNX embedder.
// Requires the model and tokenizer at the given paths (pre-downloaded).
// Run with: CGO_LDFLAGS="-L$(pwd)/native/compute/target/release" go test -tags rust -run TestLocalEmbedder ./pkg/embeddings/
func TestLocalEmbedderInteg(t *testing.T) {
	modelPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx"
	tokenizerPath := "/tmp/kektordb-test-models/tokenizer.json"

	emb, err := NewLocalEmbedder(modelPath, tokenizerPath)
	if err != nil {
		t.Fatalf("NewLocalEmbedder: %v", err)
	}

	vec, err := emb.Embed("hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(vec) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(vec))
	}

	// Verify not all zeros
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum < 1e-10 {
		t.Error("embedding is all zeros — model not producing output")
	}

	t.Logf("OK: embedding dim=%d, sample=[%.6f, %.6f, %.6f]", len(vec), vec[0], vec[1], vec[2])
}

// TestLocalEmbedderConsistency verifies repeated calls produce identical output.
func TestLocalEmbedderConsistency(t *testing.T) {
	modelPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx"
	tokenizerPath := "/tmp/kektordb-test-models/tokenizer.json"

	emb, _ := NewLocalEmbedder(modelPath, tokenizerPath)
	v1, _ := emb.Embed("hello world")
	v2, _ := emb.Embed("hello world")

	if len(v1) != len(v2) {
		t.Fatalf("dimension mismatch: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		diff := v1[i] - v2[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > 1e-5 {
			t.Errorf("output mismatch at index %d: %.10f vs %.10f", i, v1[i], v2[i])
			break
		}
	}
}
