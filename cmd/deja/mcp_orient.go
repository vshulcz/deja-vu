package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// orient answers the question an agent otherwise answers by reading: how is
// work done in this repository. The other modes all wait for a question —
// recall needs one, fix needs an error, how needs a name to look up — so the
// first minutes of a session go on globbing for a Makefile and opening files
// to find out where the work lives, and that reading is the expensive part.
// Every file the agent opens stays in the prompt and is re-read on every later
// turn, so the cost of exploring is the length of what it found times the turns
// that follow it.
//
// What comes back is what past sessions in this project actually did: the
// commands they ran, ranked by how many sessions ran them, and the files they
// opened or edited. It is a map, not an instruction — the closing line says so,
// because a stale command run confidently is worse than no answer.
const (
	orientCommandsShown = 6
	orientFilesShown    = 8
	// orientPathMax keeps a generated or vendored path from filling a line.
	orientPathMax = 80
	// orientCommandMax is where a line stops being a command and starts being a
	// paragraph the reader skips.
	orientCommandMax = 72
)

func mcpOrient(dir, name string, raw json.RawMessage) (string, int, error) {
	var a struct {
		Project string    `json:"project"`
		Limit   mcpNumber `json:"limit"`
	}
	if err := decodeToolArgs(name, raw, &a); err != nil {
		return "", 0, err
	}
	if line := buildingNowForAgent(dir); line != "" {
		return line, 0, nil
	}
	if _, err := index.EnsureForSearchStale(dir, search.Options{}, mcpProgress()); err != nil {
		return "", 0, err
	}
	limit := int(a.Limit)
	if limit <= 0 {
		limit = orientCommandsShown
	}
	scope := howScope(howCwd(), a.Project, false)
	cmds, hidden, ignored, err := howEntries(dir, nil, scope, policy.ActivationMCP)
	if err != nil {
		return "", 0, err
	}
	cwd := howCwd()
	files := orientFiles(dir, scope, cwd)
	where := howScopeName(scope)
	if where == "" {
		where = "this machine"
	}
	if len(cmds) == 0 && len(files) == 0 {
		if note := ignoredHiddenNoteFor("answer", ignored); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		if note := policyHiddenNote(policy.ActivationMCP, hidden); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		return fmt.Sprintf("No past session on this machine worked in %s.", where) + emptyStoreNote(dir), 0, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "how work has been done in %s, from past sessions here\n", where)
	if len(cmds) > 0 {
		fmt.Fprintln(&b, "commands, by how many sessions ran them:")
		for i, e := range cmds {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "- %s · %s", orientCommand(e.Command), pluralSessions(len(e.Sessions)))
			if e.Failures > 0 {
				fmt.Fprintf(&b, " · failed in %d", e.Failures)
			}
			if !e.Last.IsZero() {
				fmt.Fprintf(&b, " · last %s", e.Last.Format("Jan 2"))
			}
			fmt.Fprintln(&b)
		}
	}
	if len(files) > 0 {
		fmt.Fprintln(&b, "files those sessions opened or edited:")
		for i, f := range files {
			if i >= orientFilesShown {
				break
			}
			fmt.Fprintf(&b, "- %s · %s\n", f.Path, pluralSessions(f.Sessions))
		}
	}
	fmt.Fprint(&b, "This is what was done here before, not a rule — check a command still fits before running it.")
	return frameRecall(b.String()), len(cmds) + len(files), nil
}

// orientCommand is the command without the way one machine reached it. Half
// these lines start "cd /Users/<name>/…; " because that is how an agent runs a
// command from somewhere else, and the prefix is both the longest part of the
// line and the part that means nothing to the reader.
func orientCommand(cmd string) string {
	cmd = strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")
	for {
		low := strings.ToLower(cmd)
		if !strings.HasPrefix(low, "cd ") {
			break
		}
		i := strings.IndexAny(cmd, ";&")
		if i < 0 || i+1 >= len(cmd) {
			break
		}
		cmd = strings.TrimSpace(strings.TrimLeft(cmd[i+1:], "&; "))
	}
	if len(cmd) > orientCommandMax {
		cmd = strings.TrimSpace(cmd[:orientCommandMax]) + "…"
	}
	return cmd
}

// orientPath is the path as the reader's editor would name it: relative to the
// directory the question was asked from, when it is under it.
func orientPath(p, cwd string) string {
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// orientFile is one path and how many sessions in the scope touched it.
type orientFile struct {
	Path     string
	Sessions int
	Last     time.Time
}

// orientFiles ranks what this project's sessions opened or edited. The paths
// come from tool inputs rather than from prose, which is the only reliable
// answer to which file a session was about (#542).
func orientFiles(dir string, projects []string, cwd string) []orientFile {
	pol := policy.Load()
	type acc struct {
		sessions map[string]bool
		last     time.Time
	}
	byPath := map[string]*acc{}
	_ = index.EachRecordOfRole(dir, sources.RoleFiles, func(meta index.SessionMeta, r index.Record) {
		if !howProjectMatches(meta.Project, projects) {
			return
		}
		if !pol.Allows(policy.ActivationMCP, meta.Project) || pol.Ignored(meta.Path, meta.Project) {
			return
		}
		for _, line := range strings.Split(r.Text, "\n") {
			p := strings.TrimSpace(line)
			if p == "" || len(p) > orientPathMax {
				continue
			}
			a := byPath[p]
			if a == nil {
				a = &acc{sessions: map[string]bool{}}
				byPath[p] = a
			}
			a.sessions[r.Key] = true
			if r.Time.After(a.last) {
				a.last = r.Time
			}
		}
	})
	out := make([]orientFile, 0, len(byPath))
	for p, a := range byPath {
		// One session opening a file says nothing about the project; the point
		// of the list is where work keeps landing.
		if len(a.sessions) < 2 {
			continue
		}
		out = append(out, orientFile{Path: orientPath(p, cwd), Sessions: len(a.sessions), Last: a.last})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Path < out[j].Path
	})
	return out
}
