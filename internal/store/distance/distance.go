package distance

import (
	"errors"
	"fmt"
	"github.com/klauspost/cpuid/v2"
	"github.com/x448/float16"
	"math"
)

// DistanceMetric è un tipo per rappresentare le nostre metriche
type DistanceMetric string

const (
	Euclidean DistanceMetric = "euclidean"
	Cosine    DistanceMetric = "cosine"
)

// distanceFuncs è una mappa che collega le metriche alle loro implementazioni.
var distanceFuncs = map[DistanceMetric]DistanceFunc{
	Euclidean: squaredEuclideanDistanceGo,
	// Per il coseno, usiamo il prodotto scalare. HNSW dovrà essere consapevole
	// che un valore PIÙ ALTO significa "più vicino".
	Cosine: cosineDistanceGo,
}

// GetDistanceFunc restituisce la funzione di distanza ottimizzata per una data metrica.
func GetDistanceFunc(metric DistanceMetric) (DistanceFunc, error) {
	fn, ok := distanceFuncs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica di distanza non supportata: %s", metric)
	}
	return fn, nil
}

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

// funzione di distanza euclidea con float16 per minor carico sulla memoria
func squaredEuclideanDistanceGoFloat16(v1, v2 []uint16) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("i vettori float16 devono avere la stessa lunghezza")
	}
	var sum float32
	for i := range v1 {
		// Converti f16 in f32 per il calcolo
		f1 := float16.Frombits(v1[i]).Float32()
		f2 := float16.Frombits(v2[i]).Float32()
		diff := f1 - f2
		sum += diff * diff
	}
	return float64(sum), nil
}

// cosineDistanceGo calcola la distanza del coseno (1 - cosSimilarity).
func cosineDistanceGo(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("cosineDistance: vectors must have the same length")
	}
	var dot, norm1, norm2 float64
	for i := range v1 {
		f1 := float64(v1[i])
		f2 := float64(v2[i])
		dot += f1 * f2
		norm1 += f1 * f1
		norm2 += f2 * f2
	}
	if norm1 == 0 || norm2 == 0 {
		return 0, errors.New("cosineDistance: zero-length vector")
	}
	cosine := dot / (math.Sqrt(norm1) * math.Sqrt(norm2))
	// Convertiamo la similarità in distanza
	return 1.0 - cosine, nil
}

// DA ELIMINARE
// funzione per distanza cousine
// dotProductGo calcola il prodotto scalare tra due vettori.
// Questa è la versione di riferimento in Go puro.
func dotProductGo(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("dotProduct: vectors must have the same length")
	}
	var sum float32
	for i := range v1 {
		sum += v1[i] * v2[i]
	}
	return float64(sum), nil
}

// logica di dispatch

// nuovo tipo per gestire le diverse funzioni di distanza in modo comune
type DistanceFunc func([]float32, []float32) (float64, error)

// --- NUOVO TIPO PER GESTIRE LE DIVERSE FUNZIONI ---
// tipo per le nostre funzioni di distanza float16
type DistanceFuncFloat16 func([]uint16) (float64, error)

var (
	// Funzioni di default per float32
	defaultDistanceFunc = squaredEuclideanDistanceGo

	// --- NUOVO: Funzioni di default per float16 ---
	defaultDistanceFuncFloat16 = squaredEuclideanDistanceGoFloat16
)

/*
var (
	// funzione di distanza predefinita, verrà sovrascritta se la CPU supporta ottimizzazioni
	defaultDistanceFunc = squaredEuclideanDistanceGo
)
*/

// init() è una funzione speciale di go che viene eseguita automaticamente
// all'avvio prima della funzione main()
// qui servirà per la logica di dispatch e verificare se la CPU supporta l'ottimizzazione
func init() {
	// se la CPU supporta AVX2 + FMA, scegli la versione più veloce
	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.FMA3) {
		distanceFuncs[Euclidean] = squaredEuclideanDistanceAVX2FMA
		distanceFuncs[Cosine] = dotProductAVX2FMA
		return
	} else if cpuid.CPU.Has(cpuid.AVX2) {
		// se si sovrascrile la funzione di default con quella ottimizzata
		distanceFuncs[Euclidean] = squaredEuclideanDistanceAVX2
		distanceFuncs[Cosine] = dotProductAVX2
	}

	// --- NUOVO: Dispatch per float16 ---
	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
		// La versione base AVX2 è il nostro punto di partenza
		defaultDistanceFuncFloat16 = squaredEuclideanDistanceAVX2Float16

		// Se abbiamo anche FMA, usiamo la versione ancora più veloce
		if cpuid.CPU.Has(cpuid.FMA3) {
			defaultDistanceFuncFloat16 = squaredEuclideanDistanceAVX2Float16FMA
		}
	}
	// qui in futuro andranno altri check per altre ottimizzazioni

}
