//go:build !noasm && !appengine && !js

package distance

// dotProductAVX2FMA è la dichiarazione della nostra funzione Assembly per il prodotto scalare.
func dotProductAVX2FMA(x, y []float32) (float64, error)
