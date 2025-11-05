# --- Variabili di Configurazione ---
VERSION ?= $(shell git describe --tags --abbrev=0 || echo "v0.0.0")
BINARY_NAME=kektordb
RELEASE_DIR=release

# --- Target Principali ---
.PHONY: all test test-rust bench bench-rust clean release

# Il target di default è un test veloce della build Go-pura
all: test

# Esegue la versione Go-pura
run:
	@echo "==> Running KektorDB (Go-pure implementation)..."
	@go run ./cmd/kektordb/ $(ARGS)

# Esegue la versione ottimizzata con Rust
run-rust: build-rust-native
	@echo "==> Running KektorDB (Rust CGO implementation)..."
	# Impostiamo le variabili d'ambiente del linker qui
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release" \
	go run -tags rust ./cmd/kektordb/ $(ARGS)

# --- Target di Test e Benchmark ---
test: generate-avo
	@echo "==> Running Go tests (Go/AVO implementation)..."
	@go test -short -v ./...

test-rust: build-rust-native
	@echo "==> Running Go tests (Rust CGO implementation)..."
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release" \
	go test -tags rust -short -v ./...

bench: generate-avo
	@echo "==> Running Go benchmarks (Go/AVO implementation)..."
	@go test -bench=. ./...

bench-rust: build-rust-native
	@echo "==> Running Go benchmarks (Rust CGO implementation)..."
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release" \
	go test -tags rust -bench=. ./...

# --- Target di Build ---
# Compila Rust per la piattaforma nativa corrente
build-rust-native:
	@echo "==> Building Rust compute library (native)..."
	@cd native/compute && cargo build --release

# Compila Rust per un target specifico (usato dalla release)
build-rust-target:
	@echo "==> Building Rust compute library for target: $(TARGET)..."
	@cd native/compute && cargo build --release --target=$(TARGET)

generate-avo:
	@echo "==> Generating Assembly code with AVO..."
	@go generate ./pkg/core/distance/avo_gen.go


# --- Logica Condizionale per LDFLAGS (al livello corretto) ---
# Questa sezione viene valutata da 'make' prima di eseguire qualsiasi regola.

# Flag di base per linkare la nostra libreria Rust
#LDFLAGS_BASE = -L$(CURDIR)/native/compute/target/$(TARGET)/release -lkektordb_compute

# Definisci i flag extra in base a GOOS
#ifeq ($(GOOS),linux)
#	LDFLAGS_EXTRA = -ldl -lm -lgcc_s -lc -lpthread
#else ifeq ($(GOOS),darwin)
#	LDFLAGS_EXTRA = -ldl -lm
#else ifeq ($(GOOS),windows)
    # --- CORREZIONE QUI: Aggiungi le librerie di sistema di Windows ---
#	LDFLAGS_EXTRA = -lws2_32 -luserenv -ladvapi32 -lbcrypt -lntdll 
#else
	# Windows non ha bisogno di flag extra
#	LDFLAGS_EXTRA =
#endif

# --- Target di Release ---
# Questo è il comando principale che verrà eseguito da GitHub Actions
release: clean
	@echo "Building releases for all targets..."
	@mkdir -p $(RELEASE_DIR)
	# Per linux, ora passiamo un flag speciale al linker
	# Linux AMD64
	@make release-build TARGET=x86_64-unknown-linux-gnu ZIG_TARGET=x86_64-linux-gnu \
	GOOS=linux GOARCH=amd64 \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-unknown-linux-gnu/release -lkektordb_compute -ldl -lm -lgcc_s -lc -lpthread"

	# Linux ARM64
	@make release-build TARGET=aarch64-unknown-linux-gnu ZIG_TARGET=aarch64-linux-gnu \
	GOOS=linux GOARCH=arm64 \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/aarch64-unknown-linux-gnu/release -lkektordb_compute -ldl -lm -lgcc_s -lc -lpthread"

	# Windows AMD64
	@make release-build TARGET=x86_64-pc-windows-gnu ZIG_TARGET=x86_64-windows-gnu \
	GOOS=windows GOARCH=amd64 EXT=.exe \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-pc-windows-gnu/release -lkektordb_compute -lws2_32 -luserenv -ladvapi32 -lbcrypt -lntdll -lgcc_s"

	# --- macOS (Go Puro) ---
	# target release-build-pure. 
	@make release-build-pure GOOS=darwin GOARCH=amd64
	@make release-build-pure GOOS=darwin GOARCH=arm64

	# macOS AMD64
	# @make release-build TARGET=x86_64-apple-darwin ZIG_TARGET=x86_64-macos-none \
	# GOOS=darwin GOARCH=amd64 \
	# CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-apple-darwin/release -lkektordb_compute -ldl -lm"

	# macOS ARM64
	# @make release-build TARGET=aarch64-apple-darwin ZIG_TARGET=aarch64-macos-none \
	# GOOS=darwin GOARCH=arm64 \
	# CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/aarch64-apple-darwin/release -lkektordb_compute -ldl -lm"



# Build ottimizzata con Rust (per Linux e Windows)
release-build: build-rust-target
	@echo "==> Cross-compiling KektorDB for $(GOOS)/$(GOARCH)..."
	@echo "Using Linker Flags: $(CGO_LDFLAGS)"

	@CGO_LDFLAGS="$(CGO_LDFLAGS)" \
	CGO_ENABLED=1 \
	GOOS=$(GOOS) GOARCH=$(GOARCH) \
	CC="zig cc -target $(ZIG_TARGET)" CXX="zig c++ -target $(ZIG_TARGET)" \
	go build -tags "rust netgo" -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT)" ./cmd/kektordb


# Build pura Go (per macOS)
release-build-pure:
	@echo "==> Compiling pure-Go KektorDB for $(GOOS)/$(GOARCH)..."
	@CGO_ENABLED=0 \
	GOOS=$(GOOS) GOARCH=$(GOARCH) \
	go build -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT)" ./cmd/kektordb



# --- Target di Pulizia ---
clean:
	@echo "==> Aggressively cleaning all caches and artifacts..."
	@rm -f pkg/core/distance/distance_avo_amd64.s pkg/core/distance/stubs_avo_amd64.go
	@rm -rf native/compute/target
	@rm -rf $(RELEASE_DIR)
	@go clean -cache -modcache -testcache
