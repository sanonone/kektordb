//go:build !noasm && !appengine && !js

package distance

// squaredEuclideanDistanceAVX2Float16 è la dichiarazione della funzione Assembly per float16
// accetta []uint16
func squaredEuclideanDistanceAVX2Float16(x, y []uint16) (float64, error)
