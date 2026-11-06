package auth

// EngineStore adapts an object exposing the engine's KV methods (KVGet, KVSet,
// KVDelete) to the Store interface the auth layer expects.
//
// The engine writes every KV mutation to the AOF; the raw core.KVStore does
// not. Wiring the engine through this adapter is what makes the signing key,
// the revocation denylist and the API-key policies survive a restart.
type EngineStore struct {
	kv kvBackend
}

// kvBackend is the subset of *engine.Engine the auth layer needs. Declaring it
// here keeps pkg/auth free of an import cycle with pkg/engine.
type kvBackend interface {
	KVGet(key string) ([]byte, bool)
	KVSet(key string, value []byte) error
	KVDelete(key string) error
}

// NewEngineStore wraps an engine (or anything with the same KV methods).
func NewEngineStore(kv kvBackend) Store {
	return &EngineStore{kv: kv}
}

func (s *EngineStore) Get(key string) ([]byte, bool) { return s.kv.KVGet(key) }

func (s *EngineStore) Set(key string, value []byte) error { return s.kv.KVSet(key, value) }

func (s *EngineStore) Delete(key string) error { return s.kv.KVDelete(key) }
