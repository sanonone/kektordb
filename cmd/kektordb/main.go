// Package main starts the KektorDB server with configurable options.
//
// It parses command-line flags, initializes the server with persistence (AOF),
// snapshot policies, and vectorizer configuration, then runs the REST API.
// Graceful shutdown is supported via SIGINT/SIGTERM.
//
// Usage:
//   kektordb --http-addr :9091 --aof-path data.aof --save "60 1000"
//
// Flags:
//   --http-addr              HTTP address for the REST API (default: :9091)
//   --aof-path               Path to the Append-Only File for persistence
//   --save                   Snapshot policy: "seconds writes" (e.g., "60 1000")
//   --aof-rewrite-percentage Percentage growth to trigger AOF rewrite (0 to disable)
//   --vectorizers-config     Path to YAML config for automatic vectorizers

package main

import (
	"errors"
	"flag"
	"github.com/sanonone/kektordb/internal/server"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Command-line flags with default values and help descriptions
	httpAddr := flag.String("http-addr", ":9091", "HTTP address and port for the REST API (e.g., :9091 or 127.0.0.1:8080)")
	aofPath := flag.String("aof-path", "kektordb.aof", "Path to the Append-Only File (AOF) for data persistence")
	savePolicy := flag.String("save", "60 1000", "Auto-snapshot policy: \"seconds writes\". Use empty string to disable.")
	aofRewritePercentage := flag.Int("aof-rewrite-percentage", 100, "Rewrite AOF when it grows by this percentage. Set 0 to disable.")
	vectorizersConfigPath := flag.String("vectorizers-config", "", "Path to YAML configuration file for vectorizers")

	flag.Parse() // Parse flags into the variables above

	// Initialize the server with configuration
	srv, err := server.NewServer(*aofPath, *savePolicy, *aofRewritePercentage, *vectorizersConfigPath)
	if err != nil {
		log.Fatalf("Impossibile creare il server: %v", err)
	}

	// Channel to receive OS shutdown signals (Ctrl+C, SIGTERM)
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in a goroutine to avoid blocking main
	go func() {
		log.Printf("Starting KektorDB server: REST API on %s, AOF at %s", *httpAddr, *aofPath)
		// log.Fatal(srv.Run(*httpAddr)) // avvia il server con i parametri dell'utente
		if err := srv.Run(*httpAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Block until shutdown signal is received
	<-shutdownChan

	// Perform clean shutdown (flush AOF, stop vectorizers, etc.)
	srv.Shutdown()
	log.Println("Server stopped.")
}
