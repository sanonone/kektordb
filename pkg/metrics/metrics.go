package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Definiamo le variabili globali per le metriche.
// Usiamo 'promauto' che registra automaticamente le metriche senza bisogno di init complessi.

var (
	// 1. HTTP Requests Total (Counter)
	// Conta quante richieste arrivano, divise per metodo (GET, POST), percorso e status code.
	HttpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kektordb_http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"}, // Etichette (Labels)
	)

	// 2. HTTP Request Duration (Histogram)
	// Misura quanto tempo ci mette il server a rispondere.
	// Fondamentale per vedere se HyDe sta rallentando tutto.
	HttpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "kektordb_http_request_duration_seconds",
			Help: "Duration of HTTP requests in seconds",
			// Buckets personalizzati per coprire da microsecondi (cache hit) a secondi (LLM generation)
			Buckets: []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"method", "path"},
	)

	// 3. Vector Count (Gauge)
	// Tiene traccia di quanti vettori abbiamo in totale.
	TotalVectors = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kektordb_vectors_total",
			Help: "Total number of indexed vectors",
		},
		[]string{"index_name"},
	)
)
