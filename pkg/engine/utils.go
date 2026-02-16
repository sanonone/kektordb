package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// float32SliceToString converts a float32 slice to a space-separated string.
// Used for AOF serialization.
func float32SliceToString(slice []float32) string {
	var b strings.Builder
	for i, v := range slice {
		if i > 0 {
			b.WriteString(" ")
		}
		// 'f' for format, -1 for the minimum necessary precision, 32 for float32.
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	return b.String()
}

// parseVectorFromString parses a space-separated string into a []float32.
// Used for AOF replay.
func parseVectorFromString(s string) ([]float32, error) {
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
