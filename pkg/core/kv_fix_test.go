package core

import (
	"testing"
)

// TestKVStoreDefensiveCopies verifies that Set copies the caller's slice and
// Get returns a copy — mutating either never corrupts the stored state.
func TestKVStoreDefensiveCopies(t *testing.T) {
	kv := NewKVStore()

	// Set must copy: later caller mutations must not affect the store.
	value := []byte("secret-token")
	kv.Set("auth", value)
	value[0] = 'X'
	value = append(value, '!') // re-slice/grow in place

	got, ok := kv.Get("auth")
	if !ok {
		t.Fatal("key not found")
	}
	if string(got) != "secret-token" {
		t.Errorf("Set did not copy the caller's slice: stored %q", got)
	}

	// Get must copy: mutating the returned slice must not affect the store.
	got[0] = 'Y'
	again, _ := kv.Get("auth")
	if string(again) != "secret-token" {
		t.Errorf("Get did not return a copy: %q", again)
	}

	// Missing key returns nil, false.
	if v, ok := kv.Get("missing"); ok || v != nil {
		t.Errorf("Get(missing) = %q, %v; want nil, false", v, ok)
	}
}
