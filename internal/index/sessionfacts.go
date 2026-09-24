package index

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A recall hit says what a session talked about, and then the reader runs
// something to find out whether it is true — correctly, because prose is a
// claim. What the session ran is recorded, with the exit status the harness
// appended, and "exit 0 here" is not a claim.
//
// Per session rather than aggregated, so commands.gob cannot answer it: that
// table is about what recurs across the store, and this is about the one
// session in front of the reader. The files are already in the manifest
// (SessionMeta.Touched), so they are not repeated here.
//
// The record log holds all of it already, but only a full scan can find one
// session's records in it, which costs seconds — too slow for a surface that
// answers in milliseconds. So the durable part is computed once at build, the
// way the command tables are.
const (
	sessionFactsFile = "sessionfacts.gob"
	// What one session's entry may carry. A session that ran eighty commands
	// is a marathon, and the last few are the ones that worked; the rest is
	// bytes in every index and lines in an answer nobody reads.
	sessionFactsCommands = 4
	sessionFactsTextMax  = 200
	// sessionFactsMax bounds the table on a store with a very long tail.
	sessionFactsMax = 40000
)

// SessionFact is what one session ran.
type SessionFact struct {
	// Commands are the commands it ran, most recently first, with the outcome
	// the transcript recorded.
	Commands []SessionCommand `json:",omitempty"`
}

// SessionCommand is one command a session ran and what the transcript said
// happened to it.
type SessionCommand struct {
	Text string
	// Exit is the status the harness appended, and Known says whether it did:
	// most harnesses record one, and a command with no status is evidence of
	// nothing either way. The pair is what lets an answer say "passed here"
	// rather than "was run here".
	Exit  int  `json:",omitempty"`
	Known bool `json:",omitempty"`
}

// Passed reports a command the transcript saw finish cleanly.
func (c SessionCommand) Passed() bool { return c.Known && c.Exit == 0 }

func sessionFactsPath(dir string) string { return filepath.Join(dir, sessionFactsFile) }

// buildSessionFacts writes the per-session table from the sessions a full build
// holds. Failures are swallowed: it is an extra, never a reason to fail a build.
func buildSessionFacts(tmp string, ss []model.Session) {
	out := make(map[string]SessionFact, len(ss))
	for _, s := range ss {
		if f, ok := sessionFactOf(s.Messages); ok {
			out[s.Harness+":"+s.ID] = f
		}
		if len(out) >= sessionFactsMax {
			break
		}
	}
	if len(out) == 0 {
		return
	}
	_ = writeGob(sessionFactsPath(tmp), out)
}

// buildSessionFactsFromIndex is the same from the records an index already
// holds. Unlike the aggregate tables this one could be merged per session, but
// reading it back out of the records is exact and shorter: by this point the
// records in tmp are the whole corpus.
func buildSessionFactsFromIndex(tmp string) {
	byKey := map[string][]model.Message{}
	if err := EachRecordOfRole(tmp, roleCommand, func(meta SessionMeta, r Record) {
		byKey[r.Key] = append(byKey[r.Key], model.Message{Role: roleCommand, Text: r.Text, Time: r.Time})
	}); err != nil {
		return
	}
	out := make(map[string]SessionFact, len(byKey))
	for key, msgs := range byKey {
		// The walk arrives in record order rather than in time order, and the
		// commands are kept most recent first.
		sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Time.Before(msgs[j].Time) })
		if f, ok := sessionFactOf(msgs); ok {
			out[key] = f
		}
	}
	if len(out) == 0 {
		return
	}
	_ = writeGob(sessionFactsPath(tmp), out)
}

// sessionFactOf reduces one session's messages to what a reader can act on.
func sessionFactOf(msgs []model.Message) (SessionFact, bool) {
	var f SessionFact
	seenCmd := map[string]bool{}
	// Backwards: the file a session ended in and the command that finally ran
	// are the ones worth handing back, and a long session's opening moves are
	// usually the ones it abandoned.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		switch m.Role {
		case roleCommand:
			if len(f.Commands) >= sessionFactsCommands {
				continue
			}
			// The marker a source appends sits at the end of the record, which
			// for a one-line command record is the end of the line — but a
			// record carrying the command and its output is the same shape with
			// the marker further down, and reading only the first line called
			// every such command's outcome unknown.
			body, code, recorded := CommandExitOutcome(strings.TrimRight(m.Text, " \t\r\n"))
			line := strings.TrimSpace(firstTextLine(body))
			cmd := withoutExitStatus(line)
			if cmd == "" || len(cmd) > sessionFactsTextMax {
				continue
			}
			key := normalizeCommand(cmd)
			if key == "" || seenCmd[key] {
				continue
			}
			seenCmd[key] = true
			sc := SessionCommand{Text: cmd}
			if recorded {
				sc.Exit, sc.Known = code, true
			} else if c, ok := CommandExitStatus(line); ok {
				sc.Exit, sc.Known = c, true
			}
			f.Commands = append(f.Commands, sc)
		}
	}
	if len(f.Commands) == 0 {
		return SessionFact{}, false
	}
	return f, true
}

// ReadSessionFacts reads the table, or nil when this index was built before it
// existed — every caller treats a missing entry as "nothing to add".
func ReadSessionFacts(dir string) map[string]SessionFact {
	var out map[string]SessionFact
	if err := readGob(sessionFactsPath(dir), &out); err != nil {
		return nil
	}
	return out
}

// SessionFactOf is the entry for one session, if the table has one.
func SessionFactOf(dir, harness, id string) (SessionFact, bool) {
	facts := ReadSessionFacts(dir)
	if facts == nil {
		return SessionFact{}, false
	}
	f, ok := facts[harness+":"+id]
	return f, ok
}

// BuildSessionFactsForTest writes the table from sessions in memory, for tests
// in other packages that need one entry rather than a whole index.
func BuildSessionFactsForTest(dir string, ss []model.Session) { buildSessionFacts(dir, ss) }
