package auth

import "testing"

// TestHasAccessRoleHierarchy pins the role check.
//
// HasAccess only tested for RoleWrite, so an admin-only route (requiredRole
// "admin") fell through to the namespace check and a read token was accepted.
// pprof, /system/* and /auth/* are all admin-only.
func TestHasAccessRoleHierarchy(t *testing.T) {
	tests := []struct {
		name         string
		role         string
		requiredRole string
		want         bool
	}{
		{"admin needs admin", RoleAdmin, RoleAdmin, true},
		{"write cannot admin", RoleWrite, RoleAdmin, false},
		{"read cannot admin", RoleRead, RoleAdmin, false},

		{"admin can write", RoleAdmin, RoleWrite, true},
		{"write can write", RoleWrite, RoleWrite, true},
		{"read cannot write", RoleRead, RoleWrite, false},

		{"admin can read", RoleAdmin, RoleRead, true},
		{"write can read", RoleWrite, RoleRead, true},
		{"read can read", RoleRead, RoleRead, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &APIKeyPolicy{Role: tt.role, Namespaces: []string{"*"}}
			if got := p.HasAccess(tt.requiredRole, "any"); got != tt.want {
				t.Errorf("HasAccess(%q, ...) with role %q = %v, want %v",
					tt.requiredRole, tt.role, got, tt.want)
			}
		})
	}
}

// TestHasAccessNamespaceStillEnforced keeps the namespace check alive.
func TestHasAccessNamespaceStillEnforced(t *testing.T) {
	p := &APIKeyPolicy{Role: RoleRead, Namespaces: []string{"docs"}}

	if !p.HasAccess(RoleRead, "docs") {
		t.Error("read token should reach its own namespace")
	}
	if p.HasAccess(RoleRead, "secrets") {
		t.Error("read token reached a namespace it was not granted")
	}
}
