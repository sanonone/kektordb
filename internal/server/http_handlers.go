// codice delle API http
package server

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/internal/store/distance"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// registerHTTPHandlers imposta le route per la API REST
func (s *Server) registerHTTPHandlers(mux *http.ServeMux) {
	// Endpoint per il Key-Value Store
	mux.HandleFunc("/kv/", s.handleKV) // Un solo handler per /kv/

	// Endpoint per gli Indici Vettoriali
	mux.HandleFunc("/vector/create", s.handleVectorCreate)
	mux.HandleFunc("/vector/add", s.handleVectorAdd)
	mux.HandleFunc("/vector/search", s.handleVectorSearch)
	// (Potremmo aggiungere /vector/delete in futuro)
}

// --- Handler per KV ---

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request) {
	// estrae la chiave dall'URL. es. /kv/mia_chiave
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "La chiave non può essere vuota")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleKVGet(w, r, key)
	case http.MethodPost, http.MethodPut:
		s.handleKVSet(w, r, key)
	case http.MethodDelete:
		s.handleKVDelete(w, r, key)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Metodo non supportato")
	}
}

func (s *Server) handleKVGet(w http.ResponseWriter, r *http.Request, key string) {
	value, found := s.store.GetKVStore().Get(key)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, "Chiave non trovata")
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
}

func (s *Server) handleKVSet(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Impossibile leggere il corpo della richiesta")
		return
	}

	// Logga il comando sull'AOF
	// costruisce la stringa del comando come se arrivasse dal TCP
	aofCommand := fmt.Sprintf("SET %s %s\n", key, string(body))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.store.GetKVStore().Set(key, body)
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleKVDelete(w http.ResponseWriter, r *http.Request, key string) {
	aofCommand := fmt.Sprintf("DEL %s\n", key)
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.store.GetKVStore().Delete(key)
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

// --- Handler per Vettori (VCREATE, VADD, VSEARCH) ---
type VectorCreateRequest struct {
	IndexName string `json:"index_name"`
	// omitempty per il campo metric così se il client non lo invia non sarà
	// presente nel json permettendo l'uso di un default
	Metric         string `json:"metric,omitempty"`
	M              int    `json:"m,omitempty"`
	EfConstruction int    `json:"ef_construction,omitempty"`
}

func (s *Server) handleVectorCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name è obbligatorio")
		return
	}

	// Se la metrica non è specificata, usiamo Euclidean come default.
	metric := distance.DistanceMetric(req.Metric)
	if metric == "" {
		metric = distance.Euclidean
	}

	// scrittura AOF per persistenza
	aofCommand := fmt.Sprintf("VCREATE %s METRIC %s M %d EF_CONSTRUCTION %d\n", req.IndexName, metric, req.M, req.EfConstruction)
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand) // gestire l'errore qui in un sistema di produzione
	s.aofMutex.Unlock()

	err := s.store.CreateVectorIndex(req.IndexName, metric, req.M, req.EfConstruction)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Indice creato"})
}

type VectorAddRequest struct {
	IndexName string         `json:"index_name"`
	Id        string         `json:"id"`
	Vector    []float32      `json:"vector"`
	Metadata  map[string]any `json:"metadata,omitempty"` // omitempty = se il campo è nullo non apparirà nel json
}

func (s *Server) handleVectorAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	// Validazione base dell'input
	if req.IndexName == "" || req.Id == "" || len(req.Vector) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name, id, e vector sono campi obbligatori")
		return
	}

	idx, found := s.store.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	/*
		// scrittura AOF per persistenza
		// ricostruisce la stringa del vettore separata da spazi
		vectorStr := float32SliceToString(req.Vector)
		aofCommand := fmt.Sprintf("VADD %s %s %s\n", req.IndexName, req.Id, vectorStr)
		s.aofMutex.Lock()
		s.aofFile.WriteString(aofCommand)
		s.aofMutex.Unlock()
	*/

	internalID, err := idx.Add(req.Id, req.Vector)
	if err != nil {
		// Controlla se l'errore è "ID già esistente"
		// In un'API REST, questo corrisponde a un errore 409 Conflict
		if strings.Contains(err.Error(), "già esistente") {
			s.writeHTTPError(w, http.StatusConflict, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("errore nell'aggiungere il vettore: %v", err))
		}
		return
	}

	// se ci sono metadati li indicizza
	if req.Metadata != nil {
		if err := s.store.AddMetadata(req.IndexName, internalID, req.Metadata); err != nil {
			// operazione critica, se fallisce dovremmo disfare l'aggiunta del vettore (rollback)
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("errore nell'indicizzare i metadati: %v", err))
			return
		}
	}

	// --- LOGICA AOF ---
	// Costruisci il comando AOF solo dopo che tutte le validazioni sono passate.
	aofCommand, err := buildVAddAOFCommand(req.IndexName, req.Id, req.Vector, req.Metadata)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, "errore nella creazione del comando AOF")
		return
	}

	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()
	// --- FINE LOGICA AOF ---

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Vettore aggiunto"})
}

type VectorSearchRequest struct {
	IndexName   string    `json:"index_name"`
	K           int       `json:"k"`
	QueryVector []float32 `json:"query_vector"`
	Filter      string    `json:"filter,omitempty"` // filtro opzionale
}

func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	idx, found := s.store.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	// --- NUOVA LOGICA DI FILTRAGGIO ---
	var allowList map[uint32]struct{}
	var err error

	if req.Filter != "" {
		allowList, err = s.store.FindIDsByFilter(req.IndexName, req.Filter)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("filtro invalido: %v", err))
			return
		}
		// Se il filtro non produce risultati, può terminare subito
		if len(allowList) == 0 {
			s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": []string{}})
			return
		}
	}

	results := idx.Search(req.QueryVector, req.K, allowList)
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": results})
}

// --- Helper per le Risposte HTTP ---

func (s *Server) writeHTTPResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeHTTPError(w http.ResponseWriter, statusCode int, message string) {
	s.writeHTTPResponse(w, statusCode, map[string]string{"error": message})
}

func float32SliceToString(slice []float32) string {
	// strings.Builder è il modo più efficiente per costruire stringhe in Go
	var b strings.Builder
	for i, v := range slice {
		if i > 0 {
			b.WriteString(" ")
		}
		// 'f' per formato, -1 per la minima precisione necessaria, 32 per float32
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	return b.String()
}

// Funzione helper per costruire la stringa del comando AOF
func buildVAddAOFCommand(indexName, id string, vector []float32, metadata map[string]any) (string, error) {
	vectorStr := float32SliceToString(vector) // Usiamo la funzione che avevamo già

	if len(metadata) == 0 {
		return fmt.Sprintf("VADD %s %s %s\n", indexName, id, vectorStr), nil
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}

	// Racchiudiamo il JSON tra virgolette singole per sicurezza, anche se il nostro
	// parser TCP attuale non le gestisce (è una buona pratica per il futuro).
	return fmt.Sprintf("VADD %s %s %s %s\n", indexName, id, vectorStr, string(metadataBytes)), nil
}
