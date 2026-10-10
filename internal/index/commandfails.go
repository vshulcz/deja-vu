package index

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// commandFailOutputRead, when set, is told each time a tool output is searched
// for the line it ended on. Tests count it to see how much of the index an
// update read.
var commandFailOutputRead func()

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

// commandFailAcc gathers the failures command shapes have ended in, session by
// session. Per session so an update can take back what a replaced session
// held and read only that session again, rather than every output in the
// index (#4288). Exported fields: it is also the state kept between updates.
type commandFailAcc struct {
	Sessions map[string]*commandFailSession
}

// commandFailSession is one session's part of the table.
type commandFailSession struct {
	// Fails holds each shape and line a run in this session ended in, once.
	Fails []commandFailPair
	// Pending is the head of the command this session last ran, so the output
	// that follows it can be attributed. Records and messages both arrive in
	// the order they were written. First is that command's first program, to
	// tell whose glob failed.
	Pending, First string
}

type commandFailPair struct{ Head, Line string }

func newCommandFailAcc() *commandFailAcc {
	return &commandFailAcc{Sessions: map[string]*commandFailSession{}}
}

func (a *commandFailAcc) session(key string) *commandFailSession {
	s := a.Sessions[key]
	if s == nil {
		s = &commandFailSession{}
		a.Sessions[key] = s
	}
	return s
}

// stop ends the run an output would belong to.
func (a *commandFailAcc) stop(key string) {
	if s := a.Sessions[key]; s != nil {
		s.Pending = ""
	}
}

func (a *commandFailAcc) command(key, text string) {
	if !failureIsTheHeads(text) {
		a.stop(key)
		return
	}
	s := a.session(key)
	s.Pending = CommandHead(text)
	seg := strings.TrimSpace(firstTextLine(text))
	if i := strings.IndexAny(seg, "|;&"); i > 0 {
		seg = seg[:i]
	}
	s.First = seg
}

// failureSegmentRE splits a command line into the programs it runs.
var failureSegmentRE = regexp.MustCompile(`\|\||&&|;|\||&`)

var failureQuotedRE = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// outputFilters only reshape what the command before them printed, so an error
// in the output is still that command's.
var outputFilters = map[string]bool{
	"tail": true, "head": true, "grep": true, "rg": true, "egrep": true, "cut": true,
	"sort": true, "uniq": true, "wc": true, "tee": true, "cat": true, "less": true,
	"echo": true, "printf": true, "true": true, "tr": true, "column": true,
}

// failureIsTheHeads reports whether an error in this command's output can be
// laid at its first program. The warning is keyed on that program, and in a
// chain the error came from whichever part failed: `gh pr checks 41; go test`
// was on file as gh ending in a failing Go test, `git fetch && [ $a == $b ]` as
// git ending in zsh's "== not found". Over 835 warnings in real transcripts the
// command that followed failed 2.5% of the time, against 2.6% for every Bash
// run — the line predicted nothing. A pipe into a filter keeps the error the
// first program's.
func failureIsTheHeads(text string) bool {
	line := strings.TrimSpace(withoutExitStatus(strings.TrimSpace(firstTextLine(text))))
	line = strings.TrimPrefix(line, "$ ")
	// A pattern in quotes is an argument, not a pipe: `grep -E "ok|FAIL"`.
	line = failureQuotedRE.ReplaceAllString(line, "q")
	for _, r := range []string{"2>&1", ">&2", "&>"} {
		line = strings.ReplaceAll(line, r, " ")
	}
	segs := failureSegmentRE.Split(line, -1)
	for _, seg := range segs[1:] {
		f := strings.Fields(seg)
		if len(f) == 0 {
			continue
		}
		if !outputFilters[filepath.Base(f[0])] {
			return false
		}
	}
	return true
}

func (a *commandFailAcc) output(key, text string) {
	s := a.Sessions[key]
	if s == nil || s.Pending == "" {
		return
	}
	if commandFailOutputRead != nil {
		commandFailOutputRead()
	}
	line, _, ok := firstFrictionLine(text)
	if !ok {
		return
	}
	// zsh refuses the whole line over one unmatched glob, and names it. When
	// the glob is not in the first program's part, the error is not its:
	// `go test ./... | grep -rn --include=*.go` was on file as go test ending
	// in "no matches found: --include=*.go".
	if glob, isGlob := strings.CutPrefix(line, "no matches found: "); isGlob && !strings.Contains(s.First, glob) {
		s.Pending = ""
		return
	}
	// One failure per run: the first friction line is what the run ended on,
	// and the rest is that failure repeating itself.
	f := commandFailPair{Head: s.Pending, Line: line}
	s.Pending = ""
	for _, have := range s.Fails {
		if have == f {
			return
		}
	}
	s.Fails = append(s.Fails, f)
}

// table totals the sessions project knows; a session it does not is no longer
// in the index.
//
// A row names the project most of its sessions ran in, the first by name on a
// tie. It used to be whichever session the walk met first, which an update
// reading only what changed cannot know, and which already differed between a
// full build and an update of the same transcripts.
func (a *commandFailAcc) table(project func(key string) (string, bool)) []CommandFailure {
	type row struct {
		fail     CommandFailure
		projects map[string]int
	}
	by := map[commandFailPair]*row{}
	for key, s := range a.Sessions {
		p, ok := project(key)
		if !ok {
			continue
		}
		for _, f := range s.Fails {
			r := by[f]
			if r == nil {
				r = &row{fail: CommandFailure{Head: f.Head, Line: f.Line}, projects: map[string]int{}}
				by[f] = r
			}
			r.fail.Sessions++
			r.projects[p]++
		}
	}
	out := make([]CommandFailure, 0, len(by))
	for _, r := range by {
		if r.fail.Sessions < commandFailMinSessions {
			continue
		}
		for p, n := range r.projects {
			best := r.projects[r.fail.Project]
			if n > best || (n == best && p < r.fail.Project) {
				r.fail.Project = p
			}
		}
		out = append(out, r.fail)
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
	projects := map[string]string{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		if _, ok := projects[key]; !ok {
			projects[key] = s.Project
		}
		for _, m := range s.Messages {
			switch m.Role {
			case roleCommand:
				acc.command(key, m.Text)
			case roleToolOutput:
				acc.output(key, m.Text)
			default:
				// Anything else ends the run this output would belong to.
				acc.stop(key)
			}
		}
	}
	writeCommandFails(tmp, acc.table(func(key string) (string, bool) {
		p, ok := projects[key]
		return p, ok
	}))
}

// buildCommandFailsFromIndex is the same from the records an index already
// holds, for the incremental path — which has only the sessions it touched,
// while these counts are over the whole corpus (the reason
// buildCommandsFromIndex exists).
//
// Walking every output for its failure line was most of an update: 1.26 s of
// 1.85 s for one appended run at 10,000 sessions (#4288). So the walk keeps
// its state beside the table, and an update starts from it: prior covers the
// records before its Size, and the sessions in redo are read again from the
// start. A nil prior, or one this index did not write, walks everything.
func buildCommandFailsFromIndex(dir string, prior *commandFailState, redo map[string]bool) {
	out, st, err := commandFailsFromIndex(dir, prior, redo)
	if err != nil {
		// An extra, never a reason to fail an update.
		return
	}
	writeCommandFails(dir, out)
	_ = writeGobAtomic(commandFailStatePath(dir), st)
}

const (
	commandFailStateFile = "commandfails-state.gob"
	// commandFailStateVersion changes with what the state means; a state of
	// another version is walked again rather than read. 2: a pending run ends
	// on any other turn, as in a full build (#4309).
	commandFailStateVersion = 2
)

func commandFailStatePath(dir string) string { return filepath.Join(dir, commandFailStateFile) }

// commandFailState is the walk as it stood at the end of records.bin.
type commandFailState struct {
	Version int
	// Generation and Size say which log and how much of it: an append keeps
	// the generation and grows the log, anything else starts a new one.
	Generation string
	Size       int64
	Acc        commandFailAcc
}

// readCommandFailState is the state the index in dir last left, or nil.
func readCommandFailState(dir string) *commandFailState {
	var st commandFailState
	if err := readGob(commandFailStatePath(dir), &st); err != nil || st.Version != commandFailStateVersion || st.Acc.Sessions == nil {
		return nil
	}
	return &st
}

// carriedCommandFailState is dir's state for an update rebuilt into tmp,
// which writes every record it keeps again. It then covers all of tmp's log,
// less the sessions in redo: the ones the update dropped records of or read
// again, which the caller puts there, and the ones with a command or an output
// in dir's log past where the state stopped. A pass with nothing for this
// table does not run it, so a prompt-only append leaves that tail behind, and
// refusing the state over it walked everything on the next rewrite.
func carriedCommandFailState(dir string, old Manifest, tmp, generation string, redo map[string]bool) *commandFailState {
	st := readCommandFailState(dir)
	if st == nil || st.Generation != old.Generation {
		return nil
	}
	path := filepath.Join(dir, "records.bin")
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < st.Size {
		return nil
	}
	if fi.Size() > st.Size {
		// Another turn in the tail ends the session's pending run, as the
		// walk would have; a command or an output reads the session again.
		if _, err := eachCommandAndOutputFrom(path, old, st.Size, nil, func(r Record) { redo[r.Key] = true }, st.Acc.stop); err != nil {
			return nil
		}
	}
	nfi, err := os.Stat(filepath.Join(tmp, "records.bin"))
	if err != nil {
		return nil
	}
	st.Generation, st.Size = generation, nfi.Size()
	return st
}

// commandFailsFromIndex is the table and the state after it.
func commandFailsFromIndex(dir string, prior *commandFailState, redo map[string]bool) ([]CommandFailure, *commandFailState, error) {
	var out []CommandFailure
	var st *commandFailState
	err := walkRecordsStable(dir, func(m Manifest) error {
		path := filepath.Join(dir, "records.bin")
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		st = prior
		from := int64(0)
		if st != nil && st.Generation == m.Generation && st.Size <= fi.Size() {
			from = st.Size
			for key := range redo {
				delete(st.Acc.Sessions, key)
			}
		} else {
			st = &commandFailState{Version: commandFailStateVersion, Acc: *newCommandFailAcc()}
			redo = nil
		}
		end, err := eachCommandAndOutputFrom(path, m, from, redo, func(r Record) {
			if r.Role == roleCommand {
				st.Acc.command(r.Key, r.Text)
				return
			}
			st.Acc.output(r.Key, r.Text)
		}, st.Acc.stop)
		if err != nil {
			return err
		}
		st.Generation, st.Size = m.Generation, end
		project := func(key string) (string, bool) {
			meta, ok := m.Sessions[key]
			return meta.Project, ok
		}
		// A session the index no longer holds takes its runs with it, and one
		// with nothing on file reads the same as one never seen.
		for key, s := range st.Acc.Sessions {
			if _, ok := project(key); !ok || (len(s.Fails) == 0 && s.Pending == "") {
				delete(st.Acc.Sessions, key)
			}
		}
		out = st.Acc.table(project)
		return nil
	})
	return out, st, err
}

// eachCommandAndOutputFrom streams the command and tool output records of the
// sessions m holds, in the order they were written: all of them from byte
// from on, and before it only the sessions in redo. The rest are skipped on
// their prefix, undecoded. Each record of another role is handed to stop by
// its key alone: a full build ends a pending run on any other turn, and a walk
// that never saw the prompt or the Read between a clean `make build` and a
// failing log laid the log at the build (#4309). It returns where the last
// whole record ended.
func eachCommandAndOutputFrom(path string, m Manifest, from int64, redo map[string]bool, fn func(Record), stop func(key string)) (int64, error) {
	f, err := openIndexFile(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	off := from
	if len(redo) > 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	t := tablesFromManifest(m)
	r := bufio.NewReaderSize(f, 1024*1024)
	var hdr [4]byte
	buf := make([]byte, 0, 64*1024)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return off, nil
		}
		size := binary.LittleEndian.Uint32(hdr[:])
		if size > maxRecordSize {
			return off, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, size)
		}
		if cap(buf) < int(size) {
			buf = make([]byte, size)
		}
		payload := buf[:size]
		if _, err := io.ReadFull(r, payload); err != nil {
			return off, nil
		}
		at := off
		off += int64(len(hdr)) + int64(size)
		key, role, ok := recordRoleIn(payload, t)
		if !ok {
			return off, errShortRecord
		}
		if at < from && !redo[key] {
			continue
		}
		if _, ok := m.Sessions[key]; !ok {
			continue
		}
		if role != roleCommand && role != roleToolOutput {
			stop(key)
			continue
		}
		rec, err := decodeRecord(payload, t)
		if err != nil {
			return off, err
		}
		fn(rec)
	}
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
