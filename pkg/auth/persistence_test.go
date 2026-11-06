package auth_test

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/auth"
	"github.com/sanonone/kektordb/pkg/core"
)

// countingStore records writes and can simulate a persistence failure.
type countingStore struct {
	kv      *core.KVStore
	sets    int
	deletes int
	failOn  string
}

func newCountingStore() *countingStore {
	return &countingStore{kv: core.NewKVStore()}
}

func (s *countingStore) Get(key string) ([]byte, bool) { return s.kv.Get(key) }

func (s *countingStore) Set(key string, value []byte) error {
	if s.failOn == "set" {
		return errPersist
	}
	s.sets++
	s.kv.Set(key, value)
	return nil
}

func (s *countingStore) Delete(key string) error {
	if s.failOn == "delete" {
		return errPersist
	}
	s.deletes++
	s.kv.Delete(key)
	return nil
}

type persistError struct{}

func (persistError) Error() string { return "simulated persistence failure" }

var errPersist = persistError{}

// TestSigningKeyGoesThroughStore guards the bug behind "all tokens invalid
// after restart": loadOrCreateSigner wrote to the raw KVStore, which the engine
// never flushes to the AOF.
func TestSigningKeyGoesThroughStore(t *testing.T) {
	store := newCountingStore()

	if _, err := auth.NewJWTProvider(store); err != nil {
		t.Fatalf("NewJWTProvider: %v", err)
	}

	if store.sets == 0 {
		t.Fatal("generating a signing key wrote nothing through Store: " +
			"the key lives in memory only and dies on restart")
	}
	if _, found := store.kv.Get("_sys_auth::ecdsa_private_key"); !found {
		t.Error("signing key not stored under the expected key")
	}
}

// TestSigningKeySurvivesRestart is the behavioural check: a second provider over
// the same store must reuse the key, so previously issued tokens stay valid.
func TestSigningKeySurvivesRestart(t *testing.T) {
	store := newCountingStore()

	first, err := auth.NewJWTProvider(store)
	if err != nil {
		t.Fatalf("first provider: %v", err)
	}
	token, _, err := first.GenerateKey("test", auth.RoleRead, []string{"*"})
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// Simulate a restart: a fresh provider over the same persisted store.
	second, err := auth.NewJWTProvider(store)
	if err != nil {
		t.Fatalf("second provider: %v", err)
	}
	if _, err := second.VerifyToken(token); err != nil {
		t.Fatalf("token issued before the restart is now invalid: %v", err)
	}
}

// TestRevocationGoesThroughStore covers "a revoked token comes back to life".
func TestRevocationGoesThroughStore(t *testing.T) {
	store := newCountingStore()
	provider, err := auth.NewJWTProvider(store)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	before := store.sets
	if err := provider.RevokeKey("jti-123"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if store.sets == before {
		t.Fatal("RevokeKey wrote nothing through Store: the revocation is memory-only")
	}

	// After a restart the denylist must still be there.
	restarted, err := auth.NewJWTProvider(store)
	if err != nil {
		t.Fatalf("restarted provider: %v", err)
	}
	if _, revoked := store.kv.Get("_sys_auth::revoked::jti-123"); !revoked {
		t.Error("revocation entry missing after restart")
	}
	_ = restarted
}

// TestRevocationErrorPropagates: a failed persist must not be silent, or the
// caller believes a token is dead while it is still valid.
func TestRevocationErrorPropagates(t *testing.T) {
	store := newCountingStore()
	provider, err := auth.NewJWTProvider(store)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	store.failOn = "set"
	if err := provider.RevokeKey("jti-abc"); err == nil {
		t.Fatal("RevokeKey returned nil although the write failed")
	}
}

// TestAPIKeyGoesThroughStore covers API-key minting and revocation.
func TestAPIKeyGoesThroughStore(t *testing.T) {
	store := newCountingStore()
	svc := auth.NewAuthService(store)

	before := store.sets
	token, _, err := svc.GenerateKey("test", auth.RoleRead, []string{"*"})
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if store.sets == before {
		t.Fatal("GenerateKey wrote nothing through Store: the key dies on restart")
	}

	if _, err := svc.VerifyToken(token); err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}

	before = store.deletes
	// RevokeKey takes the hash, which AuthService does not expose, so call it
	// through the public surface with an empty value and check the write path.
	_ = svc.RevokeKey("deadbeef")
	if store.deletes == before {
		t.Fatal("RevokeKey wrote nothing through Store: the revocation is memory-only")
	}
}
