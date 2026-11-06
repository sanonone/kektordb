package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/auth"
	"github.com/sanonone/kektordb/pkg/engine"
)

// kvTestEnv builds a server with authentication enabled and returns its handler.
func kvTestEnv(t *testing.T) (http.Handler, *engine.Engine, *Server) {
	t.Helper()
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	srv, err := NewServer(eng, ":0", "", "root", tmpDir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv.httpServer.Handler, eng, srv
}

// issueReadToken mints a token with the given role and namespaces.
func issueToken(t *testing.T, srv *Server, role string, namespaces []string) string {
	t.Helper()
	km, ok := srv.authService.(auth.KeyManager)
	if !ok {
		t.Fatalf("auth backend %T cannot mint tokens", srv.authService)
	}
	token, _, err := km.GenerateKey("test-"+role, role, namespaces)
	if err != nil {
		t.Fatalf("generate %s token: %v", role, err)
	}
	return token
}

// TestKVDoesNotExposeSigningKey is the regression test for an escalation that
// let any token with read access mint admin tokens.
//
// The ECDSA signing key lives in the KV store under _sys_auth::ecdsa_private_key.
// The KV HTTP handlers had no prefix filter, so GET /kv/_sys_auth::ecdsa_private_key
// returned it. Token verification only uses the public half, so whoever holds
// the private half can sign a token with role=admin and the server accepts it.
func TestKVDoesNotExposeSigningKey(t *testing.T) {
	handler, eng, srv := kvTestEnv(t)

	if err := eng.KVSet("_sys_auth::ecdsa_private_key", []byte("PRIVATE")); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if err := eng.KVSet("_sys_auth::revoked::abc", []byte("1")); err != nil {
		t.Fatalf("seed revoked: %v", err)
	}

	readToken := issueToken(t, srv, auth.RoleRead, []string{"*"})

	for _, key := range []string{
		"_sys_auth::ecdsa_private_key",
		"_sys_auth::revoked::abc",
		"_sys_auth::anything",
	} {
		t.Run(key, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/kv/"+key, nil)
			req.Header.Set("Authorization", "Bearer "+readToken)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK {
				t.Fatalf("GET /kv/%s returned 200 for a read token; body: %s", key, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "PRIVATE") {
				t.Fatalf("GET /kv/%s leaked the secret value: %s", key, rec.Body.String())
			}
		})
	}
}

// TestKVReservedPrefixCannotBeWritten keeps the reserved namespace immutable
// through the HTTP surface: a write there could rewrite the signing key.
func TestKVReservedPrefixCannotBeWritten(t *testing.T) {
	handler, eng, srv := kvTestEnv(t)

	if err := eng.KVSet("_sys_auth::ecdsa_private_key", []byte("ORIGINAL")); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	writeToken := issueToken(t, srv, auth.RoleWrite, []string{"*"})
	body, _ := json.Marshal(map[string]string{"value": "ATTACKER-CONTROLLED"})

	req := httptest.NewRequest(http.MethodPost, "/kv/_sys_auth::ecdsa_private_key", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+writeToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("POST /kv/_sys_auth::ecdsa_private_key succeeded; the signing key is rewritable")
	}

	got, _ := eng.KVGet("_sys_auth::ecdsa_private_key")
	if string(got) != "ORIGINAL" {
		t.Errorf("signing key was overwritten: %q", got)
	}
}

// TestKVReservedPrefixCannotBeDeleted mirrors the write case.
func TestKVReservedPrefixCannotBeDeleted(t *testing.T) {
	handler, eng, srv := kvTestEnv(t)

	if err := eng.KVSet("_sys_auth::ecdsa_private_key", []byte("ORIGINAL")); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	writeToken := issueToken(t, srv, auth.RoleWrite, []string{"*"})
	req := httptest.NewRequest(http.MethodDelete, "/kv/_sys_auth::ecdsa_private_key", nil)
	req.Header.Set("Authorization", "Bearer "+writeToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("DELETE /kv/_sys_auth::ecdsa_private_key succeeded; the signing key is deletable")
	}
	if _, found := eng.KVGet("_sys_auth::ecdsa_private_key"); !found {
		t.Error("signing key was deleted through the KV HTTP surface")
	}
}

// TestKVUserKeysStillWork guards against an over-broad fix.
func TestKVUserKeysStillWork(t *testing.T) {
	handler, _, srv := kvTestEnv(t)
	writeToken := issueToken(t, srv, auth.RoleWrite, []string{"*"})

	body, _ := json.Marshal(map[string]string{"value": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/kv/my_pref", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+writeToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /kv/my_pref = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	readToken := issueToken(t, srv, auth.RoleRead, []string{"*"})
	req = httptest.NewRequest(http.MethodGet, "/kv/my_pref", nil)
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /kv/my_pref = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("GET /kv/my_pref body = %s, want it to contain hello", rec.Body.String())
	}
}
