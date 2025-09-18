// File: internal/store/distance/dot_product_amd64.go
//go:build !noasm && !appengine && !js

package distance

// dotProductAVX2 è la dichiarazione della nostra funzione Assembly per il prodotto scalare.
func dotProductAVX2(x, y []float32) (float64, error)
