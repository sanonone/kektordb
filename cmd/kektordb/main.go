package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpi "github.com/sanonone/kektordb/internal/mcp"
	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/internal/setup"
	"github.com/sanonone/kektordb/internal/tui"
	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/compiler"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/llm"
	"github.com/sanonone/kektordb/pkg/proxy"
)

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// logEmbedderDimension probes the embedder to discover and log its vector dimension.
// Makes dimension mismatches immediately visible at boot, preventing the
// silent data corruption that occurs when switching embedders mid-session.
func logEmbedderDimension(emb embeddings.Embedder) {
	if _, ok := emb.(embeddings.NoopEmbedder); ok {
		return
	}
	probe, err := emb.Embed("kektor-dim-probe")
	if err != nil {
		slog.Warn("Embedder probe failed", "error", err)
		return
	}
	slog.Info("Embedder ready", "dim", len(probe))
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
	// Handle subcommands before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			cmdSetup()
			return
		case "--help", "-h":
			printUsage()
			return
		}
	}

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
	cognitiveConfig := flag.String("cognitive-config", "", "Cognitive engine YAML config (enables Gardener)")

	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")

	// Flags proxy
	enableProxy := flag.Bool("enable-proxy", false, "Enable the AI Semantic Proxy")
	// proxyPort := flag.String("proxy-port", ":9092", "Port for the AI Semantic Proxy")
	// proxyTarget := flag.String("proxy-target", "http://localhost:11434", "Target LLM URL (e.g. Ollama)")
	// firewallIndex := flag.String("firewall-index", "prompt_guard", "Index containing forbidden prompts")
	// proxyConfigPath := flag.String("proxy-config", "", "Path to proxy.yaml config file")
	proxyConfigPath := flag.String("proxy-config", "", "Path to proxy.yaml config file")

	modeMCP := flag.Bool("mcp", false, "Run as MCP Server (Stdio)")

	toolsFlag := flag.String("tools", "all", "MCP tool profile: all, agent, admin, or comma-separated tool names")

	embedderModeFlag := flag.String("embedder", "auto", "Embedder mode: auto, ollama, openai, local")
	embedderModelFlag := flag.String("embedder-model", "", "Path to directory with ONNX model and tokenizer (local mode)")

	modeTUI := flag.Bool("tui", false, "Launch terminal dashboard")

	flag.Parse()

	// SETUP LOGGER
	setupLogger(*logLevel)
	slog.Info("Starting KektorDB...", "version", "v0.6.0", "log_level", *logLevel)

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

	// TUI mode: start server in background, launch terminal dashboard.
	if *modeTUI {
		embCfg := embeddings.EmbedderConfig{
			Mode:        *embedderModeFlag,
			OllamaURL:   "http://localhost:11434/api/embeddings",
			OllamaModel: "nomic-embed-text",
			ModelDir:    *embedderModelFlag,
		}
		emb, _ := embeddings.SelectEmbedder(embCfg, dataDir)
		if emb == nil {
			emb = embeddings.NoopEmbedder{}
		}
		runTUI(*httpAddr, eng, *vectorizersConfig, *authToken, dataDir, *cognitiveConfig, *enableProxy, proxyConfigPath, emb)
		return
	}

	if *modeMCP {
		// Redirect logs to file to keep Stdio clean for JSON-RPC
		logFile, err := os.OpenFile("kektordb_mcp.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			log.Printf("Warning: failed to open log file: %v", err)
			// Continue with default logging to stderr
		} else {
			slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
			defer logFile.Close()
		}

		// Ensure engine is closed on exit to flush AOF and release resources
		defer func() {
			if err := eng.Close(); err != nil {
				slog.Error("Error closing engine", "err", err)
			}
		}()

		// MCP needs an embedder. Use auto-select with graceful degradation.
		embedderCfg := embeddings.EmbedderConfig{
			Mode:        *embedderModeFlag,
			OllamaURL:   getEnv("MCP_EMBEDDER_URL", "http://localhost:11434/api/embeddings"),
			OllamaModel: getEnv("MCP_EMBEDDER_MODEL", "nomic-embed-text"),
			ModelDir:    *embedderModelFlag,
		}
		embedder, embedderErr := embeddings.SelectEmbedder(embedderCfg, dataDir)
		if embedderErr != nil {
			slog.Warn("No embedder available, MCP semantic tools will return errors", "err", embedderErr)
			slog.Warn("Install Ollama or rebuild with -tags rust for built-in embedding.")
			embedder = embeddings.NoopEmbedder{}
		}
		logEmbedderDimension(embedder)

		// --- Cognitive Engine & LLM setup ---
		// Load from cognitive.yaml first (if available), then override with env vars.
		gardenerCfg, llmCfg, cfgErr := cognitive.LoadConfig(*cognitiveConfig)
		if cfgErr != nil {
			slog.Warn("Cognitive config load failed, using defaults", "err", cfgErr)
		}

		// Env var overrides for LLM settings (MCP_LLM_*)
		if url := getEnv("MCP_LLM_URL", ""); url != "" {
			llmCfg.BaseURL = url
			llmCfg.Model = getEnv("MCP_LLM_MODEL", llmCfg.Model)
			llmCfg.APIKey = getEnv("MCP_LLM_API_KEY", llmCfg.APIKey)
			// Implicitly enable Gardener if LLM env vars are set without YAML
			if !gardenerCfg.Enabled {
				gardenerCfg = cognitive.DefaultConfig()
				gardenerCfg.Enabled = true
			}
		}

		// Create LLM client if advanced/meta mode and LLM is configured
		var brain llm.Client
		var gardener *cognitive.Gardener
		if gardenerCfg.Enabled {
			if gardenerCfg.Mode == "advanced" || gardenerCfg.Mode == "meta" {
				if llmCfg.BaseURL == "" {
					slog.Warn("Cognitive Engine: advanced/meta mode requires an LLM, falling back to basic")
					gardenerCfg.Mode = "basic"
				} else {
					brain = llm.NewClient(llmCfg)
					slog.Info("Cognitive Engine: LLM client created", "url", llmCfg.BaseURL, "model", llmCfg.Model)
				}
			}
			gardener = cognitive.NewGardener(eng, brain, gardenerCfg)
			gardener.Start()
			defer gardener.Stop()
			slog.Info("Cognitive Engine: Gardener started", "mode", gardenerCfg.Mode, "interval", gardenerCfg.Interval)
		} else {
			slog.Info("Cognitive Engine: Gardener NOT started (no config). " +
				"Pass --cognitive-config=<path> or set MCP_LLM_URL to enable cognitive features.")
		}

		// Init Compiler (with LLM, or nil if no LLM configured)
		comp := compiler.NewCompiler(eng, brain, embedder)

		// Init Artifact Watcher (requires Gardener lifecycle hooks)
		if gardener != nil {
			compiler.NewWatcher(comp, eng, &gardenerCfg, gardenerCfg.TargetIndexes)
		}

		// Init MCP Server
		allowlist := mcpi.ResolveTools(*toolsFlag)
		mcpSrv := mcpi.NewMCPServer(eng, embedder, allowlist, comp, gardener)

		// Create a Stdio transport
		// Note from docs/server.md: "The server will have the 'tools' capability if any tool is added..."
		// We can just use the server instance we created.

		// Serve on Stdio
		// Protocol: JSON-RPC 2.0 over Stdio
		slog.Info("Starting MCP Server on Stdio...")
		// var t mcp.Transport = &mcp.StdioTransport{} // Use pointer to satisfy interface if needed, or check SDK
		// Using the example pattern:
		// t := &mcp.LoggingTransport{Transport: &mcp.StdioTransport{}, Writer: logFile} // Optional logging transport

		// Direct Stdio transport
		stdioTransport := &mcp.StdioTransport{}

		// Run the server
		if err := mcpSrv.Run(context.Background(), stdioTransport); err != nil {
			slog.Error("MCP Server Error", "err", err)
			os.Exit(1)
		}
		return
	}

	// Select embedder for HTTP mode (shared with compiler)
	embedderCfg := embeddings.EmbedderConfig{
		Mode:        *embedderModeFlag,
		OllamaURL:   getEnv("EMBEDDER_URL", "http://localhost:11434/api/embeddings"),
		OllamaModel: getEnv("EMBEDDER_MODEL", "nomic-embed-text"),
		ModelDir:    *embedderModelFlag,
	}
	embedder, embedderErr := embeddings.SelectEmbedder(embedderCfg, dataDir)
	if embedderErr != nil {
		slog.Warn("No embedder available for semantic features",
			"err", embedderErr, "hint", "Install Ollama or rebuild with -tags rust")
		embedder = embeddings.NoopEmbedder{}
	}
	logEmbedderDimension(embedder)

	// Starting HTTP Server
	srv, err := server.NewServer(eng, *httpAddr, *vectorizersConfig, *authToken, dataDir, *cognitiveConfig, embedder)
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
			embCfg := embeddings.EmbedderConfig{
				Mode:    proxyCfg.EmbedderType,
				URL:     proxyCfg.EmbedderURL,
				Model:   proxyCfg.EmbedderModel,
				APIKey:  proxyCfg.EmbedderAPIKey,
				Timeout: proxyCfg.EmbedderTimeout,
			}
			var err error
			proxyCfg.Embedder, err = embeddings.SelectEmbedder(embCfg, dataDir)
			if err != nil {
				slog.Warn("Proxy embedder creation failed, continuing without embedder", "err", err)
				proxyCfg.Embedder = embeddings.NoopEmbedder{}
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

// cmdSetup handles the "kektordb setup" subcommand.
func cmdSetup() {
	agents := setup.SupportedAgents()

	// Extract optional --embedder= flag from remaining args (after agent name)
	embedderMode := ""
	for i := 3; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--embedder=") {
			embedderMode = strings.TrimPrefix(os.Args[i], "--embedder=")
			break
		}
		if os.Args[i] == "--embedder" && i+1 < len(os.Args) {
			embedderMode = os.Args[i+1]
			break
		}
	}

	// Non-interactive: kektordb setup <agent> [--embedder=<mode>]
	if len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		agentName := os.Args[2]
		switch agentName {
		case "list":
			fmt.Println("Supported agents:")
			for _, a := range agents {
				fmt.Printf("  %-15s %s\n", a.Name, a.Description)
			}
			return
		case "status":
			cmdSetupStatus(agents)
			return
		default:
			result, err := setup.Install(agentName, embedderMode)
			if err != nil {
				fmt.Fprintf(os.Stderr, "setup failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ Installed %s plugin (%d files)\n", result.Agent, result.Files)
			fmt.Printf("  → %s\n", result.Destination)
			printPostInstall(result)
			return
		}
	}

	// Interactive menu (kektordb setup without arguments)
	fmt.Println("kektordb setup — Install agent plugin")
	fmt.Println()
	fmt.Println("Which agent do you want to set up?")
	fmt.Println()
	for i, a := range agents {
		fmt.Printf("  [%d] %s\n", i+1, a.Description)
		fmt.Printf("      Install to: %s\n\n", a.InstallDir)
	}
	fmt.Printf("Enter choice (1-%d): ", len(agents))

	var input string
	fmt.Scanln(&input)
	choice, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || choice < 1 || choice > len(agents) {
		fmt.Fprintln(os.Stderr, "Invalid choice.")
		os.Exit(1)
	}

	selected := agents[choice-1]
	result, err := setup.Install(selected.Name, embedderMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Installed %s plugin (%d files)\n", result.Agent, result.Files)
	fmt.Printf("  → %s\n", result.Destination)
	printPostInstall(result)
}

func cmdSetupStatus(agents []setup.Agent) {
	found := 0
	for _, a := range agents {
		// Check if config file/dir exists (heuristic: look for the install dir)
		dir := a.InstallDir
		if _, err := os.Stat(dir); err == nil {
			fmt.Printf("  ✅ %-15s found at %s\n", a.Name, dir)
			found++
		} else if _, err := os.Stat(dir + "kektordb.ts"); err == nil {
			// Special case for OpenCode plugin
			fmt.Printf("  ✅ %-15s found at %s\n", a.Name, dir)
			found++
		}
	}
	if found == 0 {
		fmt.Println("No agents configured. Run 'kektordb setup <agent>' to get started.")
	}
}

func printPostInstall(result *setup.Result) {
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Restart your AI agent (Claude Code / Cursor / Gemini CLI / OpenCode)")
	fmt.Println("  2. The agent will now have access to KektorDB memory tools")
	fmt.Println()
	fmt.Println("To verify, ask your agent: 'What memory tools are available?'")
}

func printUsage() {
	fmt.Println("KektorDB — Cognitive memory layer for AI agents")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  kektordb [flags]                 Start the server")
	fmt.Println("  kektordb setup [agent]            Configure MCP for an AI agent")
	fmt.Println("  kektordb setup list               List supported agents")
	fmt.Println("  kektordb setup status             Show configured agents")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --http-addr        HTTP address (default :9091)")
	fmt.Println("  --aof-path         Path to AOF file")
	fmt.Println("  --save             Auto-snapshot policy")
	fmt.Println("  --auth-token       API authentication token")
	fmt.Println("  --log-level        Log level: debug, info, warn, error")
	fmt.Println("  --embedder         Embedder mode: auto, ollama, openai, local (default auto)")
	fmt.Println("  --embedder-model   Path to directory with ONNX model (local mode)")
	fmt.Println("  --mcp              Run as MCP Server (Stdio)")
	fmt.Println("  --tools            MCP tool profile: all, agent, admin (default all)")
	fmt.Println("  --enable-proxy     Enable the AI Semantic Proxy")
	fmt.Println()
	fmt.Println("Setup Subcommands:")
	fmt.Println("  kektordb setup                   Interactive menu")
	fmt.Println("  kektordb setup <agent>           Directly configure an agent")
	fmt.Println("  kektordb setup list              List supported agents")
	fmt.Println("  kektordb setup status            Show configured agents")
}

// runTUI starts the HTTP server in a background goroutine and launches
// the Bubble Tea terminal dashboard in the foreground.
func runTUI(httpAddr string, eng *engine.Engine, vectorizersConfig, authToken, dataDir, cognitiveConfig string, enableProxy bool, proxyConfigPath *string, emb embeddings.Embedder) {
	// Redirect ALL logs to discard — nothing must touch stdout/stderr
	// while the TUI owns the terminal.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Start HTTP server in background.
	srv, err := server.NewServer(eng, httpAddr, vectorizersConfig, authToken, dataDir, cognitiveConfig, emb)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	go func() {
		slog.Info("TUI: HTTP server listening", "addr", httpAddr)
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "err", err)
		}
	}()

	// Optional proxy
	if enableProxy && proxyConfigPath != nil && *proxyConfigPath != "" {
		// Proxy startup omitted for TUI mode — API server is enough.
		slog.Warn("TUI: proxy not supported in TUI mode")
	}

	// Handle graceful shutdown — idempotent via sync.Once so signal handler
	// and clean-exit path don't race on double Shutdown()/Close().
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			slog.Info("TUI: shutting down...")
			srv.Shutdown()
			eng.Close()
		})
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		shutdown()
		os.Exit(0)
	}()

	// Launch TUI (blocks until user quits with q/Ctrl-C).
	slog.Info("TUI: launching terminal dashboard")
	if err := tui.RunTUI(httpAddr); err != nil {
		log.Fatalf("TUI error: %v", err)
	}

	// Clean exit
	shutdown()
}
