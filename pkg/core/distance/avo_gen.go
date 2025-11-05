//go:build avo && !rust && amd64

package distance

// SquaredEuclideanFloat16AVX2 calcola la distanza euclidea al quadrato per vettori float16 (rappresentati come uint16) usando AVX2.
//
//go:generate go run ./gen -stubs ./stubs_avo_amd64.go -out ./distance_avo_amd64.s
//func SquaredEuclideanFloat16AVX2(v1 []uint16, v2 []uint16) float32
