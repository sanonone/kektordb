// Package core provides the fundamental data structures and logic for the KektorDB engine.
//
// This file implements a thread-safe, in-memory key-value store. It uses a
// read-write mutex to allow concurrent reads while ensuring exclusive access
// for write operations (Set, Delete).

package core

import (
	"sync"
)

// KVStore is a thread-safe, in-memory key-value store.
// It uses a sync.RWMutex to manage concurrent access, allowing for multiple
// concurrent readers or a single exclusive writer.
type KVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewKVStore creates and returns a new, empty KVStore instance.
func NewKVStore() *KVStore {
	return &KVStore{
		data: make(map[string][]byte),
	}
}

// Set adds or updates a value for a given key.
// This is a write operation and is fully thread-safe.
func (s *KVStore) Set(key string, value []byte) {
	s.mu.Lock() // Lock for writing
	defer s.mu.Unlock()

	s.data[key] = value
}

// Get retrieves the value for a given key.
// It returns the value and a boolean indicating whether the key was found.
// This is a read operation and is safe for concurrent use.
func (s *KVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, found := s.data[key]
	return value, found
}

// Delete removes a key and its associated value from the store.
// This is a write operation and is fully thread-safe
func (s *KVStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
}

// RLock locks the store for reading.
// It should be used in conjunction with RUnlock for operations that require a
// consistent read-only view across multiple steps at a higher level.
func (s *KVStore) RLock() {
	s.mu.RLock()
}

// RUnlock unlocks the store after a read lock.
// It must be called once for every call to RLock.
func (s *KVStore) RUnlock() {
	s.mu.RUnlock()
}
