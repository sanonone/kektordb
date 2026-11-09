package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sanonone/kektordb/internal/server"
)

// kektordb status — one screen that answers "is this working?".
//
// The server exposes the same picture at /system/status; this command does not
// open the database, it asks the running instance. That matters: opening the
// engine would take the file lock, and a diagnostic command must not disturb the
// process it is diagnosing.
//
// Exit codes: 0 healthy, 1 warnings present, 2 server unreachable. The codes
// make it usable from a script or a shell prompt.

const (
	statusExitOK       = 0
	statusExitWarnings = 1
	statusExitNoServer = 2
)

// statusOptions carries what the flags produced.
type statusOptions struct {
	baseURL   string
	authToken string
	asJSON    bool
}

// runStatus fetches and prints the status. Returns the process exit code.
func runStatus(opts statusOptions) int {
	st, err := fetchStatus(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "impossibile leggere lo stato da %s: %v\n", opts.baseURL, err)
		fmt.Fprintf(os.Stderr, "il server e' in esecuzione? avvialo con: kektordb\n")
		return statusExitNoServer
	}

	if opts.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			fmt.Fprintf(os.Stderr, "errore di scrittura: %v\n", err)
			return statusExitNoServer
		}
	} else {
		printStatus(os.Stdout, st, opts.baseURL)
	}

	if len(st.Warnings) > 0 {
		return statusExitWarnings
	}
	return statusExitOK
}

// fetchStatus performs the request and decodes the response.
func fetchStatus(opts statusOptions) (server.SystemStatus, error) {
	var st server.SystemStatus

	req, err := http.NewRequest("GET", strings.TrimSuffix(opts.baseURL, "/")+"/system/status", nil)
	if err != nil {
		return st, err
	}
	if opts.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+opts.authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return st, fmt.Errorf("il server ha risposto %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return st, fmt.Errorf("risposta non valida: %w", err)
	}
	return st, nil
}

// printStatus writes the human view.
//
// Layout rules, all of them about readability rather than decoration:
//   - only sections that have something to say appear;
//   - numbers are grouped (8.412, not 8412) and sized (12.4 MB, not 12999 kB);
//   - percentages appear only when the share is meaningful;
//   - ATTENZIONE comes last, and only when non-empty.
func printStatus(w io.Writer, st server.SystemStatus, baseURL string) {
	printHeader(w, st, baseURL)
	printMemory(w, st.Memory)
	printMaintenance(w, st.Maintenance)
	printLLM(w, st.LLM)
	printHealth(w, st.Health)
	printWarnings(w, st.Warnings)
	printErrors(w, st.Errors)
}

func printHeader(w io.Writer, st server.SystemStatus, baseURL string) {
	uptime := formatDuration(time.Duration(st.UptimeSeconds) * time.Second)
	fmt.Fprintf(w, "KektorDB · %s · %s · attivo da %s\n", st.Version, baseURL, uptime)
	fmt.Fprintln(w)
}

func printMemory(w io.Writer, m *server.StatusMemory) {
	if m == nil {
		return
	}
	fmt.Fprintln(w, "  MEMORIA")
	fmt.Fprintf(w, "    %s indici · %s memorie\n",
		formatCount(len(m.Indexes)), formatCount(m.TotalVectors))

	for _, idx := range m.Indexes {
		line := fmt.Sprintf("    %-14s %s", idx.Name, formatCount(idx.Vectors))
		if len(idx.Layers) > 0 {
			line += "   " + formatLayers(idx.Layers)
		}
		fmt.Fprintln(w, line)
	}

	if m.Graph.Edges > 0 {
		fmt.Fprintf(w, "    grafo: %s archi · %s nodi collegati · %s pinnati\n",
			formatCount(m.Graph.Edges), formatCount(m.Graph.NodesWithLink), formatCount(m.Graph.Pinned))
	}
	fmt.Fprintln(w)
}

func printMaintenance(w io.Writer, mt *server.StatusMaint) {
	if mt == nil {
		return
	}
	if !mt.Enabled {
		fmt.Fprintln(w, "  MANUTENZIONE          spenta")
		fmt.Fprintln(w)
		return
	}

	last := "mai eseguito"
	if mt.LastCycleAgoSecond > 0 {
		last = "ultimo ciclo " + formatAgo(time.Duration(mt.LastCycleAgoSecond)*time.Second)
	} else if mt.LastCycleAgoSecond == 0 {
		last = "ultimo ciclo ora"
	}
	fmt.Fprintf(w, "  MANUTENZIONE          attiva · %s · ogni %s · %s\n",
		mt.Mode, mt.Interval, last)

	for _, f := range mt.Features {
		state := "attivo "
		if !f.Enabled {
			state = "spento "
		}
		fmt.Fprintf(w, "    %-18s %s  %s\n", f.Name, state, f.Detail)
	}
	fmt.Fprintln(w)
}

func printLLM(w io.Writer, l *server.StatusLLM) {
	if l == nil {
		return
	}
	if !l.Configured {
		fmt.Fprintln(w, "  COSTO LLM             nessun modello configurato")
		fmt.Fprintln(w)
		return
	}

	header := "  COSTO LLM             " + l.Provider
	if l.Model != "" {
		header += " · " + l.Model
	}
	fmt.Fprintln(w, header)

	if l.Total.Calls == 0 {
		fmt.Fprintln(w, "    nessuna chiamata registrata")
		fmt.Fprintln(w)
		return
	}

	tokens := "token non riportati dal provider"
	if l.TokensReported {
		tokens = fmt.Sprintf("%s token in · %s out",
			formatCount64(int(l.Total.PromptTokens)), formatCount64(int(l.Total.OutputTokens)))
	}
	fmt.Fprintf(w, "    %s chiamate · %s\n", formatCount64(int(l.Total.Calls)), tokens)

	// Only features that actually spent something, biggest first.
	entries := l.Features
	sort.Slice(entries, func(i, j int) bool { return entries[i].Calls > entries[j].Calls })
	for _, f := range entries {
		if f.Calls == 0 {
			continue
		}
		share := float64(f.Calls) / float64(l.Total.Calls) * 100
		// A share below 10% is noise in a one-screen view.
		if share < 10 && len(entries) > 1 {
			fmt.Fprintf(w, "    %-18s %s\n", shortFeature(f.Feature), formatCount64(int(f.Calls)))
			continue
		}
		fmt.Fprintf(w, "    %-18s %s  (%.0f%%)\n",
			shortFeature(f.Feature), formatCount64(int(f.Calls)), share)
	}
	fmt.Fprintln(w)
}

func printHealth(w io.Writer, h *server.StatusHealth) {
	if h == nil {
		return
	}
	fmt.Fprintln(w, "  SALUTE")

	if p := h.Persistence; p != nil {
		snapshot := "mai salvato"
		if p.EverSnapshotted {
			snapshot = "ultimo snapshot " + formatAgo(time.Duration(p.LastSnapshotAgoS)*time.Second)
		}
		fmt.Fprintf(w, "    persistenza    %-4s  AOF %s · %s\n",
			p.Status, formatBytes(p.AOFSizeMB), snapshot)
	}

	if e := h.Embedder; e != nil {
		state := e.Active
		if state == "" {
			state = "nessuno"
		}
		detail := ""
		if e.Model != "" {
			detail = " · " + e.Model
		}
		if e.Dimension > 0 {
			detail += fmt.Sprintf(" · %d dimensioni", e.Dimension)
		}
		fmt.Fprintf(w, "    embedder       ok    %s%s\n", state, detail)
	}
	fmt.Fprintln(w)
}

func printWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(w, "  ATTENZIONE")
	for _, warn := range warnings {
		fmt.Fprintf(w, "    ! %s\n", warn)
	}
	fmt.Fprintln(w)
}

func printErrors(w io.Writer, failures []server.StatusFailure) {
	if len(failures) == 0 {
		return
	}
	fmt.Fprintln(w, "  SEZIONI NON DISPONIBILI")
	for _, f := range failures {
		fmt.Fprintf(w, "    ! %s: %s\n", f.Section, f.Error)
	}
	fmt.Fprintln(w)
}

// --- formatting helpers ---
//
// Kept here rather than importing a library: a handful of small functions with
// tests beats a dependency for five formats.

// formatCount groups thousands with a dot, the convention the rest of the
// output uses.
func formatCount(n int) string {
	return formatThousands(n)
}

func formatCount64(n int) string {
	return formatThousands(n)
}

func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// formatBytes takes megabytes and picks a readable unit.
func formatBytes(mb float64) string {
	switch {
	case mb >= 1024:
		return fmt.Sprintf("%.1f GB", mb/1024)
	case mb >= 1:
		return fmt.Sprintf("%.1f MB", mb)
	default:
		return fmt.Sprintf("%.0f KB", mb*1024)
	}
}

// formatAgo renders a duration the way a person reads it.
func formatAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds fa", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm fa", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh fa", int(d.Hours()))
	default:
		return fmt.Sprintf("%dg fa", int(d.Hours()/24))
	}
}

// formatDuration renders an uptime.
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dg %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// formatLayers renders the per-layer breakdown compactly.
func formatLayers(layers map[string]int) string {
	order := []string{"episodic", "semantic", "procedural"}
	var parts []string
	seen := make(map[string]bool)
	for _, name := range order {
		if n, ok := layers[name]; ok {
			parts = append(parts, fmt.Sprintf("%s %s", name, formatCount(n)))
			seen[name] = true
		}
	}
	// Any layer outside the three well-known ones, alphabetically.
	var extra []string
	for name := range layers {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		parts = append(parts, fmt.Sprintf("%s %s", name, formatCount(layers[name])))
	}
	return strings.Join(parts, " / ")
}

// shortFeature trims the "gardener_" prefix, which every feature shares and
// which therefore carries no information.
func shortFeature(name string) string {
	return strings.TrimPrefix(name, "gardener_")
}

// cmdStatus parses its own flags and runs the status view.
//
// A separate FlagSet keeps the command independent from the server flags: a user
// typing `kektordb status --http-addr :9091` should get a clear answer, not a
// silent acceptance of a flag that does nothing here.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	opts := statusOptions{}
	fs.StringVar(&opts.baseURL, "url", "http://127.0.0.1:9091", "base URL of the running server")
	fs.StringVar(&opts.authToken, "auth-token", "", "API auth token, if configured")
	fs.BoolVar(&opts.asJSON, "json", false, "output JSON instead of the human view")
	if err := fs.Parse(args); err != nil {
		return statusExitNoServer
	}
	return runStatus(opts)
}
