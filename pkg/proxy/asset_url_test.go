package proxy

import (
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/engine"
)

// TestAssetURLRewritingDefault verifies default AssetBaseURL derivation from port.
func TestAssetURLRewritingDefault(t *testing.T) {
	eng, err := engine.Open(engine.DefaultOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Port:      ":9092",
		TargetURL: "http://localhost:11434",
		// AssetBaseURL is empty - should default to localhost:9092
	}

	p, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// Verify the prefix is correctly derived
	expected := "](http://localhost:9092/assets/"
	if p.assetURLPrefix != expected {
		t.Errorf("Expected prefix '%s', got '%s'", expected, p.assetURLPrefix)
	}

	// Verify rewriting works
	input := "Here is an image: ![alt](/assets/image.png)"
	result := strings.ReplaceAll(input, "](/assets/", p.assetURLPrefix)
	if !strings.Contains(result, "http://localhost:9092/assets/image.png") {
		t.Errorf("Rewriting failed: %s", result)
	}
}

// TestAssetURLRewritingCustom verifies custom AssetBaseURL configuration.
func TestAssetURLRewritingCustom(t *testing.T) {
	eng, err := engine.Open(engine.DefaultOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Port:         ":9092",
		TargetURL:    "http://localhost:11434",
		AssetBaseURL: "https://api.example.com",
	}

	p, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// Verify the prefix is correctly derived from custom URL
	expected := "](https://api.example.com/assets/"
	if p.assetURLPrefix != expected {
		t.Errorf("Expected prefix '%s', got '%s'", expected, p.assetURLPrefix)
	}

	// Verify rewriting works
	input := "![img](/assets/docs/page1.png)"
	result := strings.ReplaceAll(input, "](/assets/", p.assetURLPrefix)
	if !strings.Contains(result, "https://api.example.com/assets/docs/page1.png") {
		t.Errorf("Rewriting failed: %s", result)
	}
}

// TestAssetURLTrailingSlash verifies trailing slash is removed.
func TestAssetURLTrailingSlash(t *testing.T) {
	eng, err := engine.Open(engine.DefaultOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Port:         ":9092",
		TargetURL:    "http://localhost:11434",
		AssetBaseURL: "https://api.example.com/", // Trailing slash
	}

	p, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// Trailing slash should be removed
	expected := "](https://api.example.com/assets/"
	if p.assetURLPrefix != expected {
		t.Errorf("Expected prefix '%s', got '%s'", expected, p.assetURLPrefix)
	}
}

// TestAssetURLMultipleImages verifies rewriting of multiple asset references.
func TestAssetURLMultipleImages(t *testing.T) {
	eng, err := engine.Open(engine.DefaultOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Port:         ":9091",
		TargetURL:    "http://localhost:11434",
		AssetBaseURL: "https://docs.company.com",
	}

	p, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	input := "First: ![img1](/assets/a.png) and second: ![img2](/assets/b.png)"
	result := strings.ReplaceAll(input, "](/assets/", p.assetURLPrefix)

	expected := "First: ![img1](https://docs.company.com/assets/a.png) and second: ![img2](https://docs.company.com/assets/b.png)"
	if result != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, result)
	}
}
