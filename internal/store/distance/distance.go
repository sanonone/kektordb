package distance

import (
	"errors"
	"fmt"
	"github.com/klauspost/cpuid/v2"
	"github.com/x448/float16"
)

// --- Tipi Pubblici ---

type DistanceMetric string
type PrecisionType string

const (
	Euclidean DistanceMetric = "euclidean"
	Cosine    DistanceMetric = "cosine"

	Float32 PrecisionType = "float32"
	Float16 PrecisionType = "float16"
	Int8    PrecisionType = "int8"
)

// Definiamo i tipi di funzione per ogni precisione
type DistanceFuncF32 func(v1, v2 []float32) (float64, error)
type DistanceFuncF16 func(v1, v2 []uint16) (float64, error)

// Restituisce int32 per evitare overflow durante la somma.
type DistanceFuncI8 func(v1, v2 []int8) (int32, error) // Usiamo int64 per massima sicurezza

// (Aggiungeremo DistanceFuncI8 in futuro)

// --- Catalogo delle Funzioni Disponibili ---

// float32Funcs contiene le implementazioni per la precisione float32.
var float32Funcs = map[DistanceMetric]DistanceFuncF32{
	Euclidean: squaredEuclideanDistanceGo,
	Cosine:    dotProductAsDistanceGo,
}

// float16Funcs contiene le implementazioni per la precisione float16.
var float16Funcs = map[DistanceMetric]DistanceFuncF16{
	Euclidean: squaredEuclideanDistanceGoFloat16,
}

// implementazioni per la quantizzazione int8
var int8Funcs = map[DistanceMetric]DistanceFuncI8{
	Cosine: dotProductGoInt8,
}

///////////////

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

// Questa è la nostra funzione di riferimento per la metrica Coseno su dati normalizzati.
func dotProductAsDistanceGo(v1, v2 []float32) (float64, error) {
	dot, err := dotProductGo(v1, v2)
	if err != nil {
		return 0, err
	}
	return 1.0 - float64(dot), nil
}

func dotProductGoInt8(v1, v2 []int8) (int32, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("i vettori int8 devono avere la stessa lunghezza")
	}
	var sum int32
	for i := range v1 {
		sum += int32(v1[i]) * int32(v2[i])
	}
	return sum, nil
}

// --- Funzioni Wrapper per SIMD ---

// cosineDistanceAVX2 è un wrapper che usa il prodotto scalare SIMD
// per calcolare la distanza coseno completa.
func cosineDistanceAVX2(v1, v2 []float32) (float64, error) {
	// 1. Calcola il prodotto scalare usando l'assembly veloce
	dot, err := dotProductAVX2(v1, v2)
	if err != nil {
		return 0, err
	}

	return 1.0 - float64(dot), nil
}

// --- Funzioni Wrapper per SIMD ---

// cosineDistanceAVX2 è un wrapper che usa il prodotto scalare SIMD
// per calcolare la distanza coseno completa.
func cosineDistanceAVX2FMA(v1, v2 []float32) (float64, error) {
	// 1. Calcola il prodotto scalare usando l'assembly veloce
	dot, err := dotProductAVX2FMA(v1, v2)
	if err != nil {
		return 0, err
	}
	return 1.0 - float64(dot), nil
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

// init() è una funzione speciale di go che viene eseguita automaticamente
// all'avvio prima della funzione main()
// qui servirà per la logica di dispatch e verificare se la CPU supporta l'ottimizzazione
func init() {
	// caso float32
	// se la CPU supporta AVX2 + FMA, scegli la versione più veloce
	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.FMA3) {
		float32Funcs[Euclidean] = squaredEuclideanDistanceAVX2FMA
		float32Funcs[Cosine] = cosineDistanceAVX2FMA
		return
	} else if cpuid.CPU.Has(cpuid.AVX2) {
		// se si sovrascrile la funzione di default con quella ottimizzata
		float32Funcs[Euclidean] = squaredEuclideanDistanceAVX2
		float32Funcs[Cosine] = cosineDistanceAVX2
	}

	// --- NUOVO: Dispatch per float16 ---
	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
		// La versione base AVX2 è il nostro punto di partenza
		float16Funcs[Euclidean] = squaredEuclideanDistanceAVX2Float16

		// Se abbiamo anche FMA, usiamo la versione ancora più veloce
		if cpuid.CPU.Has(cpuid.FMA3) {
			float16Funcs[Euclidean] = squaredEuclideanDistanceAVX2Float16FMA
		}
	}

	if cpuid.CPU.Has(cpuid.AVX2) {
		int8Funcs[Cosine] = dotProductAVX2Int8
	}
	// qui in futuro andranno altri check per altre ottimizzazioni

}

// --- Funzioni Getter Pubbliche (L'API del package) ---

// GetFloat32Func restituisce la migliore funzione di distanza per float32
func GetFloat32Func(metric DistanceMetric) (DistanceFuncF32, error) {
	fn, ok := float32Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione float32", metric)
	}
	return fn, nil
}

// GetFloat16Func restituisce la migliore funzione di distanza per float16
func GetFloat16Func(metric DistanceMetric) (DistanceFuncF16, error) {
	fn, ok := float16Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione float16", metric)
	}
	return fn, nil
}

// restituisce la funzione per int8
func GetInt8Func(metric DistanceMetric) (DistanceFuncI8, error) {
	fn, ok := int8Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione int8", metric)
	}
	return fn, nil
}
