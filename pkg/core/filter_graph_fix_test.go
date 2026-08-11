package core

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// --- Set 2.1: RemoveEdge soft targets the ACTIVE edge ---

func TestRemoveEdgeSoftAfterEvolution(t *testing.T) {
	db := NewDB()
	defer db.Close()

	db.AddEdge("src", "dst", "rel", 1.0, []byte("props-v1"), 1000)
	// Evolution: different props → v1 soft-deleted (2000), v2 appended active.
	db.AddEdge("src", "dst", "rel", 2.0, []byte("props-v2"), 2000)

	// Current view: only the active v2 is visible.
	out, _ := db.GetOutEdges("src", "rel", 0)
	if len(out) != 1 || out[0].Weight != 2.0 {
		t.Fatalf("expected only the active v2 edge in current view, got %+v", out)
	}

	// Soft remove: must mark the ACTIVE edge (v2), not re-mark v1.
	db.RemoveEdge("src", "dst", "rel", false, 3000)

	// Current view now empty — the active edge was really removed.
	out, ok := db.GetOutEdges("src", "rel", 0)
	if ok || len(out) != 0 {
		t.Errorf("active edge still visible after soft remove: %+v", out)
	}
	// Historical view between v2's creation and removal still shows it.
	hist, _ := db.GetOutEdges("src", "rel", 2500)
	if len(hist) != 1 || hist[0].Weight != 2.0 {
		t.Errorf("time-travel view broken: %+v", hist)
	}

	// Inspect the shards directly: DeletedAt values.
	shardSource, shardTarget := db.LockTwoShards("src", "dst")
	defer db.UnlockTwoShards("src", "dst")
	outList := shardSource.nodes["src"].OutEdges["rel"]
	if len(outList) != 2 {
		t.Fatalf("expected 2 edge versions stored, got %d", len(outList))
	}
	if outList[0].DeletedAt != 2000 {
		t.Errorf("historical edge DeletedAt changed: %d, want 2000 (unchanged)", outList[0].DeletedAt)
	}
	if outList[1].DeletedAt != 3000 {
		t.Errorf("active edge not soft-deleted: DeletedAt=%d, want 3000", outList[1].DeletedAt)
	}

	// Reverse edge is marked too.
	inList := shardTarget.nodes["dst"].InEdges["rel"]
	if len(inList) == 0 {
		t.Fatal("expected reverse edge")
	}
	if inList[0].DeletedAt != 3000 {
		t.Errorf("reverse edge not soft-deleted: DeletedAt=%d, want 3000", inList[0].DeletedAt)
	}
}

func TestRemoveEdgeSoftActiveOnlyLeavesOtherActiveEdge(t *testing.T) {
	db := NewDB()
	defer db.Close()

	// Two independent edges src→a and src→b on the same relation.
	db.AddEdge("src", "a", "rel", 1.0, []byte("1"), 1000)
	db.AddEdge("src", "b", "rel", 1.0, []byte("2"), 1000)

	db.RemoveEdge("src", "a", "rel", false, 2000)

	// Current view: only the untouched edge to 'b' remains.
	out, _ := db.GetOutEdges("src", "rel", 0)
	if len(out) != 1 || out[0].TargetID != "b" {
		t.Errorf("expected only the untouched edge to 'b', got %+v", out)
	}

	shard, _ := db.LockTwoShards("src", "a")
	defer db.UnlockTwoShards("src", "a")
	outList := shard.nodes["src"].OutEdges["rel"]
	if len(outList) != 2 {
		t.Fatalf("expected 2 edges stored, got %d", len(outList))
	}
	for _, e := range outList {
		if e.TargetID == "a" && e.DeletedAt != 2000 {
			t.Errorf("edge to 'a' not deleted: %+v", e)
		}
		if e.TargetID == "b" && e.DeletedAt != 0 {
			t.Errorf("edge to 'b' wrongly deleted: %+v", e)
		}
	}
}

// --- Set 2.2: quote-aware filter operator parsing ---

func TestFindFilterOperatorQuoteAware(t *testing.T) {
	cases := []struct {
		filter  string
		wantOp  string
		wantIdx int
	}{
		{"tag = 'value'", "=", 4},
		{`tag = "a<=b"`, "=", 4},      // "<=" inside the value must be ignored
		{`version >= "1.0"`, ">=", 8}, // compound found before the value
		{"price != 10", "!=", 6},
		{"name = 'O'Brien'", "=", 5}, // apostrophe inside single quotes
		{"count > 3", ">", 6},
		{"a <= b AND c", "<=", 2},
		{"no-operator-here", "", -1},
	}
	for _, tc := range cases {
		op, idx := findFilterOperator(tc.filter)
		if op != tc.wantOp || idx != tc.wantIdx {
			t.Errorf("findFilterOperator(%q) = (%q,%d), want (%q,%d)",
				tc.filter, op, idx, tc.wantOp, tc.wantIdx)
		}
	}
}

func TestEvaluateBooleanFilterValueWithOperator(t *testing.T) {
	db := NewDB()
	defer db.Close()
	if err := db.CreateVectorIndex("idx", distance.Cosine, 16, 100, distance.Float32, "english", ""); err != nil {
		t.Fatal(err)
	}

	idx, _ := db.GetVectorIndex("idx")
	id1, _ := idx.Add("n1", []float32{0.1, 0.2, 0.3, 0.4})
	if err := db.AddMetadata("idx", id1, map[string]any{"tag": "a<=b", "content": "first"}); err != nil {
		t.Fatal(err)
	}
	id2, _ := idx.Add("n2", []float32{0.2, 0.3, 0.4, 0.5})
	if err := db.AddMetadata("idx", id2, map[string]any{"tag": "c", "content": "second"}); err != nil {
		t.Fatal(err)
	}

	// Value containing an operator must match exactly.
	set, err := db.FindIDsByFilter("idx", `tag = "a<=b"`)
	if err != nil {
		t.Fatalf("filter with operator in value: %v", err)
	}
	if !set.Contains(id1) || set.Contains(id2) {
		t.Errorf("expected only n1 for tag = \"a<=b\", got %v", set)
	}
}

// --- Set 2.3: malformed filters must not panic ---

func TestFindIDsByFilterMalformedNoPanic(t *testing.T) {
	db := NewDB()
	defer db.Close()
	if err := db.CreateVectorIndex("idx", distance.Cosine, 16, 100, distance.Float32, "english", ""); err != nil {
		t.Fatal(err)
	}

	idx, _ := db.GetVectorIndex("idx")
	id1, _ := idx.Add("n1", []float32{0.1, 0.2, 0.3, 0.4})
	_ = db.AddMetadata("idx", id1, map[string]any{"a": "1"})

	for _, filter := range []string{
		"a=1 OR AND AND",
		"OR AND",
		"AND AND OR x=1",
		"a=1 OR",
		" OR ",
		"a=1 AND ",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on filter %q: %v", filter, r)
				}
			}()
			set, err := db.FindIDsByFilter("idx", filter)
			if err != nil && filter != " OR " {
				t.Logf("filter %q returned error (acceptable): %v", filter, err)
			}
			_ = set
		}()
	}
}
