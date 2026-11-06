package doctest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"testing"
)

// collectExported records the exported identifiers declared in a Go source file:
// top-level funcs, types, vars and consts. Test files are excluded by the
// caller (build.ImportDir reports them separately).
func collectExported(path string, out map[string]bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Methods are collected too: the docs call client.VSearch(...),
			// so the method name must be reachable by the same lookup.
			if d.Name.IsExported() {
				out[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out[s.Name.Name] = true
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							out[name.Name] = true
						}
					}
				}
			}
		}
	}
}

// listGoFiles returns the absolute paths of the non-test Go files in dir.
func listGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}
