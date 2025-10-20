package server

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/text"
	"log"
	"os"
	"path/filepath" // Per manipolare i percorsi dei file
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// il worker che rappresenta un sinfolo task di sincronizzazione, il guardiano per la cartella da monitorare
type Vectorizer struct {
	config       VectorizerConfig
	server       *Server // riiferimento al server per accedere allo store ecc
	ticker       *time.Ticker
	stopCh       chan struct{}
	lastRun      time.Time
	currentState atomic.Value // Per memorizzare lo stato in modo thread-safe
	wg           *sync.WaitGroup
}

// NewVectorizer crea e avvia un nuovo worker per un task di sincronizzazione.
func NewVectorizer(config VectorizerConfig, server *Server, wg *sync.WaitGroup) (*Vectorizer, error) {
	// Parsa la durata della schedulazione (es. "30s", "5m")
	schedule, err := time.ParseDuration(config.Schedule)
	if err != nil {
		return nil, fmt.Errorf("formato schedule non valido per il vectorizer '%s': %w", config.Name, err)
	}

	vec := &Vectorizer{
		config: config,
		server: server,
		ticker: time.NewTicker(schedule),
		stopCh: make(chan struct{}),
		wg:     wg,
	}

	// Aggiunge 1 al contatore del WaitGroup PRIMA di avviare la goroutine
	// vec.wg.Add(1)
	// Avvia il loop principale del worker in una goroutine
	// go vec.run()

	log.Printf("Vectorizer '%s' avviato. Controllerà ogni %s.", config.Name, config.Schedule)
	return vec, nil
}

// run è il core loop del worker.
func (v *Vectorizer) run() {
	// Segnala che questa goroutine è terminata quando la funzione finisce.
	defer v.wg.Done()
	defer log.Printf("Vectorizer '%s' fermato.", v.config.Name)

	// Esegui un primo check subito all'avvio
	log.Printf("Vectorizer '%s': Esecuzione del primo controllo di sincronizzazione...", v.config.Name)
	v.currentState.Store("idle") // Imposta lo stato iniziale
	v.synchronize()

	for {
		select {
		case <-v.ticker.C:
			// Il ticker è scattato, esegui la sincronizzazione
			log.Printf("Vectorizer '%s': Controllo periodico avviato...", v.config.Name)
			v.currentState.Store("synchronizing")
			v.synchronize()
			v.lastRun = time.Now()
			v.currentState.Store("idle")
		case <-v.stopCh:
			// Abbiamo ricevuto un segnale di stop
			v.ticker.Stop()
			return
		}
	}
}

// synchronize è la funzione che contiene la logica di business:
// 1. Controlla la sorgente per i cambiamenti.
// 2. Chiama l'embedder per i file nuovi/modificati.
// 3. Salva i risultati in KektorDB.
func (v *Vectorizer) synchronize() {
	sourcePath := v.config.Source.Path
	if v.config.Source.Type != "filesystem" {
		log.Printf("Vectorizer '%s': tipo di sorgente '%s' non supportato.", v.config.Name, v.config.Source.Type)
		return
	}

	// 1. Trova i file che sono cambiati dall'ultima sincronizzazione
	changedFiles, err := v.findChangedFiles(sourcePath)
	if err != nil {
		log.Printf("ERRORE nel Vectorizer '%s': impossibile scansionare la sorgente: %v", v.config.Name, err)
		return
	}

	if len(changedFiles) == 0 {
		log.Printf("Vectorizer '%s': Nessun file nuovo o modificato trovato.", v.config.Name)
		return
	}

	log.Printf("Vectorizer '%s': Trovati %d file da processare.", v.config.Name, len(changedFiles))

	// 2. Processa ogni file cambiato
	var successful, failed int
	for _, filePath := range changedFiles {
		err := v.processFile(filePath)
		if err != nil {
			log.Printf("ERRORE nel Vectorizer '%s': fallimento nel processare il file '%s': %v", v.config.Name, filePath, err)
			failed++
		} else {
			successful++
		}
	}

	log.Printf("Vectorizer '%s': Sincronizzazione completata. Successi: %d, Fallimenti: %d.", v.config.Name, successful, failed)

}

// Stop ferma il worker in modo pulito.
func (v *Vectorizer) Stop() {
	close(v.stopCh)
}

// HELPER

// findChangedFiles scansiona una directory e restituisce una lista di file
// che sono nuovi o sono stati modificati dall'ultimo controllo.
func (v *Vectorizer) findChangedFiles(root string) ([]string, error) {
	var changedFiles []string

	// filepath.Walk è una funzione Go standard per visitare ricorsivamente una directory
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Ignora le directory
		if info.IsDir() {
			return nil
		}

		// --- Logica di Stato ---
		// Usiamo il KV store per memorizzare l'ultimo timestamp processato per ogni file.
		// La chiave sarà univoca per questo vectorizer e questo file.
		stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, path)

		lastModTimeBytes, found := v.server.db.GetKVStore().Get(stateKey)

		lastModTime := int64(0)
		if found {
			lastModTime, _ = strconv.ParseInt(string(lastModTimeBytes), 10, 64)
		}

		currentModTime := info.ModTime().UnixNano()

		// Se il file è stato modificato (o è nuovo), lo aggiungiamo alla lista.
		if currentModTime > lastModTime {
			changedFiles = append(changedFiles, path)
		}

		return nil
	})

	return changedFiles, err

}

// Aggiungi un metodo GetStatus
func (v *Vectorizer) GetStatus() VectorizerStatus {
	return VectorizerStatus{
		Name:         v.config.Name,
		IsRunning:    true, // Se l'oggetto esiste, è in esecuzione
		LastRun:      v.lastRun,
		CurrentState: v.currentState.Load().(string),
	}
}

// processFile gestisce il flusso completo per un singolo file.
func (v *Vectorizer) processFile(filePath string) error {
	log.Printf("  -> Processando '%s'...", filePath)

	// 1. Leggi il contenuto del file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("impossibile leggere il file: %w", err)
	}

	textContent := string(content)

	// Prendi i parametri di chunking dalla configurazione.
	// Usa dei default sensati se non sono specificati.
	chunkSize := v.config.DocProcessor.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 500 // Default a 500 caratteri
	}
	overlapSize := chunkSize / 10 // Default a 10% di sovrapposizione

	// Dividi il documento in chunk
	chunks := text.FixedSizeChunker(textContent, chunkSize, overlapSize)
	log.Printf("     -> File diviso in %d chunk.", len(chunks))

	idx, ok := v.server.db.GetVectorIndex(v.config.KektorIndex)
	if !ok {
		log.Printf("Vectorizer '%s': indice di destinazione '%s' non trovato. Tentativo di creazione automatica...", v.config.Name, v.config.KektorIndex)

		// Definisci i parametri di default per la creazione automatica
		metric := distance.Cosine
		precision := distance.Float32
		textLang := "english"
		m := 16               // Default ragionevole
		efConstruction := 200 // Default ragionevole

		// --- CORREZIONE AOF PER VCREATE ---
		// Scrivi il comando di creazione sull'AOF PRIMA di creare l'indice.
		aofCommand := formatCommandAsRESP("VCREATE",
			[]byte(v.config.KektorIndex),
			[]byte("METRIC"), []byte(metric),
			[]byte("PRECISION"), []byte(precision),
			[]byte("M"), []byte(strconv.Itoa(m)),
			[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efConstruction)),
			[]byte("TEXT_LANGUAGE"), []byte(textLang),
		)
		v.server.aofMutex.Lock()
		v.server.aofFile.WriteString(aofCommand)
		v.server.aofMutex.Unlock()

		// Ora crea l'indice in memoria
		err := v.server.db.CreateVectorIndex(v.config.KektorIndex, metric, m, efConstruction, precision, textLang)
		if err != nil {
			return fmt.Errorf("impossibile creare automaticamente l'indice '%s': %w", v.config.KektorIndex, err)
		}

		idx, _ = v.server.db.GetVectorIndex(v.config.KektorIndex)
		log.Printf("Indice '%s' creato automaticamente.", v.config.KektorIndex)
	}

	// Recupera le informazioni del file una sola volta
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("impossibile ottenere informazioni per il file '%s': %w", filePath, err)
	}
	modTimeStr := fileInfo.ModTime().Format(time.RFC3339) // Formato standard ISO 8601

	// --- LOOP SUI CHUNK ---
	var failedChunks int
	for _, chunk := range chunks {
		// 1. Ottieni l'embedding per il contenuto del chunk
		vector, err := v.getEmbedding(chunk.Content)
		if err != nil {
			// Se un chunk fallisce, logghiamo l'errore ma continuiamo con gli altri
			log.Printf("ERRORE (Chunk %d): Impossibile ottenere l'embedding per '%s': %v", chunk.ChunkNumber, filePath, err)
			failedChunks++
			continue
		}

		// 2. Crea un ID univoco per il chunk
		vectorID := fmt.Sprintf("%s-chunk-%d", filePath, chunk.ChunkNumber)

		// 3. Costruisci i metadati
		metadata := map[string]interface{}{
			"source_file":   filePath,
			"chunk_number":  chunk.ChunkNumber,
			"content_chunk": chunk.Content, // Memorizza il testo del chunk per le app RAG
		}

		// Applica il template definito dall'utente
		for key, valueTpl := range v.config.MetadataTemplate {
			val := valueTpl // Inizia con il valore del template
			val = strings.ReplaceAll(val, "{{file_path}}", filePath)
			val = strings.ReplaceAll(val, "{{mod_time}}", modTimeStr)
			val = strings.ReplaceAll(val, "{{chunk_num}}", strconv.Itoa(chunk.ChunkNumber))
			metadata[key] = val
		}

		// 4. Aggiungi il vettore e i metadati a KektorDB
		// (La logica di Add, AddMetadata e rollback rimane la stessa)
		internalID, err := idx.Add(vectorID, vector)
		if err != nil {
			if strings.Contains(err.Error(), "già esistente") {
				// Se l'ID esiste già, lo stiamo aggiornando.
				// In futuro potremmo implementare una logica di update più esplicita.
				// Per ora, lo ignoriamo.
				continue
			}
			log.Printf("ERRORE: Impossibile aggiungere il vettore per il chunk %d di '%s': %v", chunk.ChunkNumber, filePath, err)
			continue
		}
		if err := v.server.db.AddMetadata(v.config.KektorIndex, internalID, metadata); err != nil {
			idx.Delete(vectorID) // Rollback
			log.Printf("ERRORE: Impossibile aggiungere i metadati per il chunk %d di '%s': %v", chunk.ChunkNumber, filePath, err)
			continue
		}

		// --- CORREZIONE AOF PER VADD ---
		// Dopo che l'operazione in memoria ha avuto successo, scrivi sull'AOF.
		vectorStr := float32SliceToString(vector)
		metadataBytes, _ := json.Marshal(metadata)

		aofCommand := formatCommandAsRESP("VADD",
			[]byte(v.config.KektorIndex),
			[]byte(vectorID),
			[]byte(vectorStr),
			metadataBytes,
		)
		v.server.aofMutex.Lock()
		v.server.aofFile.WriteString(aofCommand)
		v.server.aofMutex.Unlock()
	}
	// --- FINE LOOP ---

	// Aggiorna lo stato nel KV store per l'INTERO file
	info, _ := os.Stat(filePath)
	stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, filePath)
	newModTime := fmt.Sprintf("%d", info.ModTime().UnixNano())

	aofCommand := formatCommandAsRESP("SET",
		[]byte(stateKey),
		[]byte(newModTime),
	)
	v.server.aofMutex.Lock()
	v.server.aofFile.WriteString(aofCommand)
	v.server.aofMutex.Unlock()

	v.server.db.GetKVStore().Set(stateKey, []byte(newModTime))

	// Se tutti i chunk sono falliti, restituisci un errore per l'intero file.
	if failedChunks == len(chunks) && len(chunks) > 0 {
		return fmt.Errorf("fallimento nell'ottenere l'embedding per tutti i %d chunk", len(chunks))
	}

	return nil

	//////////////////////////////////////////////////////////////////////////
	/*
		// 2. Ottieni l'embedding chiamando l'API esterna
		vector, err := v.getEmbedding(textContent)
		if err != nil {
			return fmt.Errorf("impossibile ottenere l'embedding: %w", err)
		}

		// L'ID del vettore sarà il percorso del file.
		vectorID := filePath

		// --- INIZIO BLOCCO CORRETTO PER L'AGGIUNTA ---

		// 3. Aggiungi il vettore all'indice HNSW per ottenere l'ID interno
		idx, ok := v.server.db.GetVectorIndex(v.config.KektorIndex)
		if !ok {
			log.Printf("Vectorizer '%s': indice di destinazione '%s' non trovato. Tentativo di creazione automatica...", v.config.Name, v.config.KektorIndex)
			// Crea l'indice con parametri di default.
			// Per la ricerca ibrida, 'cosine' e 'english' sono buoni default.
			errCreate := v.server.db.CreateVectorIndex(
				v.config.KektorIndex,
				distance.Cosine, // Default a Cosine
				0, 0,            // Default a M e efConstruction
				distance.Float32, // Default a float32
				"english",        // Default per l'analisi del testo
			)
			if errCreate != nil {
				return fmt.Errorf("impossibile creare automaticamente l'indice '%s': %w", v.config.KektorIndex, err)
			}
			// Ora recupera l'indice appena creato
			idx, _ = v.server.db.GetVectorIndex(v.config.KektorIndex)
			log.Printf("Indice '%s' creato automaticamente.", v.config.KektorIndex)
		}

		internalID, err := idx.Add(vectorID, vector)
		if err != nil {
			// Se l'ID esiste già, potrebbe essere un aggiornamento. Per ora, lo trattiamo
			// come un errore e ci fermiamo. In futuro potremmo implementare una logica di update.
			return fmt.Errorf("impossibile aggiungere il vettore al database: %w", err)
		}

		// 4. Costruisci e aggiungi i metadati
		// TODO: Implementare una logica per sostituire i placeholder come {{file_path}}
		metadata := map[string]interface{}{
			"source_file": filePath,
			"content":     textContent, // Salviamo anche il contenuto per la ricerca ibrida
		}

		// Chiama il metodo pubblico dello store che gestisce il locking
		if err := v.server.db.AddMetadataUnlocked(v.config.KektorIndex, internalID, metadata); err != nil {
			// Eseguiamo un rollback se i metadati falliscono
			idx.Delete(vectorID)
			return fmt.Errorf("impossibile aggiungere i metadati: %w", err)
		}

		// --- FINE BLOCCO CORRETTO ---

		// 5. Aggiorna lo stato nel KV store per non riprocessare il file
		info, _ := os.Stat(filePath)
		stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, filePath)
		newModTime := fmt.Sprintf("%d", info.ModTime().UnixNano())

		// `Set` nel KVStore è thread-safe, quindi possiamo chiamarlo direttamente
		v.server.db.GetKVStore().Set(stateKey, []byte(newModTime))

		return nil
	*/
}
