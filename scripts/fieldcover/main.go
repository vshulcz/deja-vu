// Command fieldcover says, for every harness deja reads, how much of what the
// client recorded survives the parser: the share of sessions that carry a user
// turn, an assistant turn, tool output, commands, files, edits, a project and
// timestamps, and how long the load took.
//
// A parser that stops seeing one record type after a client update keeps
// returning sessions, so search still works and nothing fails; the share of
// sessions with commands or edits just drops to zero. This is the number that
// moves when that happens. It only reads the stores, through the same loaders
// the index uses, and never opens the index.
//
//	go run ./scripts/fieldcover            # table, this HOME's stores
//	go run ./scripts/fieldcover -json      # for diffing two runs
//	go run ./scripts/fieldcover DIR...     # each DIR read as a HOME (#4161)
//
// A DIR is a home directory the way the clients lay it out, a fixture tree or
// a copy of a store. The loaders find their stores from HOME, the XDG and
// Windows profile variables, and a variable of their own per client; under a
// DIR the first are pointed into it and the client's own are cleared, so a
// DEJA_CLAUDE_ROOT left in the shell does not answer for the DIR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

var roles = []string{"user", "assistant", sources.RoleToolOutput, sources.RoleCommand, sources.RoleFiles, sources.RoleEdit, sources.RoleWrote}

type row struct {
	Root     string             `json:"root,omitempty"`
	Harness  string             `json:"harness"`
	Sessions int                `json:"sessions"`
	Messages int                `json:"messages"`
	Share    map[string]float64 `json:"share"`
	LoadMS   int64              `json:"load_ms"`
}

func main() {
	asJSON := flag.Bool("json", false, "print JSON")
	only := flag.String("harness", "", "one harness")
	flag.Parse()
	var rows []row
	if flag.NArg() == 0 {
		rows = cover("", *only)
	}
	for _, dir := range flag.Args() {
		abs, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "fieldcover: %s is not a directory\n", dir)
			os.Exit(2)
		}
		restore := useHome(abs)
		rows = append(rows, cover(abs, *only)...)
		restore()
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	cols := append(append([]string{}, roles...), "project", "time")
	for i, r := range rows {
		if i == 0 || r.Root != rows[i-1].Root {
			if i > 0 {
				fmt.Println()
			}
			if r.Root != "" {
				fmt.Println(r.Root)
			}
			fmt.Printf("%-14s %6s %7s", "harness", "sess", "msgs")
			for _, k := range cols {
				fmt.Printf(" %8s", k)
			}
			fmt.Printf(" %7s\n", "load")
		}
		fmt.Printf("%-14s %6d %7d", r.Harness, r.Sessions, r.Messages)
		for _, k := range cols {
			if r.Sessions == 0 {
				fmt.Printf(" %8s", "-")
			} else {
				fmt.Printf(" %7.0f%%", 100*r.Share[k])
			}
		}
		fmt.Printf(" %6dms\n", r.LoadMS)
	}
}

// cover runs every registered loader against the stores the environment
// points at and counts, per harness, the share of sessions carrying each role.
func cover(root, only string) []row {
	var rows []row
	for _, h := range sources.AllHarnesses() {
		if only != "" && h.Name != only {
			continue
		}
		t := time.Now()
		ss := h.Load()
		r := row{Root: root, Harness: h.Name, Sessions: len(ss), Share: map[string]float64{}, LoadMS: time.Since(t).Milliseconds()}
		has := map[string]int{}
		for _, s := range ss {
			r.Messages += len(s.Messages)
			seen := map[string]bool{}
			stamped := false
			for _, m := range s.Messages {
				seen[m.Role] = true
				if !m.Time.IsZero() {
					stamped = true
				}
			}
			for _, k := range roles {
				if seen[k] {
					has[k]++
				}
			}
			if s.Project != "" {
				has["project"]++
			}
			if stamped || !s.Started.IsZero() {
				has["time"]++
			}
		}
		for _, k := range append(append([]string{}, roles...), "project", "time") {
			if r.Sessions > 0 {
				r.Share[k] = float64(has[k]) / float64(r.Sessions)
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Sessions > rows[j].Sessions })
	return rows
}

// storeVar is a variable that moves a client's store somewhere else:
// DEJA_CLAUDE_ROOT, CODEX_HOME, DEJA_OPENCODE_DB, CLINE_DIR and the like.
var storeVar = regexp.MustCompile(`_(HOME|ROOT|ROOTS|DB|DSN|DIR|DIR_NAME|FILE|PATH|CONFIG|DIFFS)$`)

// useHome points the process at dir as its home and returns what puts the
// environment back.
func useHome(dir string) func() {
	saved := os.Environ()
	for _, kv := range saved {
		name, _, _ := strings.Cut(kv, "=")
		if storeVar.MatchString(name) {
			_ = os.Unsetenv(name)
		}
	}
	for name, v := range map[string]string{
		"HOME":            dir,
		"USERPROFILE":     dir,
		"XDG_CONFIG_HOME": filepath.Join(dir, ".config"),
		"XDG_DATA_HOME":   filepath.Join(dir, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(dir, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(dir, ".cache"),
		"APPDATA":         filepath.Join(dir, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(dir, "AppData", "Local"),
	} {
		_ = os.Setenv(name, v)
	}
	return func() {
		os.Clearenv()
		for _, kv := range saved {
			name, v, _ := strings.Cut(kv, "=")
			_ = os.Setenv(name, v)
		}
	}
}
