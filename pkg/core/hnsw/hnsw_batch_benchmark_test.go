package hnsw

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

const (
	BenchmarkDim        = 128
	BenchmarkM          = 16
	BenchmarkEf         = 200
	BenchmarkNumVectors = 50000
	BenchmarkK          = 10
	BenchmarkEfSearch   = 50
)

func generateBenchmarkVectors(numVectors, dim int) []types.BatchObject {
	rng := rand.New(rand.NewSource(42))
	objects := make([]types.BatchObject, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, dim)
		norm := float32(0)
		for j := 0; j < dim; j++ {
			vec[j] = rng.Float32()
			norm += vec[j] * vec[j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		if norm > 0 {
			for j := 0; j < dim; j++ {
				vec[j] /= norm
			}
		}
		objects[i] = types.BatchObject{
			Id:     fmt.Sprintf("vec-%d", i),
			Vector: vec,
		}
	}
	return objects
}

func computeEuclideanDistance(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i] - b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}

func bruteForceSearch(query []float32, k int, dataset []types.BatchObject) []types.BatchObject {
	type result struct {
		obj  types.BatchObject
		dist float64
	}
	results := make([]result, len(dataset))
	for i, obj := range dataset {
		results[i] = result{obj: obj, dist: computeEuclideanDistance(query, obj.Vector)}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})
	ret := make([]types.BatchObject, k)
	for i := 0; i < k && i < len(results); i++ {
		ret[i] = results[i].obj
	}
	return ret
}

func computeRecall(retrieved, expected []types.BatchObject) float64 {
	if len(expected) == 0 {
		return 1.0
	}
	expectedSet := make(map[string]bool)
	for _, e := range expected {
		expectedSet[e.Id] = true
	}
	hits := 0
	for _, r := range retrieved {
		if expectedSet[r.Id] {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

func BenchmarkAddBatchWithRecall(b *testing.B) {
	dataset := generateBenchmarkVectors(BenchmarkNumVectors, BenchmarkDim)

	type BatchResult struct {
		writeTime     time.Duration
		qps           float64
		recallAt10    float64
		vectorsPerSec float64
	}

	var results []BatchResult

	for n := 0; n < b.N; n++ {
		b.StopTimer()
		idx, err := New(BenchmarkM, BenchmarkEf, distance.Euclidean, distance.Float32, "", "")
		if err != nil {
			b.Fatalf("Failed to create index: %v", err)
		}
		b.StartTimer()

		startWrite := time.Now()
		err = idx.AddBatch(dataset)
		writeTime := time.Since(startWrite)

		if err != nil {
			b.Fatalf("AddBatch failed: %v", err)
		}

		vectorsPerSec := float64(len(dataset)) / writeTime.Seconds()
		qps := float64(len(dataset)) / writeTime.Seconds()

		numQueries := 100
		queryStartIdx := BenchmarkNumVectors - 1000

		var totalRecall float64
		for i := 0; i < numQueries; i++ {
			queryIdx := queryStartIdx + i
			if queryIdx >= len(dataset) {
				queryIdx = queryIdx % len(dataset)
			}
			query := dataset[queryIdx].Vector

			expected := bruteForceSearch(query, BenchmarkK, dataset)

			searchResults := idx.SearchWithScores(query, BenchmarkK, nil, BenchmarkEfSearch)
			retrieved := make([]types.BatchObject, len(searchResults))
			for j, sr := range searchResults {
				retrieved[j] = types.BatchObject{Id: fmt.Sprintf("vec-%d", sr.DocID)}
			}

			recall := computeRecall(retrieved, expected)
			totalRecall += recall
		}

		avgRecall := totalRecall / float64(numQueries)

		result := BatchResult{
			writeTime:     writeTime,
			qps:           qps,
			recallAt10:    avgRecall,
			vectorsPerSec: vectorsPerSec,
		}
		results = append(results, result)

		b.Logf("Run %d: WriteTime=%v, QPS=%.2f, Recall@10=%.4f, Vectors/sec=%.2f",
			n+1, writeTime, qps, avgRecall, vectorsPerSec)

		b.StopTimer()
		idx.Close()
		b.StartTimer()
	}

	var avgWriteTime time.Duration
	var avgQPS, avgRecall, avgVectorsSec float64
	for _, r := range results {
		avgWriteTime += r.writeTime
		avgQPS += r.qps
		avgRecall += r.recallAt10
		avgVectorsSec += r.vectorsPerSec
	}
	count := float64(len(results))
	b.ReportMetric(float64(avgWriteTime.Nanoseconds()/int64(count)/1e6), "avg_write_time_ms")
	b.ReportMetric(avgQPS/count, "avg_qps")
	b.ReportMetric(avgRecall/count, "avg_recall@10")
	b.ReportMetric(avgVectorsSec/count, "avg_vectors/sec")

	b.Logf("\n=== AGGREGATE RESULTS ===")
	b.Logf("Avg Write Time: %v", avgWriteTime/time.Duration(count))
	b.Logf("Avg QPS: %.2f", avgQPS/count)
	b.Logf("Avg Recall@10: %.4f", avgRecall/count)
	b.Logf("Avg Vectors/sec: %.2f", avgVectorsSec/count)
}

func BenchmarkFederatedAddBatchWithRecall(b *testing.B) {
	dataset := generateBenchmarkVectors(BenchmarkNumVectors, BenchmarkDim)

	type BatchResult struct {
		writeTime     time.Duration
		qps           float64
		recallAt10    float64
		vectorsPerSec float64
	}

	var results []BatchResult

	for n := 0; n < b.N; n++ {
		b.StopTimer()
		idx, err := NewFederatedIndex(BenchmarkM, BenchmarkEf, distance.Euclidean, distance.Float32, 0)
		if err != nil {
			b.Fatalf("Failed to create federated index: %v", err)
		}
		b.StartTimer()

		startWrite := time.Now()
		err = idx.AddBatch(dataset)
		writeTime := time.Since(startWrite)

		if err != nil {
			b.Fatalf("Federated AddBatch failed: %v", err)
		}

		vectorsPerSec := float64(len(dataset)) / writeTime.Seconds()
		qps := float64(len(dataset)) / writeTime.Seconds()

		numQueries := 100
		queryStartIdx := BenchmarkNumVectors - 1000

		var totalRecall float64
		for i := 0; i < numQueries; i++ {
			queryIdx := queryStartIdx + i
			if queryIdx >= len(dataset) {
				queryIdx = queryIdx % len(dataset)
			}
			query := dataset[queryIdx].Vector

			expected := bruteForceSearch(query, BenchmarkK, dataset)

			searchResults := idx.SearchWithScores(query, BenchmarkK, nil, BenchmarkEfSearch)
			retrieved := make([]types.BatchObject, len(searchResults))
			for j, sr := range searchResults {
				extID, _ := idx.GetExternalIDFromDocID(sr.DocID)
				retrieved[j] = types.BatchObject{Id: extID}
			}

			recall := computeRecall(retrieved, expected)
			totalRecall += recall
		}

		avgRecall := totalRecall / float64(numQueries)

		result := BatchResult{
			writeTime:     writeTime,
			qps:           qps,
			recallAt10:    avgRecall,
			vectorsPerSec: vectorsPerSec,
		}
		results = append(results, result)

		b.Logf("Run %d: WriteTime=%v, QPS=%.2f, Recall@10=%.4f, Vectors/sec=%.2f",
			n+1, writeTime, qps, avgRecall, vectorsPerSec)

		b.StopTimer()
		idx.Close()
		b.StartTimer()
	}

	var avgWriteTime time.Duration
	var avgQPS, avgRecall, avgVectorsSec float64
	for _, r := range results {
		avgWriteTime += r.writeTime
		avgQPS += r.qps
		avgRecall += r.recallAt10
		avgVectorsSec += r.vectorsPerSec
	}
	count := float64(len(results))
	b.ReportMetric(float64(avgWriteTime.Nanoseconds()/int64(count)/1e6), "avg_write_time_ms")
	b.ReportMetric(avgQPS/count, "avg_qps")
	b.ReportMetric(avgRecall/count, "avg_recall@10")
	b.ReportMetric(avgVectorsSec/count, "avg_vectors/sec")

	b.Logf("\n=== AGGREGATE RESULTS (Federated) ===")
	b.Logf("Avg Write Time: %v", avgWriteTime/time.Duration(count))
	b.Logf("Avg QPS: %.2f", avgQPS/count)
	b.Logf("Avg Recall@10: %.4f", avgRecall/count)
	b.Logf("Avg Vectors/sec: %.2f", avgVectorsSec/count)
}
