package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/sanonone/kektordb/pkg/llm"
)

// printLLMUsage queries a running server for its per-feature LLM cost and
// prints a table. Exits non-zero on failure so it can be used in scripts.
//
// I contatori vivono nel processo server: questo comando non li legge in
// locale, li chiede all'istanza in esecuzione.
func printLLMUsage(baseURL, authToken string) {
	url := baseURL + "/system/llm-usage"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "errore: %v\n", err)
		os.Exit(1)
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "impossibile contattare il server su %s: %v\n", baseURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "il server ha risposto %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var snap llm.UsageSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		fmt.Fprintf(os.Stderr, "risposta non valida: %v\n", err)
		os.Exit(1)
	}

	if len(snap.Features) == 0 {
		fmt.Println("nessuna chiamata LLM registrata (il server non ha ancora usato il modello)")
		return
	}

	// Tabella: la colonna che conta e' il costo per feature, non il totale.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FEATURE\tCALLS\tERRORS\tTOKENS IN\tTOKENS OUT\tTIME (s)")
	fmt.Fprintln(w, "-------\t-----\t------\t---------\t----------\t--------")
	for _, f := range snap.Features {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f\n",
			f.Feature, f.Calls, f.Errors, f.PromptTokens, f.OutputTokens,
			float64(f.TotalMillis)/1000.0)
	}
	fmt.Fprintln(w, "-------\t-----\t------\t---------\t----------\t--------")
	fmt.Fprintf(w, "TOTALE\t%d\t%d\t%d\t%d\t%.1f\n",
		snap.Total.Calls, snap.Total.Errors, snap.Total.PromptTokens,
		snap.Total.OutputTokens, float64(snap.Total.TotalMillis)/1000.0)
	w.Flush()

	if !snap.ProviderTokensReported {
		fmt.Fprintln(os.Stderr, "\nNota: il provider non riporta i token. Le chiamate sono contate,")
		fmt.Fprintln(os.Stderr, "i token no: il costo reale e' ignoto, non zero.")
	} else {
		// Stima indicativa con i prezzi DeepSeek (input $0.15/1M, output $0.60/1M).
		cost := float64(snap.Total.PromptTokens)/1e6*0.15 +
			float64(snap.Total.OutputTokens)/1e6*0.60
		fmt.Fprintf(os.Stderr, "\nCosto stimato (DeepSeek: $0.15/1M in, $0.60/1M out): $%.4f\n", cost)
	}
}
