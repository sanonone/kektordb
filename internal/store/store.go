package store 

import "sync"

// store è il contenitore principale che contiene tutti i tipi di dato di kektorDB
type Store struct {
	mu sync.RWMutex
	kvStore *KVStore 
	vectorIndexes map[string]VectorIndex 
}

func NewStore() *Store {
	return &Store{
		kvStore: NewKVStore(),
		vectorIndexes: make(map[string]VectorIndex),
	}
}

// restituisce lo store KVStore 
func (s *Store) GetKVStore() *KVStore {
	return s.kvStore 
}

// crea un nuovo indice vettoriale 
func (s *Store) CreateVectorIndex(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// parametri che dovrebbero essere configurabili ma al 
	// momento uso come valori di default 
	const (
		defaultM = 16
		defaultEfConst = 200
	)

	s.vectorIndexes[name] = NewHNSWIndex(defaultM, defaultEfConst)

	// al momento crea sempre un brute force index 
	// in futuro si potrà passare un argomento per scegliere il tipo di indice 
	// s.vectorIndexes[name] = NewBruteForceIndex()
}

// recupera un indice vettoriale per nome 
func (s *Store) GetVectorIndex(name string) (VectorIndex, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, found := s.vectorIndexes[name]
	return idx, found
}
