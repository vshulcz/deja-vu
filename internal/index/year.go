package index

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// YearWork is what the agents did over a window, counted in one pass over the
// records: how many of each work record fell inside it, and how many distinct
// files and commands those name.
//
// It needs its own pass for the same reason SpanInventory does — a files,
// command or edit record is served only when asked for by role, so none of
// them reach a caller loading sessions the ordinary way — and one pass rather
// than one per role because the log is the expensive part.
type YearWork struct {
	// Records is role to how many records of it are in the window.
	Records map[string]int `json:"records"`
	// Files and Commands are the distinct paths and command lines behind
	// those records: 40 records naming one file is one file.
	Files    int `json:"files"`
	Commands int `json:"commands"`
	// Spans and SpanFiles are the replaced text `deja restore` can hand back,
	// counted over the same window — SpanInventory counts the whole store.
	Spans     int `json:"spans"`
	SpanFiles int `json:"span_files"`
	// Undated is how many work records carry no time at all. Some stores keep
	// no per-message timestamp, so their records cannot be placed in a window
	// and are left out of every count above rather than assumed recent.
	Undated int `json:"undated"`
}

// ScanYearWork counts the work records at or after `from`. Records from a
// session the ignore rule covers are left out, so the figures match what the
// commands over the same material would answer.
func ScanYearWork(dir string, from time.Time) (YearWork, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	out := YearWork{Records: map[string]int{}}
	m, err := readManifestCached(dir)
	if err != nil {
		return out, err
	}
	pol := policy.Load()
	files := map[string]bool{}
	commands := map[string]bool{}
	spanFiles := map[string]bool{}
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		switch r.Role {
		case roleFiles, roleCommand, roleEdit, roleWrote:
		default:
			return
		}
		meta, ok := m.Sessions[r.Key]
		if !ok || pol.Ignored(meta.Path, meta.Project) {
			return
		}
		if r.Time.IsZero() {
			out.Undated++
			return
		}
		if r.Time.Before(from) {
			return
		}
		out.Records[r.Role]++
		switch r.Role {
		case roleFiles:
			// One record holds the paths of one turn, a path per line.
			for _, p := range strings.Split(r.Text, "\n") {
				if p = strings.TrimSpace(p); p != "" {
					files[p] = true
				}
			}
		case roleCommand:
			if c := strings.TrimSpace(r.Text); c != "" {
				commands[c] = true
			}
		case roleEdit:
			out.Spans++
			if i := strings.IndexByte(r.Text, '\n'); i > 0 {
				spanFiles[r.Text[:i]] = true
			}
		}
	})
	out.Files = len(files)
	out.Commands = len(commands)
	out.SpanFiles = len(spanFiles)
	return out, err
}
