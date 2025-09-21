//go:build !noasm && !appengine && !js

package distance

// squaredEuclideanDistanceAVX2Float16FMA è la dichiarazione della funzione Assembly per float16
// accetta []uint16
func squaredEuclideanDistanceAVX2Float16FMA(x, y []uint16) (float64, error)
