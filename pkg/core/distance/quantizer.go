package distance

import (
	"log"
	"math"
	"sort"
)

// contiene i parametri per la quantizzazione scalare simmetrica
type Quantizer struct {
	AbsMax float32
}

/*
// versione iniziale, da problemi con outlier
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
*/

// Train calcola i parametri di quantizzazione usando un approccio basato sui quantili
// per essere più robusto agli outlier.
func (q *Quantizer) Train(vectors [][]float32) {
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return // Niente da addestrare
	}

	// 1. Raccogli tutti i valori assoluti in una singola, grande slice.
	// Pre-allochiamo la slice per efficienza.
	numValues := len(vectors) * len(vectors[0])
	allAbsValues := make([]float32, 0, numValues)

	for _, vec := range vectors {
		for _, val := range vec {
			allAbsValues = append(allAbsValues, float32(math.Abs(float64(val))))
		}
	}

	// 2. Ordina la slice per poter trovare il quantile.
	// Questa è l'operazione più costosa del training.
	sort.Slice(allAbsValues, func(i, j int) bool {
		return allAbsValues[i] < allAbsValues[j]
	})

	// 3. Calcola l'indice del 99.9-esimo percentile.
	// Usiamo il 99.9% per escludere lo 0.1% di valori più estremi (gli outlier).
	quantileIndex := int(float64(len(allAbsValues)) * 0.999)

	// Assicurati che l'indice sia valido.
	if quantileIndex >= len(allAbsValues) {
		quantileIndex = len(allAbsValues) - 1
	}
	if quantileIndex < 0 {
		quantileIndex = 0
	}

	// 4. Imposta il nuovo AbsMax.
	q.AbsMax = allAbsValues[quantileIndex]

	// Aggiungiamo un log per il debug
	log.Printf("[DEBUG QUANTIZER] Training completato. AbsMax impostato al 99.9-esimo percentile: %f", q.AbsMax)
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

		// --- NUOVA LOGICA DI CLIPPING ---
		if scaled > 127.0 {
			scaled = 127.0
		} else if scaled < -127.0 {
			scaled = -127.0
		}
		// --- FINE NUOVA LOGICA ---

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
