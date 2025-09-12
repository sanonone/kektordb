//go:build !noasm && !appengine && !js

package store

func squaredEuclideanDistanceAVX2FMA(x, y []float32) (float64, error)
