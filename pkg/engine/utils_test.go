package engine

import (
	"math"
	"testing"
)

func TestParseVectorAcceptsHexAndDecimal(t *testing.T) {
	// Legacy decimal format (old AOFs) must still parse.
	dec, err := parseVectorFromString("0.5 -1.25 3")
	if err != nil {
		t.Fatalf("decimal parse: %v", err)
	}
	if len(dec) != 3 || dec[0] != 0.5 || dec[1] != -1.25 || dec[2] != 3 {
		t.Errorf("decimal parse mismatch: %v", dec)
	}

	// Hex format round-trip.
	orig := []float32{0.5, -1.25, 3.75, 1e-9, math.MaxFloat32}
	hex := float32SliceToHexString(orig)
	if hex[0] != 'h' {
		t.Fatalf("hex string missing marker: %q", hex)
	}
	parsed, err := parseVectorFromString(hex)
	if err != nil {
		t.Fatalf("hex parse: %v", err)
	}
	if len(parsed) != len(orig) {
		t.Fatalf("length mismatch: %d vs %d", len(parsed), len(orig))
	}
	for i := range orig {
		if parsed[i] != orig[i] {
			t.Errorf("value %d mismatch: got %v (bits %08x), want %v (bits %08x)",
				i, parsed[i], math.Float32bits(parsed[i]), orig[i], math.Float32bits(orig[i]))
		}
	}
}

func TestParseHexVectorRejectsBadInput(t *testing.T) {
	if _, err := parseVectorFromString("h0102"); err == nil {
		t.Error("expected error for non-multiple-of-8 hex length")
	}
	if _, err := parseVectorFromString("hzzzzzzzz"); err == nil {
		t.Error("expected error for invalid hex chars")
	}
	if _, err := parseVectorFromString(""); err == nil {
		t.Error("expected error for empty vector string")
	}
}
