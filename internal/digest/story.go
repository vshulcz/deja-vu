package digest

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Story is how a session went, in the shape a reader skims to tell whether it
// is the one they want: the asks and what came back, the commands, the files.
// It reads the same records the handover does with the same filters; the
// handover keeps the last of each thing, this keeps them all in order.
type Story struct {
	Turns  []Turn
	Runs   []Run
	Edited []string // newest first, throwaway paths left out
}

// Turn is one ask, or the reply that closed it.
type Turn struct {
	User bool
	Text string
}

// Run is one command record and the output that came back under it. Whether
// it failed is the caller's to judge: the exit marker and the error rules live
// in packages above this one.
type Run struct {
	Command string
	Output  string
}

// storyFileCap bounds the edited list; past it a preview is a file listing.
const storyFileCap = 12

// StoryOf reads a session in order. An agent's reply is the last thing it said
// before the next real ask, so the "let me look" lines in between give way to
// what it found; a nudge ("да", "go on") is not an ask and does not cut it.
func StoryOf(s model.Session) Story {
	var st Story
	var files []string
	reply := ""
	flush := func() {
		if reply != "" {
			st.Turns = append(st.Turns, Turn{Text: reply})
			reply = ""
		}
	}
	for _, m := range s.Messages {
		text := strings.TrimSpace(m.Text)
		switch m.Role {
		case "user":
			if worthAsAsk(text) {
				flush()
				st.Turns = append(st.Turns, Turn{User: true, Text: firstSentences(text, 1)})
			}
		case "assistant":
			if text == "" || IsAgentArtifact(text) || IsCompactionSummary(text) || !SaysSomething(text) {
				continue
			}
			reply = firstSentences(text, 1)
		case sources.RoleEdit, sources.RoleWrote:
			// The path is the first line; the rest is the replaced span or
			// the hashes of what was written.
			if f := firstTextLine(text); looksLikeAPath(f) && !throwawayPath(f) {
				files = append(files, f)
			}
		case sources.RoleCommand:
			st.Runs = append(st.Runs, Run{Command: text})
		case sources.RoleToolOutput:
			if n := len(st.Runs); n > 0 && st.Runs[n-1].Output == "" {
				st.Runs[n-1].Output = text
			}
		}
	}
	flush()
	st.Edited = lastDistinct(files, storyFileCap)
	return st
}
