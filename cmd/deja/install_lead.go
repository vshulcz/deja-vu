package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// installLead is what the install says under "deja already knows this
// machine:" before the listing — the questions this machine has asked more
// than once, then the error it keeps hitting, plus a settled constraint from
// the current project when running inside one (#3064, #2967, #5774).
func installLead(dir string) string {
	return joinNotesWith("\n  ", repeatQuestionsLine(dir), recurringErrorLine(dir), currentProjectRecallLine(dir))
}

func currentProjectRecallLine(dir string) string {
	if !index.HasManifest(dir) {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return ""
	}
	if h := homeDir(); h != "" && filepath.Clean(cwd) == filepath.Clean(h) {
		return ""
	}
	for _, cand := range digest.ProjectNameCandidates(cwd) {
		ss, err := index.SearchWithRecovery(dir, search.Options{Project: cand, All: true}, io.Discard)
		if err != nil || len(ss) == 0 {
			continue
		}
		kept, _ := policyFilterSessionsCounted(policy.ActivationSearch, ss)
		if len(kept) == 0 {
			continue
		}
		var dec string
		for _, s := range kept {
			if d := digest.ResumeFrom(s, nil).Decision; d != "" {
				dec = search.SafeLine(d)
				break
			}
			if rules := digest.ExtractStandingRules(s, nil); len(rules) > 0 && rules[0].Text != "" {
				dec = search.SafeLine(rules[0].Text)
				break
			}
		}
		if dec == "" {
			continue
		}
		if len(dec) > 76 {
			dec = digest.UTF8SafeCut(dec, 76) + "…"
		}
		return fmt.Sprintf("in %s (%d session%s): settled — %s", cand, len(kept), pluralS(len(kept)), dec)
	}
	return ""
}

// repeatQuestionsLine reads the whole store once. That is an install-time
// cost, paid after the build, on the one screen every user reads.
func repeatQuestionsLine(dir string) string {
	if !index.HasManifest(dir) {
		return ""
	}
	ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		return ""
	}
	// The listing below obeys the trust policy; a count over sessions the
	// rule keeps out of recall would contradict it (#937).
	kept, _ := policyFilterSessionsSplit(policy.ActivationSearch, ss)
	example, n := stats.RepeatQuestionExample(kept)
	return repeatQuestionsText(n, example)
}

func repeatQuestionsText(n int, example string) string {
	if n == 0 {
		return ""
	}
	line := fmt.Sprintf("%d question%s asked more than once on this machine", n, pluralS(n))
	if example != "" {
		line += fmt.Sprintf(" — one of them: %q", example)
	}
	return line + " — next time the agent hears the earlier answer first"
}

// joinNotesWith joins the non-empty notes with sep.
func joinNotesWith(sep string, notes ...string) string {
	out := ""
	for _, n := range notes {
		if n == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += n
	}
	return out
}
