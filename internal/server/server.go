package server

import (
	"bufio"
	"bytes"
	"context" // per gestire graceful shutdown (ctrl + c)
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sanonone/kektordb/internal/protocol"
	"github.com/sanonone/kektordb/internal/store"
	"github.com/sanonone/kektordb/internal/store/distance"
	"io"
	"log"
	"net"
	"net/http" // per il web server
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// crea un nuovo tipo commandHandler, questo tipo rappresenta
// qualsiasi funzione che abbia i parametri definiti e nessun valore
// di ritorno
type commandHandler func(conn net.Conn, args [][]byte)

// struct server con informazioni necessarie

type Server struct {
	listener net.Listener
	commands map[string]commandHandler
	store    *store.Store // puntatore allo store

	aofFile    *os.File     // file aof per persistenza
	aofMutex   sync.Mutex   // mutex per gestire la scrittura sul file
	httpServer *http.Server // il server http
}

// crea una nuova istanza del server
func NewServer(aofPath string) (*Server, error) {
	// aprie o crea il file AOF
	//0666 sono i permessi del file
	file, err := os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("Impossibile aprire il file AOF: %v", err)
	}

	s := &Server{
		// la mappa che fa da dispatcher
		// la chiave è il nome del comando in maiuscolo
		// il valore è la funzione che lo gestisce
		commands: make(map[string]commandHandler),
		store:    store.NewStore(), // inizializzo store
		aofFile:  file,
	}
	// registrazione dei comandi
	s.registerCommands()
	return s, nil
}

func (s *Server) registerCommands() {
	// i nuovi comandi si aggiungeranno tutti qui
	s.commands["PING"] = s.handlePing
	s.commands["SET"] = s.handleSet
	s.commands["GET"] = s.handleGet
	s.commands["DELETE"] = s.handleDelete
	s.commands["VCREATE"] = s.handleVCreate
	s.commands["VADD"] = s.handleVAdd
	s.commands["VSEARCH"] = s.handleVSearch
	s.commands["VDEL"] = s.handleVDelete
}

// avvia i server TCP e HTTP e li mette in ascolto sulle porte specificate
func (s *Server) Run(tcpAddr string, httpAddr string) error {
	// prima di tutto carica i dati dall'AOF
	if err := s.loadFromAOF(); err != nil {
		return fmt.Errorf("impossibile caricare da AOF: %w", err)
	}

	// crea tcpListener sulla porta ricevuta come parametro
	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return err
	}
	defer s.Close() // metodo per chiusura tcpListener e file

	// se ci sono errori durante la funzione chiude il tcpListener
	// defer tcpListener.Close()

	s.listener = tcpListener

	log.Printf("Server TCP in ascolto su %s", tcpAddr)
	go s.acceptTCPLoop() // funzione che gestisce il loop infinito per accettare richieste TCP

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

// funzione per gestire le richieste TCP
func (s *Server) acceptTCPLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Se il listener è chiuso, usciamo dal loop
			if errors.Is(err, net.ErrClosed) {
				log.Println("Server TCP fermato.")
				return
			}
			log.Printf("Errore nell'accettare la connessione TCP: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// vectorIndexState contiene lo stato di un singolo indice vettoriale.
type vectorIndexState struct {
	metric         distance.DistanceMetric
	m              int
	efConstruction int
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

		cmd, err := protocol.Parse(line)
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

		case "VCREATE":
			// Formato AOF ora atteso: VCREATE <index_name> [METRIC <metric>] [M <m_val>] [EF_CONSTRUCTION <ef_val>]
			if len(cmd.Args) >= 1 {
				indexName := string(cmd.Args[0])

				// Valori di default
				metric := distance.Euclidean
				m := 0 // 0 per usare il default in NewHNSWIndex
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

	// --- FASE 3: Ricostruzione dello Stato nello Store ---
	log.Println("Ricostruzione dello stato compattato in memoria...")

	// Ricostruisci il KV store
	for key, value := range state.kvData {
		s.store.GetKVStore().Set(key, value)
	}

	// Ricostruisci gli indici vettoriali
	totalVectors := 0
	addedVectors := 0
	skippedDeleted := 0
	for indexName, indexState := range state.vectorIndexes {
		log.Printf("[AOF] Ricostruzione indice '%s' (Metrica: %s) - Vettori: %d", indexName, indexState.metric, len(indexState.entries))
		// --- CHIAMATA CORRETTA ---
		// Ora passiamo la metrica che abbiamo salvato nello stato
		err := s.store.CreateVectorIndex(indexName, indexState.metric, indexState.m, indexState.efConstruction)
		if err != nil {
			log.Printf("[AOF] ERRORE: impossibile creare l'indice '%s' con metrica %s, M=%d, EF=%d: %v",
				indexName, indexState.metric, indexState.m, indexState.efConstruction, err)
			continue
		}
		idx, found := s.store.GetVectorIndex(indexName)
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
					s.store.AddMetadata(indexName, internalID, entry.metadata)
				}
			}
		}

		log.Printf("[AOF] indice '%s' ricostruito: aggiunti=%d, skippati_deleted=%d", indexName, addedVectors, skippedDeleted)
	}

	log.Printf("Caricamento AOF completato. vettori_totali=%d aggiunti=%d skippati_deleted=%d", totalVectors, addedVectors, skippedDeleted)
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

/*
func (s *Server) loadFromAOF() error {
	log.Println("Caricamento di dati dal file AOF...")

	// sposta il cursore all'inizio del file per la lettura
	s.aofFile.Seek(0, 0)

	// lo scanner per leggere il file riga per riga
	scanner := bufio.NewScanner(s.aofFile)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// fa il parsing e riesegue tutti i comandi nel file
		cmd, err := protocol.Parse(line)
		if err != nil {
			log.Printf("Errore AOF: impossibile parsare la riga '%s': %v", line, err)
			continue
		}

		handler, ok := s.commands[cmd.Name]
		if !ok {
			log.Printf("Errore AOF: comando sconosciuto '%s'", cmd.Name)
			continue
		}

		// eseguo l'handler ma senza passare la vera connessione di rete
		// ma nil visto che non deve rispondere a nessuno
		handler(nil, cmd.Args)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	log.Println("Caricamento AOF completato")
	return nil
}
*/

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("Nuovo client connesso: %s", conn.RemoteAddr())

	// un reader con buffer per efficientare riducendo il numero di chiamate di sistema per leggere i dati dalla rete
	reader := bufio.NewReader(conn)

	for {
		// legge fino al nuova riga '\n'
		line, err := reader.ReadString('\n')
		if err != nil {
			// gestione caso End Of File io.EOF che si riceve se il client chiude la connessione
			if err == io.EOF {
				log.Printf("Client disconnesso: %s", conn.RemoteAddr())
			} else {
				log.Printf("Errore di lettura: %v", err)
			}
			return // esce e chiude la connessione
		}

		// non esegue direttamente ma chiama un metodo che si occuperà
		// anche della persistenza
		s.dispatchCommand(conn, line)

		/* [vecchia versione senza persistenza]
		// 1) pasring della riga di testo in un comando strutturato
		cmd, err := protocol.Parse(line)
		if err != nil {
			// se il comando non è valido invia un errore al client
			s.writeError(conn, fmt.Sprintf("Protocol error: %v", err))
			continue
		}

		// 2) dispatch: cerca la funzione corrispondente al nome del comando parsato
		handler, ok := s.commands[cmd.Name]
		if !ok {
			s.writeError(conn, fmt.Sprintf("comando sconosciuto '%s'", cmd.Name))
			continue
		}

		// 3) esegue la funzione trovata
		handler(conn, cmd.Args)

		// per il momento risponde al client con la stessa riga
		// fmt.Printf("Ricevuto: %s", line)
		// conn.Write([]byte(line))
		*/
	}
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

	// Ferma il server TCP
	if s.listener != nil {
		s.listener.Close()
	}

	// Chiude il file AOF
	if s.aofFile != nil {
		s.aofFile.Close()
	}
	log.Println("Shutdown completato.")
}

// dispatchCommand è il nuovo punto centrale
func (s *Server) dispatchCommand(conn net.Conn, line string) {
	cmd, err := protocol.Parse(line)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("protocol error: %v", err))
		return
	}

	// --- LOG DI DEBUG ---
	log.Printf("[DEBUG] Comando Parsato: Nome='%s', NumArgs=%d", cmd.Name, len(cmd.Args))
	for i, arg := range cmd.Args {
		log.Printf("[DEBUG] Arg %d: '%s'", i, string(arg))
	}
	// --- FINE LOG DI DEBUG ---

	handler, ok := s.commands[cmd.Name]
	if !ok {
		s.writeError(conn, fmt.Sprintf("comando sconosciuto '%s'", cmd.Name))
		return
	}

	// se è un comando di scrittura
	if s.isWriteCommand(cmd.Name) {
		// gestione scrittura su file con un mutex
		s.aofMutex.Lock()
		defer s.aofMutex.Unlock()

		// scrive il comando sul file AOF
		// TrimSpace rimuove il \n finale letto da ReadString
		// e scrivere una riga pulita
		_, err := s.aofFile.WriteString(strings.TrimSpace(line) + "\n")
		if err != nil {
			log.Printf("ERRORE CRITICO AOF: impossibile scrivere su file: %v", err)
			// nella realtà si potrebbe terminare il server o entrare in modalità sola lettura
			s.writeError(conn, "errore interno del server (AOF)")
			return
		}
	}

	// esegue il comando
	handler(conn, cmd.Args)
}

// Funzione helper per identificare i comandi di scrittura.
func (s *Server) isWriteCommand(name string) bool {
	switch name {
	case "SET", "DEL", "VCREATE", "VADD", "VDEL":
		return true
	default:
		return false
	}
}

// per chiudere le risorse in modo pulito
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	if s.aofFile != nil {
		s.aofFile.Close()
	}
}

// handlePing gestisce il comando PING
func (s *Server) handlePing(conn net.Conn, args [][]byte) {
	// Secondo il protocollo, PING risponde con PONG
	s.writeSimpleString(conn, "PONG")
}

func (s *Server) handleSet(conn net.Conn, args [][]byte) {
	if len(args) < 2 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per il comando 'SET'")
		}
		return
	}

	key := string(args[0])
	value := bytes.Join(args[1:], []byte(" "))

	s.store.GetKVStore().Set(key, value)

	if conn != nil {
		// risponde con +OK
		s.writeSimpleString(conn, "OK")
	}
}

func (s *Server) handleGet(conn net.Conn, args [][]byte) {
	// GET key
	if len(args) != 1 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per il comando 'GET'")
		}
		return
	}

	key := string(args[0])

	value, found := s.store.GetKVStore().Get(key)
	if !found {
		// Rispondi con un Null Bulk String se la chiave non esiste.
		s.writeNullBulkString(conn)
		return
	}

	// Rispondi con un Bulk String se la chiave esiste.
	s.writeBulkString(conn, value)
}

func (s *Server) handleDelete(conn net.Conn, args [][]byte) {
	// DEL key
	if len(args) != 1 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per il comando 'DEL'")
		}
		return
	}

	key := string(args[0])
	s.store.GetKVStore().Delete(key)

	if conn != nil {
		// risponde con OK.
		s.writeSimpleString(conn, "OK")
	}
}

func (s *Server) handleVCreate(conn net.Conn, args [][]byte) {
	// Formato: VCREATE <index_name> [METRIC <metric>] [M <m_val>] [EF_CONSTRUCTION <ef_val>]
	if len(args) < 1 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per 'VCREATE'")
		}
		return
	}

	indexName := string(args[0])
	// valori di default
	metric := distance.Euclidean // Default
	m := 0                       //  0 così sarà usato il valore di default in NewHNSWIndex
	efConstruction := 0          // 0 per motivo sopra

	// Parsing degli argomenti opzionali
	// Iteriamo sulla slice di argomenti a coppie
	for i := 1; i < len(args); i += 2 {
		// Controlla che ci sia un valore dopo la chiave
		if i+1 >= len(args) {
			if conn != nil {
				s.writeError(conn, "parametro opzionale incompleto per 'VCREATE'")
			}
			return
		}

		key := strings.ToUpper(string(args[i]))
		value := string(args[i+1])

		switch key {
		case "METRIC":
			metric = distance.DistanceMetric(value)
		case "M":
			val, err := strconv.Atoi(value)
			if err != nil {
				if conn != nil {
					s.writeError(conn, "il valore per 'M' deve essere un intero")
				}
				return
			}
			m = val
		case "EF_CONSTRUCTION":
			val, err := strconv.Atoi(value)
			if err != nil {
				if conn != nil {
					s.writeError(conn, "il valore per 'EF_CONSTRUCTION' deve essere un intero")
				}
				return
			}
			efConstruction = val
		default:
			if conn != nil {
				s.writeError(conn, fmt.Sprintf("parametro sconosciuto '%s' per 'VCREATE'", key))
			}
			return
		}
	}

	// La logica AOF è gestita da dispatchCommand, non dobbiamo fare nulla qui.

	err := s.store.CreateVectorIndex(indexName, metric, m, efConstruction)
	if err != nil {
		if conn != nil {
			s.writeError(conn, err.Error())
		}
		return
	}

	if conn != nil {
		s.writeSimpleString(conn, "OK")
	}
}

// handleVAdd gestisce l'aggiunta di un vettore a un indice.

func (s *Server) handleVAdd(conn net.Conn, args [][]byte) {

	// --- LOG DI DEBUG ---
	log.Printf("[DEBUG] handleVAdd ricevuto: NumArgs=%d", len(args))
	for i, arg := range args {
		log.Printf("[DEBUG] handleVAdd Arg %d: '%s'", i, string(arg))
	}
	// --- FINE LOG DI DEBUG ---

	if len(args) < 3 { // Accettiamo anche 4 argomenti, quindi il check è un po' più complesso
		if conn != nil { // <-- AGGIUNGI QUESTO CONTROLLO
			s.writeError(conn, "numero di argomenti errato per 'VADD'")
		}
		return // Esci silenziosamente se stiamo caricando da AOF
	}

	indexName := string(args[0])
	vectorID := string(args[1])
	vectorParts := args[2:]

	var metadataJSON []byte

	// --- LOGICA DI SEPARAZIONE METADATI ---
	// Controlla se l'ULTIMO argomento è un JSON.
	if len(vectorParts) > 0 {
		lastArg := vectorParts[len(vectorParts)-1]
		// Un check semplice ma efficace: un JSON valido inizia con { e finisce con }
		if len(lastArg) > 1 && lastArg[0] == '{' && lastArg[len(lastArg)-1] == '}' {
			metadataJSON = lastArg
			vectorParts = vectorParts[:len(vectorParts)-1] // Rimuovi il JSON dalle parti del vettore
		}
	}
	// --- FINE LOGICA DI SEPARAZIONE ---

	if len(vectorParts) == 0 {
		if conn != nil {
			s.writeError(conn, "il comando VADD non contiene un vettore")
		}
		return
	}

	vector, err := parseVectorFromByteParts(vectorParts)
	if err != nil {
		if conn != nil { // <-- AGGIUNGI QUESTO CONTROLLO
			s.writeError(conn, fmt.Sprintf("formato vettore invalido: %v", err))
		}
		return
	}

	idx, found := s.store.GetVectorIndex(indexName)
	if !found {
		if conn != nil { // <-- AGGIUNGI QUESTO CONTROLLO
			s.writeError(conn, fmt.Sprintf("indice '%s' non trovato", indexName))
		}
		return
	}

	// Ora la logica di inserimento...
	internalID, err := idx.Add(vectorID, vector)
	if err != nil {
		if conn != nil { // <-- AGGIUNGI QUESTO CONTROLLO
			s.writeError(conn, fmt.Sprintf("errore nell'aggiungere il vettore: %v", err))
		}
		return
	}

	// Gestione Metadati (ora usiamo la nostra variabile `metadataJSON`)
	if len(metadataJSON) > 0 {
		var metadata map[string]any
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			if conn != nil {
				s.writeError(conn, "formato JSON dei metadati invalido")
			}
			return
		}

		if err := s.store.AddMetadata(indexName, internalID, metadata); err != nil {
			if conn != nil {
				s.writeError(conn, fmt.Sprintf("errore nell'indicizzare i metadati: %v", err))
			}
			return
		}
	}

	// aggiungere logica AOF

	// Infine, scrivi la risposta di successo solo se c'è una connessione
	if conn != nil {
		s.writeSimpleString(conn, "OK")
	}
}

/*
func (s *Server) handleVAdd(conn net.Conn, args [][]byte) {
	// VADD <index> <id> <vector> [metadata_json]
	if len(args) < 3 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti insufficiente per 'VADD'")
		}
		return
	}
	indexName := string(args[0])
	vectorID := string(args[1])
	// vectorStr := string(args[2])
	vectorStr := args[2:]

	idx, found := s.store.GetVectorIndex(indexName)
	if !found {
		s.writeError(conn, fmt.Sprintf("indice '%s' non trovato", indexName))
		return
	}

	vector, err := parseVectorFromParts(vectorStr)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("formato vettore invalido: %v", err))
		return
	}

	internalID, err := idx.Add(vectorID, vector)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("errore nell'aggiungere il vettore: %v", err))
		return
	}

	// Se ci sono metadati, parsali e indicizzali.
	if len(args) == 4 && len(args[3]) > 0 {
		var metadata map[string]any
		if err := json.Unmarshal(args[3], &metadata); err != nil {
			s.writeError(conn, "formato JSON dei metadati invalido")
			return // Potremmo voler "disfare" l'aggiunta del vettore, ma per ora è ok.
		}

		if err := s.store.AddMetadata(indexName, internalID, metadata); err != nil {
			s.writeError(conn, fmt.Sprintf("errore nell'indicizzare i metadati: %v", err))
			return
		}
	}

	s.writeSimpleString(conn, "OK")
}
*/

// handleVSearch gestisce la ricerca in un indice.
func (s *Server) handleVSearch(conn net.Conn, args [][]byte) {
	if len(args) < 3 {
		s.writeError(conn, "numero di argomenti errato per 'VSEARCH'")
		return
	}
	indexName := string(args[0])
	kStr := string(args[1])
	// vectorStr := string(args[2])
	vectorParts := args[2:]

	k, err := strconv.Atoi(kStr)
	if err != nil || k <= 0 {
		s.writeError(conn, "k deve essere un intero positivo")
		return
	}

	idx, found := s.store.GetVectorIndex(indexName)
	if !found {
		s.writeError(conn, fmt.Sprintf("indice '%s' non trovato", indexName))
		return
	}

	queryVector, err := parseVectorFromByteParts(vectorParts)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("formato vettore di query invalido: %v", err))
		return
	}

	results := idx.Search(queryVector, k, nil)

	// Dobbiamo formattare la risposta. Una Bulk String con gli ID separati da spazi.
	responseStr := strings.Join(results, " ")
	s.writeBulkString(conn, []byte(responseStr))
}

// gestisce l'eliminazione di un vettore da un indice
func (s *Server) handleVDelete(conn net.Conn, args [][]byte) {
	// VDEL <index_name> <id>
	if len(args) != 2 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per 'VDEL'")
		}
		return
	}
	indexName := string(args[0])
	vectorID := string(args[1])

	idx, found := s.store.GetVectorIndex(indexName)
	if !found {
		if conn != nil {
			s.writeError(conn, fmt.Sprintf("indice '%s' non trovato", indexName))
		}
		return
	}

	idx.Delete(vectorID)

	if conn != nil {
		s.writeSimpleString(conn, "OK")
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

// Nuova funzione helper per parsare una stringa, da non confondere con parseVectorFromParts
func parseVectorFromString(s string) ([]float32, error) {
	parts := strings.Fields(s) // Dividi la stringa per gli spazi
	if len(parts) == 0 {
		return nil, fmt.Errorf("la stringa del vettore è vuota")
	}
	vector := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(part, 32)
		if err != nil {
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}

/*
// Nuova funzione helper per parsare da una slice di [][]byte
func parseVectorFromParts(parts [][]byte) ([]float32, error) {
	vector := make([]float32, len(parts))
	for i, part := range parts {
		// Dobbiamo convertire []byte in string prima di parsare.
		val, err := strconv.ParseFloat(string(part), 32)
		if err != nil {
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}
*/

/*
// Funzione helper per parsare una stringa di numeri float separati da spazi.
func parseVector(s string) ([]float32, error) {
	parts := strings.Fields(s)
	vector := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(part, 32)
		if err != nil {
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}
*/

// --- Helper per le Risposte

// writeSimpleString scrive una stringa semplice (es. +OK)
func (s *Server) writeSimpleString(conn net.Conn, msg string) {
	// il protocollo richiede + e \r\n
	conn.Write([]byte(fmt.Sprintf("+%s\r\n", msg)))
}

// writeError scrive un errore (es. -ERROR message)
func (s *Server) writeError(conn net.Conn, msg string) {
	// il protocollo richiede - e \r\n
	conn.Write([]byte(fmt.Sprintf("-%s\r\n", strings.ToUpper(msg))))
}

// writeBulkString scrive una bulk string (es. $4\r\ncasa\r\n)
func (s *Server) writeBulkString(conn net.Conn, data []byte) {
	conn.Write([]byte(fmt.Sprintf("$%d\r\n", len(data))))
	conn.Write(data)
	conn.Write([]byte("\r\n"))
}

// writeNullBulkString scrive un null bulk string (es. $-1\r\n)
func (s *Server) writeNullBulkString(conn net.Conn) {
	conn.Write([]byte("$-1\r\n"))
}
