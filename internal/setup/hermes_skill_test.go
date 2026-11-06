package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/internal/mcp"
)

// TestHermesSkillHasFrontmatter guards the agentskills.io contract.
//
// Hermes loads skills through the open Agent Skills standard, which requires
// YAML frontmatter with `name` and `description`. Without it the skill fails
// validation and its metadata is missing from the discovery index, so the
// agent cannot see the skill at all.
func TestHermesSkillHasFrontmatter(t *testing.T) {
	body := readHermesSkill(t)

	if !strings.HasPrefix(body, "---\n") {
		t.Fatalf("SKILL.md does not start with YAML frontmatter (---):\n%s", firstLines(body, 5))
	}
	rest := strings.TrimPrefix(body, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		t.Fatal("SKILL.md frontmatter is not terminated with ---")
	}
	front := rest[:end]

	for _, field := range []string{"name:", "description:"} {
		if !strings.Contains(front, field) {
			t.Errorf("frontmatter is missing required field %q", strings.TrimSuffix(field, ":"))
		}
	}

	// The spec requires name to match the parent directory.
	dir := filepath.Base(hermesSkillDir())
	name := frontmatterValue(front, "name")
	if name != dir {
		t.Errorf("frontmatter name = %q, want it to match the skill directory %q", name, dir)
	}
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
		t.Errorf("name %q must be lowercase letters, digits and hyphens", name)
	}
}

// TestHermesSkillNamesRealTools fails when the skill advertises a tool that the
// MCP server does not register. An agent that reads the skill tries the tool,
// gets "unknown tool", and burns turns on a failure the docs caused.
func TestHermesSkillNamesRealTools(t *testing.T) {
	body := readHermesSkill(t)
	real := mcp.AllToolNames()

	// Backticked tokens that look like tool names: snake_case, no spaces.
	re := regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")
	seen := map[string]bool{}
	var missing []string

	for _, match := range re.FindAllStringSubmatch(body, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if real[name] {
			continue
		}
		// Relation types and parameter names are also backticked and are not
		// tools. Skip anything the server would not accept as a tool name.
		if isRelationType(name) || isParameterName(name) {
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("SKILL.md advertises tools that do not exist:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestHermesSkillNoInventedPrefixes catches category prefixes presented as if
// they were tool namespaces.
//
// The skill used to say "graph_ — temporal knowledge graph queries" and
// "people_ — identity management". No tool has those prefixes: the agent looks
// for graph_traverse, finds nothing, and concludes the server is broken.
func TestHermesSkillNoInventedPrefixes(t *testing.T) {
	body := readHermesSkill(t)

	// A token like `graph_` (trailing underscore, no suffix) claims a prefix
	// family. Every tool carrying that prefix must exist.
	re := regexp.MustCompile("`([a-z][a-z0-9]*_)`")
	real := mcp.AllToolNames()

	for _, match := range re.FindAllStringSubmatch(body, -1) {
		prefix := match[1]
		var matches int
		for name := range real {
			if strings.HasPrefix(name, prefix) {
				matches++
			}
		}
		if matches == 0 {
			t.Errorf("SKILL.md advertises the tool prefix %q, but no registered tool starts with it", prefix)
		}
	}
}

// TestHermesSkillParametersExist checks the documented argument names against
// the real schema: the skill tells the agent which fields to pass.
func TestHermesSkillParametersExist(t *testing.T) {
	body := readHermesSkill(t)

	// Arguments of save_memory, per internal/mcp/types.go SaveMemoryArgs.
	realParams := map[string]bool{
		"content": true, "index_name": true, "layer": true, "links": true,
		"tags": true, "pin": true, "session_id": true, "user_id": true,
	}

	for _, param := range []string{"index", "related"} {
		if !strings.Contains(body, "**"+param+"**") {
			continue
		}
		if !realParams[param] {
			t.Errorf("SKILL.md documents parameter %q, which save_memory does not accept", param)
		}
	}

	// The default index is mcp_memory, not main.
	if strings.Contains(body, "default index is `main`") {
		t.Error("SKILL.md claims the default index is `main`; the server defaults to `mcp_memory`")
	}
}

func readHermesSkill(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("plugins", "hermes", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	return string(data)
}

func frontmatterValue(front, key string) string {
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// isRelationType reports whether name is a graph relation type documented in
// the skill rather than a tool. The server accepts any relation string, so a
// missing one is not a bug.
func isRelationType(name string) bool {
	switch name {
	case "before", "after", "similar_to", "caused_by", "depends_on", "part_of",
		"mentions", "related_to", "author_of":
		return true
	}
	return false
}

// isParameterName reports whether name is an argument name or a literal value
// used in examples, not a tool.
func isParameterName(name string) bool {
	switch name {
	case "index_name", "session_id", "user_id", "memory_layer", "hard_delete",
		"mcp_memory": // default index name
		return true
	}
	return false
}
