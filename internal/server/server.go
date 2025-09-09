package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"strconv"
	"os"
	"sync"
	"github.com/sanonone/kektordb/internal/store"
	"github.com/sanonone/kektordb/internal/protocol"
)

// crea un nuovo tipo commandHandler, questo tipo rappresenta
// qualsiasi funzione che abbia i parametri definiti e nessun valore
// di ritorno
type commandHandler func(conn net.Conn, args [][]byte)

// struct server con informazioni necessarie
type Server struct{
	listener net.Listener 
	commands map[string]commandHandler
	store *store.Store // puntatore allo store 

	aofFile *os.File // file aof per persistenza
	aofMutex sync.Mutex // mutex per gestire la scrittura sul file 
}

// crea una nuova istanza del server 
func NewServer() *Server{
	// aprie o crea il file AOF 
	//0666 sono i permessi del file 
	file, err := os.OpenFile("kektordb.aof", os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("Impossibile aprire il file AOF: %v", err)
	}

	s := &Server{
		// la mappa che fa da dispatcher
		// la chiave è il nome del comando in maiuscolo
		// il valore è la funzione che lo gestisce
		commands: make(map[string]commandHandler),
		store: store.NewStore(), // inizializzo store 
		aofFile: file,
	}
	// registrazione dei comandi. Per ora solo PING
	s.registerCommands()
	return s
}

func (s *Server) registerCommands(){
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

// avvia il server e lo mette in ascolto sulla porta specificata
func (s *Server) Start(address string) error{
	// prima di tutto carica i dati dall'AOF 
	if err := s.loadFromAOF(); err != nil {
		return fmt.Errorf("impossibile caricare da AOF: %w", err)
	}

	// crea listener sulla porta ricevuta come parametro
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer s.Close() // metodo per chiusura listener e file 

	// se ci sono errori durante la funzione chiude il listener 
	// defer listener.Close()

	s.listener = listener

	log.Printf("Server in ascolto su %s", address)

	// loop infinito del server per accettare nuove connessioni 
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Se il listener è stato chiuso, è un'uscita pulita.
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				break
			}
			log.Printf("Errore nell'accettare la connessione: %v", err)
			continue
				}

		// gestisce la connessione accettata con una nuova goroutine
		go s.handleConnection(conn)

	}
	return nil
}

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

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("Nuovo client connesso: %s", conn.RemoteAddr())

	// un reader con buffer per efficientare riducendo il numero di chiamate di sistema per leggere i dati dalla rete 
	reader := bufio.NewReader(conn)

	for {
		// legge fino al nuova riga '\n'
		line, err := reader.ReadString('\n')
		if err != nil{
			// gestione caso End Of File io.EOF che si riceve se il client chiude la connessione
			if err == io.EOF{
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

// dispatchCommand è il nuovo punto centrale
func (s *Server) dispatchCommand(conn net.Conn, line string) {
	cmd, err := protocol.Parse(line)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("protocol error: %v", err))
		return
	}

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
	if len(args) != 2{
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per il comando 'SET'")
		}
		return	
	}

	key := string(args[0])
	value := args[1]

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
	if len(args) != 1 {
		if conn != nil {
			s.writeError(conn, "numero di argomenti errato per 'VCREATE'")
		}
		return
	}
	indexName := string(args[0])
	s.store.CreateVectorIndex(indexName)
	if conn != nil {
		s.writeSimpleString(conn, "OK")
	}
}

// handleVAdd gestisce l'aggiunta di un vettore a un indice.
func (s *Server) handleVAdd(conn net.Conn, args [][]byte) {
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

	idx.Add(vectorID, vector)
	if conn != nil {
		s.writeSimpleString(conn, "OK")
	}
}

// handleVSearch gestisce la ricerca in un indice.
func (s *Server) handleVSearch(conn net.Conn, args [][]byte) {
	if len(args) < 3 {
		s.writeError(conn, "numero di argomenti errato per 'VSEARCH'")
		return
	}
	indexName := string(args[0])
	kStr := string(args[1])
	// vectorStr := string(args[2])
	vectorStr := args[2:]
	
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

	queryVector, err := parseVectorFromParts(vectorStr)
	if err != nil {
		s.writeError(conn, fmt.Sprintf("formato vettore di query invalido: %v", err))
		return
	}

	results := idx.Search(queryVector, k)

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

