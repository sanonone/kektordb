package client

import (
	"testing"

	"github.com/sanonone/kektordb/internal/version"
)

// TestVersionMatchesInternal guards against version drift: the exported
// client Version must always mirror the centralized internal/version package
// (single source of truth, injected at build time via Makefile -X ldflags).
func TestVersionMatchesInternal(t *testing.T) {
	if Version != version.Version {
		t.Errorf("client.Version = %q, want %q (internal/version)", Version, version.Version)
	}
}
