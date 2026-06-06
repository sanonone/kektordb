package compiler

import (
	"encoding/json"
	"fmt"
)

// compileFieldLLM compiles a single field using the configured LLM.
// It builds a prompt, calls the LLM, parses the JSON response,
// validates against the field schema, and enriches provenance.
func (c *Compiler) compileFieldLLM(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
	req CompileRequest,
	template *CompileTemplate,
) (value any, provenance []Provenance, confidence float64, err error) {
	prompt := c.buildFieldPrompt(fieldName, fieldDef, nodes, req, template)

	response, err := c.llm.Chat(systemPromptCompile, prompt)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("LLM call failed for field '%s': %w", fieldName, err)
	}

	var result struct {
		Value      json.RawMessage `json:"value"`
		Confidence float64         `json:"confidence"`
		Sources    []Provenance    `json:"sources"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		value, provenance, confidence, err := c.parseLLMFallback(response, fieldName, fieldDef)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("LLM response parse failed for field '%s': %w", fieldName, err)
		}
		return value, provenance, confidence, nil
	}

	if len(result.Value) == 0 || string(result.Value) == "null" {
		return nil, nil, 0, nil
	}

	validated, err := fieldDef.UnmarshalField(result.Value)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("field '%s' validation failed: %w", fieldName, err)
	}

	if err := fieldDef.Validate(validated); err != nil {
		return nil, nil, 0, fmt.Errorf("field '%s' schema validation failed: %w", fieldName, err)
	}

	provenance = c.enrichProvenance(result.Sources, nodes)

	if result.Confidence > 0 {
		confidence = result.Confidence
	} else {
		confidence = 0.5
	}

	return validated, provenance, confidence, nil
}

// parseLLMFallback attempts to extract a field value from a raw LLM
// response when JSON parsing fails. It tries common heuristics:
// extracting content between quotes, using the raw text for strings,
// and returning nothing for complex types.
func (c *Compiler) parseLLMFallback(
	response string,
	fieldName string,
	fieldDef FieldDef,
) (value any, provenance []Provenance, confidence float64, err error) {
	switch fieldDef.Type {
	case "string":
		// Use the raw response directly as a best-effort value
		if len(response) > 5000 {
			response = response[:5000]
		}
		return response, nil, 0.2, nil

	case "number":
		// Try to extract a number from the response
		var f float64
		if _, scanErr := fmt.Sscanf(response, "%f", &f); scanErr == nil {
			return f, nil, 0.2, nil
		}
		return nil, nil, 0, fmt.Errorf("cannot parse number from LLM response: %s", response)

	case "array":
		if fieldDef.Items != nil && fieldDef.Items.Type == "string" {
			var arr []any
			if err := json.Unmarshal([]byte(response), &arr); err == nil {
				return arr, nil, 0.3, nil
			}
		}
		return nil, nil, 0, fmt.Errorf("cannot parse array from LLM response")

	default:
		return nil, nil, 0, fmt.Errorf("cannot fallback-parse type '%s' from LLM response", fieldDef.Type)
	}
}
