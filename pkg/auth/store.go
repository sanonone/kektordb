package auth

import "github.com/sanonone/kektordb/pkg/core"

// Store is the persistence surface the auth layer needs.
//
// It exists because the raw KVStore is memory-only: writing to it skips the
// AOF, so the signing key, the revocation denylist and the API-key policies
// vanished on restart. Wiring the engine's KV methods through this interface
// makes every auth write durable without teaching this package about AOF
// framing.
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte) error
	Delete(key string) error
}

// staticStore adapts a plain KVStore, which has no persistence. Used by tests
// and by callers that hold no engine (OIDC-only deployments, for instance).
type staticStore struct {
	kv *core.KVStore
}

// NewMemoryStore wraps a KVStore in a Store whose writes are not persisted.
//
// The name says what it does on purpose: a deployment using this store loses
// every auth write on restart. Callers outside tests should pass the engine.
func NewMemoryStore(kv *core.KVStore) Store {
	return &staticStore{kv: kv}
}

func (s *staticStore) Get(key string) ([]byte, bool) { return s.kv.Get(key) }

func (s *staticStore) Set(key string, value []byte) error {
	s.kv.Set(key, value)
	return nil
}

func (s *staticStore) Delete(key string) error {
	s.kv.Delete(key)
	return nil
}
