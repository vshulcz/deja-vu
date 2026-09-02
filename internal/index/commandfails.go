package index

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	commandFailsFile = "commandfails.gob"
	// commandFailMinSessions is the bar a warning has to clear. The same as
	// the command table's own: one session hitting something is an anecdote,
	// two is a property of this machine.
	commandFailMinSessions = 2
	// commandFailsMax bounds the table. It is read whole on the per-action
	// hook, which fires before every tool call an agent makes.
	commandFailsMax = 500
	// commandFailHeadWords is how much of a command a warning is about: the
	// program, its subcommand, and the first argument that is not a flag.
	//
	// Looser than CommandHistory's whole-line match on purpose. That rule is
	// what keeps deja from *endorsing* a command it only half recognises —
	// "git status\nrm -rf /" must never be answered as "you have run this".
	// A warning is the other direction: it recommends nothing, and the cost of
	// being wrong is a line the reader ignores rather than a command they run.
	// Measured on a real store, the whole-line key confirms 25 command-failure
	// pairs and the head confirms 45 (#2924).
	commandFailHeadWords = 3
)

func commandFailsPath(dir string) string { return filepath.Join(dir, commandFailsFile) }

// CommandFailure is what a command did to this machine last time, for the hook
// that fires before it runs again.
type CommandFailure struct {
	// Head is the command shape this is about, normalised.
	Head string
	// Line is the friction the run ended with, and Sessions how many separate
	// sessions saw this command end that way.
	Line     string
	Sessions int
	// Project is one of the projects it happened in, for the trust policy.
	Project string
}

// CommandHead is the part of a command a warning is about: the program, its
// subcommand and the first argument that is not a flag. Empty when there is no
// such shape — a bare `cd`, an assignment, an empty line.
func CommandHead(cmd string) string {
	cmd = strings.TrimSpace(withoutExitStatus(strings.TrimSpace(firstTextLine(cmd))))
	// A stored command carries the prompt marker the transcript wrote it with;
	// the one the hook is asked about does not.
	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "$ "))
	// Only the first command of a compound: what follows ran in a state this
	// one made, and a warning about it would be about a different situation.
	if i := strings.IndexAny(cmd, "|;&"); i > 0 {
		cmd = cmd[:i]
	}
	fields := strings.Fields(strings.ToLower(cmd))
	if len(fields) == 0 {
		return ""
	}
	// An assignment is not a program, and `cd` on its own says nothing about
	// what failed.
	if strings.Contains(fields[0], "=") || fields[0] == "cd" {
		return ""
	}
	head := []string{fields[0]}
	skipNext := false
	for _, f := range fields[1:] {
		// A redirect is not an argument. `make check --jobs 4 2>&1 | tail`
		// cut at the pipe leaves "2>" behind, and a shape with that in it
		// matches nothing a reader would type.
		if strings.ContainsAny(f, "<>") {
			continue
		}
		if strings.HasPrefix(f, "-") {
			// A flag's value is part of the flag, not part of the shape:
			// `make check --jobs 4` is a `make check`, and keeping the 4 made
			// it a shape of its own. A flag written with "=" carries its value
			// already.
			skipNext = !strings.Contains(f, "=")
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		head = append(head, f)
		if len(head) == commandFailHeadWords {
			break
		}
	}
	return strings.Join(head, " ")
}

// commandFailAcc gathers the failures a command shape has ended in.
type commandFailAcc struct {
	by map[string]*struct {
		fail     CommandFailure
		sessions map[string]bool
	}
	// The command each session last ran, so the output that follows it can be
	// attributed. Records and messages both arrive in the order they were
	// written.
	pending map[string]string
}

func newCommandFailAcc() *commandFailAcc {
	return &commandFailAcc{
		by: map[string]*struct {
			fail     CommandFailure
			sessions map[string]bool
		}{},
		pending: map[string]string{},
	}
}

func (a *commandFailAcc) command(key, text string) { a.pending[key] = CommandHead(text) }

func (a *commandFailAcc) output(key, project, text string) {
	head := a.pending[key]
	if head == "" {
		return
	}
	line, _, ok := firstFrictionLine(text)
	if !ok {
		return
	}
	// One failure per run: the first friction line is what the run ended on,
	// and the rest is that failure repeating itself.
	delete(a.pending, key)
	k := head + "\x00" + line
	entry := a.by[k]
	if entry == nil {
		entry = &struct {
			fail     CommandFailure
			sessions map[string]bool
		}{fail: CommandFailure{Head: head, Line: line, Project: project}, sessions: map[string]bool{}}
		a.by[k] = entry
	}
	entry.sessions[key] = true
}

func (a *commandFailAcc) table() []CommandFailure {
	out := make([]CommandFailure, 0, len(a.by))
	for _, entry := range a.by {
		if len(entry.sessions) < commandFailMinSessions {
			continue
		}
		entry.fail.Sessions = len(entry.sessions)
		out = append(out, entry.fail)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].Head != out[j].Head {
			return out[i].Head < out[j].Head
		}
		return out[i].Line < out[j].Line
	})
	if len(out) > commandFailsMax {
		out = out[:commandFailsMax]
	}
	return out
}

// buildCommandFails mines the failures out of the sessions a full build holds.
func buildCommandFails(tmp string, ss []model.Session) {
	acc := newCommandFailAcc()
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		for _, m := range s.Messages {
			switch m.Role {
			case roleCommand:
				acc.command(key, m.Text)
			case roleToolOutput:
				acc.output(key, s.Project, m.Text)
			default:
				// Anything else ends the run this output would belong to.
				delete(acc.pending, key)
			}
		}
	}
	writeCommandFails(tmp, acc.table())
}

// buildCommandFailsFromIndex is the same from the records an index already
// holds, for the incremental path — which has only the sessions it touched,
// while these counts are over the whole corpus (the reason
// buildCommandsFromIndex exists).
func buildCommandFailsFromIndex(tmp string) {
	acc := newCommandFailAcc()
	err := eachCommandAndOutput(tmp, func(meta SessionMeta, r Record) {
		if r.Role == roleCommand {
			acc.command(r.Key, r.Text)
			return
		}
		acc.output(r.Key, meta.Project, r.Text)
	})
	if err != nil {
		// An extra, never a reason to fail an update.
		return
	}
	writeCommandFails(tmp, acc.table())
}

func writeCommandFails(tmp string, out []CommandFailure) {
	if len(out) == 0 {
		return
	}
	_ = writeGobAtomic(commandFailsPath(tmp), out)
}

// ReadCommandFails loads the mined failures. An index built before they existed
// simply has none.
func ReadCommandFails(dir string) []CommandFailure {
	var out []CommandFailure
	if err := readGob(commandFailsPath(dir), &out); err != nil {
		return nil
	}
	return out
}

// CommandFailedBefore is what this command shape ended in on this machine, or
// false when nothing confirmed is on file.
func CommandFailedBefore(dir, cmd string, allow func(project string) bool) (CommandFailure, bool) {
	head := CommandHead(cmd)
	if head == "" {
		return CommandFailure{}, false
	}
	for _, f := range ReadCommandFails(dir) {
		if f.Head != head {
			continue
		}
		if allow != nil && !allow(f.Project) {
			continue
		}
		return f, true
	}
	return CommandFailure{}, false
}

// eachCommandAndOutput streams the command records and the tool output records
// in the order they were written, which is what ties a run to what it printed.
func eachCommandAndOutput(dir string, fn func(SessionMeta, Record)) error {
	return eachRecordOfRoles(dir, map[string]bool{roleCommand: true, roleToolOutput: true}, fn)
}
