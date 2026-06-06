package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
)

const systemPromptCompile = `You are a knowledge compiler. Your job is to extract structured data from source nodes and compile it into a specific field of a knowledge artifact.

You MUST respond with valid JSON in this exact format:
{
  "value": <the extracted value matching the field schema>,
  "confidence": <float between 0.0 and 1.0>,
  "sources": [
    {"source_id": "<node_id>", "confidence": <float>, "evidence": "<exact quote or reasoning>"}
  ]
}

Rules:
- Extract ONLY information that is present in the source nodes. Do NOT hallucinate.
- If you cannot find relevant information, set value to null and confidence to 0.
- Always cite the source node IDs from which you extracted information.
- Confidence should reflect how certain you are that the extracted value is accurate.
- If the field schema specifies an enum, your value MUST be one of the enum values.
- If the field type is "array", your value MUST be a JSON array.
- If the field type is "number", your value MUST be a JSON number.
- If the field type is "string", your value MUST be a JSON string.
- For array fields, each item must independently satisfy the items schema.
- Keep evidence concise (max 100 characters).`

// buildFieldPrompt constructs the user prompt for compiling a single field.
func (c *Compiler) buildFieldPrompt(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
	req CompileRequest,
	template *CompileTemplate,
) string {
	var sb strings.Builder

	// Source context
	sb.WriteString("SOURCE NODES:\n")
	limit := 20
	if len(nodes) < limit {
		limit = len(nodes)
	}
	for _, node := range nodes[:limit] {
		sb.WriteString(fmt.Sprintf("\n--- Node: %s ---\n", node.ID))
		if node.Content != "" {
			sb.WriteString(fmt.Sprintf("Content: %s\n", node.Content))
		}
		for k, v := range node.Metadata {
			if isRelevantMetadata(k) {
				sb.WriteString(fmt.Sprintf("%s: %v\n", k, v))
			}
		}
	}

	// Field schema
	schemaJSON, _ := json.MarshalIndent(fieldDef, "", "  ")

	// Task description
	taskDesc := ""
	if req.TaskSpec != nil && req.TaskSpec.Description != "" {
		taskDesc = req.TaskSpec.Description
	}
	if template != nil {
		taskDesc = template.Description
	}

	return fmt.Sprintf(`TASK: %s
FIELD: %s
FIELD SCHEMA:
%s

SOURCE NODES:
%s

Extract the value for field "%s" from the source nodes above.
Respond ONLY with the JSON object as specified in the system prompt.`,
		taskDesc, fieldName, string(schemaJSON), sb.String(), fieldName)
}

// isRelevantMetadata returns true for metadata keys worth including in the prompt.
func isRelevantMetadata(key string) bool {
	// Skip internal/system keys
	if strings.HasPrefix(key, "_") || strings.HasPrefix(key, "__") {
		return false
	}
	// Include commonly useful keys
	useful := map[string]bool{
		"type": true, "entity_id": true, "name": true, "title": true,
		"session_id": true, "source": true,
	}
	if useful[key] {
		return true
	}
	// Include custom keys (not internal and not commonly useless)
	return true
}
