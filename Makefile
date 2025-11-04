# --- Variabili di Configurazione ---
VERSION ?= $(shell git describe --tags --abbrev=0 || echo "v0.0.0")
BINARY_NAME=kektordb
RELEASE_DIR=release

# --- Target Principali ---
.PHONY: all test test-rust bench bench-rust clean release

# Il target di default è un test veloce della build Go-pura
all: test

# --- Target di Test e Benchmark ---
test: generate-avo
	@echo "==> Running Go tests (Go/AVO implementation)..."
	@go test -short -v ./...

test-rust: build-rust-native
	@echo "==> Running Go tests (Rust CGO implementation)..."
	@go test -tags rust -short -v ./...

bench: generate-avo
	@echo "==> Running Go benchmarks (Go/AVO implementation)..."
	@go test -bench=. ./...

bench-rust: build-rust-native
	@echo "==> Running Go benchmarks (Rust CGO implementation)..."
	@go test -tags rust -bench=. ./...

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

# --- Target di Release ---
# Questo è il comando principale che verrà eseguito da GitHub Actions
release: clean
	@echo "Building releases for all targets..."
	@mkdir -p $(RELEASE_DIR)
	# sia il target per Rust/Cargo che quello per Zig
	@make release-build TARGET=x86_64-unknown-linux-gnu ZIG_TARGET=x86_64-linux-gnu GOOS=linux GOARCH=amd64
	@make release-build TARGET=aarch64-unknown-linux-gnu ZIG_TARGET=aarch64-linux-gnu GOOS=linux GOARCH=arm64
	@make release-build TARGET=x86_64-pc-windows-gnu ZIG_TARGET=x86_64-windows-gnu GOOS=windows GOARCH=amd64 EXT=.exe
	@make release-build TARGET=x86_64-apple-darwin ZIG_TARGET=x86_64-macos-none GOOS=darwin GOARCH=amd64
	@make release-build TARGET=aarch64-apple-darwin ZIG_TARGET=aarch64-macos-none GOOS=darwin GOARCH=arm64

# Target helper per una singola build di release 
release-build: build-rust-target
	@echo "==> Cross-compiling KektorDB for $(GOOS)/$(GOARCH)..."
	# target al percorso del linker LDFLAGS.
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/$(TARGET)/release" \
	# la variabile ZIG_TARGET per il compilatore C
	@CC="zig cc -target $(ZIG_TARGET)" CXX="zig c++ -target $(ZIG_TARGET)" \
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 \
	go build -tags rust -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT)" ./cmd/kektordb



# --- Target di Pulizia ---
clean:
	@echo "==> Cleaning generated files, build cache, and release artifacts..."
	@rm -f pkg/core/distance/distance_avo.s pkg/core/distance/stubs_avo.go
	@rm -rf native/compute/target
	@rm -rf $(RELEASE_DIR)
	@go clean -testcache
