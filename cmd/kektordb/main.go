package main

import (
	"flag"
	//"fmt"
	"github.com/sanonone/kektordb/internal/server"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// flag.String e flag.Int per definire parametri
	// la funzione restituisce un puntatore alle variabili che conterrà il valore
	// flag.String(nome, val_default, descrizione per help)
	tcpAddr := flag.String("tcp-addr", ":9090", "Indirizzo e porta per il server TCP (es. :9090 o 127.0.0.1:9090)")
	httpAddr := flag.String("http-addr", ":9091", "Indirizzo e porta per il server API REST (es. :9091)")
	aofPath := flag.String("aof-path", "kektordb.aof", "Percorso del file di persistenza AOF")

	flag.Parse() // popola le variabili sopra con i valori forniti dall'utente sulla riga di comando

	// crea istanza server e passo la configurazione come parametro
	srv, err := server.NewServer(*aofPath)
	if err != nil {
		log.Fatalf("Impossibile creare il server: %v", err)
	}

	// canale in ascolto del segnale di interruzione (Ctrl+c)
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// avvia il server TCP e HTTP in una goroutine per non bloccare il main
	go func() {
		log.Fatal(srv.Run(*tcpAddr, *httpAddr)) // avvia il server con i parametri dell'utente
	}()

	// blocca il main in attesa del segnale di shutdown
	<-shutdownChan

	// esegue lo spegnimento pulito
	srv.Shutdown()

}
