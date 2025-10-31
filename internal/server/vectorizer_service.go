// Package server implements the main KektorDB server logic.
//
// This file specifically handles the VectorizerService, which is responsible for
// managing the lifecycle of all background vectorizer workers. It orchestrates
// their creation, startup, and graceful shutdown.

package server

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// VectorizerService manages the lifecycle of all Vectorizer workers.
// It holds references to all active vectorizers and coordinates their
// start and stop operations using a shared WaitGroup.
type VectorizerService struct {
	server      *Server
	vectorizers []*Vectorizer
	wg          sync.WaitGroup
}

// NewVectorizerService creates and initializes the main vectorizer service.
// It iterates through the loaded vectorizer configurations, creates a worker
// for each, and prepares the service to manage them.
func NewVectorizerService(server *Server) (*VectorizerService, error) {
	service := &VectorizerService{
		server: server,
		// The WaitGroup is initialized to zero automatically.
	}

	// Iterate over the loaded configurations and start a worker for each one.
	for _, config := range server.vectorizerConfig.Vectorizers {
		// Pass the service's WaitGroup to each new worker.
		vec, err := NewVectorizer(config, server, &service.wg)
		if err != nil {
			log.Printf("ERROR: Could not start vectorizer '%s': %v", config.Name, err)
			continue // Skip this one and move to the next.
		}
		service.vectorizers = append(service.vectorizers, vec)
	}

	return service, nil
}

// Start begins the lifecycle of all workers managed by the service.
// Each worker is started in its own background goroutine.
func (vs *VectorizerService) Start() {
	if vs == nil || len(vs.vectorizers) == 0 {
		return
	}
	log.Println("Starting VectorizerService and all background workers...")
	for _, v := range vs.vectorizers {
		// Tell the WaitGroup that a goroutine is about to start.
		v.wg.Add(1)
		// Start the worker's goroutine.
		go v.run()
	}
}

// Stop gracefully stops all workers managed by the service.
// It signals each worker to stop and then waits for them to complete
// their current tasks and shut down.
func (vs *VectorizerService) Stop() {
	log.Println("Stopping VectorizerService... Waiting for workers...")
	for _, v := range vs.vectorizers {
		v.Stop() // Send the stop signal.
	}

	// Wait for all goroutines that have called Add() to call Done().
	vs.wg.Wait()
	log.Println("All Vectorizer workers have been stopped.")
}

// VectorizerStatus is a public-facing struct for the API, containing no internal fields.
// It provides a snapshot of a vectorizer's current state.
type VectorizerStatus struct {
	Name         string    `json:"name"`
	IsRunning    bool      `json:"is_running"`
	LastRun      time.Time `json:"last_run,omitempty"`
	CurrentState string    `json:"current_state"`
}

// GetStatuses returns the current status of all managed vectorizers.
// This is used to provide information via the HTTP API.
func (vs *VectorizerService) GetStatuses() []VectorizerStatus {
	statuses := make([]VectorizerStatus, 0, len(vs.vectorizers))
	for _, v := range vs.vectorizers {
		// We use a method on the Vectorizer to get its individual status.
		statuses = append(statuses, v.GetStatus())
	}
	return statuses
}

// Trigger manually starts the synchronization process for a specific vectorizer by name.
// The synchronization is run in a separate goroutine to avoid blocking the caller.
func (vs *VectorizerService) Trigger(name string) error {
	for _, v := range vs.vectorizers {
		if v.config.Name == name {
			log.Printf("Manual trigger received for vectorizer '%s'", name)
			go v.synchronize() // Start synchronization in a goroutine to avoid blocking.
			return nil
		}
	}
	return fmt.Errorf("vectorizer with name '%s' not found", name)
}
