package engine

import (
	"slices"
	"testing"
)

func TestBidirectionalGraph(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	source := "chunk_A"
	target := "entity_Go"
	rel := "mentions"

	// 2. Create Link
	// We link "chunk_A" -> "entity_Go" with relation "mentions"
	if err := eng.VLink(source, target, rel, "", 1.0, nil); err != nil {
		t.Fatalf("VLink failed: %v", err)
	}

	// 3. Test Forward Link (Did we save 'rel'?)
	// Should return ["entity_Go"]
	links, found := eng.VGetLinks(source, rel)
	if !found || !slices.Contains(links, target) {
		t.Errorf("Forward link missing. Got %v, want %v", links, target)
	}

	// 4. Test Reverse Link (Did we auto-save 'rev'?)
	// Should return ["chunk_A"]
	// This proves that "Who mentions Go?" works.
	incoming, found := eng.VGetIncoming(target, rel)
	if !found || !slices.Contains(incoming, source) {
		t.Errorf("Reverse link missing. Got %v, want %v", incoming, source)
	}

	// 5. Test Unlink
	if err := eng.VUnlink(source, target, rel, "", false); err != nil {
		t.Fatalf("VUnlink failed: %v", err)
	}

	// Verify cleanup
	_, foundFwd := eng.VGetLinks(source, rel)
	if foundFwd {
		t.Error("Forward link should have been deleted")
	}
	_, foundRev := eng.VGetIncoming(target, rel)
	if foundRev {
		t.Error("Reverse link should have been deleted")
	}
}
