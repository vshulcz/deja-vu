package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// What a compaction actually costs, measured on 58 transcripts and 43
// compaction events (#543): 2.59 GB of conversation replaced by 0.72 MB of
// summary, and the loss is lopsided.
//
//	decision sentences   ~77% survive
//	file paths            9.8%
//	commands that ran     0.2%
//	measured numbers      1.9%
//
// The compactor is good at what it was built for. The summary remembers what
// was decided and forgets what the decision rested on — the command that proved
// it, the file it was in, the number that made it right.
//
// That material is exactly what the index started keeping in 0.16.4, and the
// session it belongs to is the one being compacted. So after a compaction the
// hook hands the session back its own evidence rather than generic recall.
const (
	// compactEvidenceFiles and compactEvidenceCommands bound the block. It is
	// injected into a context window that was just emptied on purpose; taking a
	// meaningful bite out of it again would be its own kind of loss.
	compactEvidenceFiles    = 8
	compactEvidenceCommands = 8
	// compactCommandMax is the length past which a command is a pasted script
	// rather than something worth handing back.
	compactCommandMax = 120
)

// compactEvidence describes what a session did before it was compacted, or ""
// when the session is not in the index yet or recorded nothing worth saying.
func compactEvidence(dir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	s, ok, err := index.FindByID(dir, sessionID)
	if err != nil || !ok {
		return ""
	}
	// Both lists come from the session's own records, and a record can be a path
	// or a command the parser lifted out of a tool call — whatever the harness
	// wrote into the payload, escape bytes included (#2000).
	files := lastDistinct(s.Messages, "files", compactEvidenceFiles,
		func(text string) []string { return strings.Split(text, "\n") },
		func(p string) string { return search.SafeLine(trimPath(p)) })
	commands := lastDistinct(s.Messages, "command", compactEvidenceCommands,
		func(text string) []string {
			// A multi-line command is a heredoc or a pasted script. Truncated to
			// one line it says nothing, and whole it would swallow the block.
			if strings.Contains(text, "\n") || len(text) > compactCommandMax {
				return nil
			}
			return []string{text}
		}, search.SafeLine)
	if len(files) == 0 && len(commands) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("What this session did before the compaction, from deja's index — " +
		"a summary keeps conclusions and drops the specifics they rest on:\n")
	if len(files) > 0 {
		fmt.Fprintf(&b, "  files it touched: %s\n", strings.Join(files, ", "))
	}
	if len(commands) > 0 {
		b.WriteString("  commands it ran:\n")
		for _, c := range commands {
			fmt.Fprintf(&b, "    %s\n", c)
		}
	}
	return b.String()
}

// lastDistinct collects the most recent distinct entries of one role, newest
// first. Recency rather than frequency: after a compaction the useful thing is
// what the session was doing when the window filled, not what it did an hour
// earlier.
func lastDistinct(ms []model.Message, role string, limit int, split func(string) []string, format func(string) string) []string {
	seen := map[string]bool{}
	var out []string
	for i := len(ms) - 1; i >= 0 && len(out) < limit; i-- {
		if ms[i].Role != role {
			continue
		}
		for _, p := range split(ms[i].Text) {
			p = strings.TrimSpace(p)
			if p == "" || isScratch(p) {
				continue
			}
			// Distinct as printed, not as stored: format trims a path to its
			// last segments and strips control bytes, so two records that
			// differ only in what it removes are one row, printed twice.
			f := format(p)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
