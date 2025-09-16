// File: internal/store/distance_amd64.go
//go:build !noasm && !appengine && !js

// quella sopra è la direttiva per il compilatore che dice di includere questo file solo in build normali,
// escludendo l'assembly se è disabilitato (noasm) o per architetture tipo App Engine o javascript/WASM
package store

// è la dichiarazione della funzione assembly, il corpo della funzione non verrà mai eseguito
// e serve solo al compilatore. La vera implementzione sarà nel file .s corrispondente
// per funzionare la firma deve corrispondere esattamente a ciò che si aspetta l'assembly
func squaredEuclideanDistanceAVX2(x, y []float32) (float64, error)
