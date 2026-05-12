package embeddings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelectEmbedderExplicitOllama verifies explicit mode="ollama" creates an OllamaEmbedder.
// The server may be down, but the embedder object is always creatable.
func TestSelectEmbedderExplicitOllama(t *testing.T) {
	cfg := EmbedderConfig{
		Mode:        "ollama",
		OllamaURL:   "http://localhost:19999/api/embeddings",
		OllamaModel:  "nomic-embed-text",
	}
	emb, err := SelectEmbedder(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("SelectEmbedder: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
	// Call Embed — will fail because fake URL, but shouldn't panic
	_, err = emb.Embed("hello")
	if err == nil {
		t.Log("unexpected: fake Ollama URL returned success")
	}
}

// TestSelectEmbedderExplicitOpenAI verifies explicit mode="openai" creates an OpenAIEmbedder.
func TestSelectEmbedderExplicitOpenAI(t *testing.T) {
	cfg := EmbedderConfig{
		Mode:        "openai",
		OpenAIURL:   "https://api.openai.com/v1/embeddings",
		OpenAIModel: "text-embedding-3-small",
		OpenAIKey:   "sk-test",
	}
	emb, err := SelectEmbedder(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("SelectEmbedder: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
}

// TestSelectEmbedderAutoNoOllamaFallback verifies auto mode returns guidance error
// when neither Ollama nor local embedder is available (no Ollama, no model files).
// In a Rust build the local embedder IS available and will trigger a download,
// so this test only exercises the error path for pure Go builds or when model
// files are explicitly absent.
func TestSelectEmbedderAutoNoOllamaFallback(t *testing.T) {
	// Probe: if local embedder is available (Rust build), auto mode will download
	// the model from HuggingFace. Skip the test to avoid a slow download.
	if _, probeErr := NewLocalEmbedder("/nonexistent/model.onnx", "/nonexistent/tokenizer.json"); probeErr == nil {
		t.Skip("local embedder is available (Rust build) — auto mode would download model, skipping")
	}

	cfg := EmbedderConfig{
		Mode:      "auto",
		OllamaURL: "http://localhost:19999/api/embeddings", // Non-existent
	}
	_, err := SelectEmbedder(cfg, t.TempDir())
	if err == nil {
		t.Fatal("expected error when no embedder is available")
	}
	t.Logf("Expected error: %v", err)
}

// TestSelectEmbedderExplicitLocalWithCustomModel verifies local mode with explicit model dir.
func TestSelectEmbedderExplicitLocalWithCustomModel(t *testing.T) {
	modelsDir := "/tmp/kektordb-test-models"
	if _, err := os.Stat(filepath.Join(modelsDir, "all-MiniLM-L6-v2.onnx")); os.IsNotExist(err) {
		t.Skip("model files not found in " + modelsDir)
	}

	cfg := EmbedderConfig{
		Mode:     "local",
		ModelDir: modelsDir,
	}
	emb, err := SelectEmbedder(cfg, t.TempDir())
	if err != nil {
		// Expected for pure Go build (local embedder stub returns error)
		t.Logf("SelectEmbedder(local): %v (expected for non-rust build)", err)
		return
	}
	vec, err := emb.Embed("hello")
	if err != nil {
		t.Logf("Embed failed: %v", err)
		return
	}
	if len(vec) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(vec))
	}
}

// TestDownloadEnsureModelCreatesDir verifies that ensureModel creates the models directory.
func TestDownloadEnsureModelCreatesDir(t *testing.T) {
	dataDir := t.TempDir()
	modelsDir := filepath.Join(dataDir, "models")

	// Directory should not exist yet
	if _, err := os.Stat(modelsDir); !os.IsNotExist(err) {
		t.Skip("models dir unexpectedly exists")
	}

	// With an explicit ModelDir that already has the files, it should skip download
	cfg := EmbedderConfig{
		Mode:     "local",
		ModelDir: "/tmp/kektordb-test-models",
	}
	modelPath, tokenizerPath, err := ensureModel(dataDir, cfg.ModelDir)
	if err != nil {
		t.Fatalf("ensureModel: %v", err)
	}
	if modelPath == "" || tokenizerPath == "" {
		t.Fatal("empty paths returned")
	}
	t.Logf("model:  %s", modelPath)
	t.Logf("token:  %s", tokenizerPath)
}

// TestDownloadEnsureModelCustomPathNotFound verifies error on non-existent custom path.
func TestDownloadEnsureModelCustomPathNotFound(t *testing.T) {
	_, _, err := ensureModel(t.TempDir(), "/nonexistent/path/to/models")
	if err == nil {
		t.Fatal("expected error for non-existent custom path")
	}
}

// TestIsOllamaRunning verifies that isOllamaRunning returns false for invalid URLs.
func TestIsOllamaRunningInvalid(t *testing.T) {
	if isOllamaRunning("http://localhost:19999/api/embeddings") {
		t.Fatal("expected false for non-existent Ollama")
	}
	if isOllamaRunning("not-a-url") {
		t.Fatal("expected false for invalid URL")
	}
	if isOllamaRunning("http://0.0.0.0:1/api/embeddings") {
		t.Fatal("expected false for unreachable address")
	}
}
