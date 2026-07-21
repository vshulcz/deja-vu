package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// runStatsImpact prints the measured proof that recall changes outcomes.
// Every line is counted from this machine's usage log; the closing note says
// exactly what was counted so nobody has to take the numbers on faith.
func runStatsImpact(w io.Writer, dir string, jsonOut bool) error {
	r := usage.Impact(dir)
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	if r.Recalls == 0 && r.Injections == 0 {
		fmt.Fprintln(w, "deja: no recall activity recorded yet — impact numbers appear once agents start recalling")
		return nil
	}
	fmt.Fprintln(w, "deja impact — measured on this machine, nothing modeled")
	fmt.Fprintf(w, "  recalls served     %d agent-initiated recalls returned matches\n", r.Recalls)
	fmt.Fprintf(w, "  memory at start    %d session starts began with project memory\n", r.Injections)
	if r.RawBytes > 0 && r.ServedBytes > 0 {
		ratio := float64(r.RawBytes) / float64(r.ServedBytes)
		fmt.Fprintf(w, "  context distilled  %s served instead of %s of raw transcripts (%.0f× less)\n",
			humanBytes(int64(r.ServedBytes)), humanBytes(r.RawBytes), ratio)
	}
	if r.ReusedTwice > 0 {
		fmt.Fprintf(w, "  knowledge re-used  %d sessions recalled 2+ times — fixes that keep paying\n", r.ReusedTwice)
	}
	if r.DejaVuMoments > 0 {
		fmt.Fprintf(w, "  déjà vu moments    %d prompts matched work you had already done\n", r.DejaVuMoments)
	}
	fmt.Fprintln(w, "\ncounted: served bytes = digests actually returned to agents; raw bytes =")
	fmt.Fprintln(w, "the source transcripts those digests distilled. `deja log` shows every entry.")
	fmt.Fprintln(w, "for retrieval timing on your own corpus, run `deja bench recall`.")
	return nil
}
