package store 

import (
	
	"github.com/klauspost/cpuid/v2"
	"errors"
)

// funzione per distanza euclidea in go, in alternativa a quella assembly in caso di incompatibilità
func squaredEuclideanDistanceGo(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		// return 0, math.ErrUnsupported
		return 0, errors.New("squaredEuclideanDistance: vectors must have the same length")
	}
	var sum float32
	for i := range v1 {
		diff := v1[i] - v2[i]
		sum += diff * diff
	}
	return float64(sum), nil
}

// logica di dispatch

// nuovo tipo per gestire le diverse funzioni di distanza in modo comune
type DistanceFunc func([]float32, []float32) (float64, error)

var (
	// funzione di distanza predefinita, verrà sovrascritta se la CPU supporta ottimizzazioni 
	defaultDistanceFunc = squaredEuclideanDistanceGo
)

// init() è una funzione speciale di go che viene eseguita automaticamente 
// all'avvio prima della funzione main()
// qui servirà per la logica di dispatch e verificare se la CPU supporta l'ottimizzazione 
func init() {
	// se la CPU supporta AVX2 + FMA, scegli la versione più veloce
    if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.FMA3) {
        defaultDistanceFunc = squaredEuclideanDistanceAVX2FMA
        return
    }
	// controlla se la CPU ha il set di istruzioni AVX2
	if cpuid.CPU.Has(cpuid.AVX2) {
		// se si sovrascrile la funzione di default con quella ottimizzata 
		defaultDistanceFunc = squaredEuclideanDistanceAVX2
	}
	// qui in futuro andranno altri check per altre ottimizzazioni 

}
