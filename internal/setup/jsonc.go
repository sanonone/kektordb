package setup

import (
	"encoding/json"
	"fmt"
)

// parseJSONC parses a JSON document that may contain // and /* */ comments and
// trailing commas, the dialect used by opencode.jsonc.
//
// encoding/json rejects comments outright, so pointing the installer at an
// opencode.jsonc produced "invalid character '/'" and the MCP entry was never
// written — silently, since the caller only logged a warning. Editors write
// these files with comments by default, so this is the common case, not an edge
// one.
//
// The stripper is deliberately conservative: it works on bytes, tracks string
// state and escapes, and never rewrites content inside a string. It also drops
// trailing commas, which some editors leave behind.
func parseJSONC(data []byte) (map[string]json.RawMessage, error) {
	cleaned := stripJSONC(data)

	var out map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &out); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return out, nil
}

// stripJSONC removes comments and trailing commas from a JSONC document.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))

	var (
		inString bool
		escaped  bool
	)

	for i := 0; i < len(data); i++ {
		c := data[i]

		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch {
		case c == '"':
			inString = true
			out = append(out, c)

		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			// Line comment: skip to the newline, keep the newline itself so line
			// numbers in a parse error still match the original file.
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}

		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			// Block comment: skip to the closing marker.
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				if data[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i++ // step onto the '/'

		case c == ',':
			// Trailing comma: look past whitespace and comments for a closing
			// brace or bracket. If that is what follows, the comma is dropped.
			j := i + 1
			for j < len(data) {
				if data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r' {
					j++
					continue
				}
				if data[j] == '/' && j+1 < len(data) && data[j+1] == '/' {
					for j < len(data) && data[j] != '\n' {
						j++
					}
					continue
				}
				if data[j] == '/' && j+1 < len(data) && data[j+1] == '*' {
					j += 2
					for j+1 < len(data) && !(data[j] == '*' && data[j+1] == '/') {
						j++
					}
					j += 2
					continue
				}
				break
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // drop the trailing comma
			}
			out = append(out, c)

		default:
			out = append(out, c)
		}
	}

	return out
}
