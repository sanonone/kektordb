package doctest_test

import (
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/internal/doctest"
)

// repoRoot is the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// TestReadmeExamplesCompile builds every block marked as ```go example.
//
// A documentation example that does not compile is worse than no example:
// the reader copies it, it fails, and they assume the library is broken.
func TestReadmeExamplesCompile(t *testing.T) {
	root := repoRoot(t)
	readmes, err := doctest.ReadmeBlocks(root)
	if err != nil {
		t.Fatalf("walk readmes: %v", err)
	}

	var checked int
	for file, blocks := range readmes {
		for i, block := range blocks {
			if !block.Example {
				continue
			}
			checked++
			name := file + " block " + itoa(i+1)
			t.Run(name, func(t *testing.T) {
				if !doctest.IsCompleteProgram(block.Code) {
					t.Fatalf("%s:%d is marked `go example` but is not a standalone program "+
						"(a block marked as an example must start with `package main`)",
						block.File, block.Line)
				}
				buildExample(t, block.Code)
			})
		}
	}

	if checked == 0 {
		t.Skip("no blocks marked `go example` yet: see internal/doctest/README.md")
	}
}

// buildExample compiles code in a throwaway module that requires the local
// repository, so `import "github.com/sanonone/kektordb/..."` resolves to this
// working tree rather than to a published version.
func buildExample(t *testing.T, code string) {
	t.Helper()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not on PATH")
	}

	root := repoRoot(t)
	dir := t.TempDir()

	modFile := "module doctest_example\n\ngo 1.26.0\n\n" +
		"require github.com/sanonone/kektordb v0.0.0\n\n" +
		"replace github.com/sanonone/kektordb => " + root + "\n"

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modFile), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Reuse the repository's dependency versions.
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
			t.Fatalf("write go.sum: %v", err)
		}
	}

	cmd := exec.Command(goBin, "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GO111MODULE=on")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("example does not compile:\n%s", out)
	}
}

// TestReadmeSymbolsExist checks that every `pkg.Symbol` mentioned in a README
// exists in that package. It catches documentation for methods that were
// renamed or never existed.
func TestReadmeSymbolsExist(t *testing.T) {
	root := repoRoot(t)
	readmes, err := doctest.ReadmeBlocks(root)
	if err != nil {
		t.Fatalf("walk readmes: %v", err)
	}

	// package name -> import path
	packages := map[string]string{
		"client": "github.com/sanonone/kektordb/pkg/client",
		"engine": "github.com/sanonone/kektordb/pkg/engine",
	}

	for _, blocks := range readmes {
		for _, block := range blocks {
			for pkgName, importPath := range packages {
				names, err := doctest.Parse(block.Code, pkgName)
				if err != nil {
					// Fragments containing undefined identifiers still parse;
					// only syntax errors land here, and those are caught by
					// TestReadmeExamplesCompile for example blocks.
					continue
				}
				if len(names) == 0 {
					continue
				}
				pkg, err := build.ImportDir(packageDir(root, importPath), 0)
				if err != nil {
					t.Fatalf("import %s: %v", importPath, err)
				}
				exported := map[string]bool{}
				for _, n := range pkg.GoFiles {
					collectExported(filepath.Join(packageDir(root, importPath), n), exported)
				}

				var missing []string
				for _, name := range names {
					if !exported[name] {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					t.Errorf("%s:%d documents %s.%s, which does not exist in %s",
						block.File, block.Line, pkgName,
						strings.Join(missing, ", "+pkgName+"."), importPath)
				}
			}
		}
	}
}

// packageDir converts an import path under this module to a filesystem path.
func packageDir(root, importPath string) string {
	const mod = "github.com/sanonone/kektordb"
	rel := strings.TrimPrefix(importPath, mod)
	return filepath.Join(root, filepath.FromSlash(rel))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
