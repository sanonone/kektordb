package core

import (
	"sync"
)

// lo store key-value in memory
type KVStore struct {
	// con rwmutex permetto letture concorrenti ma scritture esclusive
	mu   sync.RWMutex
	data map[string][]byte
}

// crea e restituisce un vuovo store
func NewKVStore() *KVStore {
	return &KVStore{
		data: make(map[string][]byte),
	}
}

// SET: imposta un valore per una determinata chiave
// scrittura quindi si usa lock()
func (s *KVStore) Set(key string, value []byte) {
	s.mu.Lock() // blocca la scrittura
	defer s.mu.Unlock()

	s.data[key] = value
}

// GET: recupera il valore data una chiave
// lettura quindi usaimo rlock() condiviso
func (s *KVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, found := s.data[key]
	return value, found
}

// DELETE: rimuove una chiave dallo store
// scrittura quindi lock()
func (s *KVStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
}

func (s *KVStore) RLock() {
	s.mu.RLock()
}

func (s *KVStore) RUnlock() {
	s.mu.RUnlock()
}
