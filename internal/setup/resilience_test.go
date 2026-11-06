package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestInjectMCPEntrySurvivesNullSection covers a panic that made the installer
// crash on a perfectly ordinary config file.
//
// `{"mcpServers": null}` unmarshals into a nil map without an error, and the
// assignment that follows panics: "assignment to entry in nil map". Editors and
// users leave empty sections behind, so this is not exotic input.
func TestInjectMCPEntrySurvivesNullSection(t *testing.T) {
	for _, body := range []string{
		`{"mcpServers": null}`,
		`null`,
		`{}`,
		`{"mcpServers": {}}`,
	} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}

			entry := map[string]any{"command": "kektordb", "args": []string{"--mcp"}}
			if err := injectMCPEntry(path, "mcpServers", "kektordb", entry); err != nil {
				t.Fatalf("injectMCPEntry returned an error: %v", err)
			}

			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("the file is no longer valid JSON: %v\n%s", err, out)
			}
			var servers map[string]json.RawMessage
			if err := json.Unmarshal(parsed["mcpServers"], &servers); err != nil {
				t.Fatalf("mcpServers is not an object: %v", err)
			}
			if _, ok := servers["kektordb"]; !ok {
				t.Errorf("the server entry was not written: %s", out)
			}
		})
	}
}

// TestJSONCParsing covers the dialect opencode.jsonc uses.
//
// The installer pointed at opencode.jsonc but parsed it with encoding/json,
// which rejects comments: the file failed to parse and the MCP entry was never
// registered. The failure was only a warning, so the integration silently did
// nothing.
func TestJSONCParsing(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "line comment",
			body: "{\n  // this is a comment\n  \"a\": 1\n}",
		},
		{
			name: "block comment",
			body: "{\n  /* block\n     comment */\n  \"a\": 1\n}",
		},
		{
			name: "trailing comma",
			body: "{\n  \"a\": 1,\n}",
		},
		{
			name: "comment and trailing comma",
			body: "{\n  \"a\": 1, // keep\n}",
		},
		{
			name: "comment markers inside a string are preserved",
			body: `{"url": "http://example.com//path", "glob": "/*not a comment*/"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJSONC([]byte(tt.body))
			if err != nil {
				t.Fatalf("parseJSONC: %v\ninput: %s", err, tt.body)
			}
			if len(got) == 0 {
				t.Error("parsed to an empty object")
			}
		})
	}
}

// TestJSONCPreservesStringContent pins the stripper's most dangerous failure
// mode: mangling a value that merely looks like a comment.
func TestJSONCPreservesStringContent(t *testing.T) {
	body := `{"url": "http://example.com//path", "glob": "/*x*/"}`
	got, err := parseJSONC([]byte(body))
	if err != nil {
		t.Fatalf("parseJSONC: %v", err)
	}
	var url, glob string
	if err := json.Unmarshal(got["url"], &url); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got["glob"], &glob); err != nil {
		t.Fatal(err)
	}
	if url != "http://example.com//path" {
		t.Errorf("url = %q, the stripper ate the // inside the string", url)
	}
	if glob != "/*x*/" {
		t.Errorf("glob = %q, the stripper treated a string as a comment", glob)
	}
}

// TestCommandAllowedSurvivesNullAllowList covers the second panic site.
func TestCommandAllowedSurvivesNullAllowList(t *testing.T) {
	origHome := userHomeDir
	origRead := readFileFn
	origWrite := writeFileFn
	origStat := statFn
	origMkdir := mkdirAllFn
	defer func() {
		userHomeDir, readFileFn, writeFileFn, statFn, mkdirAllFn = origHome, origRead, origWrite, origStat, origMkdir
	}()

	dir := t.TempDir()
	userHomeDir = func() (string, error) { return dir, nil }
	readFileFn, writeFileFn, statFn, mkdirAllFn = os.ReadFile, os.WriteFile, os.Stat, os.MkdirAll

	path := claudeSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions": null}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := addClaudeCodeAllowlist(); err != nil {
		t.Fatalf("addClaudeCodeAllowlist returned an error: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["permissions"]; !ok {
		t.Errorf("permissions block was not written: %s", out)
	}
}
