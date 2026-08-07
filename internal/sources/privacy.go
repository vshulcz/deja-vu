package sources

import (
	"bufio"
	"os"
	"path/filepath"
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
		if ok, _ := filepath.Match(pattern, project); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, bare); ok {
			return true
		}
	}
	return false
}

func ExcludedProject(project string) bool { return NewExcluder().Match(project) }

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
