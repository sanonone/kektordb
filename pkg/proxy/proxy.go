package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/engine"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// AIProxy sits between the client and the LLM.
type AIProxy struct {
	cfg          Config
	engine       *engine.Engine // Access to KektorDB for checks
	reverseProxy *httputil.ReverseProxy
}

func NewAIProxy(cfg Config, dbEngine *engine.Engine) (*AIProxy, error) {
	target, err := url.Parse(cfg.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	return &AIProxy{
		cfg:          cfg,
		engine:       dbEngine,
		reverseProxy: httputil.NewSingleHostReverseProxy(target),
	}, nil
}

// ServeHTTP implements the http.Handler interface.
func (p *AIProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Read Body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore for Proxy

	// 2. Extract Text
	promptText := extractPrompt(bodyBytes)
	// Check if request asks for streaming
	// (Ollama/OpenAI usano il campo JSON "stream": true)
	isStreaming := checkStreaming(bodyBytes)

	var promptVec []float32
	if promptText != "" && (p.cfg.FirewallEnabled || p.cfg.CacheEnabled) {
		// Calculate embedding once
		v, err := p.cfg.Embedder.Embed(promptText)
		if err == nil {
			promptVec = v
		} else {
			log.Printf("[Proxy] Embedding failed: %v", err)
		}
	}

	// 3. FIREWALL CHECK
	if p.cfg.FirewallEnabled && len(promptVec) > 0 {
		if blocked, reason := p.checkFirewallWithVec(promptVec); blocked {
			log.Printf("[Firewall] BLOCKED: %s", reason)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("Blocked by Semantic Firewall: %s", reason),
			})
			return
		}
	}

	// 4. CACHE READ (Only if not streaming)
	if !isStreaming && p.cfg.CacheEnabled && len(promptVec) > 0 {
		if cachedResp, hit := p.checkCache(promptVec); hit {
			log.Printf("[Cache] HIT")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Kektor-Cache", "HIT")
			w.Write([]byte(cachedResp))
			return
		}
	}

	// 5. FORWARD & CACHE WRITE
	if isStreaming || !p.cfg.CacheEnabled || len(promptVec) == 0 {
		// Direct pass-through for streaming or if cache disabled
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	// Capture response for caching
	capturer := &responseCapturer{
		ResponseWriter: w,
		body:           new(bytes.Buffer),
		statusCode:     http.StatusOK, // Default assumption
	}

	p.reverseProxy.ServeHTTP(capturer, r)

	// Save to cache ONLY on success (200 OK)
	if capturer.statusCode == http.StatusOK {
		// Run in background to not block the response
		go p.saveToCache(promptVec, promptText, capturer.body.Bytes())
	}
}

// checkStreaming parses the JSON body to see if "stream": true is set.
func checkStreaming(body []byte) bool {
	// Fast path check string to avoid full unmarshal if possible?
	// Safer to unmarshal map.
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return false
	}
	if val, ok := data["stream"].(bool); ok {
		return val
	}
	return false // Default is usually false for most APIs if omitted, but depends on LLM.
}

// checkFirewallWithVec ottimizzato per usare il vettore già calcolato
func (p *AIProxy) checkFirewallWithVec(vec []float32) (bool, string) {
	results, err := p.engine.VSearchWithScores(p.cfg.FirewallIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return false, ""
	}
	bestMatch := results[0]
	if float32(bestMatch.Score) < p.cfg.FirewallThreshold {
		return true, fmt.Sprintf("Similar to '%s' (Dist: %.4f)", bestMatch.ID, bestMatch.Score)
	}
	return false, ""
}

// checkCache cerca una risposta esistente
func (p *AIProxy) checkCache(vec []float32) (string, bool) {
	results, err := p.engine.VSearchWithScores(p.cfg.CacheIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return "", false
	}

	best := results[0]
	// Cache Hit solo se molto simile
	if float32(best.Score) < p.cfg.CacheThreshold {
		data, err := p.engine.VGet(p.cfg.CacheIndex, best.ID)
		if err == nil {

			// --- LOGICA TTL ---
			if createdAt, ok := data.Metadata["created_at"].(float64); ok {
				createdTime := time.Unix(int64(createdAt), 0)

				// Se è scaduto...
				if p.cfg.CacheTTL > 0 && time.Since(createdTime) > p.cfg.CacheTTL {
					log.Printf("[Cache] Item expired (Age: %v). Triggering cleanup.", time.Since(createdTime))

					// AZIONE: Cancelliamo il nodo.
					// Il Vacuum passerà poi a liberare la RAM e ricucire il grafo.
					go func(id string) {
						_ = p.engine.VDelete(p.cfg.CacheIndex, id)
					}(best.ID)

					return "", false // È un Cache Miss per l'utente
				}
			}
			// ------------------

			if resp, ok := data.Metadata["response"].(string); ok {
				return resp, true
			}
		}
	}
	return "", false
}

// saveToCache salva la coppia Domanda/Risposta
func (p *AIProxy) saveToCache(queryVec []float32, queryText string, responseBytes []byte) {
	if len(responseBytes) == 0 {
		return
	}

	// 1. CHECK LIMITE DIMENSIONE
	// Otteniamo info sull'indice cache
	info, err := p.engine.DB.GetSingleVectorIndexInfoAPI(p.cfg.CacheIndex)
	if err == nil {
		// Se siamo pieni, no cache (politica "Drop New")
		// Alternativa futura: Cancellare vecchi con Vacuum
		if p.cfg.MaxCacheItems > 0 && info.VectorCount >= p.cfg.MaxCacheItems {
			log.Printf("[Cache] Full (%d items). Skipping save.", info.VectorCount)
			return
		}
	}

	id := fmt.Sprintf("cache_%d_%d", time.Now().UnixNano(), len(queryText)) // ID univoco temporale

	meta := map[string]interface{}{
		"query":    queryText,
		"response": string(responseBytes),
		// 2. SALVA TIMESTAMP (Unix Nano)
		"created_at": float64(time.Now().Unix()),
	}

	maintConfig := &hnsw.AutoMaintenanceConfig{
		// Dobbiamo castare time.Duration nel tipo custom hnsw.Duration
		// (se abbiamo usato il wrapper per il JSON nel file config.go di hnsw)
		VacuumInterval:  hnsw.Duration(p.cfg.CacheVacuumInterval),
		DeleteThreshold: p.cfg.CacheDeleteThreshold,
		RefineEnabled:   false,
	}

	// Passiamo la config a VCreate
	// Se l'indice non esiste, viene creato con QUESTA configurazione di manutenzione.
	_ = p.engine.VCreate(p.cfg.CacheIndex, distance.Cosine, 16, 200, distance.Float32, "", maintConfig)

	p.engine.VAdd(p.cfg.CacheIndex, id, queryVec, meta)

	// Log accorciato safe
	preview := queryText
	if len(preview) > 30 {
		preview = preview[:30]
	}
	log.Printf("[Cache] Saved: %s...", preview)
}

// checkFirewall returns true if the prompt matches a forbidden pattern.
func (p *AIProxy) checkFirewall(text string) (bool, string) {
	vec, err := p.cfg.Embedder.Embed(text)
	if err != nil {
		return false, ""
	}

	// Cerchiamo il match più vicino
	results, err := p.engine.VSearchWithScores(p.cfg.FirewallIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return false, ""
	}

	bestMatch := results[0]

	// Logica Threshold:
	// Cosine: Score è 0.0 (uguale) -> 2.0 (opposto).
	// Attenzione: KektorDB ritorna "Distanza" (1 - CosineSimilarity) o score normalizzato?
	// Nel codice SearchWithScores originale: results[i].Score = c.Distance.
	// E c.Distance per Cosine è (1 - sim). Quindi 0 = identico.

	// Se la distanza è MOLTO BASSA (es. < 0.1), vuol dire che il prompt è molto simile all'attacco.
	if float32(bestMatch.Score) < p.cfg.FirewallThreshold {
		return true, fmt.Sprintf("Similar to known threat '%s' (Dist: %.4f)", bestMatch.ID, bestMatch.Score)
	}

	return false, ""
}

// Helper per estrarre testo da JSON generici (Ollama/OpenAI)
func extractPrompt(jsonBody []byte) string {
	var data map[string]interface{}
	if err := json.Unmarshal(jsonBody, &data); err != nil {
		return ""
	}

	// 1. Caso "prompt" (Ollama standard)
	if v, ok := data["prompt"].(string); ok {
		return v
	}

	// 2. Caso "messages" (OpenAI chat format)
	if msgs, ok := data["messages"].([]interface{}); ok {
		var sb strings.Builder
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				if role, _ := msgMap["role"].(string); role == "user" {
					if content, _ := msgMap["content"].(string); content != "" {
						sb.WriteString(content)
						sb.WriteString("\n")
					}
				}
			}
		}
		return sb.String()
	}

	return ""
}
