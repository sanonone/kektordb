package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/sanonone/kektordb/pkg/auth"
	"github.com/sanonone/kektordb/pkg/metrics"
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

// Flush delegates to the underlying writer's Flush method.
// Required for Server-Sent Events (SSE) to work through the logging middleware.
func (rw *responseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the underlying writer's Hijack method.
// Required for WebSocket upgrades and some streaming protocols.
func (rw *responseWrapper) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}

// authMiddleware wraps an http.Handler and checks for the Bearer token.
// It implements Role-Based Access Control (RBAC) checking the requested endpoint
// against the token's policy.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Se il Root Token è vuoto E non ci sono altre policy, il server è aperto a tutti.
		// (Per sicurezza in produzione, se non vuoi open-access, cambia questa logica)
		if s.authToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)

		// 1. ROOT TOKEN CHECK (Master Key Bypass)
		if token == s.authToken {
			// Il master token può fare tutto.
			next.ServeHTTP(w, r)
			return
		}

		// 2. RBAC POLICY CHECK
		// Se non è il master token, verifichiamo la policy nel KV Store.
		policy, err := s.authService.VerifyToken(token)
		if err != nil {
			slog.Warn("RBAC Unauthorized access attempt", "error", err, "ip", r.RemoteAddr)
			http.Error(w, "Unauthorized: invalid or revoked token", http.StatusUnauthorized)
			return
		}

		// 3. ENFORCE PERMISSIONS
		// Determiniamo il livello di permesso richiesto in base al path
		requiredRole := auth.RoleRead // Default per GET o /search

		path := r.URL.Path
		method := r.Method

		// Regole basilari: se non è una lettura, è una scrittura.
		// Operazioni di sistema richiedono sempre ruolo Admin.
		if strings.HasPrefix(path, "/system/") || strings.HasPrefix(path, "/auth/") {
			requiredRole = auth.RoleAdmin
		} else if method == http.MethodPost || method == http.MethodPut || method == http.MethodDelete {
			// Eccezioni: le chiamate POST di ricerca sono in realtà "Read"
			if strings.HasSuffix(path, "search") || strings.HasSuffix(path, "search-with-scores") ||
				strings.HasSuffix(path, "get-vectors") || strings.HasSuffix(path, "get-links") ||
				strings.HasSuffix(path, "get-incoming") || strings.HasSuffix(path, "traverse") ||
				strings.HasSuffix(path, "extract-subgraph") || strings.HasSuffix(path, "search-nodes") ||
				strings.HasSuffix(path, "get-node-properties") || strings.HasSuffix(path, "get-edges") ||
				strings.HasSuffix(path, "get-all-relations") || strings.HasSuffix(path, "get-all-incoming") ||
				strings.HasSuffix(path, "find-path") || path == "/rag/retrieve" || strings.HasPrefix(path, "/ui/") {
				requiredRole = auth.RoleRead
			} else {
				requiredRole = auth.RoleWrite
			}
		}

		// Se la policy non è Admin, dobbiamo verificare il Namespace (IndexName)
		if policy.Role != auth.RoleAdmin {
			targetNamespace := extractNamespaceFromRequest(r)

			if !policy.HasAccess(requiredRole, targetNamespace) {
				slog.Warn("RBAC Access Denied", "role", policy.Role, "required", requiredRole, "target", targetNamespace)
				http.Error(w, "Forbidden: insufficient permissions for this namespace/action", http.StatusForbidden)
				return
			}
		}

		// Permesso accordato. Passiamo la richiesta al prossimo handler.
		next.ServeHTTP(w, r)
	})
}

// extractNamespaceFromRequest tenta di leggere l'index_name dal body JSON o dal path.
// Rigenera r.Body affinché possa essere letto nuovamente dagli handler.
func extractNamespaceFromRequest(r *http.Request) string {
	// A. Controllo dal Path (es. /vector/indexes/{name})
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) >= 4 && parts[1] == "vector" && parts[2] == "indexes" {
		// e.g. /vector/indexes/my_index/...
		return parts[3]
	}

	// B. Controllo dal Body (es. POST /vector/actions/add)
	if r.Body != nil && r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return "*" // Fallback se il body è illeggibile
		}

		// Rigeneriamo il body per i prossimi lettori (fondamentale!)
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var payload struct {
			IndexName string `json:"index_name"`
		}

		// Parsing parziale: ignoriamo campi sconosciuti per essere veloci
		if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.IndexName != "" {
			return payload.IndexName
		}
	}

	return "*" // Se non riusciamo a determinarlo, ritorniamo "*" (che fallirà il check se l'utente non ha permessi globali)
}

// defaultMaxBodySize is the maximum request body size in bytes (10 MB).
const defaultMaxBodySize = 10 << 20

// bodySizeLimitMiddleware wraps each request with an http.MaxBytesReader
// to prevent memory exhaustion from oversized payloads (DoS protection).
func (s *Server) bodySizeLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, defaultMaxBodySize)
		next.ServeHTTP(w, r)
	})
}
