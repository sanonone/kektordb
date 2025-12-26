package main

import (
	"errors"
	"flag"
	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/proxy"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// setupLogger configures the global slog logger based on the level.
func setupLogger(levelStr string) {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Create a text handler (human readable).
	// Use JSONHandler if you want machine-readable logs for Prometheus/Grafana later.
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		// AddSource: true, // Uncomment to see file:line in logs (useful for debug)
	})

	logger := slog.New(handler)
	slog.SetDefault(logger) // Set as global default
}

func main() {
	defaultAddr := getEnv("KEKTOR_PORT", ":9091")
	if !strings.HasPrefix(defaultAddr, ":") && !strings.Contains(defaultAddr, ":") {
		defaultAddr = ":" + defaultAddr
	}

	defaultAof := getEnv("KEKTOR_DATA_DIR", "kektordb.aof")
	if info, err := os.Stat(defaultAof); err == nil && info.IsDir() {
		defaultAof = filepath.Join(defaultAof, "kektordb.aof")
	}

	defaultAuth := getEnv("KEKTOR_TOKEN", "")

	// Flags
	httpAddr := flag.String("http-addr", ":9091", "HTTP address")
	aofPath := flag.String("aof-path", "kektordb.aof", "Path to AOF file")
	savePolicy := flag.String("save", "60 1000", "Auto-snapshot policy: 'seconds changes'")
	authToken := flag.String("auth-token", defaultAuth, "API Authentication Token (empty = disabled)")
	aofRewritePerc := flag.Int("aof-rewrite-percentage", 100, "Rewrite AOF at X% growth")
	vectorizersConfig := flag.String("vectorizers-config", "", "Vectorizers YAML config")

	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")

	// Flags proxy
	enableProxy := flag.Bool("enable-proxy", false, "Enable the AI Semantic Proxy")
	proxyPort := flag.String("proxy-port", ":9092", "Port for the AI Semantic Proxy")
	proxyTarget := flag.String("proxy-target", "http://localhost:11434", "Target LLM URL (e.g. Ollama)")
	firewallIndex := flag.String("firewall-index", "prompt_guard", "Index containing forbidden prompts")
	proxyConfigPath := flag.String("proxy-config", "", "Path to proxy.yaml config file")

	flag.Parse()

	// SETUP LOGGER
	setupLogger(*logLevel)
	slog.Info("Starting KektorDB...", "version", "v0.4.0", "log_level", *logLevel)

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
	srv, err := server.NewServer(eng, *httpAddr, *vectorizersConfig, *authToken)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Graceful Shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("KektorDB is ready on %s", *httpAddr)

		if *authToken != "" {
			log.Println("Security: API Authentication ENABLED")
		} else {
			log.Println("Security: API Authentication DISABLED (Dev Mode)")
		}

		if err := srv.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Server error: %v", err)
		}
	}()

	// --- AVVIO PROXY (Se abilitato) ---
	if *enableProxy {
		go func() {
			log.Printf("Initializing AI Proxy...")

			// 1. Carica Configurazione
			var proxyCfg proxy.Config
			var err error

			if *proxyConfigPath != "" {
				proxyCfg, err = proxy.LoadConfig(*proxyConfigPath)
				if err != nil {
					log.Printf("❌ Failed to load proxy config: %v", err)
					return
				}
				log.Printf("   Loaded config from %s", *proxyConfigPath)
			} else {
				// Fallback: Default + Flag overrides (retro-compatibilità rapida)
				proxyCfg = proxy.DefaultConfig()
				if *proxyPort != "" {
					proxyCfg.Port = *proxyPort
				}
				if *proxyTarget != "" {
					proxyCfg.TargetURL = *proxyTarget
				}
				if *firewallIndex != "" {
					proxyCfg.FirewallIndex = *firewallIndex
				}
				// Attiva feature se i flag base sono usati
				proxyCfg.FirewallEnabled = true
				proxyCfg.CacheEnabled = true

				// Nota: In un refactor futuro, meglio pulire i flag e usare solo il file YAML
				// per configurazioni complesse come l'embedder del proxy.

				// Dobbiamo configurare l'embedder manualmente se usiamo i flag
				embedder := embeddings.NewOllamaEmbedder(proxyCfg.EmbedderURL, proxyCfg.EmbedderModel, proxyCfg.EmbedderTimeout)
				proxyCfg.Embedder = embedder
			}

			// Se abbiamo caricato da file, l'embedder va inizializzato
			if proxyCfg.Embedder == nil {
				proxyCfg.Embedder = embeddings.NewOllamaEmbedder(proxyCfg.EmbedderURL, proxyCfg.EmbedderModel, proxyCfg.EmbedderTimeout)
			}

			log.Printf("   Proxy listening on %s -> %s", proxyCfg.Port, proxyCfg.TargetURL)

			proxy, err := proxy.NewAIProxy(proxyCfg, eng)
			if err != nil {
				log.Printf("❌ Failed to create Proxy: %v", err)
				return
			}

			if err := http.ListenAndServe(proxyCfg.Port, proxy); err != nil {
				log.Printf("❌ Proxy server stopped: %v", err)
			}
		}()
	}

	<-shutdownChan
	srv.Shutdown()
	log.Println("KektorDB stopped.")
}
