package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const hexDigits = "0123456789abcdef"

// float32SliceToHexString encodes a float32 slice as a compact hex bit
// pattern: the byte representation of each float32 as 8 hex characters,
// prefixed with 'h' (fixed width, no separators). Deterministic and much
// cheaper than decimal formatting (B4: AOF write-path latency).
//
// Format: "h" + 8*len hex chars. Replay accepts both this format and the
// legacy space-separated decimal format.
func float32SliceToHexString(slice []float32) string {
	var b strings.Builder
	b.Grow(1 + len(slice)*8)
	b.WriteByte('h')
	buf := make([]byte, 8)
	for _, v := range slice {
		bits := math.Float32bits(v)
		for j := 7; j >= 0; j-- {
			buf[j] = hexDigits[bits&0xF]
			bits >>= 4
		}
		b.Write(buf)
	}
	return b.String()
}

// parseVectorFromString parses a vector string into a []float32.
// Used for AOF replay. Accepts both the current hex format ("h" + 8 hex
// chars per float) and the legacy space-separated decimal format.
func parseVectorFromString(s string) ([]float32, error) {
	if strings.HasPrefix(s, "h") {
		return parseHexVector(s[1:])
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return nil, fmt.Errorf("vector string is empty")
	}
	vector := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(part, 32)
		if err != nil {
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}

// parseHexVector parses the "h" hex vector payload (8 hex chars per float32).
func parseHexVector(hexStr string) ([]float32, error) {
	if len(hexStr) == 0 || len(hexStr)%8 != 0 {
		return nil, fmt.Errorf("invalid hex vector length %d", len(hexStr))
	}
	n := len(hexStr) / 8
	vector := make([]float32, n)
	for i := 0; i < n; i++ {
		bits, err := strconv.ParseUint(hexStr[i*8:i*8+8], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hex vector token at %d: %w", i, err)
		}
		vector[i] = math.Float32frombits(uint32(bits))
	}
	return vector, nil
}

// helper
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0
	}
}
