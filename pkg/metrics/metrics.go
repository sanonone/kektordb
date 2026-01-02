package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Define global variables for metrics.
// We use 'promauto' which automatically registers metrics without complex initialization.

var (
	// 1. HTTP Requests Total (Counter)
	// Counts how many requests arrive, labeled by method, path, and status code.
	HttpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kektordb_http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"}, // Labels
	)

	// 2. HTTP Request Duration (Histogram)
	// Measures server response time.
	// Critical for monitoring if HyDe is slowing things down.
	HttpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "kektordb_http_request_duration_seconds",
			Help: "Duration of HTTP requests in seconds",
			// Custom buckets covering from microseconds (cache hit) to seconds (LLM generation)
			Buckets: []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"method", "path"},
	)

	// 3. Vector Count (Gauge)
	// Tracks the total number of vectors indexed.
	TotalVectors = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kektordb_vectors_total",
			Help: "Total number of indexed vectors",
		},
		[]string{"index_name"},
	)
)
