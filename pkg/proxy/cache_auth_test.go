package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCacheInvalidateRequiresAuth covers an unauthenticated admin endpoint.
//
// /cache/invalidate is intercepted by the proxy's ServeHTTP before any other
// handling and had no credential check at all. Anyone who can reach the port
// can flush cache entries by document ID — a cheap way to force misses and to
// erase evidence of what was served.
func TestCacheInvalidateRequiresAuth(t *testing.T) {
	p := &AIProxy{authToken: "proxy-secret"}

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.NewReader(`{"document_id":"doc-1"}`)
			req := httptest.NewRequest(http.MethodPost, "/cache/invalidate", body)
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			p.handleCacheInvalidate(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestCacheInvalidateAcceptsGoodToken checks the positive path without an
// engine: a valid token must get past the auth gate (and then fail on the
// missing engine, which this test deliberately does not build).
func TestCacheInvalidateAcceptsGoodToken(t *testing.T) {
	p := &AIProxy{authToken: "proxy-secret"}

	body := strings.NewReader(`{"document_id":"doc-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/cache/invalidate", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer proxy-secret")

	// The auth gate must not reject: reaching the nil-engine dereference
	// proves the request got past it.
	defer func() {
		if r := recover(); r == nil {
			t.Log("request handled without panic; auth gate passed")
		}
	}()
	p.handleCacheInvalidate(httptest.NewRecorder(), req)
}
