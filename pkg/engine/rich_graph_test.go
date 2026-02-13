package engine

import (
	"reflect"
	"testing"
	"time"
)

func TestRichGraphFeatures(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	source := "User_Alice"
	target := "Doc_Specs"
	rel := "authored"

	// Define rich properties
	// Note: JSON decoding converts numbers to float64, so we define inputs as float64 to match output for DeepEqual
	inputProps := map[string]interface{}{
		"role":       "editor",
		"confidence": 0.95,
	}
	inputWeight := float32(0.8)

	// ----------------------------------------------------------------
	// SCENARIO A: Rich Link Creation & Retrieval
	// ----------------------------------------------------------------
	t.Run("RichLinkCreation", func(t *testing.T) {
		err := eng.VLink(source, target, rel, "", inputWeight, inputProps)
		if err != nil {
			t.Fatalf("VLink failed: %v", err)
		}

		// Verify Forward Edge (VGetEdges)
		edges, found := eng.VGetEdges(source, rel, 0) // 0 = Now
		if !found || len(edges) != 1 {
			t.Fatalf("Expected 1 edge, got %d", len(edges))
		}

		edge := edges[0]
		if edge.TargetID != target {
			t.Errorf("Wrong target. Expected %s, got %s", target, edge.TargetID)
		}
		if edge.Weight != inputWeight {
			t.Errorf("Wrong weight. Expected %f, got %f", inputWeight, edge.Weight)
		}
		if !reflect.DeepEqual(edge.Props, inputProps) {
			t.Errorf("Wrong properties. Expected %v, got %v", inputProps, edge.Props)
		}
		if edge.CreatedAt == 0 {
			t.Error("CreatedAt timestamp was not set")
		}
		if edge.DeletedAt != 0 {
			t.Error("DeletedAt should be 0 for active edge")
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO B: Reverse Index Consistency
	// ----------------------------------------------------------------
	t.Run("ReverseIndexProps", func(t *testing.T) {
		// Check Incoming Edges on Target
		inEdges, found := eng.VGetIncomingEdges(target, rel, 0)
		if !found || len(inEdges) != 1 {
			t.Fatalf("Reverse edge missing")
		}

		revEdge := inEdges[0]
		// In reverse index, TargetID is actually the SourceID of the relationship
		if revEdge.TargetID != source {
			t.Errorf("Reverse target mismatch. Expected %s, got %s", source, revEdge.TargetID)
		}
		// Properties should have been duplicated to reverse link
		if !reflect.DeepEqual(revEdge.Props, inputProps) {
			t.Errorf("Reverse properties missing. Got %v", revEdge.Props)
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO C: Time Travel (Soft Delete)
	// ----------------------------------------------------------------
	t.Run("TimeTravel", func(t *testing.T) {
		// 1. Take a snapshot of "Now" BEFORE deleting
		// Sleep 1ms to ensure timestamps differ
		time.Sleep(1 * time.Millisecond)
		pastSnapshot := time.Now().UnixNano()
		time.Sleep(1 * time.Millisecond)

		// 2. Perform Soft Delete
		if err := eng.VUnlink(source, target, rel, "", false); err != nil {
			t.Fatalf("Soft VUnlink failed: %v", err)
		}

		// 3. Check Present (Should be empty)
		edgesNow, _ := eng.VGetEdges(source, rel, 0)
		if len(edgesNow) != 0 {
			t.Errorf("Soft delete failed. Edge still visible in present: %v", edgesNow)
		}

		// 4. Check Past (Should exist)
		edgesPast, _ := eng.VGetEdges(source, rel, pastSnapshot)
		if len(edgesPast) != 1 {
			t.Errorf("Time Travel failed. Edge not found in snapshot at %d", pastSnapshot)
		} else {
			// Verify it's the correct edge
			if edgesPast[0].TargetID != target {
				t.Errorf("Time Travel retrived wrong edge")
			}
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO D: Hard Delete
	// ----------------------------------------------------------------
	t.Run("HardDelete", func(t *testing.T) {
		// Re-create a link first
		eng.VLink("X", "Y", "temp", "", 1.0, nil)

		// Verify it exists
		e, _ := eng.VGetEdges("X", "temp", 0)
		if len(e) != 1 {
			t.Fatal("Setup failed")
		}

		// Hard Delete
		eng.VUnlink("X", "Y", "temp", "", true)

		// Verify it's gone from Present
		eNow, _ := eng.VGetEdges("X", "temp", 0)
		if len(eNow) != 0 {
			t.Error("Hard delete failed (visible in present)")
		}

		// Verify it's gone from History (Time Travel shouldn't find it if physically removed)
		// Note: VGetEdges filters by list content. If list entry is gone, it's gone forever.
		// We use a timestamp from "before" the delete (which is now)
		ePast, _ := eng.VGetEdges("X", "temp", time.Now().UnixNano())
		if len(ePast) != 0 {
			t.Error("Hard delete failed (visible in past/history)")
		}
	})
	// ----------------------------------------------------------------
	// SCENARIO E: Subgraph Time Travel
	// ----------------------------------------------------------------
	t.Run("SubgraphTimeTravel", func(t *testing.T) {
		// Setup: Root -> (link) -> Leaf.
		// 1. Create link
		eng.VLink("Root", "Leaf", "test_sub", "", 1.0, nil)

		// 2. Snapshot Past
		time.Sleep(1 * time.Millisecond)
		past := time.Now().UnixNano()
		time.Sleep(1 * time.Millisecond)

		// 3. Soft Delete
		eng.VUnlink("Root", "Leaf", "test_sub", "", false)

		// 4. Extract Subgraph NOW (Should verify link is gone)
		subNow, _ := eng.VExtractSubgraph("", "Root", []string{"test_sub"}, 1, 0)
		if len(subNow.Edges) != 0 {
			t.Errorf("Subgraph NOW should be empty, got %d edges", len(subNow.Edges))
		}

		// 5. Extract Subgraph PAST (Should see the link)
		subPast, _ := eng.VExtractSubgraph("", "Root", []string{"test_sub"}, 1, past)
		if len(subPast.Edges) != 1 {
			t.Errorf("Subgraph PAST should have 1 edge, got %d", len(subPast.Edges))
		} else {
			if subPast.Edges[0].Target != "Leaf" {
				t.Errorf("Subgraph PAST returned wrong target: %s", subPast.Edges[0].Target)
			}
		}
	})
}
