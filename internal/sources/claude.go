package sources

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ClaudeConfigDir is the directory Claude Code keeps its state in
// (settings.json, projects/, ...). Claude Code lets users relocate it with the
// CLAUDE_CONFIG_DIR environment variable — common when juggling separate
// personal/work profiles — so we honor the same variable to read from and
// install into whichever profile is active. Falls back to the default
// ~/.claude.
func ClaudeConfigDir() string {
	return EnvPath("CLAUDE_CONFIG_DIR", filepath.Join(Home(), ".claude"))
}

// ClaudeJSONPath is Claude Code's top-level state file (holds mcpServers). By
// default it lives at ~/.claude.json, a sibling of ~/.claude, but when
// CLAUDE_CONFIG_DIR is set Claude Code moves it inside that directory.
func ClaudeJSONPath() string {
	return filepath.Join(EnvPath("CLAUDE_CONFIG_DIR", Home()), ".claude.json")
}

func ClaudeRoot() string {
	return EnvPath("DEJA_CLAUDE_ROOT", filepath.Join(ClaudeConfigDir(), claudeProjectsDirName()))
}

// claudeProjectsDirName is what Claude Code calls the directory it keeps
// transcripts in. CLAUDE_CODE_PROJECT_DIR_NAME is documented and renames it;
// deja hard-coded "projects" and read nothing on a machine that set it (#2996).
func claudeProjectsDirName() string {
	if name := strings.TrimSpace(os.Getenv("CLAUDE_CODE_PROJECT_DIR_NAME")); name != "" {
		return name
	}
	return "projects"
}

// ClaudeRoots is every directory Claude Code transcripts are written to on this
// machine, not only the main one:
//
//   - <config>/<projects>, the ordinary store;
//   - <config>/transcripts, a sibling where headless and SDK-driven clients
//     write bare transcripts;
//   - ~/.cc-mirror/<variant>/.claude/<projects>, the isolated variants
//     cc-mirror runs — a session run through one reached nothing before.
//
// DEJA_CLAUDE_ROOT stays the whole answer when it is set: it is how a stand
// pins the store, and adding the machine's own directories to it would let the
// real history into an isolated run.
func ClaudeRoots() []string {
	if p := os.Getenv("DEJA_CLAUDE_ROOT"); p != "" {
		return []string{p}
	}
	cfg := ClaudeConfigDir()
	roots := []string{filepath.Join(cfg, claudeProjectsDirName())}
	roots = append(roots, filepath.Join(cfg, "transcripts"))
	roots = append(roots, ccMirrorRoots()...)
	var out []string
	for _, r := range roots {
		if fi, err := os.Stat(r); err == nil && fi.IsDir() {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		// Nothing exists yet: the main root is still the answer, so a fresh
		// machine reports the path it will use rather than none at all.
		return roots[:1]
	}
	return out
}

// ccMirrorRoots lists the transcript directories of cc-mirror's isolated
// variants. Each variant is its own Claude home under ~/.cc-mirror/<variant>,
// so a session run through one is invisible to a reader of ~/.claude.
func ccMirrorRoots() []string {
	base := EnvPath("DEJA_CC_MIRROR_ROOT", filepath.Join(Home(), ".cc-mirror"))
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(base, e.Name(), ".claude", claudeProjectsDirName()))
	}
	return out
}

func LoadClaude() []model.Session {
	return parseFiles(ClaudeFiles(), ParseClaudeFile)
}

// ClaudeFiles lists the transcript files under the Claude root without parsing
// them — a cheap count for diagnostics.
func ClaudeFiles() []string {
	var out []string
	for _, root := range ClaudeRoots() {
		out = append(out, walkFiles(root, ClaudeFileWanted)...)
	}
	return out
}

// UnderClaudeRoot reports whether a path is inside any of the roots above. The
// registry matches a transcript to its harness by prefix, and with more than
// one root that question is no longer "does it start with ClaudeRoot()".
func UnderClaudeRoot(p string) bool {
	for _, root := range ClaudeRoots() {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}

// ClaudeFileWanted reports whether a path under the Claude root belongs in
// the index. A subagent's transcript is not a copy of its parent: the parent
// keeps the launch, the agent id and a summary of what came back, while the
// turns and the tool stream live only in the child file (#1384).
//
// They used to be skipped whole, for index size. Measured on a real store that
// was 863 files, 96,168 messages and 299 MB against a 1.4 GB index — a quarter
// of the machine's Claude messages, holding 387 assistant turns that read like
// a settled answer, outside recall by default (#3009). They come in now as the
// task they were given and the answer they came back with; the reasoning and
// the tool stream in between stay out, which is where the volume is.
//
// DEJA_INCLUDE_SUBAGENTS=1 takes the whole child transcript, as before.
// DEJA_INCLUDE_SUBAGENTS=0 goes back to skipping them entirely.
func ClaudeFileWanted(p string) bool {
	if !strings.HasSuffix(p, ".jsonl") {
		return false
	}
	if os.Getenv("DEJA_INCLUDE_SUBAGENTS") == "0" {
		return !IsSubagentPath(p)
	}
	return true
}

// IsSubagentPath reports whether a transcript is a child run, by where it sits.
// The file's own isSidechain flag says the same thing for the files that carry
// it, and a sidechain transcript outside such a directory is a full session as
// far as this rule is concerned — the same reading the count in #3009 used.
func IsSubagentPath(p string) bool {
	return strings.Contains(p, string(filepath.Separator)+"subagents"+string(filepath.Separator))
}

// SubagentTailKept is how many of a child's own turns are indexed: the task it
// was handed and the last few things it said. Four is what covers a closing
// answer split across a couple of turns without taking the tool stream with it.
const SubagentTailKept = 4

// KeepSubagentTail cuts a child run down to what a reader needs from it: the
// first turn, which is the task the parent handed over, and the last few, which
// are what it concluded. The middle is the reading and the searching — verbose,
// often duplicated in the parent's summary, and the reason these files were
// skipped whole.
func KeepSubagentTail(ms []model.Message) []model.Message {
	if len(ms) <= SubagentTailKept+1 {
		return ms
	}
	kept := make([]model.Message, 0, SubagentTailKept+1)
	kept = append(kept, ms[0])
	return append(kept, ms[len(ms)-SubagentTailKept:]...)
}

func ParseClaudeFile(path string) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, 0)
}

func ParseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, offset)
}

func parseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeTypedFromOffset(path, offset)
}

// parseClaudeGenericFromOffset is the previous implementation, kept as the
// reference the typed parser is proved against — including on a real store,
// where the shapes nobody thought of live.
func parseClaudeGenericFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: claudeProjectName(claudeProjectDir(path)), Path: path}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if typ != "user" && typ != "assistant" {
			return
		}
		if meta, _ := m["isMeta"].(bool); meta && typ == "user" {
			// The harness's own user-role record (#3267); the typed parser
			// skips it the same way.
			return
		}
		side, _ := m["isSidechain"].(bool)
		agent, _ := m["agentId"].(string)
		if side && agent != "" {
			s.ID = agent
			s.Kind = "sidechain"
			s.Parent, _ = m["sessionId"].(string)
			if name, _ := m["attributionAgent"].(string); name != "" {
				s.Agent = name
			}
		} else if id, _ := m["sessionId"].(string); id != "" {
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
			var toolOut bool
			txt, toolOut = textFromContentKind(msg["content"])
			if toolOut {
				role = RoleToolOutput
			}
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
		if msg, ok := m["message"].(map[string]any); ok {
			if IndexToolPaths() {
				if p := toolPathsFromContent(msg["content"]); p != "" {
					s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexEdits() {
				for _, e := range editSpansFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: e, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range commandsFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd, Time: t})
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	// The same cut the typed parser makes: this is the reference it is proved
	// against, and a difference here reads as a parser bug (#3009).
	if IsSubagentPath(path) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
		s.Messages = KeepSubagentTail(s.Messages)
	}
	if p := projectFromPaths(s.Messages); p != "" {
		s.Project = p
	}
	return []model.Session{s}, err
}

func claudeProjectDir(path string) string {
	return projectDir(ClaudeRoot(), path)
}

func projectDir(root, path string) string {
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
			return projectSegments(segs[len(segs)-2], segs[len(segs)-1])
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
	return projectSegments(clean[len(clean)-2], clean[len(clean)-1])
}

// resolveEncodedPath finds the real directory an encoded project name points
// at. Segments are re-joined with "/" or "-" and pruned by checking each
// completed directory prefix on disk.
func resolveEncodedPath(base string) string {
	// Windows encodes the drive root too: C:\Users\x\app becomes
	// "C--Users-x-app". Peel the drive off and let the shared segment walk
	// resolve the rest from "C:" instead of from an empty root.
	root := ""
	if drive, rest, ok := splitEncodedWindowsDrive(base); ok {
		root, base = drive, rest
	} else if !strings.HasPrefix(base, "-") {
		return ""
	}
	// pi uses "--" prefix/suffix (e.g. --Users-x-app--), Claude uses
	// single "-" prefix. Strip both forms to get the segment list.
	trimmed := strings.TrimPrefix(base, "-")
	trimmed = strings.TrimPrefix(trimmed, "-")
	trimmed = strings.TrimSuffix(trimmed, "-")
	trimmed = strings.TrimSuffix(trimmed, "-")
	parts := strings.Split(trimmed, "-")
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
	return try(root, parts[0], 1)
}

// splitEncodedWindowsDrive recognises the "C--Users-x-app" form Claude Code
// writes on Windows and returns the drive root ("C:") plus the remainder in
// the unix-style encoding the resolver already understands ("-Users-x-app").
func splitEncodedWindowsDrive(base string) (root, rest string, ok bool) {
	if len(base) < 4 || base[1] != '-' || base[2] != '-' {
		return "", "", false
	}
	c := base[0]
	if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
		return "", "", false
	}
	return string(c) + ":", base[2:], true
}

// ClaudeProjectName derives the display project name using the same rules as
// the Claude source parser.
func ClaudeProjectName(dir string) string { return claudeProjectName(dir) }

// ClaudeProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "-Users-x-projects-app" for .../projects/-Users-x-projects-app/s.jsonl.
func ClaudeProjectDirBase(path string) string {
	dir := claudeProjectDir(path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// ResolveEncodedPath maps an encoded project dir name back to the real
// directory when it still exists on disk; empty string otherwise.
func ResolveEncodedPath(base string) string { return resolveEncodedPath(base) }
