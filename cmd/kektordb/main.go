package main

import (
	"flag"
	//"fmt"
	"errors"
	"github.com/sanonone/kektordb/internal/server"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// flag.String e flag.Int per definire parametri
	// la funzione restituisce un puntatore alle variabili che conterrà il valore
	// flag.String(nome, val_default, descrizione per help)
	// tcpAddr := flag.String("tcp-addr", ":9090", "Indirizzo e porta per il server TCP (es. :9090 o 127.0.0.1:9090)")
	httpAddr := flag.String("http-addr", ":9091", "Indirizzo e porta per il server API REST (es. :9091)")
	aofPath := flag.String("aof-path", "kektordb.aof", "Percorso del file di persistenza AOF")
	savePolicy := flag.String("save", "60 1000", "Policy di snapshot automatico: \"secondi scritture\". Disabilita con \"\".")
	aofRewritePercentage := flag.Int("aof-rewrite-percentage", 100, "Riscrive l'AOF quando cresce di questa percentuale. 0 per disabilitare.")
	vectorizersConfigPath := flag.String("vectorizers-config", "", "Percorso del file YAML di configurazione dei Vectorizer")

	flag.Parse() // popola le variabili sopra con i valori forniti dall'utente sulla riga di comando

	// crea istanza server e passo la configurazione come parametro
	srv, err := server.NewServer(*aofPath, *savePolicy, *aofRewritePercentage, *vectorizersConfigPath)
	if err != nil {
		log.Fatalf("Impossibile creare il server: %v", err)
	}

	// canale in ascolto del segnale di interruzione (Ctrl+c)
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// avvia il server TCP e HTTP in una goroutine per non bloccare il main
	go func() {
		log.Printf("Avvio server KektorDB: API REST su %s, AOF in %s", *httpAddr, *aofPath)
		// log.Fatal(srv.Run(*httpAddr)) // avvia il server con i parametri dell'utente
		if err := srv.Run(*httpAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Errore critico del server: %v", err)
		}
	}()

	// blocca il main in attesa del segnale di shutdown
	<-shutdownChan

	// esegue lo spegnimento pulito
	srv.Shutdown()

}
