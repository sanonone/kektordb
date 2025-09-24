package distance

import "math"

// contiene i parametri per la quantizzazione scalare simmetrica
type Quantizer struct {
	AbsMax float32
}

// calcola i parametri di quantizzazione da un campione di vettori
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

// converte il vettore float32 in int8
func (q *Quantizer) Quantize(vector []float32) []int8 {
	if q.AbsMax == 0 {
		return make([]int8, len(vector)) // per evitare divisione per 0
	}

	quantized := make([]int8, len(vector))
	for i, val := range vector {
		// mappa [-AbsMax, AbsMax] -> [-127, 127]
		scaled := (val / q.AbsMax) * 127.0
		quantized[i] = int8(math.Round(float64(scaled)))
	}
	return quantized
}

// Dequantize converte un vettore int8 nel suo approssimato float32 originale.
func (q *Quantizer) Dequantize(vector []int8) []float32 {
	if q.AbsMax == 0 {
		return make([]float32, len(vector))
	}

	dequantized := make([]float32, len(vector))
	for i, val := range vector {
		// Inverte la formula di quantizzazione
		// int_val = round((float_val / abs_max) * 127.0)
		// -> float_val = (int_val / 127.0) * abs_max
		dequantized[i] = (float32(val) / 127.0) * q.AbsMax
	}
	return dequantized
}
