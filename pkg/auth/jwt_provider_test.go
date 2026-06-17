package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
)

// newTestProvider creates a JWTProvider backed by a fresh in-memory KV store.
func newTestProvider(t *testing.T) *JWTProvider {
	t.Helper()
	kv := core.NewKVStore()
	p, err := NewJWTProvider(kv)
	if err != nil {
		t.Fatalf("NewJWTProvider: %v", err)
	}
	return p
}

// --- GenerateKey ---

func TestGenerateKey_ValidRoles(t *testing.T) {
	p := newTestProvider(t)
	for _, role := range []string{RoleAdmin, RoleWrite, RoleRead} {
		token, policy, err := p.GenerateKey("test key", role, []string{"ns1"})
		if err != nil {
			t.Fatalf("role %s: unexpected error: %v", role, err)
		}
		if token == "" {
			t.Fatalf("role %s: expected non-empty token", role)
		}
		// JWT compact form: three base64url segments separated by dots
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatalf("role %s: token does not look like a JWT (got %d parts)", role, len(parts))
		}
		if policy.Role != role {
			t.Errorf("role %s: policy.Role = %q, want %q", role, policy.Role, role)
		}
		if policy.ID == "" {
			t.Errorf("role %s: policy.ID (jti) is empty", role)
		}
	}
}

func TestGenerateKey_InvalidRole(t *testing.T) {
	p := newTestProvider(t)
	_, _, err := p.GenerateKey("bad", "superuser", []string{"*"})
	if err == nil {
		t.Fatal("expected error for invalid role, got nil")
	}
}

func TestGenerateKey_DefaultNamespace(t *testing.T) {
	p := newTestProvider(t)
	_, policy, err := p.GenerateKey("key", RoleRead, []string{"my-index"})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Namespaces) != 1 || policy.Namespaces[0] != "my-index" {
		t.Errorf("unexpected namespaces: %v", policy.Namespaces)
	}
}

// --- VerifyToken ---

func TestVerifyToken_Valid(t *testing.T) {
	p := newTestProvider(t)
	token, policy, err := p.GenerateKey("valid key", RoleWrite, []string{"idx"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Role != RoleWrite {
		t.Errorf("Role = %q, want %q", got.Role, RoleWrite)
	}
	if got.ID != policy.ID {
		t.Errorf("ID = %q, want %q", got.ID, policy.ID)
	}
	if len(got.Namespaces) != 1 || got.Namespaces[0] != "idx" {
		t.Errorf("Namespaces = %v", got.Namespaces)
	}
}

func TestVerifyToken_Tampered(t *testing.T) {
	p := newTestProvider(t)
	token, _, err := p.GenerateKey("key", RoleAdmin, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}

	// Corrupt the signature segment: replace every character with 'A'.
	// This guarantees the ECDSA signature no longer matches the header+payload,
	// regardless of base64url padding edge cases.
	parts[2] = strings.Repeat("A", len(parts[2]))
	tampered := strings.Join(parts, ".")

	if _, err := p.VerifyToken(tampered); err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestVerifyToken_Invalid(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.VerifyToken("not.a.jwt")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
}

func TestVerifyToken_WrongKey(t *testing.T) {
	// Two independent providers each have their own keypair.
	p1 := newTestProvider(t)
	p2 := newTestProvider(t)

	token, _, err := p1.GenerateKey("key", RoleRead, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	// p2 should reject a token signed by p1.
	if _, err := p2.VerifyToken(token); err == nil {
		t.Fatal("expected error: token signed by a different key should be rejected")
	}
}

// --- RevokeKey ---

func TestRevokeKey_RevokedTokenRejected(t *testing.T) {
	p := newTestProvider(t)
	token, policy, err := p.GenerateKey("revoke me", RoleWrite, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	// Token is valid before revocation.
	if _, err := p.VerifyToken(token); err != nil {
		t.Fatalf("expected valid token before revoke, got: %v", err)
	}

	// Revoke by jti.
	if err := p.RevokeKey(policy.ID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	// Token must be rejected after revocation.
	if _, err := p.VerifyToken(token); err == nil {
		t.Fatal("expected error for revoked token, got nil")
	}
}

func TestRevokeKey_EmptyJTI(t *testing.T) {
	p := newTestProvider(t)
	if err := p.RevokeKey(""); err == nil {
		t.Fatal("expected error for empty jti, got nil")
	}
}

func TestRevokeKey_OtherTokensUnaffected(t *testing.T) {
	p := newTestProvider(t)
	token1, policy1, _ := p.GenerateKey("key1", RoleRead, []string{"*"})
	token2, _, _ := p.GenerateKey("key2", RoleRead, []string{"*"})

	_ = p.RevokeKey(policy1.ID)

	if _, err := p.VerifyToken(token2); err != nil {
		t.Fatalf("token2 should still be valid after revoking token1: %v", err)
	}
	_ = token1 // only token1 is revoked
}

// --- PublicKeyJWKS ---

func TestPublicKeyJWKS_ValidJWK(t *testing.T) {
	p := newTestProvider(t)
	raw, err := p.PublicKeyJWKS()
	if err != nil {
		t.Fatalf("PublicKeyJWKS: %v", err)
	}

	var jwks struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &jwks); err != nil {
		t.Fatalf("invalid JWKS JSON: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(jwks.Keys))
	}
	key := jwks.Keys[0]
	for _, field := range []string{"kty", "crv", "use", "alg", "x", "y"} {
		if key[field] == "" {
			t.Errorf("JWKS key missing field %q", field)
		}
	}
	if key["kty"] != "EC" || key["crv"] != "P-256" || key["alg"] != "ES256" {
		t.Errorf("unexpected JWKS key parameters: %v", key)
	}
}

// --- KeyPair persistence ---

func TestSignerPersistence_SameKeyAcrossProviders(t *testing.T) {
	// Two JWTProviders sharing the same KV store should use the same keypair.
	kv := core.NewKVStore()

	p1, err := NewJWTProvider(kv)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := p1.GenerateKey("key", RoleRead, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	// p2 loads the persisted key from the same KV store.
	p2, err := NewJWTProvider(kv)
	if err != nil {
		t.Fatal(err)
	}

	// p2 must successfully verify a token issued by p1.
	if _, err := p2.VerifyToken(token); err != nil {
		t.Fatalf("p2 should verify token issued by p1 (same KV): %v", err)
	}
}

// --- HasAccess (unchanged; tested here for completeness) ---

func TestHasAccess_Admin(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleAdmin, Namespaces: []string{}}
	if !p.HasAccess(RoleWrite, "any-namespace") {
		t.Error("admin should have access to everything")
	}
}

func TestHasAccess_WriteCanRead(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleWrite, Namespaces: []string{"ns1"}}
	if !p.HasAccess(RoleRead, "ns1") {
		t.Error("write role should satisfy read requirement")
	}
}

func TestHasAccess_ReadCannotWrite(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleRead, Namespaces: []string{"ns1"}}
	if p.HasAccess(RoleWrite, "ns1") {
		t.Error("read role should not satisfy write requirement")
	}
}

func TestHasAccess_WrongNamespace(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleWrite, Namespaces: []string{"ns1"}}
	if p.HasAccess(RoleRead, "ns2") {
		t.Error("should not have access to a namespace not in the policy")
	}
}

func TestHasAccess_Wildcard(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleWrite, Namespaces: []string{"*"}}
	if !p.HasAccess(RoleWrite, "any-namespace") {
		t.Error("wildcard namespace should grant access to all namespaces")
	}
}

// --- Token TTL sanity ---

func TestGenerateKey_ExpClaimIsInFuture(t *testing.T) {
	p := newTestProvider(t)
	token, _, err := p.GenerateKey("key", RoleRead, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	// VerifyToken succeeds only if exp > now (library enforces this automatically).
	got, err := p.VerifyToken(token)
	if err != nil {
		t.Fatal(err)
	}
	// CreatedAt should be within the last few seconds.
	if time.Now().Unix()-got.CreatedAt > 5 {
		t.Errorf("CreatedAt is too far in the past: %d", got.CreatedAt)
	}
}
