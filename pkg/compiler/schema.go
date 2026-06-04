package compiler

import (
	"encoding/json"
	"fmt"
)

// OutputSchema defines the structure of compiled artifact output.
type OutputSchema struct {
	Type       string             `json:"type"`
	Properties map[string]FieldDef `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// FieldDef describes a single field in the output schema.
type FieldDef struct {
	Type        string             `json:"type"`
	Items       *FieldDef          `json:"items,omitempty"`
	Properties  map[string]FieldDef `json:"properties,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Description string             `json:"description,omitempty"`
	Source      string             `json:"source,omitempty"` // "metadata", "graph", "llm", "computed", "graph+llm"
	LLM         bool               `json:"llm,omitempty"`
}

// SchemaError represents a validation error for a compiled field.
type SchemaError struct {
	Field   string
	Message string
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("field '%s': %s", e.Field, e.Message)
}

// Validate checks whether a compiled value conforms to the field definition.
func (fd FieldDef) Validate(value any) error {
	if value == nil {
		return nil // nil is always valid
	}
	switch fd.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return &SchemaError{Message: fmt.Sprintf("expected string, got %T", value)}
		}
		if len(fd.Enum) > 0 {
			s := value.(string)
			for _, e := range fd.Enum {
				if s == e {
					return nil
				}
			}
			return &SchemaError{Message: fmt.Sprintf("value '%s' not in enum %v", s, fd.Enum)}
		}
	case "number":
		switch value.(type) {
		case float64, int, int64, json.Number:
		default:
			return &SchemaError{Message: fmt.Sprintf("expected number, got %T", value)}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return &SchemaError{Message: fmt.Sprintf("expected boolean, got %T", value)}
		}
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return &SchemaError{Message: fmt.Sprintf("expected array, got %T", value)}
		}
		if fd.Items != nil {
			for i, item := range arr {
				if err := fd.Items.Validate(item); err != nil {
					return &SchemaError{Message: fmt.Sprintf("item[%d]: %v", i, err)}
				}
			}
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return &SchemaError{Message: fmt.Sprintf("expected object, got %T", value)}
		}
	}
	return nil
}

// UnmarshalField parses a JSON value and casts it to the Go type
// implied by the field definition.
func (fd FieldDef) UnmarshalField(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch fd.Type {
	case "string":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return s, nil
	case "number":
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return n, nil
	case "boolean":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	case "array":
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		if fd.Items != nil {
			for i, item := range arr {
				itemRaw, err := json.Marshal(item)
				if err != nil {
					return nil, err
				}
				validated, err := fd.Items.UnmarshalField(itemRaw)
				if err != nil {
					return nil, fmt.Errorf("item[%d]: %w", i, err)
				}
				arr[i] = validated
			}
		}
		return arr, nil
	case "object":
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		return obj, nil
	default:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
}
