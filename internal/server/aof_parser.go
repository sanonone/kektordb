package server

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// rappresenta il comando parsato che è stato inviato dal client
type Command struct {
	Name string   // "SET" "GET" ecc
	Args [][]byte // slice di slice di byte, binary safe cioè posso passare immagini, json cose con \0 o qualsiasi cosa si voglia salvare sul db ecc
}

// ParseRESP legge un comando in formato RESP-like da un bufio.Reader.
// NOTA: Questa funzione ora ha bisogno di un `bufio.Reader` perché legge più righe.
func ParseRESP(reader *bufio.Reader) (*Command, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	line = strings.TrimSpace(line)
	if line[0] != '*' {
		return nil, fmt.Errorf("formato comando non valido, atteso '*'")
	}

	numArgs, err := strconv.Atoi(line[1:])
	if err != nil || numArgs <= 0 {
		return nil, fmt.Errorf("numero di argomenti non valido")
	}

	args := make([][]byte, numArgs)
	for i := 0; i < numArgs; i++ {
		// Leggi la lunghezza della bulk string
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line[0] != '$' {
			return nil, fmt.Errorf("formato argomento non valido, atteso '$'")
		}

		lenArg, err := strconv.Atoi(line[1:])
		if err != nil || lenArg < 0 {
			return nil, fmt.Errorf("lunghezza argomento non valida")
		}

		// Leggi i dati dell'argomento
		argData := make([]byte, lenArg)
		_, err = io.ReadFull(reader, argData)
		if err != nil {
			return nil, err
		}

		// Leggi i due byte finali \r\n
		crlf := make([]byte, 2)
		_, err = io.ReadFull(reader, crlf)
		if err != nil {
			return nil, err
		}

		args[i] = argData
	}

	return &Command{
		Name: strings.ToUpper(string(args[0])),
		Args: args[1:],
	}, nil
}

func formatCommandAsRESP(commandName string, args ...[]byte) string {
	var b strings.Builder

	// scrive l'header dell'array: num elementi
	totalArgs := 1 + len(args)
	b.WriteString(fmt.Sprintf("*%d\r\n", totalArgs))

	// scrive il nome del comando
	b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(commandName), commandName))

	// scrive ogni elemento
	for _, arg := range args {
		if arg == nil {
			b.WriteString("$-1\r\n") // Rappresentazione RESP per nil
		} else {
			b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), string(arg)))
		}
	}

	return b.String()
}

/*
// parsa la stringa grezza ricevuta nel Command struct
func Parse(raw string) (*Command, error) {
	cleanRaw := strings.TrimSpace(raw)
	if len(cleanRaw) == 0 {
		return nil, fmt.Errorf("comando vuoto")
	}

	parts := strings.Fields(cleanRaw)
	cmdName := strings.ToUpper(parts[0])

	cmd := &Command{
		Name: cmdName,
		Args: make([][]byte, 0, len(parts)-1), // Inizializza sempre
	}

	// NESSUNA LOGICA SPECIALE.
	// Tratta tutti i comandi allo stesso modo: ogni parola è un argomento.
	// Sarà responsabilità degli handler interpretare gli argomenti.
	for _, part := range parts[1:] {
		cmd.Args = append(cmd.Args, []byte(part))
	}

	return cmd, nil
}
*/

/*//vecchia versione che non gestisce metadata
func Parse(raw string) (*Command, error) {
	// strings.Fields divide per spazi e ritorna una slice di stringhe già separate
	// TrimSpace rimuove tab \n \r ecc che potrebbero essere presenti nella stringa ricevuta
	parts := strings.Fields(strings.TrimSpace(raw))

	if len(parts) == 0{
		return nil, fmt.Errorf("comando vuoto")
	}

	cmd := &Command{ // crea Command e restituisce un puntatore a quel Command, quindi cmd è il puntatore alla struct
		// trasforma i comandi in maiuscolo
		Name: strings.ToUpper(parts[0]),
		Args: make([][]byte, 0, len(parts)-1),
	}

	for _, arg := range parts[1:] { // aggiungo gli argomenti
		// []byte(arg) converte la stringa in una slice di byte
		cmd.Args = append(cmd.Args, []byte(arg)) // aggiunge l'argomento convertito in byte alla slice cmd.Args
	}

	return cmd, nil // ritorno il puntatore alla struct command

}
*/
