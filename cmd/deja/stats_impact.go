package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// runStatsImpact prints the measured proof that recall changes outcomes.
// Every line is counted from this machine's usage log; the closing note says
// exactly what was counted so nobody has to take the numbers on faith.
func runStatsImpact(w io.Writer, dir string, jsonOut bool) error {
	r := usage.Impact(dir)
	credits := 0
	// Served is not used. The one signal that tells four helpful injections
	// from four ignored ones is the agent naming deja out loud in a later
	// transcript, and that lives in the index, not in the usage log (#1062).
	// Only pay for the session scan when there is activity to explain.
	if r.Recalls > 0 || r.Injections > 0 {
		if ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, io.Discard); err == nil {
			credits, _ = stats.AgentCredits(ss, time.Now())
		}
	}
	return printImpact(w, r, credits, jsonOut)
}

func printImpact(w io.Writer, r usage.ImpactReport, credits int, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			usage.ImpactReport
			CreditedAloud int `json:"credited_aloud"`
		}{r, credits})
	}
	if r.Recalls == 0 && r.Injections == 0 {
		fmt.Fprintln(w, "deja: no recall activity recorded yet — impact numbers appear once agents start recalling")
		return nil
	}
	fmt.Fprintln(w, "deja impact — measured on this machine, nothing modeled")
	fmt.Fprintf(w, "  recalls served     %d agent-initiated recalls returned matches\n", r.Recalls)
	fmt.Fprintf(w, "  memory at start    %d session starts began with project memory\n", r.Injections)
	if r.RawBytes > 0 && r.ServedBytes > 0 {
		// The frame, the header and the session lines cost more than the text
		// they wrap when sessions are short — which is exactly the state a new
		// user is in when they first run this. Calling that "distilled (0×
		// less)" claims a saving that did not happen, and prints a ratio no
		// one can read (#731).
		ratio := float64(r.RawBytes) / float64(r.ServedBytes)
		switch {
		case ratio >= 2:
			fmt.Fprintf(w, "  context distilled  %s served instead of %s of raw transcripts (%.0f× less)\n",
				humanBytes(int64(r.ServedBytes)), humanBytes(r.RawBytes), ratio)
		case ratio >= 1:
			fmt.Fprintf(w, "  context served     %s from %s of raw transcripts\n",
				humanBytes(int64(r.ServedBytes)), humanBytes(r.RawBytes))
		default:
			fmt.Fprintf(w, "  context served     %s from %s of raw transcripts — short sessions, so the digest frame costs more than the text\n",
				humanBytes(int64(r.ServedBytes)), humanBytes(r.RawBytes))
		}
	}
	if r.ReusedTwice > 0 {
		fmt.Fprintf(w, "  knowledge re-used  %d session%s recalled 2+ times — fixes that keep paying\n", r.ReusedTwice, pluralS(r.ReusedTwice))
	}
	if r.DejaVuMoments > 0 {
		fmt.Fprintf(w, "  déjà vu moments    %d prompts matched work you had already done\n", r.DejaVuMoments)
	}
	served := r.Recalls + r.Injections
	switch {
	case credits > 0:
		fmt.Fprintf(w, "  credited aloud     %d of %d said \"deja-vu recalled\" — memory that was used, not just served\n", credits, served)
	case served > 0:
		fmt.Fprintf(w, "  credited aloud     none of %d yet — served, but no agent has said \"deja-vu recalled\"\n", served)
	}
	fmt.Fprintln(w, "\ncounted: served bytes = digests actually returned to agents; raw bytes =")
	fmt.Fprintln(w, "the source transcripts those digests distilled. `deja log` shows every entry.")
	fmt.Fprintln(w, "for retrieval timing on your own corpus, run `deja bench recall`.")
	return nil
}
