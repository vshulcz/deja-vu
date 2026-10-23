package sources

import (
	"bufio"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
// session and a long-lived `deja mcp` sees an edit without a restart. The
// unchanged case reads a snapshot without a lock: a build asks from every core.
var (
	projectAliases       atomic.Pointer[aliasSnapshot]
	projectAliasesReload sync.Mutex
)

type aliasSnapshot struct {
	path string
	mod  time.Time
	size int64 // -1: no file
	list []projectAlias
}

func loadProjectAliases() []projectAlias {
	path := ProjectAliasPath()
	mod, size := time.Time{}, int64(-1)
	if fi, err := os.Stat(path); err == nil {
		mod, size = fi.ModTime(), fi.Size()
	}
	if s := projectAliases.Load(); s != nil && s.path == path && s.size == size && s.mod.Equal(mod) {
		if len(s.list) > 0 {
			// The cached names carry the exclude-first decision too, so an
			// exclude edit drops them like an alias edit does.
			aliasExcluder()
		}
		return s.list
	}
	projectAliasesReload.Lock()
	defer projectAliasesReload.Unlock()
	old := projectAliases.Load()
	if old != nil && old.path == path && old.size == size && old.mod.Equal(mod) {
		return old.list
	}
	var list []projectAlias
	if size >= 0 {
		list = readProjectAliases(path)
	}
	if old != nil && !sameAliases(list, old.list) {
		forgetProjectNames()
	}
	projectAliases.Store(&aliasSnapshot{path: path, mod: mod, size: size, list: list})
	return list
}

func sameAliases(a, b []projectAlias) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// forgetProjectNames drops the folder names worked out under the old file, so
// a long-lived process does not keep naming a folder after an alias that is
// gone.
func forgetProjectNames() {
	for _, m := range []*sync.Map{&projectNameCache, &claudeCWDNameCache} {
		m.Range(func(k, _ any) bool {
			m.Delete(k)
			return true
		})
	}
}

// ProjectAliasFingerprint identifies the aliases in force, so an index can
// record which ones it was built under: they apply when a session is indexed,
// and an edit renames nothing already in the index until a rebuild. "none"
// with no file or no valid line; comments and line order do not count.
func ProjectAliasFingerprint() string {
	list := loadProjectAliases()
	if len(list) == 0 {
		return "none"
	}
	lines := make([]string, len(list))
	for i, a := range list {
		lines[i] = a.root + "\x00" + a.name
	}
	sort.Strings(lines)
	h := fnv.New64a()
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// ProjectAliasCount is how many trees the file names.
func ProjectAliasCount() int { return len(loadProjectAliases()) }

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
//
// The exclude file wins: a directory whose own name an exclude pattern matches
// keeps that name, so the pattern still takes it out. Renamed to the tree's
// alias it would slip past a rule written before the alias was.
func ProjectAliasFor(dir string) (string, bool) {
	name, ok := aliasFor(dir)
	if ok && aliasExcluder().Match(plainCWDProjectName(dir)) {
		return "", false
	}
	return name, ok
}

func aliasFor(dir string) (string, bool) {
	// A file URI's path keeps a slash before the drive, /c:/Users/x.
	if runtime.GOOS == "windows" && len(dir) >= 3 && (dir[0] == '/' || dir[0] == '\\') && dir[2] == ':' {
		dir = dir[1:]
	}
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

// aliasExcluder is the exclude patterns, read again only when the file or the
// variable changes: an aliased directory asks once per session indexed.
func aliasExcluder() Excluder {
	key := excludeKey{path: ExcludePath(), env: os.Getenv("DEJA_EXCLUDE_PROJECTS"), size: -1}
	if fi, err := os.Stat(key.path); err == nil {
		key.mod, key.size = fi.ModTime(), fi.Size()
	}
	if c := aliasExclude.Load(); c != nil && c.key.path == key.path && c.key.env == key.env && c.key.size == key.size && c.key.mod.Equal(key.mod) {
		return c.ex
	}
	ex := NewExcluder()
	if aliasExclude.Swap(&cachedExcluder{key: key, ex: ex}) != nil {
		forgetProjectNames()
	}
	return ex
}

type excludeKey struct {
	path, env string
	mod       time.Time
	size      int64
}

type cachedExcluder struct {
	key excludeKey
	ex  Excluder
}

var aliasExclude atomic.Pointer[cachedExcluder]
