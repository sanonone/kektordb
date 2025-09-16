//go:build !noasm && !appengine && !js

package store

// dotProductAVX2FMA è la dichiarazione della nostra funzione Assembly per il prodotto scalare.
func dotProductAVX2FMA(x, y []float32) (float64, error)
