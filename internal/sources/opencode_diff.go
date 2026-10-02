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
		if e.Patch == "" {
			continue
		}
		if IndexEdits() {
			for _, span := range unifiedDiffSpans(e.File, e.Patch) {
				s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: at})
			}
		}
		// The added side of the same diff. For most opencode sessions this
		// store is the only record of what they changed, and a commit that
		// adds a line has no replaced text to match at all (#3773).
		if IndexWrites() {
			if rec := WroteRecord(e.File, addedLinesOfUnifiedDiff(e.Patch)); rec != "" {
				s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: rec, Time: at})
			}
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

// addedLinesOfUnifiedDiff is the written side of a unified diff: the lines it
// adds, in order, for hashing under the file they went into.
func addedLinesOfUnifiedDiff(patch string) string {
	var added []string
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
			// A file header, not content.
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		}
	}
	return strings.Join(added, "\n")
}

// unifiedDiffSpans turns a unified diff into the "path\nreplaced bytes" records
// an edit produces everywhere else, so `deja restore` and `deja blame` do not
// have to know which harness wrote them. Only removed lines: the added side is
// hashed into a RoleWrote record instead, by addedLinesOfUnifiedDiff.
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

// withOpencodeDiffsFor is withOpencodeDiffs for sessions a pass read on their
// own: each looks up its own diff file rather than the pass listing them all.
// The index replaces a session it reads again from the database, so the
// session has to come back with what its diff gave it (#4207).
func withOpencodeDiffsFor(ss []model.Session) []model.Session {
	dir := OpencodeDiffDir()
	for i := range ss {
		if !strings.HasPrefix(ss[i].ID, "ses_") {
			continue
		}
		p := filepath.Join(dir, ss[i].ID+".json")
		if _, err := os.Stat(p); err != nil {
			continue
		}
		parsed, err := ParseOpencodeDiff(p)
		if err != nil {
			diagFileError(p, err)
			continue
		}
		for _, d := range parsed {
			ss[i].Messages = append(ss[i].Messages, d.Messages...)
		}
	}
	return ss
}

// ParseOpencodeDiffSession reads a changed diff file as its session: the
// database's copy of it, whole, with the diff folded in — what a full build
// holds for that id. Handed back on its own, the diff's records were added to
// the ones a full build had folded in, and a diff whose session the database
// no longer holds became a session with no conversation in it, which a full
// build does not keep (#4207).
func ParseOpencodeDiffSession(path string) ([]model.Session, error) {
	return ParseOpencodeDiffSessions([]string{path})
}

// opencodeDiffBatch bounds the ids one query names, well under SQLite's limit
// on the length of a statement.
const opencodeDiffBatch = 500

// ParseOpencodeDiffSessions is ParseOpencodeDiffSession for many files at once.
// Each read of the database also reads every session's parent and title, which
// is most of its cost: ~430 ms on a 3.8 GB store, paid once a file when a pass
// read them one by one.
func ParseOpencodeDiffSessions(paths []string) ([]model.Session, error) {
	var ids []string
	seen := map[string]bool{}
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		if !strings.HasPrefix(id, "ses_") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, "'"+sqlEscape(id)+"'")
	}
	var out []model.Session
	for len(ids) > 0 {
		n := min(len(ids), opencodeDiffBatch)
		ss, err := ParseOpencodeDBWhere(OpencodeDB(), " and s.id in ("+strings.Join(ids[:n], ",")+")", 0)
		if err != nil {
			return nil, err
		}
		out = append(out, ss...)
		ids = ids[n:]
	}
	return withOpencodeDiffsFor(out), nil
}

// parseOpencodeStore and parseOpencodeStoreSince are the database kind's
// reads: the sessions they hand back carry their diffs, as Load's do.
func parseOpencodeStore(db string) ([]model.Session, error) {
	ss, err := ParseOpencodeDB(db)
	return withOpencodeDiffsFor(ss), err
}

func parseOpencodeStoreSince(db string, t time.Time) ([]model.Session, error) {
	ss, err := ParseOpencodeDBSince(db, t)
	return withOpencodeDiffsFor(ss), err
}
