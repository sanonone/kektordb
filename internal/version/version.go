// Package version centralizes the KektorDB release version — the single
// source of truth for the server, the MCP implementation, and the Go client.
//
// Overridden at build time with:
//
//	go build -ldflags "-X github.com/sanonone/kektordb/internal/version.Version=v0.6.1"
//
// (the Makefile injects the git tag via the release build targets).
package version

// Version is the current KektorDB release version. Dev builds report
// "v0.6.1-dev" until a tagged release is built.
var Version = "v0.6.1-dev"
