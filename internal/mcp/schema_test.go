package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAllToolsExposeInputSchema drives the real MCP server over an in-memory
// transport and verifies every registered tool exposes a valid inputSchema
// (type object with properties, and every required field present in
// properties). This guards the jsonschema tags on the Args structs in
// types.go — a tool added without schema tags would fail here.
func TestAllToolsExposeInputSchema(t *testing.T) {
	_, eng, cleanup := setupTestService(t)
	defer cleanup()

	srv := NewMCPServer(eng, &mockEmbedder{}, nil, nil, nil) // nil allowlist = all tools

	ctx := context.Background()
	cTransport, sTransport := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := sdk.NewClient(&sdk.Implementation{Name: "schema-test", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	result, err := cs.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}

	for _, tool := range result.Tools {
		// InputSchema is any (jsonschema.Schema) — normalize to a map.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Errorf("tool %q: cannot marshal inputSchema: %v", tool.Name, err)
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("tool %q: invalid inputSchema: %v", tool.Name, err)
			continue
		}
		if schema == nil || len(schema) == 0 {
			t.Errorf("tool %q has no inputSchema (missing jsonschema tags?)", tool.Name)
			continue
		}
		if typ, _ := schema["type"].(string); typ != "object" {
			t.Errorf("tool %q: inputSchema.type = %v, want object", tool.Name, schema["type"])
		}
		props, hasProps := schema["properties"]
		if !hasProps || props == nil {
			// Zero-argument tools legitimately have no properties.
			if required, ok := schema["required"].([]any); ok && len(required) > 0 {
				t.Errorf("tool %q: has required fields but no properties", tool.Name)
			}
			continue
		}
		propsMap, ok := props.(map[string]any)
		if !ok {
			t.Errorf("tool %q: properties is not an object", tool.Name)
			continue
		}
		if required, ok := schema["required"].([]any); ok {
			for _, f := range required {
				name, _ := f.(string)
				if _, present := propsMap[name]; !present {
					t.Errorf("tool %q: required field %q missing from properties", tool.Name, name)
				}
			}
		}
	}
}
