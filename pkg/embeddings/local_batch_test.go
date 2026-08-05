//go:build rust

package embeddings

import (
	"math"
	"testing"
)

// TestLocalEmbedderBatchMatchesSerial verifies that EmbedBatch produces
// numerically equivalent vectors to N serial Embed calls, including texts of
// very different lengths (which exercise the padding path in the Rust batch
// inference). Requires the model at /tmp/kektordb-test-models.
// Run with: CGO_LDFLAGS="-L$(pwd)/native/compute/target/release" go test -tags rust -run TestLocalEmbedderBatch ./pkg/embeddings/
func TestLocalEmbedderBatchMatchesSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode (requires ONNX model)")
	}
	modelPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx"
	tokenizerPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2-tokenizer.json"

	emb, err := NewLocalEmbedder(modelPath, tokenizerPath)
	if err != nil {
		t.Fatalf("NewLocalEmbedder: %v", err)
	}

	// Deliberately mixed lengths: padding must not leak into the mean.
	texts := []string{
		"hello world",
		"kektordb is a cognitive memory engine for AI agents",
		"short",
		"a significantly longer text that will pad the shorter rows in the batch: " +
			"the masked mean pooling must ignore the padding tokens entirely so that " +
			"every row is numerically equivalent to an individual inference pass",
		"mid length sentence here",
	}

	batch, err := emb.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != len(texts) {
		t.Fatalf("got %d vectors for %d texts", len(batch), len(texts))
	}

	for i, text := range texts {
		single, err := emb.Embed(text)
		if err != nil {
			t.Fatalf("Embed(%q): %v", text, err)
		}
		if len(batch[i]) != len(single) {
			t.Fatalf("dimension mismatch for text %d: batch=%d serial=%d", i, len(batch[i]), len(single))
		}
		// Masked mean pooling vs plain mean should agree to float precision.
		var maxDiff float64
		for d := range single {
			diff := math.Abs(float64(batch[i][d]) - float64(single[d]))
			if diff > maxDiff {
				maxDiff = diff
			}
		}
		if maxDiff > 1e-4 {
			t.Errorf("text %d: batch and serial differ by up to %f", i, maxDiff)
		}
	}
}

// TestLocalEmbedderBatchSingleText verifies a 1-element batch behaves like Embed.
func TestLocalEmbedderBatchSingleText(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode (requires ONNX model)")
	}
	modelPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx"
	tokenizerPath := "/tmp/kektordb-test-models/all-MiniLM-L6-v2-tokenizer.json"

	emb, err := NewLocalEmbedder(modelPath, tokenizerPath)
	if err != nil {
		t.Fatalf("NewLocalEmbedder: %v", err)
	}

	batch, err := emb.EmbedBatch([]string{"hello world"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	single, err := emb.Embed("hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(batch) != 1 || len(batch[0]) != len(single) {
		t.Fatalf("batch dims wrong: %d x %d, want 1 x %d", len(batch), len(batch[0]), len(single))
	}
	var maxDiff float64
	for d := range single {
		diff := math.Abs(float64(batch[0][d]) - float64(single[d]))
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	if maxDiff > 1e-4 {
		t.Errorf("single-text batch differs by up to %f", maxDiff)
	}
}
