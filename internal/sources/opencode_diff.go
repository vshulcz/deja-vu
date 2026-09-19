package sources

// opencode writes a second store beside its database, and deja had never read
// it: `storage/session_diff/ses_<id>.json`, one file per session, a list of the
// files that session changed.
//
//	[{"file": "internal/pool/pool.go", "status": "modified",
//	  "additions": 12, "deletions": 3, "patch": "@@ -40,3 +40,12 @@\n-…\n+…"}]
//
// Measured on one machine: 1,002 files, 428 entries, 163 of them carrying a
// patch, and 10,724 recorded additions against 1,814 deletions. Of 400 of those
// sessions, 384 are in the index and **17** hold any edit record — so for the
// rest this is the only account of what the session actually changed (#3791).
//
// The ids are the ones the database already uses, so what this parses merges
// into the session deja holds rather than arriving as a second copy of it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpencodeDiffDir is where those files live: beside the database, under
// `storage/session_diff`. Derived from the database path so every override —
// DEJA_OPENCODE_DB, XDG_DATA_HOME on Linux — carries over without a second
// variable to keep in step.
func OpencodeDiffDir() string {
	if p := os.Getenv("DEJA_OPENCODE_DIFFS"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(OpencodeDB()), "storage", "session_diff")
}

// OpencodeDiffFiles lists the per-session diff files, newest last.
func OpencodeDiffFiles() []string {
	dir := OpencodeDiffDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Only the session-keyed ones: the directory is opencode's and it may
		// grow files that are not a session's diff.
		if !strings.HasPrefix(e.Name(), "ses_") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// opencodeDiffEntry is one changed file, as opencode records it.
type opencodeDiffEntry struct {
	File      string `json:"file"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// ParseOpencodeDiff turns one of those files into the session's own record of
// what it changed. Empty lists are ordinary — most sessions change nothing —
// and return no session rather than an empty one.
func ParseOpencodeDiff(path string) ([]model.Session, error) {
	id := strings.TrimSuffix(filepath.Base(path), ".json")
	if !strings.HasPrefix(id, "ses_") {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []opencodeDiffEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		// A file being written while deja reads it, or a shape this build does
		// not know: not fatal for a store whose sessions come from elsewhere.
		return nil, nil
	}
	if len(entries) == 0 {
		return nil, nil
	}
	// The diff is written when the session ends, so the file's own time is the
	// closest thing to when the change happened. The database holds the real
	// timestamps and this session merges into it.
	at := time.Time{}
	if fi, err := os.Stat(path); err == nil {
		at = fi.ModTime()
	}

	s := model.Session{Harness: "opencode", ID: id, Path: path}
	var files []string
	for _, e := range entries {
		if e.File == "" {
			continue
		}
		files = append(files, e.File)
		if !IndexEdits() || e.Patch == "" {
			continue
		}
		for _, span := range unifiedDiffSpans(e.File, e.Patch) {
			s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: at})
		}
	}
	if IndexToolPaths() && len(files) > 0 {
		s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(files, " "), Time: at})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	s.Touch(at)
	return []model.Session{s}, nil
}

// unifiedDiffSpans turns a unified diff into the "path\nreplaced bytes" records
// an edit produces everywhere else, so `deja restore` and `deja blame` do not
// have to know which harness wrote them. Only removed lines: that is what the
// rest of the index stores, and what the added side is for is a decision of its
// own (#3773).
func unifiedDiffSpans(file, patch string) []string {
	var out []string
	var removed []string
	flush := func() {
		if len(removed) == 0 {
			return
		}
		span := strings.Join(removed, "\n")
		if len(span) > editSpanMax {
			span = span[:editSpanMax]
		}
		out = append(out, file+"\n"+span)
		removed = nil
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			// File headers, not content.
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		}
	}
	flush()
	return out
}
