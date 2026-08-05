//go:build rust

package embeddings

import (
	"fmt"
	"os"
	"testing"
)

// Real-ONNX benchmarks (make bench-rust): serial embeds N texts in N CGO
// inference passes, batch embeds them in a single (N, max_seq) pass with
// masked mean pooling. This is the headline number for the v0.6.1 perf claim.
// Skips silently when the model is not present or the build is pure Go.

const benchModelPath = "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx"
const benchTokenizerPath = "/tmp/kektordb-test-models/all-MiniLM-L6-v2-tokenizer.json"

func BenchmarkLocalEmbedSerial(b *testing.B) {
	benchLocal(b, false)
}

func BenchmarkLocalEmbedBatch(b *testing.B) {
	benchLocal(b, true)
}

func benchLocal(b *testing.B, useBatch bool) {
	if _, err := os.Stat(benchModelPath); err != nil {
		b.Skipf("model not found at %s", benchModelPath)
	}
	emb, err := NewLocalEmbedder(benchModelPath, benchTokenizerPath)
	if err != nil {
		b.Skipf("local embedder unavailable: %v", err)
	}

	texts := make([]string, 64)
	for i := range texts {
		texts[i] = fmt.Sprintf("Benchmark chunk number %d: kektordb memory engine embeds this chunk of text with enough words for a realistic RAG document chunk.", i)
	}

	// Warm up the model (lazy init on first call).
	if useBatch {
		if _, err := emb.EmbedBatch(texts[:1]); err != nil {
			b.Fatal(err)
		}
	} else {
		if _, err := emb.Embed(texts[0]); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if useBatch {
			if _, err := emb.EmbedBatch(texts); err != nil {
				b.Fatal(err)
			}
		} else {
			for _, t := range texts {
				if _, err := emb.Embed(t); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}
