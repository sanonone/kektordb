// Package embeddings provides built-in ONNX model download.
// This file is always compiled (no build tags) because model download is
// useful even for pure Go builds — the user can download manually and then
// use the model with a future Rust build or an external tool.
package embeddings

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultModelName = "all-MiniLM-L6-v2"
	defaultModelDim  = 384

	// HuggingFace model repo and file paths
	hfModelRepo     = "sentence-transformers/all-MiniLM-L6-v2"
	hfModelFile     = "onnx/model.onnx"
	hfTokenizerFile = "tokenizer.json"

	// SHA256 checksums for integrity verification
	modelSHA256Sum     = "6fd5d72fe4589f189f8ebc006442dbb529bb7ce38f8082112682524616046452"
	tokenizerSHA256Sum = "be50c3628f2bf5bb5e3a7f17b1f74611b2561a3a27eeab05e5aa30f411572037"
)

// ensureModel ensures the ONNX model and tokenizer are present in dataDir/models/.
// If customModelDir is set, it uses that directory instead.
// Returns the paths to the model (.onnx) and tokenizer (.json) files.
func ensureModel(dataDir, customModelDir string) (modelPath, tokenizerPath string, err error) {
	var modelsDir string
	if customModelDir != "" {
		modelsDir = customModelDir
	} else {
		modelsDir = filepath.Join(dataDir, "models")
	}

	modelBase := defaultModelName

	// Download model if not present
	modelPath = filepath.Join(modelsDir, modelBase+".onnx")
	if _, statErr := os.Stat(modelPath); os.IsNotExist(statErr) {
		if err := os.MkdirAll(modelsDir, 0755); err != nil {
			return "", "", fmt.Errorf("cannot create models directory %s: %w", modelsDir, err)
		}
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", hfModelRepo, hfModelFile)
		slog.Info("Downloading ONNX model...", "url", url, "dest", modelPath, "size", "~87MB")
		if err := downloadWithProgress(url, modelPath, modelSHA256Sum); err != nil {
			os.Remove(modelPath)
			return "", "", fmt.Errorf("model download failed: %w", err)
		}
		slog.Info("ONNX model downloaded and verified", "path", modelPath)
	}

	// Download tokenizer if not present
	tokenizerPath = filepath.Join(modelsDir, modelBase+"-tokenizer.json")
	if _, statErr := os.Stat(tokenizerPath); os.IsNotExist(statErr) {
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", hfModelRepo, hfTokenizerFile)
		slog.Info("Downloading tokenizer...", "url", url, "dest", tokenizerPath, "size", "~456KB")
		if err := downloadWithProgress(url, tokenizerPath, tokenizerSHA256Sum); err != nil {
			os.Remove(tokenizerPath)
			return "", "", fmt.Errorf("tokenizer download failed: %w", err)
		}
		slog.Info("Tokenizer downloaded and verified", "path", tokenizerPath)
	}

	return modelPath, tokenizerPath, nil
}

// downloadWithProgress downloads a file with periodic progress logging.
// Writes to a temp file and renames atomically on success.
// Verifies SHA256 if expectedSum is non-empty.
func downloadWithProgress(url, dest, expectedSHA256 string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmpPath := dest + ".download"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	totalSize := resp.ContentLength
	var downloaded int64
	lastLog := time.Now()
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	buf := make([]byte, 32*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := writer.Write(buf[:n]); writeErr != nil {
				file.Close()
				return writeErr
			}
			downloaded += int64(n)
		}

		// Progress log every second
		if time.Since(lastLog) > time.Second {
			if totalSize > 0 {
				pct := float64(downloaded) / float64(totalSize) * 100
				mb := float64(downloaded) / (1024 * 1024)
				totalMB := float64(totalSize) / (1024 * 1024)
				slog.Info(fmt.Sprintf("Download: %.0f%% | %.1f/%.1f MB", pct, mb, totalMB))
			}
			lastLog = time.Now()
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return readErr
		}
	}
	file.Close()

	// Verify SHA256
	if expectedSHA256 != "" {
		actualSum := fmt.Sprintf("%x", hash.Sum(nil))
		if actualSum != expectedSHA256 {
			return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedSHA256, actualSum)
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}
	return nil
}
