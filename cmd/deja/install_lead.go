package main

import (
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// installLead is what the install says under "deja already knows this
// machine:" before the listing — the questions this machine has asked more
// than once, then the error it keeps hitting. Both come from the store the
// install just built; neither is new data (#3064, #2967).
func installLead(dir string) string {
	return joinNotesWith("\n  ", repeatQuestionsLine(dir), recurringErrorLine(dir))
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
