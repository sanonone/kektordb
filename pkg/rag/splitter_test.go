package rag

import (
	"fmt"
	"testing"
	"unicode/utf8"
)

func TestRecursiveSplitter(t *testing.T) {
	text := `Go is an open source programming language that makes it easy to build simple, reliable, and efficient software.

Concurrency is a property of systems in which several computations are executing simultaneously, and potentially interacting with each other.

The Go memory model specifies the conditions under which reads of a variable in one goroutine can be guaranteed to observe values produced by writes to the same variable in a different goroutine.`

	// Test 1: Chunk piccolo (dovrebbe spezzare frasi)
	splitter := NewRecursiveSplitter(50, 0) // 50 chars, no overlap
	chunks := splitter.SplitText(text)

	fmt.Println("--- Test 1 (Small Chunks) ---")
	for i, c := range chunks {
		count := utf8.RuneCountInString(c)
		fmt.Printf("[%d] (%d chars): %q\n", i, count, c)
		if count > 50 {
			t.Errorf("Chunk %d too large: %d > 50", i, count)
		}
	}

	// Test 2: Chunk medio (dovrebbe tenere paragrafi)
	splitter2 := NewRecursiveSplitter(150, 20)
	chunks2 := splitter2.SplitText(text)

	fmt.Println("\n--- Test 2 (Paragraphs + Overlap) ---")
	for i, c := range chunks2 {
		fmt.Printf("[%d]: %q\n", i, c)
	}

	// Verifica empirica: il primo paragrafo (~110 chars) dovrebbe stare tutto intero nel primo chunk
	if len(chunks2) > 0 {
		if !contains(chunks2[0], "build simple, reliable") {
			t.Errorf("Splitter broke the first paragraph unnecessarily")
		}
	}

	// Test 3: Overlap Verification
	// Text: "ABCDE", Chunk: 3, Overlap: 1 -> "ABC", "CDE" (assuming separator is "")
	// For this test we use a dummy separator to control behavior or just use strict sizes.
	// Since our splitter splits by separators first, let's use a sentence with words.
	text3 := "word1 word2 word3 word4 word5"
	// split by space -> [word1, word2, word3, word4, word5]
	// ChunkSize enough for 3 words + spaces?
	// word is 5 chars. space 1.
	// 3 words = 5*3 + 2 = 17 chars.
	// Let's try ChunkSize=17, Overlap=6 (enough for 1 word + space)

	splitter3 := NewRecursiveSplitter(17, 6)
	chunks3 := splitter3.SplitText(text3)

	fmt.Println("\n--- Test 3 (Explicit Overlap) ---")
	for i, c := range chunks3 {
		fmt.Printf("[%d]: %q\n", i, c)
	}

	// Expected:
	// Chunk 0: "word1 word2 word3"
	// Chunk 1: "word3 word4 word5" (Because 'word3' (5) < 6 overlap, so we keep it?)
	// Let'strace:
	// [word1 word2 word3] -> >17? No. Next word4.
	// [word1 word2 word3 word4] -> 17+1+5 = 23 > 17.
	// Close chunk 1: "word1 word2 word3"
	// Overlap logic: remove 'word1' 'word2'.
	// Removed 'word1' (5). Total 23-5-1 = 17. > 6? Yes.
	// Removed 'word2' (5). Total 17-5-1 = 11. > 6? Yes.
	// Wait, removeFirstUntilOverlap logic:
	// parts: [word1, word2, word3]. Total len: 17.
	// OverlapSize: 6.
	// Remove word1. Remaining: word2, word3. Len: 11. > 6? Yes.
	// Remove word2. Remaining: word3. Len: 5. <= 6? Yes. Stop.
	// So carried over: "word3".
	// Next loop adds word4, word5.
	// "word3 word4 word5" -> 17 chars. Fits.
	// So we expect 2 chunks, and 'word3' is in both.

	if len(chunks3) < 2 {
		t.Errorf("Expected at least 2 chunks, got %d", len(chunks3))
	} else {
		if !contains(chunks3[1], "word3") {
			t.Errorf("Overlap failed: 'word3' should be in second chunk")
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(substr)] == substr || len(s) > len(substr) && contains(s[1:], substr)
}
