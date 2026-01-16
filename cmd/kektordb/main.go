package main

import (
	"context"
	"flag"
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

	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/proxy"
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
	// proxyPort := flag.String("proxy-port", ":9092", "Port for the AI Semantic Proxy")
	// proxyTarget := flag.String("proxy-target", "http://localhost:11434", "Target LLM URL (e.g. Ollama)")
	// firewallIndex := flag.String("firewall-index", "prompt_guard", "Index containing forbidden prompts")
	// proxyConfigPath := flag.String("proxy-config", "", "Path to proxy.yaml config file")
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

	// log.Printf("Initializing Engine (Data: %s, Save: %v/%d ops)...", dataDir, opts.AutoSaveInterval, opts.AutoSaveThreshold)

	// Engine Startup
	eng, err := engine.Open(opts)
	if err != nil {
		log.Fatalf("Failed to open engine: %v", err)
	}

	// Starting HTTP Server
	srv, err := server.NewServer(eng, *httpAddr, *vectorizersConfig, *authToken, dataDir)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		eng.Close()
		os.Exit(1)
	}

	// Avvio Server API in Goroutine
	go func() {
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			slog.Error("API Server crashed", "error", err)
			os.Exit(1) // Se cade il server principale, cade tutto
		}
	}()

	// proxy initialization
	var proxyServer *http.Server

	if *enableProxy {
		var proxyCfg proxy.Config
		var err error

		if *proxyConfigPath != "" {
			proxyCfg, err = proxy.LoadConfig(*proxyConfigPath)
			if err != nil {
				slog.Error("Failed to load proxy config", "error", err)
				eng.Close()
				os.Exit(1)
			}
			slog.Info("Loaded proxy config", "path", *proxyConfigPath)
		} else {
			// Fallback default
			proxyCfg = proxy.DefaultConfig()
			slog.Warn("Using default proxy config (no yaml provided)")
		}

		if proxyCfg.Embedder == nil {
			slog.Info("Initializing Proxy Embedder",
				"type", proxyCfg.EmbedderType,
				"url", proxyCfg.EmbedderURL,
				"model", proxyCfg.EmbedderModel,
			)

			switch proxyCfg.EmbedderType {
			case "openai", "openai_compatible":
				proxyCfg.Embedder = embeddings.NewOllamaEmbedder(
					proxyCfg.EmbedderURL,
					proxyCfg.EmbedderModel,
					proxyCfg.EmbedderTimeout,
				)
			default:
				proxyCfg.Embedder = embeddings.NewOllamaEmbedder(
					proxyCfg.EmbedderURL,
					proxyCfg.EmbedderModel,
					proxyCfg.EmbedderTimeout,
				)
			}
		}

		// Init Proxy Handler
		proxyHandler, err := proxy.NewAIProxy(proxyCfg, eng)
		if err != nil {
			slog.Error("Failed to create Proxy", "error", err)
		} else {
			// wrap the proxy in an http.Server so we can close it gracefully
			proxyServer = &http.Server{
				Addr:    proxyCfg.Port,
				Handler: proxyHandler,
			}

			go func() {
				slog.Info("AI Proxy listening", "addr", proxyCfg.Port, "target", proxyCfg.TargetURL)
				if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("Proxy Server crashed", "error", err)
				}
			}()
		}
	}

	// GRACEFUL SHUTDOWN LOGIC

	// Channel to intercept stop signals (CTRL+C, Docker Stop)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// Hold until signal arrives
	<-stopChan

	slog.Info("\nShutting down KektorDB...")

	// context with timeout (e.g. 10 seconds)
	// If shutdown takes more than 10s, force exit.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A. Stop Proxy (active)
	if proxyServer != nil {
		slog.Info("Stopping Proxy...")
		if err := proxyServer.Shutdown(ctx); err != nil {
			slog.Error("Proxy forced shutdown", "error", err)
		}
	}

	// B. Stop API Server (The vectorizers stop here)
	slog.Info("Stopping API Server...")
	srv.Shutdown()

	// C. Stop Engine (Final flush on disc)
	// important to avoid losing data
	slog.Info("Closing Engine (Flushing data)...")
	if err := eng.Close(); err != nil {
		slog.Error("Error closing engine", "error", err)
	} else {
		slog.Info("Engine closed successfully.")
	}

	slog.Info("Bye.")
}
