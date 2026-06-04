package compiler

import "fmt"

var BuiltinTemplates = map[string]CompileTemplate{
	"user_profile": {
		Name:        "user_profile",
		Description: "Aggregated user profile from memory graph",
		EntityTypes: []string{"user"},
		Schema: OutputSchema{
			Type: "object",
			Properties: map[string]FieldDef{
				"name": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "User's name from metadata",
				},
				"preferences": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "graph+llm",
					LLM:     true,
					Description: "Extracted user preferences",
				},
				"communication_style": {
					Type:        "string",
					Source:      "llm",
					LLM:         true,
					Description: "How the user prefers to communicate",
				},
				"interaction_count": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Number of interactions with this user",
				},
				"last_interaction": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "Timestamp of last interaction",
				},
				"core_facts": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "graph",
					LLM:     false,
					Description: "Core facts extracted by Gardener",
				},
			},
		},
		Sources: SourceSpec{
			Type:   "graph_query",
			Entity: EntityRef{Type: "user"},
			Depth:  2,
		},
		CompileMode: CompileModeHybrid,
	},

	"project_summary": {
		Name:        "project_summary",
		Description: "Statistical and relational summary of a project",
		EntityTypes: []string{"project"},
		Schema: OutputSchema{
			Type: "object",
			Properties: map[string]FieldDef{
				"node_count": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Number of memory nodes in this project",
				},
				"relation_count": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Number of graph relations",
				},
				"last_activity": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "Most recent activity timestamp",
				},
				"top_entities": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "graph",
					LLM:     false,
					Description: "Most connected entities",
				},
				"summary": {
					Type:        "string",
					Source:      "llm",
					LLM:         true,
					Description: "LLM-generated summary of the project",
				},
			},
		},
		Sources: SourceSpec{
			Type:   "graph_query",
			Entity: EntityRef{Type: "project"},
			Depth:  2,
		},
		CompileMode: CompileModeHybrid,
	},

	"conversation_context": {
		Name:        "conversation_context",
		Description: "Context from a conversation session",
		EntityTypes: []string{"session"},
		Schema: OutputSchema{
			Type: "object",
			Properties: map[string]FieldDef{
				"topic": {
					Type:        "string",
					Source:      "llm",
					LLM:         true,
					Description: "Main topic of the conversation",
				},
				"key_decisions": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "llm",
					LLM:     true,
					Description: "Key decisions made during the conversation",
				},
				"participants": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "metadata",
					LLM:     false,
					Description: "Participants in the session",
				},
				"duration_minutes": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Duration of the session in minutes",
				},
				"memory_count": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Number of memories saved in this session",
				},
			},
		},
		Sources: SourceSpec{
			Type:   "graph_query",
			Entity: EntityRef{Type: "session"},
			Depth:  1,
		},
		CompileMode: CompileModeHybrid,
	},

	"topic_overview": {
		Name:        "topic_overview",
		Description: "Overview of a topic from the knowledge graph",
		EntityTypes: []string{},
		Schema: OutputSchema{
			Type: "object",
			Properties: map[string]FieldDef{
				"related_entities": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "graph",
					LLM:     false,
					Description: "Entities related to this topic",
				},
				"relation_types": {
					Type:    "array",
					Items:   &FieldDef{Type: "string"},
					Source:  "graph",
					LLM:     false,
					Description: "Types of relations found",
				},
				"sentiment": {
					Type:   "string",
					Source: "computed",
					LLM:    false,
					Enum:   []string{"positive", "negative", "neutral", "mixed"},
					Description: "Overall sentiment",
				},
				"summary": {
					Type:        "string",
					Source:      "llm",
					LLM:         true,
					Description: "LLM-generated topic summary",
				},
			},
		},
		Sources: SourceSpec{
			Type:  "semantic_search",
			Depth: 2,
		},
		CompileMode: CompileModeHybrid,
	},

	"entity_card": {
		Name:        "entity_card",
		Description: "Compact card for any entity in the graph",
		EntityTypes: []string{},
		Schema: OutputSchema{
			Type: "object",
			Properties: map[string]FieldDef{
				"name": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "Entity name or title",
				},
				"type": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "Entity type",
				},
				"connection_count": {
					Type:        "number",
					Source:      "computed",
					LLM:         false,
					Description: "Number of connections",
				},
				"last_updated": {
					Type:        "string",
					Source:      "metadata",
					LLM:         false,
					Description: "Last update timestamp",
				},
				"key_attributes": {
					Type:    "object",
					Source:  "metadata",
					LLM:     false,
					Description: "Key metadata attributes",
				},
			},
		},
		Sources: SourceSpec{
			Type:  "graph_query",
			Depth: 1,
		},
		CompileMode: CompileModeDeterministic,
	},
}

func GetTemplate(name string) (*CompileTemplate, error) {
	tmpl, ok := BuiltinTemplates[name]
	if !ok {
		return nil, fmt.Errorf("unknown template: %s", name)
	}
	return &tmpl, nil
}

func ListTemplates() []string {
	names := make([]string, 0, len(BuiltinTemplates))
	for name := range BuiltinTemplates {
		names = append(names, name)
	}
	return names
}
