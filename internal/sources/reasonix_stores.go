package sources

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

// reasonixRootIsStore reports whether a root was pointed straight at a
// directory of sessions rather than at a state root.
func reasonixRootIsStore(root string) bool {
	for _, d := range []string{"sessions", "projects", "sessions-v4", "desktop-sessions-v5"} {
		if dirExists(filepath.Join(root, d)) {
			return false
		}
	}
	return true
}

// reasonixSessionDirs lists the directories under one root that hold JSONL
// transcripts: the global sessions/ and one per workspace.
func reasonixSessionDirs(root string) []string {
	dirs := []string{filepath.Join(root, "sessions")}
	projects, _ := filepath.Glob(filepath.Join(root, "projects", "*", "sessions"))
	dirs = append(dirs, projects...)
	if reasonixRootIsStore(root) {
		dirs = append(dirs, root)
	}
	return dirs
}

// reasonixV4StoreDirs lists the directories under one root whose children are
// 1.x session directories: the CLI's sessions-v4 roots (config/paths.go:440-446,
// 533-540) and the desktop's by-id root (paths.go:452-458).
func reasonixV4StoreDirs(root string) []string {
	dirs := []string{filepath.Join(root, "sessions-v4")}
	projects, _ := filepath.Glob(filepath.Join(root, "projects", "*", "sessions-v4"))
	dirs = append(dirs, projects...)
	dirs = append(dirs, filepath.Join(root, "desktop-sessions-v5", "by-id"))
	if reasonixRootIsStore(root) {
		dirs = append(dirs, root)
	}
	return dirs
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// reasonixSidecarSuffixes end in .jsonl and are not transcripts; Reasonix
// keeps the same list in internal/state/store/session.go.
var reasonixSidecarSuffixes = []string{
	".events.jsonl", ".conflicts.jsonl", ".guardian.jsonl", ".wire.jsonl",
	".adjudication.jsonl", ".execution.jsonl", ".turns.jsonl",
}

// isReasonixTranscriptName reports whether a file name is a conversation. A
// flat subagent-*.jsonl is a delegated worker's log, reached through its parent.
func isReasonixTranscriptName(name string) bool {
	if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "subagent-") {
		return false
	}
	for _, s := range reasonixSidecarSuffixes {
		if strings.HasSuffix(name, s) {
			return false
		}
	}
	return true
}

func reasonixTranscriptsIn(root string) []string {
	var out []string
	for _, dir := range reasonixSessionDirs(root) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && isReasonixTranscriptName(e.Name()) {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	return out
}

// reasonixV4SessionsIn lists the events.frames of every v4 session directory
// in a store. Dot entries are Reasonix's own caches, locks and content store.
func reasonixV4SessionsIn(store string) []string {
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(store, e.Name(), "events.frames")
		if fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

// reasonixCandidate is one copy of a session. rank orders the stores from
// newest to oldest: desktop v5, then v4, then JSONL.
type reasonixCandidate struct {
	path, id   string
	rank, root int
}

// reasonixDesktopSources maps the sources the desktop app imported to the
// session it imported them as (desktop/internal/workspacestate: State
// .SourceMappings, SourceMapping{path, sessionId}).
func reasonixDesktopSources(root string) map[string]string {
	var st struct {
		SourceMappings map[string]struct {
			Path      string `json:"path"`
			SessionID string `json:"sessionId"`
		} `json:"sourceMappings"`
	}
	b, err := os.ReadFile(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range st.SourceMappings {
		if m.Path != "" && m.SessionID != "" {
			out[filepath.Clean(m.Path)] = m.SessionID
		}
	}
	return out
}

// ReasonixSessionFiles lists one file per session across every store.
// Reasonix copies without deleting: the engine mirrors a JSONL transcript into
// sessions-v4 under the same id (cli/resume_catalog.go:224-229), a migration
// copies it under a new id and names the original in the manifest's source
// (session/migrate.go:310), and the desktop imports both into its own store and
// records the source in workspace-state-v1.json. The newest store's copy is
// read; the others are left for doctor to place.
func ReasonixSessionFiles() []string {
	key := func(id string) string { return id }
	if runtime.GOOS == "windows" {
		// Names are case-insensitive there: two spellings are one file.
		key = strings.ToLower
	}
	var cands []reasonixCandidate
	superseded := map[string]bool{}
	for ri, root := range ReasonixRoots() {
		v5 := map[string]bool{}
		for _, store := range reasonixV4StoreDirs(root) {
			rank := 1
			if filepath.Base(store) == "by-id" && filepath.Base(filepath.Dir(store)) == "desktop-sessions-v5" {
				rank = 2
			}
			for _, p := range reasonixV4SessionsIn(store) {
				dir := filepath.Dir(p)
				m, ok := readReasonixV4Manifest(dir)
				if !ok {
					continue
				}
				if rank == 2 {
					v5[filepath.Base(dir)] = true
				}
				if m.Source != nil && strings.HasSuffix(m.Source.Path, ".jsonl") {
					superseded[filepath.Clean(m.Source.Path)] = true
				}
				cands = append(cands, reasonixCandidate{path: p, id: filepath.Base(dir), rank: rank, root: ri})
			}
		}
		for src, id := range reasonixDesktopSources(root) {
			if v5[id] {
				superseded[src] = true
			}
		}
		for _, p := range reasonixTranscriptsIn(root) {
			cands = append(cands, reasonixCandidate{path: p, id: strings.TrimSuffix(filepath.Base(p), ".jsonl"), root: ri})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank > cands[j].rank
		}
		return cands[i].root < cands[j].root
	})
	// Within one store and root a repeated name is two sessions (two
	// workspaces can each hold one); across stores or roots it is a copy.
	type group struct{ rank, root int }
	taken := map[string]group{}
	var out []string
	for _, c := range cands {
		if superseded[filepath.Clean(c.path)] || (c.rank > 0 && superseded[filepath.Dir(c.path)]) {
			continue
		}
		g := group{c.rank, c.root}
		if prev, ok := taken[key(c.id)]; ok && prev != g {
			continue
		}
		taken[key(c.id)] = g
		out = append(out, c.path)
	}
	return out
}

// ReasonixSidecarFiles names everything else in the session directories —
// metadata, event logs, locks, manifests, offset indexes, subagent logs and
// the copies another store supersedes — so doctor places them instead of
// counting them as transcripts it failed on. Dot directories (.content-v1,
// .query-cache, .recovery-cache) are Reasonix's own and are not walked.
func ReasonixSidecarFiles() []string {
	read := map[string]bool{}
	for _, p := range ReasonixSessionFiles() {
		read[filepath.Clean(p)] = true
	}
	var out []string
	for _, dir := range ReasonixSessionDirsAll() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != dir && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && !read[filepath.Clean(p)] {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// ReasonixSessionDirsAll is every session directory doctor walks.
func ReasonixSessionDirsAll() []string {
	var out []string
	seen := map[string]bool{}
	for _, root := range ReasonixRoots() {
		for _, d := range append(reasonixSessionDirs(root), reasonixV4StoreDirs(root)...) {
			if d = filepath.Clean(d); !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// IsReasonixSession lets the registry claim a path for incremental ingest: a
// JSONL transcript, or a v4 session's events.frames, which is re-read whole.
func IsReasonixSession(p string) bool {
	if filepath.Base(p) == "events.frames" {
		session := filepath.Dir(p)
		if strings.HasPrefix(filepath.Base(session), ".") {
			return false
		}
		store := filepath.Dir(session)
		for _, root := range ReasonixRoots() {
			for _, d := range reasonixV4StoreDirs(root) {
				if store == filepath.Clean(d) {
					return true
				}
			}
		}
		return false
	}
	if !isReasonixTranscriptName(filepath.Base(p)) {
		return false
	}
	dir := filepath.Dir(p)
	for _, root := range ReasonixRoots() {
		for _, d := range reasonixSessionDirs(root) {
			if dir == filepath.Clean(d) {
				return true
			}
		}
	}
	return false
}

// ReasonixStore names the store a session file is in: "desktop", "global"
// (a sessions-v4 with no workspace), "project", or "jsonl".
func ReasonixStore(path string) string {
	if filepath.Base(path) != "events.frames" {
		return "jsonl"
	}
	store := filepath.Dir(filepath.Dir(path))
	switch {
	case filepath.Base(store) == "by-id" && filepath.Base(filepath.Dir(store)) == "desktop-sessions-v5":
		return "desktop"
	case reasonixV4Slug(path) != "":
		return "project"
	}
	return "global"
}

// reasonixV4Slug is <state>/projects/<slug> for a session under a workspace's
// sessions-v4, else "".
func reasonixV4Slug(path string) string {
	store := filepath.Dir(filepath.Dir(path))
	slug := filepath.Dir(store)
	if filepath.Base(store) == "sessions-v4" && filepath.Base(filepath.Dir(slug)) == "projects" {
		return slug
	}
	return ""
}

// reasonixWorkspaceSlug is Reasonix's config.WorkspaceSlug
// (config/paths.go:567-574): separators and colons to dashes, lower-cased on
// Windows, and past 255 bytes a cut with an FNV-1a hash of the whole
// (paths.go:585-597).
func reasonixWorkspaceSlug(abs string) string {
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	slug := strings.NewReplacer(string(os.PathSeparator), "-", "/", "-", "\\", "-", ":", "-").Replace(abs)
	if len(slug) <= 255 {
		return slug
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(slug))
	prefix := slug[:255-17]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return fmt.Sprintf("%s-%016x", prefix, h.Sum64())
}

// reasonixDesktopWorkspace is the workspace the desktop registry files a
// session under (desktop/internal/workspacestate/store.go:33-44).
func reasonixDesktopWorkspace(root, id string) string {
	var st struct {
		Workspaces map[string]struct {
			Root       string   `json:"root"`
			SessionIDs []string `json:"sessionIds"`
		} `json:"workspaces"`
	}
	b, err := os.ReadFile(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return ""
	}
	for _, w := range st.Workspaces {
		for _, sid := range w.SessionIDs {
			if sid == id {
				return w.Root
			}
		}
	}
	return ""
}

// reasonixV4HeaderCWD is the cwd in header.json, which the desktop app writes with the
// session's workspace (internal/session/header.go:31-38).
func reasonixV4HeaderCWD(dir string) string {
	var h struct {
		CWD string `json:"cwd"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "header.json")); err == nil && json.Unmarshal(b, &h) == nil {
		return strings.TrimSpace(h.CWD)
	}
	return ""
}

// reasonixV4Workspace is where a 1.x session was worked: header.json's cwd,
// which the desktop writes; else the workspace the host named in the session's
// context (contextWS); else, for a desktop session, the registry's.
func reasonixV4Workspace(path, contextWS string) string {
	dir := filepath.Dir(path)
	if ws := reasonixV4HeaderCWD(dir); ws != "" {
		return ws
	}
	if contextWS != "" {
		return contextWS
	}
	if ReasonixStore(path) == "desktop" {
		return reasonixDesktopWorkspace(filepath.Dir(filepath.Dir(filepath.Dir(dir))), filepath.Base(dir))
	}
	return ""
}

// reasonixV4ResumeDir is the directory `reasonix --resume <id>` finds a
// project session from. The CLI looks in the store named by the slug of its
// working directory (cli/cli.go:385-397), and the workspace the host records
// is the git root above it (boot/boot.go:2290-2301), so a candidate counts only
// when its slug is the store's.
func reasonixV4ResumeDir(path string) string {
	slugDir := reasonixV4Slug(path)
	if slugDir == "" {
		return ""
	}
	slug := filepath.Base(slugDir)
	tr, _, _, _ := loadReasonixV4(path)
	for _, ws := range []string{reasonixV4HeaderCWD(filepath.Dir(path)), tr.workspace, resolveEncodedPath(slug)} {
		if ws == "" {
			continue
		}
		got := reasonixWorkspaceSlug(ws)
		if got == slug || (runtime.GOOS == "windows" && strings.EqualFold(got, slug)) {
			return ws
		}
	}
	return ""
}
