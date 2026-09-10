package digest

import (
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A compaction is where a session's working state goes, and it is the most
// expensive moment deja can see.
//
// Measured on this machine's transcripts: a cold session takes a median of 10
// actions before its first edit, and a session takes 28 after a compaction —
// p75 61 — across 86 compactions. Nothing is missing from the store when that
// happens; what is missing is the shape. So this reads the transcript up to a
// point and answers the four questions the next turn starts by re-deriving.
//
// Everything here is derived. deja has two places an agent can write and both
// are nearly empty — 9 writes against 3793 injections over sixteen days, 2
// accepted notes after months — so nothing is asked of the agent.
//
// Counted over 96 real compactions, the material is there: what was asked in
// 94% of them, the last command in 94%, a decision the session had reached in
// 94%, and a file in flight in 76%.
type Resume struct {
	// Asked is the last thing the person asked for.
	Asked string
	// Files are the ones being worked on, newest first. A compaction on this
	// machine had a median of 38 edited files behind it and 94 at the p90, so
	// this is a ranking rather than a list.
	Files []string
	// Command is the last command run, and Failed whether it ended badly.
	Command string
	Failed  bool
	// Decision is what the session had settled, in its own words.
	Decision string
}

// resumeFileCap is how many files in flight are worth naming. Beyond a handful
// the line stops being a handover and becomes the directory listing the agent
// would have got anyway.
const resumeFileCap = 5

// ResumeFrom builds the handover from the messages of a session, reading them
// in order and keeping the last of each thing. It never looks at what a harness
// wrote: a compaction summary is not what was asked, and a tool echo is not a
// decision.
//
// readsAsFailure decides whether the output under a command says it went wrong.
// It is the caller's to supply because the rules for that live in the store
// package, which sits above this one — and a nil one means the handover reports
// what ran without judging it.
func ResumeFrom(s model.Session, readsAsFailure func(output string) bool) Resume {
	var r Resume
	var files []string
	for _, m := range s.Messages {
		text := strings.TrimSpace(m.Text)
		switch m.Role {
		case "user":
			if !worthAsAsk(text) {
				continue
			}
			r.Asked = firstSentences(text, 2)
		case "assistant":
			if text == "" || IsAgentArtifact(text) {
				continue
			}
			if worthAsDecision(text) {
				r.Decision = firstSentences(text, 1)
			}
		case sources.RoleEdit, sources.RoleFiles:
			for _, f := range strings.Fields(text) {
				if f = strings.TrimSpace(f); f != "" {
					files = append(files, f)
				}
			}
		case sources.RoleCommand:
			if line := firstTextLine(text); line != "" {
				r.Command, r.Failed = line, false
			}
		case sources.RoleToolOutput:
			if r.Command != "" && readsAsFailure != nil && readsAsFailure(text) {
				r.Failed = true
			}
		}
	}
	r.Files = newestFirst(files)
	return r
}

// worthAsAsk reports whether a user turn is the work rather than a nudge.
//
// Read over 96 real compactions, the last thing said before one is as often
// "да", "давай дальше" or "/compact" as it is the task — so taking the newest
// user turn reported `working on: да`. A pasted log is the same problem from the
// other end: it was a 4 KB HAR file offered as what the session was doing.
func worthAsAsk(text string) bool {
	if text == "" || IsAgentArtifact(text) || IsCompactionSummary(text) {
		return false
	}
	if strings.HasPrefix(text, "/") {
		// A slash command is the host's, whatever it says.
		return false
	}
	if utf8.RuneCountInString(text) < askMinRunes || utf8.RuneCountInString(text) > askMaxRunes {
		return false
	}
	if strings.Contains(text, `":`) && strings.Contains(text, "{") {
		// A pasted document, not a sentence.
		return false
	}
	low := strings.ToLower(strings.TrimRight(text, " .!…"))
	for _, nudge := range nudgeWords {
		if low == nudge || strings.HasPrefix(low, nudge+" ") && utf8.RuneCountInString(low) < askMinRunes+10 {
			return false
		}
	}
	return true
}

const (
	// askMinRunes is how much has to be said for a turn to be the task. Under
	// this it is an acknowledgement.
	askMinRunes = 20
	// askMaxRunes keeps a pasted document out.
	askMaxRunes = 600
)

// nudgeWords are the ways a reader says "carry on", which is not a task.
var nudgeWords = []string{
	"да", "нет", "ок", "окей", "давай", "давай дальше", "дальше", "продолжай",
	"продолжаем", "поехали", "угу", "ага", "спасибо",
	"ok", "okay", "yes", "no", "go", "go on", "continue", "carry on", "thanks",
	"sure", "do it", "please do",
}

// aboutToExplain reports whether the line is an agent announcing an answer
// rather than stating one. Measured on the same 96 compactions: "Хорошо,
// отвечаю по существу — как работает routepilot" carried a decision marker and
// is a preamble to one.
func aboutToExplain(text string) bool {
	low := strings.ToLower(text)
	if utf8.RuneCountInString(low) > 200 {
		low = string([]rune(low)[:200])
	}
	for _, p := range []string{
		"отвечаю", "расскажу", "объясню", "сейчас посмотрю", "давай разберём",
		"let me explain", "i'll explain", "here is how", "here's how",
		"let me look", "i'll look",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// worthAsDecision reports whether a line is something the session settled.
//
// Read across 32 projects on a real store, what the weaker rule let through was:
// "Imported from Claude session <id>", which is deja's own marker on a synced
// session; "Затащил." — merged it, one word; and a pasted design document whose
// first line is a markdown heading. Each carried a decision marker and none is a
// decision.
func worthAsDecision(text string) bool {
	if !CarriesDecision(text) || aboutToExplain(text) {
		return false
	}
	if strings.HasPrefix(text, "Imported from ") {
		return false
	}
	// A heading or a bullet opens a document, not a sentence somebody said.
	if strings.HasPrefix(text, "#") || strings.HasPrefix(text, "- ") ||
		strings.HasPrefix(text, "* ") || strings.HasPrefix(text, "|") {
		return false
	}
	// Long enough to mean something on its own, read cold, weeks later.
	return utf8.RuneCountInString(firstSentences(text, 1)) >= decisionMinRunes
}

// decisionMinRunes is the bar for a decision worth repeating back. "Затащил."
// clears every marker test and says nothing a week later.
const decisionMinRunes = 20

// newestFirst keeps the last few distinct paths in the order they were last
// touched, newest first — which is the order an agent picking the work back up
// cares about.
func newestFirst(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for i := len(files) - 1; i >= 0 && len(out) < resumeFileCap; i-- {
		f := files[i]
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// firstTextLine is the first non-empty line, which is the command itself where
// a record carries its output under it.
func firstTextLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// Empty reports whether there is nothing to hand over.
func (r Resume) Empty() bool {
	return r.Asked == "" && r.Decision == "" && r.Command == "" && len(r.Files) == 0
}

// Lines renders the handover, one fact per line and nothing deja cannot say
// from the transcript. Order is what the next turn needs first: the task, what
// was settled, where the work is, what was last run.
func (r Resume) Lines() []string {
	var out []string
	if r.Asked != "" {
		out = append(out, "working on: "+r.Asked)
	}
	if r.Decision != "" {
		out = append(out, "settled: "+r.Decision)
	}
	if len(r.Files) > 0 {
		out = append(out, "files in flight: "+strings.Join(r.Files, ", "))
	}
	if r.Command != "" {
		if r.Failed {
			out = append(out, "last command failed: "+r.Command)
		} else {
			out = append(out, "last command: "+r.Command)
		}
	}
	return out
}
