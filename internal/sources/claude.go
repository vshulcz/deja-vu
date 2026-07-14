package sources

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/model"
)

func ClaudeRoot() string {
	return EnvPath("DEJA_CLAUDE_ROOT", filepath.Join(Home(), ".claude", "projects"))
}

func LoadClaude() []model.Session {
	root := ClaudeRoot()
	files := walkFiles(root, func(p string) bool { return strings.HasSuffix(p, ".jsonl") })
	return parseFiles(files, ParseClaudeFile)
}

func ParseClaudeFile(path string) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, 0)
}

func ParseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, offset)
}

func parseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: claudeProjectName(claudeProjectDir(path)), Path: path}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if typ != "user" && typ != "assistant" {
			return
		}
		if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		role := typ
		txt := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				role = r
			}
			txt = textFromContent(msg["content"])
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func claudeProjectDir(path string) string {
	root := ClaudeRoot()
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 1 && parts[0] != "" {
			return filepath.Join(root, parts[0])
		}
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		project := filepath.Dir(filepath.Dir(dir))
		if project != "." && project != string(filepath.Separator) {
			return project
		}
	}
	return dir
}

var projectNameCache sync.Map // encoded base -> display name

func claudeProjectName(dir string) string {
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "-"
	}
	if v, ok := projectNameCache.Load(base); ok {
		return v.(string)
	}
	name := decodeProjectBase(base)
	projectNameCache.Store(base, name)
	return name
}

// decodeProjectBase turns a Claude Code project dir name back into a display
// name. Claude encodes both "/" and "-" as "-", so "-Users-x-deja-vu" is
// ambiguous; resolving against the filesystem recovers hyphenated project
// names ("deja-vu", not "deja/vu"). Falls back to the dash heuristic when the
// path no longer exists (deleted projects, dirs imported from other machines).
func decodeProjectBase(base string) string {
	if resolved := resolveEncodedPath(base); resolved != "" {
		segs := strings.Split(strings.Trim(resolved, string(filepath.Separator)), string(filepath.Separator))
		if len(segs) >= 2 {
			return filepath.Join(segs[len(segs)-2], segs[len(segs)-1])
		}
		if len(segs) == 1 {
			return segs[0]
		}
	}
	parts := strings.Split(base, "-")
	var clean []string
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return base
	}
	if len(clean) == 1 {
		return clean[0]
	}
	return filepath.Join(clean[len(clean)-2], clean[len(clean)-1])
}

// resolveEncodedPath finds the real directory an encoded project name points
// at. Segments are re-joined with "/" or "-" and pruned by checking each
// completed directory prefix on disk.
func resolveEncodedPath(base string) string {
	if !strings.HasPrefix(base, "-") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(base, "-"), "-")
	if len(parts) == 0 || len(parts) > 24 {
		return ""
	}
	var try func(done, seg string, i int) string
	try = func(done, seg string, i int) string {
		if i == len(parts) {
			p := done + string(filepath.Separator) + seg
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return p
			}
			return ""
		}
		// close the current segment with "/" first (most path characters are
		// separators), pruning when the prefix does not exist
		p := done + string(filepath.Separator) + seg
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			if r := try(p, parts[i], i+1); r != "" {
				return r
			}
		}
		// or keep extending it with a literal hyphen
		return try(done, seg+"-"+parts[i], i+1)
	}
	if parts[0] == "" {
		return ""
	}
	return try("", parts[0], 1)
}

// ClaudeProjectName derives the display project name using the same rules as
// the Claude source parser.
func ClaudeProjectName(dir string) string { return claudeProjectName(dir) }
