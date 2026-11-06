// Package doctest keeps the Go examples in the repository's READMEs honest.
//
// Documentation drifts silently: an example that no longer compiles looks
// fine on GitHub and wastes the reader's time. This package extracts the
// fenced Go blocks marked as examples and checks that
//
//   - every complete program (a block starting with `package main`) compiles,
//   - every symbol referenced as `client.X` or `engine.X` exists in the
//     corresponding package.
//
// Mark a block with `go example` in the opening fence to have it compiled as
// a standalone program. Plain `go` blocks are treated as fragments and are
// only checked for symbol references, because most of them illustrate one
// call rather than a whole program.
package doctest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Block is a fenced Go code block found in a markdown file.
type Block struct {
	// File is the markdown file the block came from, relative to the repo root.
	File string
	// Line is the 1-based line of the opening fence.
	Line int
	// Code is the block contents.
	Code string
	// Example is true when the fence was ```go example.
	Example bool
}

// Extract returns every fenced Go block in path.
func Extract(path string) ([]Block, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return extractFromString(string(data)), nil
}

func extractFromString(src string) []Block {
	var (
		blocks  []Block
		inFence bool
		isGo    bool
		example bool
		start   int
		buf     strings.Builder
	)

	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case !inFence && strings.HasPrefix(trimmed, "```"):
			// Opening fence of any language. Tracking it keeps a ```go block
			// nested inside a ```markdown example from being mistaken for a
			// real one.
			inFence = true
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			isGo = lang == "go" || lang == "go example"
			example = lang == "go example"
			start = i + 1
			buf.Reset()
		case inFence && trimmed == "```":
			if isGo {
				blocks = append(blocks, Block{
					Line:    start,
					Code:    buf.String(),
					Example: example,
				})
			}
			inFence = false
			isGo = false
		case inFence && isGo:
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return blocks
}

// IsCompleteProgram reports whether code declares its own package.
func IsCompleteProgram(code string) bool {
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		return strings.HasPrefix(trimmed, "package ")
	}
	return false
}

// Parse parses code and returns the identifiers selected from selector.
//
// For selector "client" it returns every X in `client.X` and `client.X(...)`.
// Parsing (rather than substring matching) keeps mentions inside strings and
// comments from being reported.
func Parse(code, selector string) ([]string, error) {
	fset := token.NewFileSet()
	// Wrap fragments so they parse: a bare statement list is not a file.
	src := code
	if !IsCompleteProgram(code) {
		src = "package p\nfunc _() {\n" + code + "\n}\n"
	}
	file, err := parser.ParseFile(fset, "example.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	seen := map[string]bool{}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != selector {
			return true
		}
		if !seen[sel.Sel.Name] {
			seen[sel.Sel.Name] = true
			names = append(names, sel.Sel.Name)
		}
		return true
	})
	sort.Strings(names)
	return names, nil
}

// ReadmeBlocks walks the repository looking for markdown files with Go blocks.
func ReadmeBlocks(root string) (map[string][]Block, error) {
	out := map[string][]Block{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "venv", ".venv", "target", "dist", "tempDocs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(name, "README.md") {
			return nil
		}
		blocks, err := Extract(path)
		if err != nil {
			return err
		}
		if len(blocks) == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		for i := range blocks {
			blocks[i].File = filepath.ToSlash(rel)
		}
		out[rel] = blocks
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
