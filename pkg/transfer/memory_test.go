package transfer

import (
	"context"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

type fakeEmbedder struct{ dim int }

func (f *fakeEmbedder) Embed(text string) ([]float32, error) {
	vec := make([]float32, f.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func TestTransferMemory(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := "source_idx"
	dst := "target_idx"
	if err := eng.VCreate(src, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VCreate(dst, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}
	if err := eng.VAdd(src, "mem1", vec, map[string]any{"content": "test memory"}); err != nil {
		t.Fatal(err)
	}

	result, err := TransferMemory(context.Background(), eng, &fakeEmbedder{dim: 384}, Args{
		SourceIndex:    src,
		TargetIndex:    dst,
		Query:          "test",
		Limit:          10,
		WithGraph:      false,
		TransferReason: "test transfer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TransferredCount != 1 {
		t.Errorf("expected 1 transfer, got %d", result.TransferredCount)
	}

	data, err := eng.VGet(dst, "mem1")
	if err != nil {
		t.Fatal(err)
	}
	if data.Metadata["_transfer_reason"] != "test transfer" {
		t.Errorf("unexpected transfer reason: %v", data.Metadata["_transfer_reason"])
	}
	if data.Metadata["_transferred_from"] == "" {
		t.Error("missing _transferred_from metadata")
	}
}

func TestTransferMemoryValidation(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	_, err = TransferMemory(context.Background(), eng, embeddings.NoopEmbedder{}, Args{
		SourceIndex: "",
		TargetIndex: "dst",
		Query:       "q",
	})
	if err == nil {
		t.Error("expected error for missing source index")
	}
}

func TestTransferMemoryNoEmbedder(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.VCreate("src", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VCreate("dst", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	_, err = TransferMemory(context.Background(), eng, nil, Args{
		SourceIndex: "src",
		TargetIndex: "dst",
		Query:       "q",
	})
	if err == nil {
		t.Error("expected error when embedder is nil")
	}
}
