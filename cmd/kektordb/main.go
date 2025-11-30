package main

import (
	"errors"
	"flag"
	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/engine"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Flags
	httpAddr := flag.String("http-addr", ":9091", "HTTP address")
	aofPath := flag.String("aof-path", "kektordb.aof", "Path to AOF file")
	savePolicy := flag.String("save", "60 1000", "Auto-snapshot policy: 'seconds changes'")
	aofRewritePerc := flag.Int("aof-rewrite-percentage", 100, "Rewrite AOF at X% growth")
	vectorizersConfig := flag.String("vectorizers-config", "", "Vectorizers YAML config")
	flag.Parse()

	// Engine Configuration
	dataDir := filepath.Dir(*aofPath)
	aofName := filepath.Base(*aofPath)

	opts := engine.DefaultOptions(dataDir)
	opts.AofFilename = aofName
	opts.AofRewritePercentage = *aofRewritePerc

	// "60 1000" = 60s e 1000 changes.
	if *savePolicy == "" {
		opts.AutoSaveInterval = 0
		opts.AutoSaveThreshold = 0
		log.Println("Auto-save is DISABLED")
	} else {
		parts := strings.Fields(*savePolicy)
		if len(parts) >= 2 {
			sec, _ := strconv.Atoi(parts[0])
			chg, _ := strconv.ParseInt(parts[1], 10, 64)
			opts.AutoSaveInterval = time.Duration(sec) * time.Second
			opts.AutoSaveThreshold = chg
		}
	}

	log.Printf("Initializing Engine (Data: %s, Save: %v/%d ops)...", dataDir, opts.AutoSaveInterval, opts.AutoSaveThreshold)

	// Engine Startup
	eng, err := engine.Open(opts)
	if err != nil {
		log.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close() // Close on exit

	// Starting HTTP Server
	srv, err := server.NewServer(eng, *httpAddr, *vectorizersConfig)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Graceful Shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("KektorDB is ready on %s", *httpAddr)
		if err := srv.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Server error: %v", err)
		}
	}()

	<-shutdownChan
	srv.Shutdown()
	log.Println("KektorDB stopped.")
}
