package sources

import (
	"bufio"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ExcludePath is the primary privacy configuration, kept outside the cache.
func ExcludePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(Home(), ".config")
	}
	return filepath.Join(base, "deja", "exclude")
}

func ExclusionPatterns() []string {
	var out []string
	read := func(path string) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, strings.ToLower(line))
			}
		}
	}
	read(ExcludePath())
	for _, pattern := range strings.Split(os.Getenv("DEJA_EXCLUDE_PROJECTS"), ",") {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			out = append(out, strings.ToLower(pattern))
		}
	}
	return out
}

// Excluder answers exclusion questions from patterns read once. Sync imports
// ask per record, and re-opening the exclude file for each one costs seconds
// on a large batch.
type Excluder struct{ patterns []string }

func NewExcluder() Excluder { return Excluder{patterns: ExclusionPatterns()} }

func (e Excluder) Empty() bool { return len(e.patterns) == 0 }

func (e Excluder) Match(project string) bool {
	// Sync-imported sessions are stored as "imported:<project>"; a glob
	// written for the local name must still match after the trip.
	project = strings.ToLower(project)
	bare := strings.TrimPrefix(project, "imported:")
	for _, pattern := range e.patterns {
		if strings.Contains(project, pattern) {
			return true
		}
		// path.Match, not filepath.Match: a project name is a "/"-separated
		// logical name, not a host path, so a glob must match the same way
		// wherever deja runs — filepath.Match reads "\\" as the separator on
		// windows and would not glob a "/"-joined name there.
		if ok, _ := path.Match(pattern, project); ok {
			return true
		}
		if ok, _ := path.Match(pattern, bare); ok {
			return true
		}
	}
	return false
}

func ExcludedProject(project string) bool { return NewExcluder().Match(project) }

// ExclusionFingerprint identifies the pattern set in force, so an index can
// record which one it was built under.
//
// The patterns only ever ran at ingest, and nothing noticed when they changed:
// setting one and running `deja index` applied nothing and said nothing, while
// the project stayed searchable and kept going out over sync (#1307). Order and
// case do not matter — the same set written differently is the same set, and a
// reshuffled exclude file is not a reason to tell someone to rebuild.
func ExclusionFingerprint() string {
	patterns := ExclusionPatterns()
	// "none" rather than an empty string: an index built with no exclusions has
	// recorded that fact, and a manifest written before this existed has not.
	// Collapsing the two would have kept the note silent in the ordinary case —
	// index first, decide a project is private afterwards.
	if len(patterns) == 0 {
		return "none"
	}
	sorted := append([]string(nil), patterns...)
	sort.Strings(sorted)
	h := fnv.New64a()
	for _, p := range sorted {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// CountSessions reports how many conversations a slice of parsed transcripts
// amounts to. The index keys a session on harness+id, so two transcripts that
// carry one id are one session there — a harness mid-migration writes the same
// conversation to both its old jsonl store and its new sqlite one, and a
// resumed run continues an id in a second file. Counting records instead makes
// a store read as bigger than what deja holds.
func CountSessions(ss []model.Session) int {
	seen := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		seen[s.Harness+":"+s.ID] = struct{}{}
	}
	return len(seen)
}

func FilterSessions(ss []model.Session) []model.Session {
	ex := NewExcluder()
	if ex.Empty() {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if !ex.Match(s.Project) {
			out = append(out, s)
		}
	}
	return out
}
