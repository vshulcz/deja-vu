package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
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
	if impactHasActivity(r) {
		if ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, io.Discard); err == nil {
			// Filtered like `deja stats` filters its own report: this count is
			// derived from session text, and every other surface that reads
			// sessions consults the policy first. The lines above it come from
			// this machine's usage log and stay as they are — those are events
			// that happened here, not content from a session the reader is told
			// they cannot see (#1354).
			ss, _ = policyFilterSessionsCounted(policy.ActivationSearch, ss)
			credits, _ = stats.AgentCredits(ss, time.Now())
		}
	}
	return printImpact(w, r, credits, jsonOut)
}

func printImpact(w io.Writer, r usage.ImpactReport, credits int, jsonOut bool) error {
	if jsonOut {
		// Marshalled apart and joined rather than embedded: ImpactReport writes
		// its own JSON (so a window it never opened is absent rather than year
		// 1), and an embedded type's marshaller answers for the whole outer
		// struct — which silently dropped credited_aloud.
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		// Exactly one closing brace, not every trailing one: a cutset would eat
		// the brace of a nested value too, the day this report grows one.
		body := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(string(b), " \n\t"), "}"), " \n\t")
		_, err = fmt.Fprintf(w, "%s,\n  \"credited_aloud\": %d\n}\n", body, credits)
		return err
	}
	if !impactHasActivity(r) {
		fmt.Fprintln(w, "deja: no recall activity recorded yet — impact numbers appear once agents start recalling")
		return nil
	}
	// The window, not a lifetime: the usage log is rewritten past 1MB keeping
	// 14 days, so these counts drop by half when that happens and nothing here
	// said why (#1889). `deja stats` has named its window since #763.
	if r.Since.IsZero() {
		fmt.Fprintln(w, "deja impact — measured on this machine, nothing modeled")
	} else {
		fmt.Fprintf(w, "deja impact — measured on this machine since %s, nothing modeled\n", r.Since.Local().Format("Jan 2"))
	}
	// pluralS on all three counts: n=1 is this screen's commonest state — the
	// first recall on a machine — and it read "1 agent-initiated recalls"
	// (#1652). The verbs are already invariant.
	fmt.Fprintf(w, "  recalls served     %d agent-initiated recall%s returned matches\n", r.Recalls, pluralS(r.Recalls))
	fmt.Fprintf(w, "  memory at start    %d session start%s began with project memory\n", r.Injections, pluralS(r.Injections))
	if r.ServedBytes > 0 && r.RawBytes == 0 {
		// The tool-time line is recorded with no raw size behind it — it is a
		// fact about the store rather than a digest of transcripts — so the
		// ratio block below skipped it and the bytes went unsaid (#2309).
		fmt.Fprintf(w, "  context served     %s, with no raw transcript size recorded behind it\n", humanBytes(int64(r.ServedBytes)))
	}
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
	if r.ToolLines > 0 {
		fmt.Fprintf(w, "  tool-time lines    %d command%s or file%s deja had seen before\n", r.ToolLines, pluralS(r.ToolLines), pluralS(r.ToolLines))
	}
	if r.DejaVuMoments > 0 {
		fmt.Fprintf(w, "  déjà vu moments    %d prompt%s matched work you had already done\n", r.DejaVuMoments, pluralS(r.DejaVuMoments))
	}
	served := r.Recalls + r.Injections + r.DejaVuMoments + r.ToolLines
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

// impactHasActivity says whether this machine has served anything at all.
// The two counters this used to read — recalls and session-start injections —
// are exactly the two a machine running only the prompt hook never increments:
// a per-prompt déjà vu lands in DejaVuMoments and hook-tool under no counter at
// all, both carrying their bytes. So the default install read as "nothing
// recorded" while `deja log`, the stats card and this report's own --json
// listed the injections (#2303).
func impactHasActivity(r usage.ImpactReport) bool {
	return r.Recalls > 0 || r.Injections > 0 || r.DejaVuMoments > 0 || r.ToolLines > 0 || r.ServedBytes > 0
}
