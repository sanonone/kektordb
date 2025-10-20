package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// getEmbedding chiama un'API remota per ottenere l'embedding di un testo.
func (v *Vectorizer) getEmbedding(text string) ([]float32, error) {
	if v.config.Embedder.Type != "ollama_api" {
		return nil, fmt.Errorf("tipo di embedder '%s' non supportato", v.config.Embedder.Type)
	}

	// Costruisci il payload JSON per l'API di Ollama
	payload := map[string]interface{}{
		"model":  v.config.Embedder.Model,
		"prompt": text,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Esegui la richiesta HTTP
	resp, err := http.Post(v.config.Embedder.URL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("[DEBUG EMBEDDING] Errore HTTP: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("l'API di embedding ha restituito un errore: %s", resp.Status)
	}

	// Decodifica la risposta
	var ollamaResp struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, err
	}

	return ollamaResp.Embedding, nil
}
