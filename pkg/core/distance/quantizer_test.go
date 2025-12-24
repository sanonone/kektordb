package distance

import (
	"math/rand"
	"testing"
)

func TestQuantizerTrainingSampling(t *testing.T) {
	// 1. Generate a large dataset (> 50,000)
	const NumVectors = 60000
	const Dimensions = 64

	t.Logf("Generating %d vectors for sampling test...", NumVectors)
	vectors := make([][]float32, NumVectors)
	for i := 0; i < NumVectors; i++ {
		vec := make([]float32, Dimensions)
		for j := 0; j < Dimensions; j++ {
			vec[j] = rand.Float32() * 10.0 // Random values
		}
		vectors[i] = vec
	}

	// 2. Initialize Quantizer
	q := &Quantizer{}

	// 3. Train (should trigger sampling logic)
	// We mainly verify that it doesn't crash and produces a valid AbsMax
	q.Train(vectors)

	if q.AbsMax <= 0 {
		t.Errorf("Expected positive AbsMax, got %f", q.AbsMax)
	}

	t.Logf("Training complete. AbsMax: %f", q.AbsMax)
}
