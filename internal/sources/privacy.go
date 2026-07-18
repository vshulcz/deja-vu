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

func ExcludedProject(project string) bool {
	project = strings.ToLower(project)
	for _, pattern := range ExclusionPatterns() {
		if strings.Contains(project, pattern) {
			return true
		}
		if ok, _ := filepath.Match(pattern, project); ok {
			return true
		}
	}
	return false
}

func FilterSessions(ss []model.Session) []model.Session {
	if len(ExclusionPatterns()) == 0 {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if !ExcludedProject(s.Project) {
			out = append(out, s)
		}
	}
	return out
}
