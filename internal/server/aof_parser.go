package server

import (
	"fmt"
	"strings"
)

// rappresenta il comando parsato che è stato inviato dal client
type Command struct {
	Name string   // "SET" "GET" ecc
	Args [][]byte // slice di slice di byte, binary safe cioè posso passare immagini, json cose con \0 o qualsiasi cosa si voglia salvare sul db ecc
}

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
