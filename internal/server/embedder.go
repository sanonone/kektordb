// Package server implements the main KektorDB server logic.
//
// This file contains the implementation for the getEmbedding method, which is
// part of the Vectorizer worker. It handles communication with external embedding
// services, such as Ollama, to convert text chunks into vector representations.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// getEmbedding calls the configured remote embedding service (e.g., Ollama API)
// to convert a given text string into a vector embedding.
// This is an un-exported method of the Vectorizer.
func (v *Vectorizer) getEmbedding(text string) ([]float32, error) {
	if v.config.Embedder.Type != "ollama_api" {
		return nil, fmt.Errorf("embedder type '%s' not supported", v.config.Embedder.Type)
	}

	// Build the JSON payload for the Ollama API.
	payload := map[string]interface{}{
		"model":  v.config.Embedder.Model,
		"prompt": text,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Execute the HTTP request.
	resp, err := http.Post(v.config.Embedder.URL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("[DEBUG EMBEDDING] HTTP request error: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API returned an error: %s", resp.Status)
	}

	// Decode the response.
	var ollamaResp struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, err
	}

	return ollamaResp.Embedding, nil
}
