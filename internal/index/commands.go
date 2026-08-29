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
	// ByProject holds the per-project split, so a caller can apply the trust
	// policy: exclude withheld projects from both the count AND the last-run
	// date. Storing only a count leaked the date — a command surfaced from an
	// allowed project still printed a withheld project's more-recent run. Empty
	// on a table built before this field existed; the version bump forces the
	// rebuild that fills it.
	ByProject map[string]ProjectUse `json:",omitempty"`
}

// ProjectUse is one project's share of a command: how many distinct sessions
// ran it there and when it last did.
type ProjectUse struct {
	Sessions int
	Last     time.Time
}

// commandProjectCap bounds the per-command project map. A command run in more
// projects than this is ubiquitous; the extra keys say nothing new and cost
// bytes across the whole table.
const commandProjectCap = 24

// projAcc accumulates one project's distinct sessions and last run for a
// command during a build.
type projAcc struct {
	sessions map[string]bool
	last     time.Time
}

func commandsPath(dir string) string { return filepath.Join(dir, commandsFile) }

// buildCommands writes the recurring-command table into the build directory.
// Failures are swallowed: it is an extra, never a reason to fail a build.
func buildCommands(tmp string, ss []model.Session) {
	type acc struct {
		use      CommandUse
		sessions map[string]bool
		// byProject holds the distinct session keys and last-run per project.
		byProject map[string]*projAcc
	}
	by := map[string]*acc{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		for _, m := range s.Messages {
			if m.Role != roleCommand {
				continue
			}
			cmd := withoutExitStatus(strings.TrimSpace(firstTextLine(m.Text)))
			if cmd == "" || len(cmd) > commandTextMax {
				continue
			}
			low := normalizeCommand(cmd)
			if low == "" {
				continue
			}
			a := by[low]
			if a == nil {
				a = &acc{use: CommandUse{Command: cmd}, sessions: map[string]bool{}, byProject: map[string]*projAcc{}}
				by[low] = a
			}
			a.use.Runs++
			a.sessions[key] = true
			pa := a.byProject[s.Project]
			if pa == nil {
				pa = &projAcc{sessions: map[string]bool{}}
				a.byProject[s.Project] = pa
			}
			pa.sessions[key] = true
			if m.Time.After(pa.last) {
				pa.last = m.Time
			}
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
		a.use.ByProject = cappedProjects(a.byProject)
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

// buildCommandsFromIndex rebuilds the table from the records an index already
// holds, rather than from sessions in memory.
//
// The incremental path has only the sessions this update touched, and these
// counters are aggregates — how many distinct sessions ran a command, in which
// projects, last when. A subset cannot be merged into an aggregate: for a
// session being re-read there is no way to subtract what it contributed before.
// So carrying the table forward was the only option, and it meant hook-tool
// stayed silent about every command that became a habit after the last full
// build — which is every habit, since a new session is a new file and that is
// the path this runs on.
//
// Reading it back out of the records is exact, because by this point the
// records in tmp are the whole corpus: the ones carried over plus the ones this
// update wrote. It is the same source `deja how` already answers from.
func buildCommandsFromIndex(tmp string) {
	type acc struct {
		use       CommandUse
		sessions  map[string]bool
		byProject map[string]*projAcc
	}
	by := map[string]*acc{}
	// Identity is harness:id and nothing guarantees it is unique: two transcripts
	// named session-1.jsonl in different projects share one manifest row, and the
	// row carries the winner's project. A full build files each conversation's
	// commands under its own project, because it reads the session rather than
	// the row — and the trust policy, --project and the exclude patterns all key
	// on that. Recomputing from records here would file the loser's commands
	// under the winner's project, which is the leak ByProject exists to prevent.
	// A record whose source path is not the row's path is exactly that case;
	// leave the carried table alone rather than publish a wrong attribution.
	collided := false
	err := EachRecordOfRole(tmp, roleCommand, func(meta SessionMeta, r Record) {
		if collided {
			return
		}
		if r.SourcePath != "" && meta.Path != "" && r.SourcePath != meta.Path {
			collided = true
			return
		}
		cmd := withoutExitStatus(strings.TrimSpace(firstTextLine(r.Text)))
		if cmd == "" || len(cmd) > commandTextMax {
			return
		}
		low := normalizeCommand(cmd)
		if low == "" {
			return
		}
		a := by[low]
		if a == nil {
			a = &acc{use: CommandUse{Command: cmd}, sessions: map[string]bool{}, byProject: map[string]*projAcc{}}
			by[low] = a
		}
		a.use.Runs++
		a.sessions[r.Key] = true
		pa := a.byProject[meta.Project]
		if pa == nil {
			pa = &projAcc{sessions: map[string]bool{}}
			a.byProject[meta.Project] = pa
		}
		pa.sessions[r.Key] = true
		if r.Time.After(pa.last) {
			pa.last = r.Time
		}
		if r.Time.After(a.use.Last) {
			a.use.Last = r.Time
		}
	})
	if err != nil || collided {
		// An extra, never a reason to fail an update: the carried table stays.
		return
	}
	out := make([]CommandUse, 0, len(by))
	for _, a := range by {
		if len(a.sessions) < commandMinSessions {
			continue
		}
		a.use.Sessions = len(a.sessions)
		a.use.ByProject = cappedProjects(a.byProject)
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
	// Atomic, unlike the full build's write: there the file does not exist yet,
	// here a good carried table is already sitting in tmp. A truncating write
	// that fails partway would ship an undecodable file, and ReadCommands
	// answers nothing at all for that — hook-tool would go silent until the
	// next full rebuild, which is worse than the staleness this replaces.
	_ = writeGobAtomic(commandsPath(tmp), out)
}

// cappedProjects keeps at most commandProjectCap projects for a command,
// choosing the ones with the most sessions (ties broken by name) so the choice
// is deterministic across rebuilds — a map-order cap could silence a different
// project on each build. All projects are counted; only storage is trimmed, and
// only for a command run in more projects than the cap.
func cappedProjects(byProject map[string]*projAcc) map[string]ProjectUse {
	names := make([]string, 0, len(byProject))
	for proj := range byProject {
		names = append(names, proj)
	}
	sort.Slice(names, func(i, j int) bool {
		ni, nj := len(byProject[names[i]].sessions), len(byProject[names[j]].sessions)
		if ni != nj {
			return ni > nj
		}
		return names[i] < names[j]
	})
	if len(names) > commandProjectCap {
		names = names[:commandProjectCap]
	}
	out := make(map[string]ProjectUse, len(names))
	for _, proj := range names {
		pa := byProject[proj]
		out[proj] = ProjectUse{Sessions: len(pa.sessions), Last: pa.last}
	}
	return out
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

// SessionRanCommand reports whether this session ran the command, comparing the
// way CommandHistory does. It is how a caller with a session in hand — the tool
// hook, holding the promoted decisions of a project — can ask whether the
// decision is about the command about to run (#2516).
func SessionRanCommand(s model.Session, cmd string) bool {
	want := normalizeCommand(cmd)
	if want == "" || hasSecondLine(cmd) {
		return false
	}
	for _, m := range s.Messages {
		if m.Role != roleCommand {
			continue
		}
		if normalizeCommand(m.Text) == want {
			return true
		}
	}
	return false
}

// CrossBase is filepath.Base that splits on both separators regardless of the
// host OS. A store synced from Windows holds paths like C:\src\main.go; on a
// Unix host filepath.Base leaves them whole, so a same-file lookup missed and
// the two "basename" notions disagreed with the hook's own splitter.
func CrossBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
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
	base := CrossBase(path)
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
			if t == path || CrossBase(t) == base {
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
	s = withoutExitStatus(strings.TrimSpace(firstTextLine(s)))
	s = strings.TrimPrefix(s, "$ ")
	return strings.ToLower(strings.TrimSpace(s))
}

// withoutExitStatus drops the outcome codex and opencode append to the command
// line — "$ make test  → exit 2". It is what the command did, not what it was,
// and keying the table on it split one command into two rows: the runs that
// worked and the runs that did not, counted apart, with the failing ones
// invisible to a lookup made with the command as a harness hands it over. On a
// real store four commands were split this way, `git status --short` into 445
// runs and 7 (#2590). The status stays in the records, where the fix-pair miner
// reads it (commandFailed).
func withoutExitStatus(s string) string {
	if i := strings.Index(s, "→ exit "); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// CommandWithoutExitStatus is withoutExitStatus for the surfaces outside this
// package that group commands themselves — `deja how` counts its own rows over
// the record log and split the same command the same way.
func CommandWithoutExitStatus(s string) string { return withoutExitStatus(s) }

// firstTextLine is the first line of a record, which for a command record is
// the invocation itself.
func firstTextLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
