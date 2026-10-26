package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja delete <id>` erases a session everywhere: its agent's files, and
// then deja's own copy, the way `deja forget --session` drops it. forget alone
// is the other half: out of deja, still in the agent. There is no undo, so the
// person confirms, and what counts as one session on disk is decided
// narrowly, because a wrong answer erases someone's data: every path removed
// carries the session's id in its own name, a database or a shared history
// file is never touched (only a database named by the session alone), and a directory goes only when every other indexed
// session inside it is one of its own (Claude's subagents, Kimi's agents).

// deletable are the agents whose session files were checked to be what the
// agent itself lists sessions from, so removing them takes the session out of
// the agent. VS Code extensions keep their
// chat index in VS Code's own state, Aider keeps a project's whole history in
// one file, Amp's threads live on its servers.
var deletable = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "qwen": true, "copilot": true,
	"cline": true, "cursor": true, "kimi": true, "grok": true, "deepseek": true,
	"pi": true, "senpi": true, "openclaw": true, "antigravity": true,
}

var (
	errNotDeletable  = errors.New("not checked")
	errNotFileBacked = errors.New("not a file of its own")
	errInEditor      = errors.New("in the editor's storage")
	errSubagent      = errors.New("a subagent of another session")
)

// deletableRefusal says why a session stays, and what to do instead. It opens
// with the agent: the CLI prints the program's own name before the message, so
// starting with it read `deja: deja can't delete Continue sessions yet` (#5654).
func deletableRefusal(harness string) error {
	if harness == "deja" {
		return errors.New("this is a deja note, not an agent's session; `deja forget --session <id>` removes it")
	}
	return fmt.Errorf("%s sessions can't be deleted yet: deja has not checked how %s lists them", agentName(harness), agentName(harness))
}

// deleteTakes names the agents delete erases a session from — the sessions in
// files of their own, then the ones whose own command takes the id — for the
// help page to list. Written from the two maps, so an agent added to either is
// named in help without a second edit, and the page stops describing which
// agents qualify instead of saying them (#5654).
func deleteTakes() []string {
	names := make([]string, 0, len(deletable)+len(cliStores))
	for h := range deletable {
		names = append(names, agentName(h))
	}
	for h := range cliStores {
		names = append(names, agentName(h))
	}
	slices.SortFunc(names, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	return names
}

// recentlyWritten is how fresh a file has to be for deja to take it as a
// session still running: hooks stamp the sessions they see, and the agents
// without hooks are caught by the clock.
const recentlyWritten = 5 * time.Minute

// namesSession says the base name of p carries the session's id, or the
// eight-character prefix Gemini files a chat under.
func namesSession(p, id string) bool {
	b := filepath.Base(p)
	if strings.Contains(b, id) {
		return true
	}
	return len(id) >= 8 && strings.Contains(b, id[:8])
}

// ownStore is a store named by the session alone, Antigravity's
// conversations/<id>.db with its -wal and -shm, or <id>.pb.
func ownStore(p, id string) bool {
	return strings.HasPrefix(filepath.Base(p), id+".db")
}

// sharedStore is a file that holds many sessions however few are indexed.
func sharedStore(p string) bool {
	low := strings.ToLower(filepath.Base(p))
	for _, ext := range []string{".db", ".sqlite", ".sqlite3", ".vscdb", ".db-wal", ".db-shm"} {
		if strings.HasSuffix(low, ext) {
			return true
		}
	}
	return low == "history.jsonl"
}

// deletableFiles is what one session is on disk. others is every indexed
// session; an empty list is refused, since the check against it is the
// guard that keeps another session from going along.
func deletableFiles(s model.Session, others []model.Session) ([]string, error) {
	if !deletable[s.Harness] {
		return nil, errNotDeletable
	}
	if s.Path == "" {
		return nil, errNotDeletable
	}
	if strings.Contains(filepath.ToSlash(s.Path), "/globalStorage/") {
		return nil, errInEditor
	}
	if len(s.ID) < 8 || filepath.Base(s.ID) != s.ID || strings.ContainsAny(s.ID, `/\*?[`) {
		return nil, fmt.Errorf("session id %q is too short to tell its files apart", s.ID)
	}
	if len(others) == 0 {
		return nil, errors.New("deja could not read its index, and deletes nothing it cannot check against it")
	}
	fi, err := os.Lstat(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !fi.Mode().IsRegular() || sharedStore(s.Path) {
		return nil, errNotFileBacked
	}
	files := make([]string, 0, 2)
	switch root := sessionDir(s); {
	case root != "":
		files = append(files, root)
	case namesSession(s.Path, s.ID):
		files = append(files, s.Path)
		// Claude Code keeps subagent runs and large tool results in <id>/.
		if side := strings.TrimSuffix(s.Path, filepath.Ext(s.Path)); filepath.Base(side) == s.ID {
			if fi, err := os.Lstat(side); err == nil && fi.IsDir() {
				files = append(files, side)
			}
		}
	default:
		if s.Parent != "" {
			return nil, errSubagent
		}
		return nil, errNotFileBacked
	}
	files = append(files, companions(s)...)
	for _, f := range files {
		if !namesSession(f, s.ID) || (sharedStore(f) && !ownStore(f, s.ID)) {
			return nil, fmt.Errorf("%s does not belong to session %s alone, so deja does not delete it", f, digest.Short(s.ID))
		}
		for _, o := range others {
			if o.Path == s.Path && o.ID == s.ID && o.Harness == s.Harness {
				continue
			}
			if o.Path == f {
				return nil, fmt.Errorf("%s also holds another session, so deja does not delete it", f)
			}
			// Inside a directory named by this session, another session is
			// one of its own: a subagent, a spawned agent.
			if strings.HasPrefix(o.Path, f+string(filepath.Separator)) && !childOf(o, s) {
				return nil, fmt.Errorf("%s also holds session %s, so deja does not delete it", f, digest.Short(o.ID))
			}
		}
	}
	return existing(files), nil
}

// refuseFresh takes a file written in the last minutes for a session still
// running.
func refuseFresh(s model.Session, files []string) error {
	for _, f := range files {
		if fresh(f, time.Now()) {
			return fmt.Errorf("session %s was written in the last %d minutes; if it is still running, end it first", digest.Short(s.ID), int(recentlyWritten.Minutes()))
		}
	}
	return nil
}

// childOf says o was spawned by s, by the session's own record or by living
// under a directory named by s's id.
func childOf(o, s model.Session) bool {
	if o.Parent == s.ID {
		return true
	}
	for d := filepath.Dir(o.Path); d != filepath.Dir(d); d = filepath.Dir(d) {
		if dirOfSession(filepath.Base(d), s.ID) {
			return true
		}
	}
	return false
}

// dirOfSession says a directory is named for the session alone: the id, or
// dsh's session-<id>. A name that only contains the id is not enough: Claude
// files a session run in /work/<id> under the project directory -work-<id>,
// next to every other session of that project.
func dirOfSession(base, id string) bool {
	return base == id || base == "session-"+id || base == "session_"+id
}

func fresh(p string, now time.Time) bool {
	recent := false
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || recent {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if info, err := d.Info(); err == nil && now.Sub(info.ModTime()) < recentlyWritten {
			recent = true
		}
		return nil
	})
	return recent
}

// sessionDir is the highest directory above the transcript named for the
// session alone, or "", stopping at the home directory.
func sessionDir(s model.Session) string {
	home, _ := os.UserHomeDir()
	found := ""
	for d := filepath.Dir(s.Path); d != filepath.Dir(d) && d != home; d = filepath.Dir(d) {
		if dirOfSession(filepath.Base(d), s.ID) {
			found = d
		}
	}
	return found
}

// companions are the other places an agent keeps the same session, each
// named by the session's id. Removing only the transcript deja reads would
// leave the agent listing it: Cursor's CLI keeps the conversation in
// chats/<hash>/<id>/store.db and the transcript is a copy, Antigravity keeps
// it in conversations/<id>.db beside brain/<id>, and Codex compresses a
// week-old rollout or archives it.
func companions(s model.Session) []string {
	var out []string
	switch s.Harness {
	case "codex":
		for _, root := range sources.CodexRoots() {
			for _, pattern := range []string{
				filepath.Join(root, "sessions", "*", "*", "*", "rollout-*"+s.ID+".jsonl*"),
				filepath.Join(root, "archived_sessions", "rollout-*"+s.ID+".jsonl*"),
			} {
				m, _ := filepath.Glob(pattern)
				for _, p := range m {
					if p != s.Path {
						out = append(out, p)
					}
				}
			}
		}
	case "qwen":
		// Sub-agent logs sit beside chats/, in subagents/<id>/.
		if chats := filepath.Dir(s.Path); filepath.Base(chats) == "chats" {
			out = []string{filepath.Join(filepath.Dir(chats), "subagents", s.ID)}
		}
	case "cursor":
		out, _ = filepath.Glob(filepath.Join(sources.CursorCLIRoot(), "chats", "*", s.ID))
	case "antigravity":
		if root := sessionDir(s); filepath.Base(filepath.Dir(root)) == "brain" {
			// The conversation database is the session's own, named by its id.
			ag := filepath.Dir(filepath.Dir(root))
			base := filepath.Join(ag, "conversations", s.ID)
			out = []string{base + ".db", base + ".db-shm", base + ".db-wal", base + ".pb",
				filepath.Join(ag, "annotations", s.ID+".pbtxt"), filepath.Join(ag, "implicit", s.ID+".pb")}
		}
	}
	return out
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// eraseSession removes a session from its agent. It does not touch deja's
// index; the caller forgets the session there once this succeeds.
func eraseSession(dir string, s model.Session, others []model.Session) ([]string, error) {
	if liveSessionIDs(dir)[s.ID] {
		return nil, fmt.Errorf("session %s is running right now; end it first", digest.Short(s.ID))
	}
	if c, ok := cliStores[s.Harness]; ok {
		if _, err := exec.LookPath(c.bin); err != nil {
			return nil, fmt.Errorf("deleting a %s session goes through its own command, and %s is not on PATH", agentName(s.Harness), c.bin)
		}
		if _, err := c.run("session", "delete", s.ID); err != nil {
			return nil, err
		}
		return []string{c.bin + " session " + s.ID}, nil
	}
	files, err := sessionFiles(s, others)
	if err != nil {
		return nil, err
	}
	if err := refuseFresh(s, files); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := os.RemoveAll(f); err != nil {
			return nil, fmt.Errorf("could not remove %s: %w", f, err)
		}
	}
	return files, nil
}

// sessionFiles is deletableFiles with its refusals said for a person.
func sessionFiles(s model.Session, others []model.Session) ([]string, error) {
	files, err := deletableFiles(s, others)
	switch {
	case errors.Is(err, errNotDeletable):
		return nil, deletableRefusal(s.Harness)
	case errors.Is(err, errSubagent):
		return nil, fmt.Errorf("this is a subagent run inside session %s; it goes when that session is deleted", digest.Short(s.Parent))
	case errors.Is(err, errInEditor):
		return nil, fmt.Errorf("%s keeps this session in the editor's own storage, so deja does not delete it", agentName(s.Harness))
	case errors.Is(err, errNotFileBacked):
		return nil, fmt.Errorf("%s keeps this session in a file it shares with others, so deja does not delete it", agentName(s.Harness))
	case err != nil:
		return nil, err
	case len(files) == 0:
		return nil, fmt.Errorf("session %s is already gone from %s; `deja forget --session %s` drops deja's copy", digest.Short(s.ID), agentName(s.Harness), digest.Short(s.ID))
	}
	return files, nil
}

// cliStore is an agent whose sessions live in its own database and that has a
// command to delete one: opencode. goose's remove needs its server running,
// and Hermes and Zed are left to their own tools.
type cliStore struct{ bin string }

var cliStores = map[string]cliStore{"opencode": {"opencode"}}

func (c cliStore) run(args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), c.bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w %s", c.bin, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

var deleteFlags = []string{"--dry-run", "--yes"}

func runDelete(dir string, args []string, w io.Writer) error {
	return runDeleteIn(dir, args, os.Stdin, w)
}

func runDeleteIn(dir string, args []string, in io.Reader, w io.Writer) error {
	dry, yes := false, false
	prefix := ""
	for _, a := range args {
		switch a {
		case "--dry-run":
			dry = true
		case "--yes":
			yes = true
		default:
			if strings.HasPrefix(a, "-") {
				return unknownFlag("delete", a, deleteFlags)
			}
			if prefix != "" {
				return fmt.Errorf("delete takes one session id; got %q and %q", prefix, a)
			}
			prefix = a
		}
	}
	if prefix == "" {
		return idPrefixNeeded(dir, "delete needs a session id prefix", "delete needs a session id prefix (see `deja last`)")
	}
	// Read from the index only: the fallback that reads the agents' stores
	// live has nothing to check a removal against.
	if err := ensureForCLIRead(dir); err != nil {
		return err
	}
	all, _, err := index.RecentMatchingCounted(dir, 0, search.Options{})
	if err != nil {
		return err
	}
	if n := index.PrefixMatches(dir, prefix); n > 1 {
		return fmt.Errorf("%d sessions match %q — delete erases one; use a longer prefix (`deja last` prints ids whole)", n, prefix)
	}
	s, ok, err := index.FindByPrefix(dir, prefix)
	if err != nil {
		return err
	}
	if !ok {
		return noSessionMatches(dir, prefix)
	}
	// The resolver also matches the middle of an id; an erase takes the start
	// of one, or the head…tail form deja prints.
	if !idStartsWith(s.ID, prefix) {
		return fmt.Errorf("%q is not the start of a session id; delete takes the id as `deja last` prints it", prefix)
	}
	var files []string
	if c, ok := cliStores[s.Harness]; ok {
		files = []string{"(through " + c.bin + " session delete)"}
	} else if files, err = sessionFiles(s, all); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s session %s: %s\n", agentName(s.Harness), digest.Short(s.ID), s.Title)
	for _, f := range files {
		fmt.Fprintf(w, "  %s\n", f)
	}
	if dry {
		fmt.Fprintln(w, "dry run: nothing removed; without --dry-run these go, and deja's copy with them")
		return nil
	}
	if err := refuseFresh(s, files); err != nil {
		return err
	}
	if !yes {
		if !isTerminalReader(in) {
			return errors.New("delete cannot be undone; run it at a terminal to confirm, or add --yes")
		}
		fmt.Fprint(w, "erase it from "+agentName(s.Harness)+" and from deja? this cannot be undone [y/N] ")
		var ans string
		_, _ = fmt.Fscanln(in, &ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Fprintln(w, "kept")
			return nil
		}
	}
	if _, err := eraseSession(dir, s, all); err != nil {
		return err
	}
	if err := forgetErased(dir, s.ID); err != nil {
		return fmt.Errorf("removed from %s, but deja still holds a copy: %w — `deja forget --session %s` drops it", agentName(s.Harness), err, digest.Short(s.ID))
	}
	fmt.Fprintf(w, "erased %s from %s and from deja\n", digest.Short(s.ID), agentName(s.Harness))
	return nil
}

func idStartsWith(id, p string) bool {
	if head, tail, ok := strings.Cut(p, "…"); ok {
		return strings.HasPrefix(id, head) && strings.HasSuffix(id, tail)
	}
	return strings.HasPrefix(id, p)
}

// forgetErased drops deja's copy of a session just erased from its agent.
func forgetErased(dir, id string) error {
	return runForget(dir, []string{"--session", id})
}

func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
