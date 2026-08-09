package index

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// What a machine runs is the most reusable thing it knows, and the record log
// holds it: 23825 command records over 1165 sessions on the store this was
// built against. Streaming that log to answer one question costs seconds,
// which is fine for a command someone typed and far too slow for a hook that
// fires on every action an agent takes. So the durable part — which commands
// recur, and how widely — is computed once at build.
//
// Only commands that ran in more than one session are kept. A command run once
// is a keystroke, not a practice: on that store 14346 of 14681 distinct
// commands are exactly that, and dropping them takes the file from megabytes
// to kilobytes.

const (
	commandsFile = "commands.gob"
	// commandMinSessions is what makes a command worth remembering.
	commandMinSessions = 2
	commandTextMax     = 200
	commandsMax        = 4000
)

// CommandUse is one command line and how widely this machine has run it.
type CommandUse struct {
	Command  string
	Runs     int
	Sessions int
	Last     time.Time
}

func commandsPath(dir string) string { return filepath.Join(dir, commandsFile) }

// buildCommands writes the recurring-command table into the build directory.
// Failures are swallowed: it is an extra, never a reason to fail a build.
func buildCommands(tmp string, ss []model.Session) {
	type acc struct {
		use      CommandUse
		sessions map[string]bool
	}
	by := map[string]*acc{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		for _, m := range s.Messages {
			if m.Role != roleCommand {
				continue
			}
			cmd := strings.TrimSpace(firstTextLine(m.Text))
			if cmd == "" || len(cmd) > commandTextMax {
				continue
			}
			low := normalizeCommand(cmd)
			if low == "" {
				continue
			}
			a := by[low]
			if a == nil {
				a = &acc{use: CommandUse{Command: cmd}, sessions: map[string]bool{}}
				by[low] = a
			}
			a.use.Runs++
			a.sessions[key] = true
			if m.Time.After(a.use.Last) {
				a.use.Last = m.Time
			}
		}
	}
	out := make([]CommandUse, 0, len(by))
	for _, a := range by {
		if len(a.sessions) < commandMinSessions {
			continue
		}
		a.use.Sessions = len(a.sessions)
		out = append(out, a.use)
	}
	if len(out) == 0 {
		return
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Command < out[j].Command
	})
	if len(out) > commandsMax {
		out = out[:commandsMax]
	}
	_ = writeGob(commandsPath(tmp), out)
}

// ReadCommands loads the recurring-command table. An index built before it
// existed simply has none.
func ReadCommands(dir string) []CommandUse {
	var out []CommandUse
	if err := readGob(commandsPath(dir), &out); err != nil {
		return nil
	}
	return out
}

// CommandHistory reports how widely this machine has run a command. The match
// is on the command as written, ignoring case, surrounding space and the "$ "
// the parsers prefix a stored invocation with — but not on flags: an
// invocation that differs by a flag is a different invocation.
func CommandHistory(dir, cmd string) (CommandUse, bool) {
	// A multi-line command is never the stored single-line one, and matching it
	// on its first line only is dangerous: "git status\nrm -rf /" would be
	// endorsed as "this machine has run that command" on the strength of the
	// harmless first line. Recognise only what was seen in full.
	if hasSecondLine(cmd) {
		return CommandUse{}, false
	}
	want := normalizeCommand(cmd)
	if want == "" {
		return CommandUse{}, false
	}
	for _, u := range ReadCommands(dir) {
		if normalizeCommand(u.Command) == want {
			return u, true
		}
	}
	return CommandUse{}, false
}

// hasSecondLine reports whether the command carries a non-empty line after the
// first — a compound the stored single-line invocations cannot vouch for.
func hasSecondLine(cmd string) bool {
	i := strings.IndexByte(cmd, '\n')
	return i >= 0 && strings.TrimSpace(cmd[i+1:]) != ""
}

// FileSessions returns the sessions that worked on a path, from what the
// manifest already stores. The caller filters by its own trust policy: this
// package sits below policy and must not decide what is showable.
func FileSessions(dir, path string) []SessionMeta {
	base := filepath.Base(path)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return nil
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	var out []SessionMeta
	for _, meta := range m.Sessions {
		for _, t := range meta.Touched {
			// Paths are stored as the session saw them — absolute in one
			// harness, relative in another — so the comparison is on the file
			// name, with the full path accepted as written.
			if t == path || filepath.Base(t) == base {
				out = append(out, meta)
				break
			}
		}
	}
	return out
}

// normalizeCommand puts a command in the form two records can be compared on:
// the first line, without the "$ " every parser prefixes a stored invocation
// with, lowercased.
func normalizeCommand(s string) string {
	s = strings.TrimSpace(firstTextLine(s))
	s = strings.TrimPrefix(s, "$ ")
	return strings.ToLower(strings.TrimSpace(s))
}

// firstTextLine is the first line of a record, which for a command record is
// the invocation itself.
func firstTextLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
