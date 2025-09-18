//go:build !noasm && !appengine && !js

package distance

func squaredEuclideanDistanceAVX2FMA(x, y []float32) (float64, error)
