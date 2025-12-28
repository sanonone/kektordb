// Package distance provides functions for calculating vector distances.
//
// This file implements a symmetric scalar quantizer, which is used to convert
// float32 vectors into a more memory-efficient int8 representation. The quantizer
// can be trained on a sample of vectors to determine optimal parameters that are
// robust against outliers.
package distance

import (
	"log"
	"math"
	//"math/rand"
	"sort"
)

// Quantizer holds the parameters for symmetric scalar quantization.
// It learns the optimal range from a training dataset to map float32 values
// to the int8 space [-127, 127].
type Quantizer struct {
	AbsMax float32
}

/*
// initial version, causes problems with outliers
// calculates quantization parameters from a sample of vectors
func (q *Quantizer) Train(vectors [][]float32) {
	var max float32
	for _, vec := range vectors {
		for _, val := range vec {
			absVal := float32(math.Abs(float64(val)))
			if absVal > max {
				max = absVal
			}
		}
	}
	q.AbsMax = max
}
*/

// Train calculates the quantization parameters using a quantile-based approach
// to be more robust against outliers. Instead of using the absolute maximum value,
// it uses a high percentile (e.g., 99.9th) to define the quantization range,
// effectively ignoring extreme outlier values that could skew the results.
//
// Optimization: If the dataset is large (> 50,000 vectors), it trains on a
// random sample to keep memory usage and sorting time constant O(1) relative to
// dataset size.
func (q *Quantizer) Train(vectors [][]float32) {
	totalVectors := len(vectors)
	if totalVectors == 0 || len(vectors[0]) == 0 {
		log.Println("[Quantizer] Warning: Empty dataset provided for training. Skipping.")
		return
	}

	trainingSet := vectors

	// --- SMART SAMPLING STRATEGY ---
	// Obiettivo: Bilanciare precisione statistica e velocità.
	const HardCap = 25000
	const MinThreshold = 10000 // Sotto i 10k usa tutto

	if totalVectors > MinThreshold {
		// Calcoliamo una dimensione target (es. 10% del dataset)
		targetSize := totalVectors / 10

		// Applichiamo il Cap
		if targetSize > HardCap {
			targetSize = HardCap
		}
		// Applichiamo un floor (nel caso 10% fosse troppo poco, ma MinThreshold ci protegge già)
		if targetSize < MinThreshold {
			targetSize = MinThreshold
		}

		log.Printf("[Quantizer] Sampling: Training on %d vectors (source: %d)...", targetSize, totalVectors)

		sampled := make([][]float32, 0, targetSize)

		// Uso STRIDE (Passo fisso) invece di Random.
		// Vantaggi: Deterministico, Cache-friendly, Zero lock contention.
		step := totalVectors / targetSize
		if step < 1 {
			step = 1
		}

		for i := 0; i < totalVectors; i += step {
			sampled = append(sampled, vectors[i])
			if len(sampled) >= targetSize {
				break
			}
		}
		trainingSet = sampled
	}
	// -----------------------------------------------

	// Gather all absolute values into a single, large slice.
	// Pre-allocate the slice for efficiency.
	// Using trainingSet instead of vectors
	numValues := len(trainingSet) * len(trainingSet[0])
	allAbsValues := make([]float32, 0, numValues)

	for _, vec := range trainingSet {
		for _, val := range vec {
			allAbsValues = append(allAbsValues, float32(math.Abs(float64(val))))
		}
	}

	// Sort the slice to find the quantile.
	// This is the most expensive operation during training.
	sort.Slice(allAbsValues, func(i, j int) bool {
		return allAbsValues[i] < allAbsValues[j]
	})

	// Calculate the index of the 99.9th percentile.
	// We use the 99.9th percentile to exclude the top 0.1% of extreme values (the outliers).
	quantileIndex := int(float64(len(allAbsValues)) * 0.999)

	// Ensure the index is within valid bounds.
	if quantileIndex >= len(allAbsValues) {
		quantileIndex = len(allAbsValues) - 1
	}
	if quantileIndex < 0 {
		quantileIndex = 0
	}

	// Set the new AbsMax.
	q.AbsMax = allAbsValues[quantileIndex]

	// Add a log for debugging.
	log.Printf("[DEBUG QUANTIZER] Training complete on %d vectors. AbsMax set to the 99.9th percentile value: %f", len(trainingSet), q.AbsMax)
}

// Quantize converts a float32 vector into its int8 representation.
// It scales the float values based on the trained AbsMax and then rounds them to the nearest integer.
func (q *Quantizer) Quantize(vector []float32) []int8 {
	if q.AbsMax == 0 {
		return make([]int8, len(vector)) // Avoid division by zero.
	}

	quantized := make([]int8, len(vector))
	for i, val := range vector {
		// Map the range [-AbsMax, AbsMax] to [-127, 127].
		scaled := (val / q.AbsMax) * 127.0

		// --- CLIPPING LOGIC ---
		// Clip values that fall outside the target range after scaling.
		if scaled > 127.0 {
			scaled = 127.0
		} else if scaled < -127.0 {
			scaled = -127.0
		}
		// --- END CLIPPING LOGIC ---

		quantized[i] = int8(math.Round(float64(scaled)))
	}
	return quantized
}

// Dequantize converts an int8 vector back to its approximate float32 representation.
// This process is the inverse of quantization and is useful for operations that
// require the original vector space, though it introduces some precision loss.
func (q *Quantizer) Dequantize(vector []int8) []float32 {
	if q.AbsMax == 0 {
		return make([]float32, len(vector))
	}

	dequantized := make([]float32, len(vector))
	for i, val := range vector {
		// Invert the quantization formula:
		// int_val = round((float_val / abs_max) * 127.0)
		// -> float_val ≈ (int_val / 127.0) * abs_max
		dequantized[i] = (float32(val) / 127.0) * q.AbsMax
	}
	return dequantized
}
