package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// AgentMemory is one thing an agent wrote down for itself about a project:
// a Claude Code auto-memory file today. Claude reads these at every session
// start and no other agent ever sees them, so what Claude learned about a
// project — "never add a Co-Authored-By trailer here" — is news to the Codex
// or Cursor session opened in the same directory an hour later.
type AgentMemory struct {
	Harness     string
	Name        string // the file's own slug
	Description string // the one line Claude indexes it by
	Type        string // user, feedback, project or reference
	Body        string // not redacted: see parseClaudeMemory
	Path        string
	Origin      string // the session that wrote it, when the file says
	Modified    time.Time
}

// claudeMemoryCap bounds one project's read. A memory directory is a few
// dozen files written by hand-sized edits; past this it is not one.
const claudeMemoryCap = 400

// ClaudeMemories reads Claude Code's auto-memory for the project at cwd,
// newest first. Only the project's own directory: the home directory's
// memory is about the person, not this code, and walking up to it put a
// hundred unrelated lines into every project.
//
// More than one directory names the same project from a worktree: Claude
// files memory under the checkout it was started in, so the caller passes
// the main checkout as well.
func ClaudeMemories(cwds ...string) []AgentMemory {
	var dirs []string
	seen := map[string]bool{}
	for _, cwd := range cwds {
		if cwd == "" {
			continue
		}
		slug := ClaudeEncodeProject(cwd)
		for _, root := range ClaudeRoots() {
			if d := filepath.Join(root, slug, "memory"); !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	var out []AgentMemory
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.EqualFold(name, "MEMORY.md") {
				continue
			}
			if len(out) >= claudeMemoryCap {
				break
			}
			p := filepath.Join(dir, name)
			b, err := os.ReadFile(p)
			if err != nil || len(b) > 64<<10 {
				continue
			}
			m := parseClaudeMemory(string(b))
			if m.Description == "" && m.Body == "" {
				continue
			}
			m.Harness, m.Path = "claude", p
			if m.Name == "" {
				m.Name = strings.TrimSuffix(name, ".md")
			}
			if m.Modified.IsZero() {
				if fi, err := e.Info(); err == nil {
					m.Modified = fi.ModTime()
				}
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// ClaudeEncodeProject is the directory name Claude Code files a project's
// transcripts and memory under: the absolute path with every character that
// is not a letter or digit turned into a dash.
func ClaudeEncodeProject(cwd string) string {
	var b strings.Builder
	for _, r := range filepath.ToSlash(filepath.Clean(cwd)) {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// parseClaudeMemory reads the frontmatter Claude writes — name, description
// and type at the top or under metadata — and keeps the rest as the body.
// The text is redacted like everything else deja hands on.
func parseClaudeMemory(s string) AgentMemory {
	var m AgentMemory
	s = strings.ReplaceAll(s, "\r\n", "\n")
	body := s
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			head := s[4 : 4+end]
			body = s[4+end+4:]
			for _, ln := range strings.Split(head, "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(ln), ":")
				if !ok {
					continue
				}
				v = unquoteYAML(strings.TrimSpace(v))
				switch strings.TrimSpace(k) {
				case "name":
					m.Name = v
				case "description":
					m.Description = v
				case "type":
					m.Type = v
				case "originSessionId":
					m.Origin = v
				case "modified":
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						m.Modified = t
					}
				}
			}
		}
	}
	m.Body = strings.TrimSpace(body)
	// The body is redacted by whoever shows it: a session start reads only
	// the descriptions, and redacting forty bodies it never sends was most of
	// what this read cost.
	m.Description, _ = redact.Text(m.Description)
	return m
}

func unquoteYAML(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if u, err := strconv.Unquote(v); err == nil {
			return u
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}
