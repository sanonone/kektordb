package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/text"
	"github.com/sanonone/kektordb/pkg/core/types"
)

type Vectorizer struct {
	config       VectorizerConfig
	server       *Server
	ticker       *time.Ticker
	stopCh       chan struct{}
	lastRun      time.Time
	currentState atomic.Value
	wg           *sync.WaitGroup
}

func NewVectorizer(config VectorizerConfig, server *Server, wg *sync.WaitGroup) (*Vectorizer, error) {
	schedule, err := time.ParseDuration(config.Schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule: %w", err)
	}

	vec := &Vectorizer{
		config: config,
		server: server,
		ticker: time.NewTicker(schedule),
		stopCh: make(chan struct{}),
		wg:     wg,
	}
	log.Printf("Vectorizer '%s' initialized.", config.Name)
	return vec, nil
}

func (v *Vectorizer) run() {
	defer v.wg.Done()

	v.currentState.Store("idle")
	v.synchronize() // Initial run

	for {
		select {
		case <-v.ticker.C:
			v.currentState.Store("synchronizing")
			v.synchronize()
			v.lastRun = time.Now()
			v.currentState.Store("idle")
		case <-v.stopCh:
			v.ticker.Stop()
			return
		}
	}
}

func (v *Vectorizer) synchronize() {
	if v.config.Source.Type != "filesystem" {
		return
	}
	changedFiles, err := v.findChangedFiles(v.config.Source.Path)
	if err != nil || len(changedFiles) == 0 {
		return
	}

	log.Printf("Vectorizer '%s': Starting processing of %d files...", v.config.Name, len(changedFiles))

	processed := 0
	for _, filePath := range changedFiles {
		if err := v.processFile(filePath); err != nil {
			log.Printf("Vectorizer '%s': ERROR processing '%s': %v", v.config.Name, filePath, err)
		} else {
			processed++
		}
	}
	log.Printf("Vectorizer '%s': ✓ COMPLETED - Successfully processed %d/%d files", v.config.Name, processed, len(changedFiles))
}

func (v *Vectorizer) Stop() {
	close(v.stopCh)
}

func (v *Vectorizer) findChangedFiles(root string) ([]string, error) {
	var changedFiles []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, path)

		// USARE ENGINE: KVGet
		lastModBytes, found := v.server.Engine.KVGet(stateKey)

		lastModTime := int64(0)
		if found {
			parsedTime, err := strconv.ParseInt(string(lastModBytes), 10, 64)
			if err != nil {
				// If parsing fails, consider file as never processed
				lastModTime = 0
			} else {
				lastModTime = parsedTime
			}
		}

		if info.ModTime().UnixNano() > lastModTime {
			changedFiles = append(changedFiles, path)
		}
		return nil
	})
	return changedFiles, err
}

func (v *Vectorizer) GetStatus() VectorizerStatus {
	return VectorizerStatus{
		Name:         v.config.Name,
		IsRunning:    true,
		LastRun:      v.lastRun,
		CurrentState: v.currentState.Load().(string),
	}
}

func (v *Vectorizer) processFile(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	chunkSize := v.config.DocProcessor.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 500
	}

	chunks := text.FixedSizeChunker(string(content), chunkSize, chunkSize/10)

	// Check if index exists via Engine
	if _, ok := v.server.Engine.DB.GetVectorIndex(v.config.KektorIndex); !ok {
		log.Printf("Auto-creating index '%s'", v.config.KektorIndex)
		// USARE ENGINE: VCreate (Persistenza automatica)
		err := v.server.Engine.VCreate(v.config.KektorIndex, distance.Cosine, 16, 200, distance.Float32, "english", nil)
		if err != nil {
			return err
		}
	}

	fileInfo, _ := os.Stat(filePath)
	modTimeStr := fileInfo.ModTime().Format(time.RFC3339)

	// Accumuliamo batch per efficienza (Opzionale, ma visto che abbiamo VAddBatch...)
	var batch []types.BatchObject

	for _, chunk := range chunks {
		vector, err := v.getEmbedding(chunk.Content)
		if err != nil {
			continue
		}

		vectorID := fmt.Sprintf("%s-chunk-%d", filePath, chunk.ChunkNumber)

		metadata := map[string]interface{}{
			"source_file":   filePath,
			"chunk_number":  chunk.ChunkNumber,
			"content_chunk": chunk.Content,
		}
		// Template replacement...
		for key, valueTpl := range v.config.MetadataTemplate {
			val := valueTpl
			val = strings.ReplaceAll(val, "{{file_path}}", filePath)
			val = strings.ReplaceAll(val, "{{mod_time}}", modTimeStr)
			val = strings.ReplaceAll(val, "{{chunk_num}}", strconv.Itoa(chunk.ChunkNumber))
			metadata[key] = val
		}

		batch = append(batch, types.BatchObject{
			Id:       vectorID,
			Vector:   vector,
			Metadata: metadata,
		})
	}

	// USARE ENGINE: VAddBatch (Atomico e Persistente)
	if len(batch) > 0 {
		if err := v.server.Engine.VAddBatch(v.config.KektorIndex, batch); err != nil {
			return err
		}
	}

	// Update state
	stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, filePath)
	newModTime := fmt.Sprintf("%d", fileInfo.ModTime().UnixNano())

	log.Printf("Vectorizer '%s': ✓ Processed file '%s' (%d chunks)", v.config.Name, filePath, len(chunks))

	// USARE ENGINE: KVSet
	return v.server.Engine.KVSet(stateKey, []byte(newModTime))
}
