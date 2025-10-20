package server

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// VectorizerService gestisce il ciclo di vita di tutti i worker Vectorizer.
type VectorizerService struct {
	server      *Server
	vectorizers []*Vectorizer
	wg          sync.WaitGroup
}

// NewVectorizerService crea e avvia il servizio principale dei vectorizer.
func NewVectorizerService(server *Server) (*VectorizerService, error) {
	service := &VectorizerService{
		server: server,
		// Il WaitGroup viene inizializzato automaticamente a zero
	}

	// Itera sulle configurazioni caricate e avvia un worker per ciascuna
	for _, config := range server.vectorizerConfig.Vectorizers {
		// passo WaitGroup del servizio ad ogni nuovo worker
		vec, err := NewVectorizer(config, server, &service.wg)
		if err != nil {
			log.Printf("ERRORE: Impossibile avviare il vectorizer '%s': %v", config.Name, err)
			continue // Salta questo e passa al prossimo
		}
		service.vectorizers = append(service.vectorizers, vec)
	}

	return service, nil
}

// Start avvia il ciclo di vita di tutti i worker gestiti dal servizio.
func (vs *VectorizerService) Start() {
	if vs == nil || len(vs.vectorizers) == 0 {
		return
	}
	log.Println("Avvio del VectorizerService e di tutti i worker in background...")
	for _, v := range vs.vectorizers {
		// Diciamo al WaitGroup che una goroutine sta per partire
		v.wg.Add(1)
		// Avvia la goroutine del worker
		go v.run()
	}
}

// Stop ferma tutti i worker gestiti dal servizio.
func (vs *VectorizerService) Stop() {
	log.Println("Stop del VectorizerService in corso... In attesa dei worker...")
	for _, v := range vs.vectorizers {
		v.Stop() // invia segnale di stop
	}

	// attende che tutte le goroutine che hanno chiamato Add() abbiano chiamato Done()
	vs.wg.Wait()
	log.Println("Tutti i worker del Vectorizer sono stati fermati.")
}

// VectorizerStatus è una struct "pubblica" per l'API, senza campi interni.
type VectorizerStatus struct {
	Name         string    `json:"name"`
	IsRunning    bool      `json:"is_running"`
	LastRun      time.Time `json:"last_run,omitempty"`
	CurrentState string    `json:"current_state"`
}

// GetStatuses restituisce lo stato di tutti i vectorizer gestiti.
func (vs *VectorizerService) GetStatuses() []VectorizerStatus {
	statuses := make([]VectorizerStatus, 0, len(vs.vectorizers))
	for _, v := range vs.vectorizers {
		// Dobbiamo aggiungere un metodo per ottenere lo stato di un singolo Vectorizer
		statuses = append(statuses, v.GetStatus())
	}
	return statuses
}

// Trigger avvia manualmente la sincronizzazione per un vectorizer specifico.
func (vs *VectorizerService) Trigger(name string) error {
	for _, v := range vs.vectorizers {
		if v.config.Name == name {
			log.Printf("Trigger manuale ricevuto per il vectorizer '%s'", name)
			go v.synchronize() // Avvia la sincronizzazione in una goroutine per non bloccare
			return nil
		}
	}
	return fmt.Errorf("vectorizer con nome '%s' non trovato", name)
}
