package server

import (
	"encoding/json"
	"github.com/sanonone/kektordb/pkg/metrics"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"
)

// RecoveryMiddleware catches panics, logs the stack trace, and returns a 500 error.
// It ensures the server remains stable even if a handler crashes.
func (s *Server) RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// 1. Log the critical error with stack trace
				slog.Error("CRITICAL: Panic recovered in HTTP handler",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)

				// 2. Return a generic 500 error to the client (hide internals)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Internal Server Error",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs incoming requests AND records Prometheus metrics with their duration and status.
// This replaces scatter-shot logging in handlers.
func (s *Server) LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap ResponseWriter to capture status code
		wrapped := &responseWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)

		// Structured log
		slog.Info("HTTP Request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", duration.String(),
			"ip", r.RemoteAddr,
		)

		// 2. Metrics (for machines)
		// Record the duration in the histogram
		metrics.HttpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())

		// Increment the request counter
		// Use strconv.Itoa to convert 200 to "200"
		metrics.HttpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(wrapped.statusCode)).Inc()
	})
}

// responseWrapper is a helper to capture the status code
type responseWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
