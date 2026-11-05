package mcp

import (
	"strings"
	"testing"
)

// TestProfilesAreOrdered verifica che i profili crescano per inclusione:
// minimal ⊂ standard ⊂ full. Un tool che compare in minimal deve esserci anche
// negli altri, altrimenti il passaggio a un profilo piu' ricco toglierebbe
// capacita' invece di aggiungerne.
func TestProfilesAreOrdered(t *testing.T) {
	check := func(small, big map[string]bool, smallName, bigName string) {
		for tool := range small {
			if !big[tool] {
				t.Errorf("%s ha %q ma %s no: passare a %s perderebbe un tool",
					smallName, tool, bigName, bigName)
			}
		}
	}
	check(ProfileMinimal, ProfileStandard, "minimal", "standard")
	check(ProfileStandard, ProfileFull, "standard", "full")
}

// TestMinimalHasCompleteCycle: il profilo minimo deve permettere il ciclo
// completo (salva, ricorda, profilo, grafo, correggi).
func TestMinimalHasCompleteCycle(t *testing.T) {
	required := map[string]string{
		ToolSaveMemory:         "scrittura",
		ToolRecallMemory:       "lettura base",
		ToolScopedRecall:       "lettura mirata",
		ToolGetUserProfile:     "contesto utente",
		ToolCreateEntity:       "grafo: creare",
		ToolConnectEntities:    "grafo: collegare",
		ToolExploreConnections: "grafo: navigare",
		ToolDeleteMemory:       "correzione",
	}
	for tool, why := range required {
		if !ProfileMinimal[tool] {
			t.Errorf("minimal manca %q (%s)", tool, why)
		}
	}
	if len(ProfileMinimal) > 10 {
		t.Errorf("minimal e' cresciuto a %d tool: il punto era tenerlo piccolo", len(ProfileMinimal))
	}
}

// TestAdminExcludesMemoryTools: un agente amministrativo non deve poter
// scrivere o cercare memorie, e un agente normale non deve poter fare snapshot
// o compattare l'AOF.
func TestAdminExcludesMemoryTools(t *testing.T) {
	memoryTools := []string{ToolSaveMemory, ToolRecallMemory, ToolScopedRecall, ToolCreateEntity}
	for _, tool := range memoryTools {
		if ProfileAdmin[tool] {
			t.Errorf("admin non deve contenere %q: e' un tool di memoria", tool)
		}
	}

	adminOnly := []string{ToolSaveSnapshot, ToolCompactAOF, ToolGetEmbedderStatus, ToolGetPersistenceStatus}
	for _, tool := range adminOnly {
		if ProfileFull[tool] {
			t.Errorf("full non deve contenere %q: e' manutenzione del server", tool)
		}
		if ProfileStandard[tool] {
			t.Errorf("standard non deve contenere %q", tool)
		}
		if ProfileMinimal[tool] {
			t.Errorf("minimal non deve contenere %q", tool)
		}
		if !ProfileAdmin[tool] {
			t.Errorf("admin deve contenere %q", tool)
		}
	}
}

// TestFullIncludesIntrospection: un agente "full" deve poter rispondere a
// domande sul proprio stato e sui propri consumi.
func TestFullIncludesIntrospection(t *testing.T) {
	for _, tool := range []string{ToolGetStats, ToolListIndexes, ToolGetLLMUsage, ToolKVGet, ToolKVSet} {
		if !ProfileFull[tool] {
			t.Errorf("full deve contenere %q (introspezione e stato)", tool)
		}
	}
}

// TestResolveToolsUnknownTokenFailsClosed: un typo in --tools non deve
// registrare TUTTI i tool. Prima tornava nil, che significa "tutti": esporre
// l'intera superficie, admin compresa, per un errore di battitura.
func TestResolveToolsUnknownTokenFailsClosed(t *testing.T) {
	got := ResolveTools("save_memroy") // typo voluto
	if got == nil {
		t.Fatal("un token sconosciuto non deve restituire nil (= tutti i tool)")
	}
	if len(got) != 0 {
		t.Errorf("atteso allowlist vuota, ottenuti %d tool", len(got))
	}
	if shouldRegister(ToolSaveSnapshot, got) {
		t.Error("un typo non deve esporre i tool admin")
	}
}

// TestResolveToolsProfiles verifica che i nomi dei profili risolvano.
func TestResolveToolsProfiles(t *testing.T) {
	for _, name := range []string{"minimal", "standard", "full", "admin", "agent"} {
		got := ResolveTools(name)
		if len(got) == 0 {
			t.Errorf("profilo %q risolto in allowlist vuota", name)
		}
	}
}

// TestResolveToolsMixed: profilo + tool singolo, come --tools=minimal,kv_get.
func TestResolveToolsMixed(t *testing.T) {
	got := ResolveTools("minimal,kv_get")
	if !got[ToolSaveMemory] {
		t.Error("il profilo minimal non e' stato applicato")
	}
	if !got[ToolKVGet] {
		t.Error("il tool esplicito kv_get non e' stato aggiunto")
	}
	if !strings.Contains("minimal,kv_get", "kv_get") {
		t.Fatal("sanita' del test")
	}
}
