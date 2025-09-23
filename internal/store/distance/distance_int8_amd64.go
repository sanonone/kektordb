//go:build !noasm && !appengine && !js

package distance

// dotProductAVX2Int8 è la dichiarazione della nostra funzione Assembly per il prodotto scalare int8.
func dotProductAVX2Int8(x, y []int8) (int32, error)
