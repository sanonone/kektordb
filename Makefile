# --- Configuration Variables ---
VERSION ?= $(shell git describe --tags --abbrev=0 || echo "v0.0.0")
BINARY_NAME=kektordb
RELEASE_DIR=release

# protoc is required for candle-onnx (build-time only, not runtime).
# Searches PATH first, falls back to a manually downloaded binary.
PROTOC := $(shell which protoc 2>/dev/null || echo /tmp/protoc/bin/protoc)

# --- Main Targets ---
.PHONY: all test test-rust bench bench-rust build-go build-rust clean release ensure-protoc fmt

# The default target is a quick test of the pure Go build
all: test

# Runs the Go version
run:
	@echo "==> Running KektorDB (Go-pure implementation)..."
	@go run ./cmd/kektordb/ $(ARGS)

# Run the Rust-optimized version
run-rust: build-rust-native
	@echo "==> Running KektorDB (Rust CGO implementation)..."
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release -lkektordb_compute -lm -ldl -lstdc++" \
	go run -tags rust ./cmd/kektordb/ $(ARGS)

# --- Test Targets and Benchmarks ---
test: generate-avo
	@echo "==> Running Go tests (Go/AVO implementation)..."
	@go test -short -v ./...

.PHONY: vet
vet:
	@echo "==> Running go vet..."
	@go vet ./...

test-e2e: generate-avo
	@echo "==> Running E2E tests with real server..."
	@go test -v -run "TestClientFullLifecycle|TestAPIContracts" ./pkg/client/...

test-rust: build-rust-native
	@echo "==> Running Go tests (Rust CGO implementation)..."
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release -lkektordb_compute -lm -ldl -lstdc++" \
	go test -tags rust -short -v ./...

bench: generate-avo
	@echo "==> Running Go benchmarks (Go/AVO implementation)..."
	@go test -bench=. ./...

bench-rust: build-rust-native
	@echo "==> Running Go benchmarks (Rust CGO implementation)..."
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release -lkektordb_compute -lm -ldl -lstdc++" \
	go test -tags rust -bench=. ./...

# --- Build Target ---
# Ensure protoc is available for candle-onnx (build-time only).
ensure-protoc:
	@if [ ! -x "$(PROTOC)" ]; then \
		echo "==> Downloading protoc (build-time dependency)..."; \
		mkdir -p /tmp/protoc && \
		curl -sLo /tmp/protoc.zip "https://github.com/protocolbuffers/protobuf/releases/download/v29.3/protoc-29.3-linux-x86_64.zip" && \
		unzip -o /tmp/protoc.zip -d /tmp/protoc && \
		chmod +x /tmp/protoc/bin/protoc && \
		rm /tmp/protoc.zip && \
		echo "==> protoc $(shell /tmp/protoc/bin/protoc --version) ready"; \
	fi

# Build Rust for the current native platform
build-rust-native: ensure-protoc
	@echo "==> Building Rust compute library (native)..."
	@cd native/compute && PROTOC=$(PROTOC) cargo build --release

# Compile Rust for a specific target (used by the release)
build-rust-target: ensure-protoc
	@echo "==> Building Rust compute library for target: $(TARGET)..."
	@cd native/compute && PROTOC=$(PROTOC) cargo build --release --target=$(TARGET)

generate-avo:
	@echo "==> Generating Assembly code with AVO..."
	@go generate ./pkg/core/distance/avo_gen.go


# --- Local Build Targets ---
build-go:
	@echo "==> Building KektorDB (pure Go)..."
	@mkdir -p $(RELEASE_DIR)
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)" ./cmd/kektordb
	@echo "==> Binary: $(RELEASE_DIR)/$(BINARY_NAME)"

build-rust: build-rust-native
	@echo "==> Building KektorDB (Rust CGO)..."
	@mkdir -p $(RELEASE_DIR)
	@CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/release -lkektordb_compute -lm -ldl -lstdc++" \
	CGO_ENABLED=1 \
	go build -tags "rust netgo" -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)" ./cmd/kektordb
	@echo "==> Binary: $(RELEASE_DIR)/$(BINARY_NAME)"


# --- Release Target ---
# This is the main command that will be executed by GitHub Actions
release: clean
	@echo "Building releases for all targets..."
	@mkdir -p $(RELEASE_DIR)
	# Linux AMD64 (native linker)
	@make release-build TARGET=x86_64-unknown-linux-gnu \
	GOOS=linux GOARCH=amd64 \
	BUILD_CC="gcc" BUILD_CXX="g++" \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-unknown-linux-gnu/release -lkektordb_compute -ldl -lm -lgcc_s -lc -lpthread -lstdc++"

	# Linux ARM64 (GNU cross-compiler)
	@make release-build TARGET=aarch64-unknown-linux-gnu \
	GOOS=linux GOARCH=arm64 \
	BUILD_CC="aarch64-linux-gnu-gcc" BUILD_CXX="aarch64-linux-gnu-g++" \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/aarch64-unknown-linux-gnu/release -lkektordb_compute -ldl -lm -lgcc_s -lc -lpthread -lstdc++"

	# Windows AMD64 (mingw-w64 cross-compiler)
	@make release-build TARGET=x86_64-pc-windows-gnu \
	GOOS=windows GOARCH=amd64 EXT=.exe \
	BUILD_CC="x86_64-w64-mingw32-gcc" BUILD_CXX="x86_64-w64-mingw32-g++" \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-pc-windows-gnu/release -lkektordb_compute -lws2_32 -luserenv -ladvapi32 -lbcrypt -lntdll -lgcc_s -lstdc++"

	# --- macOS (Go Puro) ---
	# target release-build-pure.
	@make release-build-pure GOOS=darwin GOARCH=amd64
	@make release-build-pure GOOS=darwin GOARCH=arm64

	# macOS AMD64
	# @make release-build TARGET=x86_64-apple-darwin \
	# GOOS=darwin GOARCH=amd64 \
	# CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-apple-darwin/release -lkektordb_compute -ldl -lm"

	# macOS ARM64
	# @make release-build TARGET=aarch64-apple-darwin \
	# GOOS=darwin GOARCH=arm64 \
	# CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/aarch64-apple-darwin/release -lkektordb_compute -ldl -lm"


# Test a single release target locally (requires gcc).
# Usage: make release-test-linux
.PHONY: release-test-linux
release-test-linux:
	@make release-build TARGET=x86_64-unknown-linux-gnu \
	GOOS=linux GOARCH=amd64 \
	BUILD_CC="gcc" BUILD_CXX="g++" \
	CGO_LDFLAGS="-L$(CURDIR)/native/compute/target/x86_64-unknown-linux-gnu/release -lkektordb_compute -ldl -lm -lgcc_s -lc -lpthread -lstdc++"

# Rust-optimized build (for Linux and Windows)
release-build: build-rust-target
	@echo "==> Cross-compiling KektorDB for $(GOOS)/$(GOARCH)..."
	@echo "Using Linker Flags: $(CGO_LDFLAGS)"

	@CGO_LDFLAGS="$(CGO_LDFLAGS)" \
	CGO_ENABLED=1 \
	GOOS=$(GOOS) GOARCH=$(GOARCH) \
	CC="$(BUILD_CC)" CXX="$(BUILD_CXX)" \
	go build -tags "rust netgo" -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT)" ./cmd/kektordb


# Pure Go build (for macOS)
release-build-pure:
	@echo "==> Compiling pure-Go KektorDB for $(GOOS)/$(GOARCH)..."
	@CGO_ENABLED=0 \
	GOOS=$(GOOS) GOARCH=$(GOARCH) \
	go build -ldflags="-s -w" -o "$(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT)" ./cmd/kektordb



# --- Formatting ---
fmt:
	@echo "==> Formatting Go source files..."
	@gofmt -w .

# --- Cleaning ---
clean:
	@echo "==> Aggressively cleaning all caches and artifacts..."
	@rm -f pkg/core/distance/distance_avo_amd64.s pkg/core/distance/stubs_avo_amd64.go
	@rm -rf native/compute/target
	@rm -rf $(RELEASE_DIR)
	@go clean -cache -testcache

# --- Skill markdown sync ---
# The MCP prompt `memory_instructions` is embedded from
# internal/mcp/memory_instructions.md (single source-of-truth for Go).
# A mirror at skills/kektordb/SKILL.md is shipped in the repo root so
# users can copy it into agent systems that support skill files
# (Hermes, Claude Code, OpenCode AGENTS.md, etc.). After editing the
# embedded file, run `make sync-skills` to refresh the mirror.
# TestMemoryInstructionsInSync enforces parity on every test run.
.PHONY: sync-skills
sync-skills:
	@echo "==> Syncing internal/mcp/memory_instructions.md -> skills/kektordb/SKILL.md..."
	@mkdir -p skills/kektordb
	@cp internal/mcp/memory_instructions.md skills/kektordb/SKILL.md
	@echo "Done. Verify with: go test -run TestMemoryInstructionsInSync ./internal/mcp/..."
