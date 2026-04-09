package textanalyzer

import (
	"strings"
	"testing"
)

func TestCompress_English(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic sentence",
			input:    "The quick brown fox jumps over the lazy dog",
			expected: "quick brown fox jumps over lazy dog",
		},
		{
			name:     "preserves negation",
			input:    "I do not like this",
			expected: "I not like this",
		},
		{
			name:     "preserves logical operators",
			input:    "If you want A and B or C but not D",
			expected: "If you want A and B or C but not D",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "only stopwords",
			input:    "the is an",
			expected: "",
		},
		{
			name:     "preserves case",
			input:    "My name is John and I live in Paris",
			expected: "My name John and I live Paris",
		},
		{
			name:     "complex sentence",
			input:    "The user has been working on this project for three years",
			expected: "user working this project three years",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Compress(tt.input, "english")
			if result != tt.expected {
				t.Errorf("Compress(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCompress_Italian(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic sentence",
			input:    "Il mio cane si chiama Fuffi",
			expected: "mio cane si chiama Fuffi",
		},
		{
			name:     "preserves negation",
			input:    "Io non sono d'accordo",
			expected: "Io non sono d'accordo",
		},
		{
			name:     "preserves logical operators",
			input:    "Se vuoi A e B o C ma non D",
			expected: "Se vuoi A e B o C ma non D",
		},
		{
			name:     "complex sentence",
			input:    "Il mio cane si chiama Fuffi e io lavoro come sviluppatore software a Milano",
			expected: "mio cane si chiama Fuffi e io lavoro come sviluppatore software a Milano",
		},
		{
			name:     "preserves articled prepositions",
			input:    "Vado al mare e torno dal lavoro",
			expected: "Vado mare e torno lavoro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Compress(tt.input, "italian")
			if result != tt.expected {
				t.Errorf("Compress(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCompress_NegationPreservation(t *testing.T) {
	// Critical test: negations must NEVER be removed
	criticalPairs := []struct {
		input    string
		mustHave []string // Accept any case variation
	}{
		{"I do not agree", []string{"not"}},
		{"This is no good", []string{"no"}},
		{"Never say never", []string{"Never", "never"}},
		{"Unless you try", []string{"Unless"}},
		{"Io non sono vegano", []string{"non"}},
		{"Non lo so mai", []string{"Non", "non"}},
		{"Se non piove", []string{"non"}},
	}

	for _, pair := range criticalPairs {
		result := Compress(pair.input, "")
		found := false
		for _, variant := range pair.mustHave {
			if strings.Contains(result, variant) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Critical: Compress(%q) removed essential word, got: %q",
				pair.input, result)
		}
	}
}

func TestCompress_LanguageCodes(t *testing.T) {
	// Test various language code formats
	testCases := []struct {
		lang     string
		input    string
		expected string
	}{
		{"en", "The quick fox", "quick fox"},
		{"eng", "The quick fox", "quick fox"},
		{"english", "The quick fox", "quick fox"},
		{"it", "Il mio cane", "mio cane"},
		{"ita", "Il mio cane", "mio cane"},
		{"italian", "Il mio cane", "mio cane"},
		{"", "The quick fox", "quick fox"}, // default to english
	}

	for _, tc := range testCases {
		result := Compress(tc.input, tc.lang)
		if result != tc.expected {
			t.Errorf("Compress(%q, lang=%q) = %q, want %q",
				tc.input, tc.lang, result, tc.expected)
		}
	}
}

func TestCompressionRatio(t *testing.T) {
	tests := []struct {
		original   string
		compressed string
		expected   float64
		tolerance  float64
	}{
		{
			original:   "The quick brown fox",
			compressed: "quick brown fox",
			expected:   0.25, // 1 of 4 words removed
			tolerance:  0.01,
		},
		{
			original:   "a b c d",
			compressed: "",
			expected:   1.0, // 100% reduction
			tolerance:  0.0,
		},
		{
			original:   "if and only then",
			compressed: "if and only then",
			expected:   0.0, // 0% reduction
			tolerance:  0.0,
		},
	}

	for _, tt := range tests {
		ratio := CompressionRatio(tt.original, tt.compressed)
		diff := ratio - tt.expected
		if diff < 0 {
			diff = -diff
		}
		if diff > tt.tolerance {
			t.Errorf("CompressionRatio(%q, %q) = %f, want %f (±%f)",
				tt.original, tt.compressed, ratio, tt.expected, tt.tolerance)
		}
	}
}

func TestCompress_PunctuationHandling(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "Hello, world!",
			expected: "Hello world",
		},
		{
			input:    "Well... that's it.",
			expected: "Well that's it",
		},
	}

	for _, tt := range tests {
		result := Compress(tt.input, "english")
		if result != tt.expected {
			t.Errorf("Compress(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// BenchmarkCompress measures the performance of the compression function
func BenchmarkCompress(b *testing.B) {
	text := "The quick brown fox jumps over the lazy dog. The user has been working on this project for three years and has not completed it yet."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compress(text, "english")
	}
}

// BenchmarkCompressLongText measures performance on longer text
func BenchmarkCompressLongText(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compress(text, "english")
	}
}
