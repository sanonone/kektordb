package server

import (
	"bufio"
	"context" // per gestire graceful shutdown (ctrl + c)
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"log"
	"net/http" // per il web server
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic" // per contatore dirty
	"time"
)

// struct server con informazioni necessarie

type Server struct {
	db *core.DB // puntatore allo core

	aofFile    *os.File     // file aof per persistenza
	aofMutex   sync.Mutex   // mutex per gestire la scrittura sul file
	httpServer *http.Server // il server http

	// Stato per il salvataggio automatico
	dirtyCounter int64 // Contatore atomico per le modifiche
	lastSaveTime time.Time

	// Configurazione
	savePolicies         []savePolicy
	aofRewritePercentage int
	aofBaseSize          int64 // Dimensione AOF dopo l'ultima riscrittura
	taskManager          *TaskManager
}

type savePolicy struct {
	Seconds int
	Changes int64
}

// crea una nuova istanza del server
func NewServer(aofPath string, savePolicyStr string, aofRewritePerc int) (*Server, error) {
	// aprie o crea il file AOF
	//0666 sono i permessi del file
	file, err := os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("Impossibile aprire il file AOF: %v", err)
	}

	policies, err := parseSavePolicies(savePolicyStr)
	if err != nil {
		return nil, err
	}

	s := &Server{
		db:                   core.NewDB(), // inizializzo core
		aofFile:              file,
		savePolicies:         policies,
		aofRewritePercentage: aofRewritePerc,
		lastSaveTime:         time.Now(),
		taskManager:          NewTaskManager(),
	}
	return s, nil
}

// avvia i server TCP e HTTP e li mette in ascolto sulle porte specificate
func (s *Server) Run(httpAddr string) error {
	// Determina i percorsi dei file
	aofPath := s.aofFile.Name()
	snapshotPath := strings.TrimSuffix(aofPath, ".aof") + ".kdb"

	// 1. Controlla se esiste lo snapshot
	if _, err := os.Stat(snapshotPath); err == nil {
		// Lo snapshot esiste, caricalo.
		log.Printf("Trovato file di snapshot '%s', avvio del ripristino da RDB...", snapshotPath)

		file, errOpen := os.Open(snapshotPath)
		if errOpen != nil {
			return fmt.Errorf("impossibile aprire il file di snapshot: %w", err)
		}

		err = s.db.LoadFromSnapshot(file)
		file.Close() // Chiudi il file dopo la lettura
		if err != nil {
			return fmt.Errorf("errore durante il caricamento dello snapshot: %w", err)
		}
		log.Println("Ripristino da snapshot completato.")

	} else if !os.IsNotExist(err) {
		// C'è stato un errore diverso da "file non trovato" nel controllare lo snapshot
		return fmt.Errorf("errore nel controllare il file di snapshot: %w", err)
	}

	// 2. Esegui sempre il replay dell'AOF.
	// Se abbiamo caricato lo snapshot, questo applicherà solo le modifiche successive.
	// Se non c'era uno snapshot, questo caricherà l'intera cronologia.
	log.Println("Avvio riproduzione del log AOF per le modifiche recenti...")
	if err := s.loadFromAOF(); err != nil {
		return fmt.Errorf("impossibile caricare da AOF: %w", err)
	}

	// Ottieni la dimensione iniziale dell'AOF dopo il caricamento
	info, _ := s.aofFile.Stat()
	s.aofBaseSize = info.Size()

	// --- NUOVO: Avvia la goroutine di manutenzione in background ---
	go s.serverCron()

	// --- configurazione ed avvio server HTTP ---
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux) // registra endpoint

	s.httpServer = &http.Server{
		Addr:    httpAddr,
		Handler: mux,
	}

	log.Printf("Server HTTP in ascolto su %s", httpAddr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("fallimento avvio server HTTP: %w", err)
	}

	return nil

}

// serverCron è il nostro "cron job" in background.
func (s *Server) serverCron() {
	// Esegui un check ogni secondo.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Acquisiamo un lock leggero per leggere i contatori in modo sicuro
		s.aofMutex.Lock()
		dirty := atomic.LoadInt64(&s.dirtyCounter)
		lastSave := s.lastSaveTime
		s.aofMutex.Unlock()

		// --- Logica di SAVE Automatico ---
		for _, policy := range s.savePolicies {
			if time.Since(lastSave).Seconds() >= float64(policy.Seconds) && dirty >= policy.Changes {
				log.Println("Condizione di salvataggio automatico raggiunta. Avvio SAVE...")
				if err := s.Save(); err == nil {
					// Resetta i contatori solo se SAVE ha successo
					s.aofMutex.Lock()
					atomic.StoreInt64(&s.dirtyCounter, 0)
					s.lastSaveTime = time.Now()
					s.aofMutex.Unlock()
					log.Println("SAVE automatico completato.")
				} else {
					log.Printf("ERRORE durante SAVE automatico: %v", err)
				}
				break // Esegui solo la prima policy che matcha
			}
		}

		// --- Logica di AOF REWRITE Automatico ---
		if s.aofRewritePercentage > 0 {
			info, err := s.aofFile.Stat()
			if err == nil {
				currentSize := info.Size()
				// Se la dimensione base è 0, evita divisione per zero. Usa una soglia minima.
				threshold := s.aofBaseSize + (s.aofBaseSize * int64(s.aofRewritePercentage) / 100)
				if s.aofBaseSize > 0 && currentSize > threshold {
					log.Println("Condizione di riscrittura AOF automatica raggiunta. Avvio AOF REWRITE...")
					if err := s.RewriteAOF(); err == nil {
						newInfo, _ := s.aofFile.Stat()
						s.aofBaseSize = newInfo.Size() // Aggiorna la dimensione base
						log.Println("AOF REWRITE automatico completato.")
					} else {
						log.Printf("ERRORE durante AOF REWRITE automatico: %v", err)
					}
				}
			}
		}
	}
}

// vectorIndexState contiene lo stato di un singolo indice vettoriale.
type vectorIndexState struct {
	metric         distance.DistanceMetric
	m              int
	efConstruction int
	precision      distance.PrecisionType
	entries        map[string]vectorEntry // map[vectorID] -> entry
}

// rappresenta lo stato finale aggregato dopo aver letto l'aof
type aofState struct {
	kvData        map[string][]byte
	vectorIndexes map[string]vectorIndexState // map[indexName] -> state
}

// contiene le informazioni di un singolo vettore
type vectorEntry struct {
	vector   []float32
	metadata map[string]any
}

// loadFromAOF è la versione che compatta lo stato prima di caricarlo,
// supporta soft-delete e separa correttamente vector / metadata.
func (s *Server) loadFromAOF() error {
	log.Println("Caricamento e compattazione dati dal file AOF...")

	// Spostiamo il cursore all'inizio del file per la lettura.
	if _, err := s.aofFile.Seek(0, 0); err != nil {
		return fmt.Errorf("seek aof: %w", err)
	}

	// --- FASE 1 & 2: Lettura e Aggregazione dello Stato ---
	state := &aofState{
		kvData:        make(map[string][]byte),
		vectorIndexes: make(map[string]vectorIndexState), // Usa la nuova struct
	}

	scanner := bufio.NewScanner(s.aofFile)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		cmd, err := Parse(line)
		if err != nil {
			log.Printf("[AOF] riga %d: impossibile parsare '%s' -> salto. err=%v", lineNo, line, err)
			continue
		}

		switch cmd.Name {
		case "SET":
			if len(cmd.Args) == 2 {
				state.kvData[string(cmd.Args[0])] = cmd.Args[1]
			} else {
				log.Printf("[AOF] riga %d: SET con argomenti inattesi (%d)", lineNo, len(cmd.Args))
			}

		case "DEL":
			if len(cmd.Args) == 1 {
				delete(state.kvData, string(cmd.Args[0]))
			} else {
				log.Printf("[AOF] riga %d: DEL con argomenti inattesi (%d)", lineNo, len(cmd.Args))
			}

		case "VDROP": // <-- NUOVO CASE
			if len(cmd.Args) == 1 {
				indexName := string(cmd.Args[0])
				// Rimuovi l'indice dalla nostra mappa di stato temporanea
				delete(state.vectorIndexes, indexName)
			}

		case "VCREATE":
			// Formato AOF ora atteso: VCREATE <index_name> [METRIC <metric>] [M <m_val>] [EF_CONSTRUCTION <ef_val>] [PRECISION <p>]
			if len(cmd.Args) >= 1 {
				indexName := string(cmd.Args[0])

				// Valori di default
				metric := distance.Euclidean
				precision := distance.Float32 // Default a float32
				m := 0                        // 0 per usare il default in NewHNSWIndex
				efConstruction := 0

				// Parsing degli argomenti opzionali
				i := 1 // Inizia dall'indice del primo argomento possibile
				for i < len(cmd.Args) {
					if i+1 >= len(cmd.Args) { // Parametro incompleto
						log.Printf("[AOF] Ignorato VCREATE invalido '%s': parametro incompleto", cmd.Name)
						break // Esci dal loop di parsing degli argomenti
					}

					key := strings.ToUpper(string(cmd.Args[i]))
					value := string(cmd.Args[i+1])

					switch key {
					case "METRIC":
						metric = distance.DistanceMetric(value)
					case "M":
						val, err := strconv.Atoi(value)
						if err == nil {
							m = val
						} // Ignora se non è un numero valido
					case "EF_CONSTRUCTION":
						val, err := strconv.Atoi(value)
						if err == nil {
							efConstruction = val
						} // Ignora se non è un numero valido
					case "PRECISION":
						precision = distance.PrecisionType(value)
					default:
						// Ignora parametri sconosciuti
					}
					i += 2 // Avanza di 2 per la prossima coppia chiave-valore
				}

				// Crea lo spazio per questo indice nello stato (se non esiste già)
				if _, ok := state.vectorIndexes[indexName]; !ok {
					state.vectorIndexes[indexName] = vectorIndexState{
						metric:         metric,
						m:              m,
						efConstruction: efConstruction,
						precision:      precision,
						entries:        make(map[string]vectorEntry),
					}
				}
			}

		case "VADD":
			// Supportiamo: VADD indexName vectorID <vectorParts...> [metadataJSON]
			if len(cmd.Args) >= 3 {
				indexName := string(cmd.Args[0])
				vectorID := string(cmd.Args[1])

				if _, ok := state.vectorIndexes[indexName]; !ok {
					continue // Ignora se l'indice non è stato creato
				}

				vectorParts := cmd.Args[2:]
				var metadataJSON []byte

				// Separa i metadati se presenti
				if len(vectorParts) > 0 {
					lastArg := vectorParts[len(vectorParts)-1]
					if len(lastArg) > 1 && lastArg[0] == '{' && lastArg[len(lastArg)-1] == '}' {
						metadataJSON = lastArg
						vectorParts = vectorParts[:len(vectorParts)-1]
					}
				}

				if len(vectorParts) == 0 {
					continue
				} // Vettore mancante

				vector, err := parseVectorFromByteParts(vectorParts)
				if err == nil {
					entry := vectorEntry{vector: vector}
					if len(metadataJSON) > 0 {
						var metadata map[string]interface{}
						if json.Unmarshal(metadataJSON, &metadata) == nil {
							entry.metadata = metadata
						}
					}
					state.vectorIndexes[indexName].entries[vectorID] = entry
				} else {
					log.Printf("Avviso AOF: impossibile parsare il vettore per '%s', saltato. Errore: %v", vectorID, err)
				}
			}

		case "VDEL":
			// Soft-delete: marca l'entry come deleted (non rimuovere l'entry dallo state)
			// Se vuoi hard-delete, usa delete(index, vectorID)
			if len(cmd.Args) == 2 {
				indexName := string(cmd.Args[0])
				vectorID := string(cmd.Args[1])
				if index, ok := state.vectorIndexes[indexName]; ok {
					delete(index.entries, vectorID)
				}
			}

		default:
			// ignora altri comandi o loggali se servono
			// log.Printf("[AOF] riga %d: comando ignorato: %s", lineNo, cmd.Name)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner aof: %w", err)
	}

	// --- FASE 3: Ricostruzione dello Stato nello core ---
	log.Println("Ricostruzione dello stato compattato in memoria...")

	// Ricostruisci il KV core
	for key, value := range state.kvData {
		s.db.GetKVStore().Set(key, value)
	}

	// Ricostruisci gli indici vettoriali
	totalVectors := 0
	addedVectors := 0
	skippedDeleted := 0
	for indexName, indexState := range state.vectorIndexes {
		log.Printf("[AOF] Ricostruzione indice '%s' (Metrica: %s, Precisione: %s) - Vettori: %d",
			indexName, indexState.metric, indexState.precision, len(indexState.entries))
		// --- CHIAMATA CORRETTA ---
		// Ora passiamo la metrica che abbiamo salvato nello stato
		err := s.db.CreateVectorIndex(indexName, indexState.metric, indexState.m, indexState.efConstruction, indexState.precision)
		if err != nil {
			log.Printf("[AOF] ERRORE: impossibile creare l'indice '%s' con metrica %s, M=%d, EF=%d: %v",
				indexName, indexState.metric, indexState.m, indexState.efConstruction, err)
			continue
		}
		idx, found := s.db.GetVectorIndex(indexName)
		if !found {
			log.Printf("[AOF] impossibile ottenere indice '%s'", indexName)
			continue
		}

		for vectorID, entry := range indexState.entries {
			totalVectors++

			// Se è marcata come soft-deleted, salta l'aggiunta al grafo
			if entry.metadata != nil {
				if vdel, ok := entry.metadata["__deleted"]; ok {
					if b, ok := vdel.(bool); ok && b {
						skippedDeleted++
						continue
					}
				}
			}

			// Se non c'è il vettore (placeholder) non possiamo aggiungerlo
			if entry.vector == nil || len(entry.vector) == 0 {
				log.Printf("[AOF] indice '%s' id '%s': vettore mancante -> skip", indexName, vectorID)
				continue
			}

			internalID, err := idx.Add(vectorID, entry.vector)
			if err != nil {
				log.Printf("[AOF] Errore durante la ricostruzione HNSW per '%s' (indice '%s'): %v", vectorID, indexName, err)
				continue
			}
			addedVectors++

			// aggiungi metadata (se presenti)
			if len(entry.metadata) > 0 {
				// rimuoviamo il flag interno prima di salvare i metadata, se presente
				if _, ok := entry.metadata["__deleted"]; ok {
					delete(entry.metadata, "__deleted")
				}
				if len(entry.metadata) > 0 {
					s.db.AddMetadata(indexName, internalID, entry.metadata)
				}
			}
		}

		log.Printf("[AOF] indice '%s' ricostruito: aggiunti=%d, skippati_deleted=%d", indexName, addedVectors, skippedDeleted)
	}

	log.Printf("Caricamento AOF completato. vettori_totali=%d aggiunti=%d skippati_deleted=%d", totalVectors, addedVectors, skippedDeleted)
	return nil
}

// RewriteAOF compatta il file AOF riscrivendolo con lo stato attuale.
func (s *Server) RewriteAOF() error {
	log.Println("Avvio riscrittura AOF...")

	// Usa i metodi di lock pubblici dello core.
	s.db.Lock()
	defer s.db.Unlock()

	// il percorso della directory del file AOF originale
	aofDir := filepath.Dir(s.aofFile.Name())

	// Usa os.CreateTemp invece del deprecato ioutil.TempFile
	tempFile, err := os.CreateTemp(aofDir, "kektordb-aof-rewrite-*.aof")
	if err != nil {
		return fmt.Errorf("impossibile creare il file AOF temporaneo in '%s': %w", aofDir, err)
	}
	defer os.Remove(tempFile.Name()) // Assicura la pulizia in caso di errore

	// --- FASE 1: Scrittura dei Comandi di Creazione Stato ---

	// 1a. Scrivi i comandi SET per il KV core
	s.db.IterateKVUnlocked(func(pair core.KVPair) {
		// Dobbiamo gestire correttamente valori che potrebbero contenere spazi.
		// Per ora il nostro protocollo è semplice, ma in futuro potremmo
		// dover "quotare" il valore.
		cmd := fmt.Sprintf("SET %s %s\n", pair.Key, string(pair.Value))
		tempFile.WriteString(cmd)
	})

	// 1b. Scrivi i comandi VCREATE per ogni indice vettoriale
	// Otteniamo prima le informazioni su tutti gli indici.
	vectorIndexInfo, err := s.db.GetVectorIndexInfoUnlocked()
	if err != nil {
		return fmt.Errorf("impossibile ottenere informazioni sugli indici vettoriali: %w", err)
	}

	for _, info := range vectorIndexInfo {
		cmd := fmt.Sprintf("VCREATE %s METRIC %s M %d EF_CONSTRUCTION %d\n",
			info.Name, info.Metric, info.M, info.EfConstruction)
		tempFile.WriteString(cmd)
	}

	// 1c. Scrivi i comandi VADD per ogni vettore in ogni indice
	s.db.IterateVectorIndexesUnlocked(func(indexName string, index *hnsw.Index, data core.VectorData) {
		cmd, err := buildVAddAOFCommand(indexName, data.ID, data.Vector, data.Metadata)
		if err == nil {
			tempFile.WriteString(cmd)
		}
	})

	// --- FASE 2: Sostituzione Atomica del File ---

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("impossibile chiudere il file AOF temporaneo: %w", err)
	}

	s.aofMutex.Lock()
	defer s.aofMutex.Unlock()

	// Il percorso del file AOF originale
	aofPath := s.aofFile.Name()

	// Chiudi il file AOF corrente prima di sostituirlo.
	if err := s.aofFile.Close(); err != nil {
		// Cerchiamo di riaprirlo per non lasciare il server in uno stato inconsistente.
		s.aofFile, _ = os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
		return fmt.Errorf("impossibile chiudere il file AOF originale prima della riscrittura: %w", err)
	}

	if err := os.Rename(tempFile.Name(), aofPath); err != nil {
		s.aofFile, _ = os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
		return fmt.Errorf("fallimento nella sostituzione del file AOF: %w", err)
	}

	newAOFFile, err := os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		// Questo è un errore grave, il server potrebbe non poter più persistere i dati.
		return fmt.Errorf("impossibile riaprire il nuovo file AOF dopo la riscrittura: %w", err)
	}
	s.aofFile = newAOFFile

	log.Println("Riscrittura AOF completata con successo.")
	return nil
}

// Save esegue uno snapshot "stop-the-world" dello stato del database
// su disco e poi tronca il file AOF.
func (s *Server) Save() error {
	log.Println("Avvio processo di snapshot (SAVE)...")

	// Usiamo un Lock() di scrittura per garantire che lo stato del database
	// sia completamente "congelato" durante l'operazione di snapshot.
	// s.store.Lock()
	// defer s.store.Unlock()

	// 2. Creazione del file temporaneo
	aofPath := s.aofFile.Name()
	snapshotPath := strings.TrimSuffix(aofPath, ".aof") + ".kdb"
	tempSnapshotPath := snapshotPath + ".tmp"

	file, err := os.Create(tempSnapshotPath)
	if err != nil {
		return fmt.Errorf("impossibile creare il file di snapshot temporaneo: %w", err)
	}
	defer file.Close()
	defer os.Remove(tempSnapshotPath) // Pulisci in caso di errore

	// 3. Scrivi lo snapshot sul file temporaneo
	log.Println("Scrittura dello stato in memoria sullo snapshot...")
	if err := s.db.Snapshot(file); err != nil {
		return fmt.Errorf("fallimento durante la scrittura dello snapshot: %w", err)
	}
	log.Println("Scrittura snapshot completata.")

	// 4. Sostituzione Atomica
	if err := os.Rename(tempSnapshotPath, snapshotPath); err != nil {
		return fmt.Errorf("fallimento nella sostituzione del file di snapshot: %w", err)
	}

	// 5. Troncare il file AOF
	// Questa è la parte più delicata. Dobbiamo bloccare le scritture AOF,
	// troncare il file e poi sbloccare.
	log.Println("Troncamento del file AOF...")
	s.aofMutex.Lock()
	defer s.aofMutex.Unlock()

	// Troncamento del file a dimensione 0
	if err := s.aofFile.Truncate(0); err != nil {
		return fmt.Errorf("fallimento nel troncamento del file AOF: %w", err)
	}
	// Riporta il cursore all'inizio per le nuove scritture
	if _, err := s.aofFile.Seek(0, 0); err != nil {
		return fmt.Errorf("fallimento nel riposizionare il cursore AOF: %w", err)
	}

	log.Println("Processo di snapshot (SAVE) completato con successo.")
	return nil
}

// looksLikeJSON rileva se un []byte inizia con '{' o '[' (semplice heuristic).
func looksLikeJSON(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	first := b[0]
	// Ignora spazi iniziali
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	if i >= len(b) {
		return false
	}
	first = b[i]
	return first == '{' || first == '['
}

// per comando ctrl + c
func (s *Server) Shutdown() {
	log.Println("Avvio graceful shutdown...")

	// Ferma il server HTTP
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("Errore shutdown server HTTP: %v", err)
	} else {
		log.Println("Server HTTP fermato.")
	}

	// Chiude il file AOF
	if s.aofFile != nil {
		s.aofFile.Close()
	}
	log.Println("Shutdown completato.")
}

// per chiudere le risorse in modo pulito
func (s *Server) Close() {
	if s.aofFile != nil {
		s.aofFile.Close()
	}
}

// Nuova funzione helper super-efficiente
func parseVectorFromByteParts(parts [][]byte) ([]float32, error) {
	vector := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(string(part), 32)
		if err != nil {
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}

// parseSavePolicies analizza la stringa della policy di salvataggio (es. "60 1000 300 10")
// e la converte in una slice di struct savePolicy.
func parseSavePolicies(policyStr string) ([]savePolicy, error) {
	policyStr = strings.TrimSpace(policyStr)
	if policyStr == "" {
		return nil, nil // Nessuna policy, è un caso valido.
	}

	parts := strings.Fields(policyStr)
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("formato policy di salvataggio non valido: numero di argomenti dispari")
	}

	var policies []savePolicy
	for i := 0; i < len(parts); i += 2 {
		seconds, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, fmt.Errorf("secondi non validi nella policy di salvataggio: '%s'", parts[i])
		}

		changes, err := strconv.ParseInt(parts[i+1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("numero di modifiche non valido nella policy di salvataggio: '%s'", parts[i+1])
		}

		if seconds <= 0 || changes <= 0 {
			return nil, fmt.Errorf("secondi e modifiche devono essere maggiori di zero")
		}

		policies = append(policies, savePolicy{
			Seconds: seconds,
			Changes: changes,
		})
	}

	// Ordina le policy dalla più stringente alla meno stringente (per tempo)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Seconds < policies[j].Seconds
	})

	return policies, nil
}

// --- NUOVE STRUCT PER LA GESTIONE DEI TASK ASINCRONI ---

// TaskStatus definisce i possibili stati di un task.
type TaskStatus string

const (
	TaskStatusStarted   TaskStatus = "started"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task rappresenta un'operazione a lunga esecuzione.
type Task struct {
	ID              string     `json:"id"`
	Status          TaskStatus `json:"status"`
	ProgressMessage string     `json:"progress_message,omitempty"`
	Error           string     `json:"error,omitempty"`
	mu              sync.RWMutex
}

// TaskManager tiene traccia di tutti i task in esecuzione.
type TaskManager struct {
	tasks map[string]*Task
	mu    sync.RWMutex
}

// NewTaskManager crea un nuovo gestore di task.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

// NewTask crea un nuovo task, lo registra e lo restituisce.
func (tm *TaskManager) NewTask() *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task := &Task{
		ID:     uuid.New().String(), // Genera un ID univoco
		Status: TaskStatusStarted,
	}
	tm.tasks[task.ID] = task
	return task
}

// GetTask recupera un task in modo sicuro.
func (tm *TaskManager) GetTask(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task, found := tm.tasks[id]
	return task, found
}

// --- Metodi di aggiornamento per un Task ---
// (Questi metodi verranno chiamati dalla goroutine in background)

func (t *Task) SetStatus(status TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
}

func (t *Task) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = TaskStatusFailed
	t.Error = err.Error()
}

func (t *Task) SetProgress(message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ProgressMessage = message
}
