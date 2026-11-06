package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/auth"
)

// TestPprofRequiresAdmin covers an escalation: pprof endpoints were reachable
// with a read token, and /debug/pprof/cmdline returns the process command line
// — which includes -auth-token when the operator passes it on the command line.
//
// A read token therefore handed over the root token. Profiling data itself
// leaks memory contents too (heap profiles contain live strings).
func TestPprofRequiresAdmin(t *testing.T) {
	handler, _, srv := kvTestEnv(t)

	readToken := issueToken(t, srv, auth.RoleRead, []string{"*"})
	writeToken := issueToken(t, srv, auth.RoleWrite, []string{"*"})

	endpoints := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
	}

	for _, ep := range endpoints {
		for name, token := range map[string]string{"read": readToken, "write": writeToken} {
			t.Run(strings.TrimPrefix(ep, "/debug/pprof/")+"/"+name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, ep, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code == http.StatusOK {
					t.Fatalf("%s returned 200 for a %s token; pprof requires admin", ep, name)
				}
			})
		}
	}
}

// TestPprofCmdlineNotLeakedToReadToken pins the specific leak: the command line.
func TestPprofCmdlineNotLeakedToReadToken(t *testing.T) {
	handler, _, srv := kvTestEnv(t)
	readToken := issueToken(t, srv, auth.RoleRead, []string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/cmdline", nil)
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "auth-token") {
		t.Errorf("/debug/pprof/cmdline leaked the process command line to a read token: %q",
			rec.Body.String())
	}
}

// TestPprofAdminStillAllowed guards the fix: an admin token keeps access.
func TestPprofAdminStillAllowed(t *testing.T) {
	handler, _, srv := kvTestEnv(t)
	adminToken := issueToken(t, srv, auth.RoleAdmin, []string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/debug/pprof/ = %d for an admin token, want 200", rec.Code)
	}
}
