package doctest

import "testing"

func TestExtractFromString(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantGo   int
		wantEx   int
		wantCode string
	}{
		{
			name:     "plain go block",
			src:      "text\n```go\nx := 1\n```\n",
			wantGo:   1,
			wantCode: "x := 1\n",
		},
		{
			name:     "example block",
			src:      "```go example\npackage main\n```\n",
			wantGo:   1,
			wantEx:   1,
			wantCode: "package main\n",
		},
		{
			name:     "non-go blocks are ignored",
			src:      "```bash\necho hi\n```\n```go\ny := 2\n```\n",
			wantGo:   1,
			wantCode: "y := 2\n",
		},
		{
			// The parser used to treat a ```go fence inside a ```markdown
			// example as a real block, so this package's own README was
			// compiled and failed.
			name:   "go fence nested in a markdown block is ignored",
			src:    "```markdown\n```go example\npackage main\n```\n```\n",
			wantGo: 0,
		},
		{
			name:   "unterminated block is dropped",
			src:    "```go\nx := 1\n",
			wantGo: 0,
		},
		{
			name:   "multiple go blocks",
			src:    "```go\na\n```\n\ntext\n\n```go\nb\n```\n",
			wantGo: 2,
		},
		{
			name:     "indented fence",
			src:      "  ```go\n  a\n  ```\n",
			wantGo:   1,
			wantCode: "  a\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromString(tt.src)
			if len(got) != tt.wantGo {
				t.Fatalf("extracted %d blocks, want %d: %+v", len(got), tt.wantGo, got)
			}
			if tt.wantEx > 0 && !got[0].Example {
				t.Error("block not marked as example")
			}
			if tt.wantCode != "" && got[0].Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got[0].Code, tt.wantCode)
			}
		})
	}
}

func TestIsCompleteProgram(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"package main\n\nfunc main() {}", true},
		{"// a comment\npackage main\n", true},
		{"\n\npackage foo\n", true},
		{"x := 1\n", false},
		{"// only a comment\nx := 1\n", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsCompleteProgram(tt.code); got != tt.want {
			t.Errorf("IsCompleteProgram(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		selector string
		want     []string
	}{
		{
			name:     "fragment referencing package symbols",
			code:     "results, err := client.VSearch(\"idx\", 10, vec, \"\", 0, 1.0, nil)",
			selector: "client",
			want:     []string{"VSearch"},
		},
		{
			name:     "complete program",
			code:     "package main\n\nfunc main() {\n\tc := client.New(\"h\", 1, \"\")\n\t_ = c\n}",
			selector: "client",
			want:     []string{"New"},
		},
		{
			name:     "duplicates are collapsed",
			code:     "a := client.Get(\"k\")\nb := client.Get(\"j\")",
			selector: "client",
			want:     []string{"Get"},
		},
		{
			// A symbol mentioned in a comment or a string is not a reference.
			name:     "strings and comments do not count",
			code:     "// client.NotReal()\ns := \"client.AlsoNotReal\"\n_ = s",
			selector: "client",
			want:     nil,
		},
		{
			name:     "other selectors are ignored",
			code:     "c := client.New(\"h\", 1, \"\")\n_ = time.Second",
			selector: "client",
			want:     []string{"New"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.code, tt.selector)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Parse = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("Parse = %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}
