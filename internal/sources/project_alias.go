package sources

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ProjectAliasPath is where a reader names a directory tree as one project.
//
// A project is otherwise the last two segments of the directory an agent ran
// in, which is right for a repository and wrong for work that spreads over
// many checkouts of one thing: a monorepo mounted in several places, or a
// worktree per task. Each of those became a project of its own, and recall
// scoped to "this project" never saw the others. One line per tree:
//
//	~/work/monorepo = work
//	~/work/worktrees = work
//
// Every directory under a listed path is that project, both when a session is
// indexed and when a hook or the MCP server asks where it stands. The longest
// matching path wins, so a subtree can still be named on its own.
func ProjectAliasPath() string {
	return filepath.Join(ConfigDir(), "projects")
}

type projectAlias struct{ root, name string }

// projectAliases is the parsed file. Each ask stats it and parses it again only
// when the stat says it changed, so an index pass does not reopen it per
// session and a long-lived `deja mcp` sees an edit without a restart.
var projectAliases struct {
	sync.Mutex
	path string
	mod  time.Time
	size int64
	list []projectAlias
}

func loadProjectAliases() []projectAlias {
	path := ProjectAliasPath()
	fi, err := os.Stat(path)
	projectAliases.Lock()
	defer projectAliases.Unlock()
	if err != nil {
		projectAliases.path, projectAliases.mod, projectAliases.size, projectAliases.list = path, time.Time{}, 0, nil
		return nil
	}
	if projectAliases.path == path && projectAliases.mod.Equal(fi.ModTime()) && projectAliases.size == fi.Size() {
		return projectAliases.list
	}
	list := readProjectAliases(path)
	projectAliases.path, projectAliases.mod, projectAliases.size, projectAliases.list = path, fi.ModTime(), fi.Size(), list
	return list
}

// readProjectAliases parses `<path> = <name>` lines. The last "=" splits them,
// since a path may hold one and a name has no reason to. Blank lines, comments,
// lines without a name and relative paths are skipped: a relative path would
// mean something different from every directory deja runs in.
func readProjectAliases(path string) []projectAlias {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []projectAlias
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, "=")
		if i < 0 {
			continue
		}
		root := expandTilde(strings.TrimSpace(line[:i]))
		name := strings.TrimSpace(line[i+1:])
		if name == "" || !filepath.IsAbs(root) {
			continue
		}
		out = append(out, projectAlias{root: filepath.Clean(root), name: name})
	}
	return out
}

// ProjectAliasFor is the project the alias file names for dir, if any.
func ProjectAliasFor(dir string) (string, bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", false
	}
	aliases := loadProjectAliases()
	if len(aliases) == 0 {
		return "", false
	}
	dir = filepath.Clean(dir)
	best, bestLen := "", -1
	for _, a := range aliases {
		if len(a.root) > bestLen && pathUnder(dir, a.root) {
			best, bestLen = a.name, len(a.root)
		}
	}
	return best, bestLen >= 0
}

// pathUnder reports whether dir is root or inside it, on cleaned paths. Windows
// paths compare without case, as the filesystem does.
func pathUnder(dir, root string) bool {
	if runtime.GOOS == "windows" {
		dir, root = strings.ToLower(dir), strings.ToLower(root)
	}
	if dir == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(dir, root)
}
