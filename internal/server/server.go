package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sanonone/kektordb/pkg/engine"
)

// Server holds the HTTP interface and the underlying Database Engine.
type Server struct {
	Engine *engine.Engine

	httpServer *http.Server

	taskManager       *TaskManager
	vectorizerConfig  *Config
	vectorizerService *VectorizerService
	authToken         string
}

// NewServer initializes the HTTP server using an existing Engine.
// Note: The Engine must be initialized (Open) before passing it here.
func NewServer(eng *engine.Engine, httpAddr string, vectorizersConfigPath string, authToken string) (*Server, error) {

	// Load Vectorizer Configuration
	vecConfig, err := LoadVectorizersConfig(vectorizersConfigPath)
	if err != nil {
		return nil, err
	}
	if len(vecConfig.Vectorizers) > 0 {
		log.Printf("Loaded %d Vectorizer configurations", len(vecConfig.Vectorizers))
	}

	s := &Server{
		Engine:           eng,
		taskManager:      NewTaskManager(),
		vectorizerConfig: vecConfig,
		authToken:        authToken,
	}

	// Initialize Vectorizer Service
	vecService, err := NewVectorizerService(s)
	if err != nil {
		log.Printf("WARNING: Vectorizer service failed to start: %v", err)
	}
	s.vectorizerService = vecService

	// Setup HTTP
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux)

	// Chain middlewares: Recovery -> Logging -> Auth -> Mux
	// Order matters! Recovery must be outer-most to catch everything.

	var handler http.Handler = mux

	// 1. Auth (Inner)
	handler = s.authMiddleware(handler)

	// 2. Logging (Middle) - Logs duration and status
	handler = s.LoggingMiddleware(handler)

	// 3. Recovery (Outer) - Catches panics
	handler = s.RecoveryMiddleware(handler)

	rootMux := http.NewServeMux()
	rootMux.HandleFunc("GET /healthz", s.handleHealthz)
	rootMux.Handle("/", handler)
	s.httpServer = &http.Server{
		Addr:    httpAddr,
		Handler: rootMux,
	}

	return s, nil
}

// Run starts the HTTP server.
// Unlike before, it does NOT handle DB loading (Engine does that).
func (s *Server) Run() error {
	// Start Vectorizers if present
	if s.vectorizerService != nil {
		s.vectorizerService.Start()
	}

	log.Printf("HTTP server listening on %s", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server startup failed: %w", err)
	}
	return nil
}

// Shutdown stops the HTTP server and the Vectorizer service.
// It does NOT close the Engine (main.go handles that for proper lifecycle management).
func (s *Server) Shutdown() {
	log.Println("Starting graceful shutdown of HTTP Server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if s.vectorizerService != nil {
		s.vectorizerService.Stop()
	}
}
