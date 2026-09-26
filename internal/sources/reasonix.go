package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Reasonix (esengine/DeepSeek-Reasonix) keeps one transcript per session, one
// message per line with no envelope, in two places under its state root:
//
//	<state>/sessions/<id>.jsonl                   sessions with no workspace
//	<state>/projects/<slug>/sessions/<id>.jsonl   everything else
//
// The state root is $REASONIX_STATE_HOME, else $REASONIX_HOME, else a
// `[storage] state` entry in <home>/config.toml, else the home itself:
// %APPDATA%\reasonix on Windows, ~/.reasonix elsewhere
// (internal/contract/config/storage_roots.go). The slug is the workspace path
// with separators and colons turned into dashes, the same fold
// pathToProjectKey makes.
//
// A message carries `createdAt` in unix milliseconds on the turns the host
// stamps, and `<id>.jsonl.meta` beside it holds created_at, updated_at, the
// workspace_root and the titles. The v0.x builds wrote the same line shape
// with OpenAI's nested tool_calls, a `<id>.meta.json` holding only the
// workspace and a summary, and the clock in `<id>.events.jsonl` as `ts`.
// Reasonix copies those into its current store without touching the
// originals, so a legacy file is read only when no current one has its id.

// reasonixHome is where config.toml lives: $REASONIX_HOME, else the OS default.
func reasonixHome() string {
	if v := os.Getenv("REASONIX_HOME"); v != "" {
		return expandReasonixDir(v)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(windowsAppData(), "reasonix")
	}
	return filepath.Join(Home(), ".reasonix")
}

func windowsAppData() string {
	if app := os.Getenv("APPDATA"); app != "" {
		return app
	}
	return filepath.Join(Home(), "AppData", "Roaming")
}

// ReasonixStateRoot is the directory holding sessions/ and projects/.
func ReasonixStateRoot() string {
	if v := os.Getenv("REASONIX_STATE_HOME"); v != "" {
		return expandReasonixDir(v)
	}
	home := reasonixHome()
	if dir := reasonixConfiguredState(filepath.Join(home, "config.toml")); dir != "" {
		return dir
	}
	return home
}

// ReasonixRoot is the state root deja reads. DEJA_REASONIX_ROOT replaces it,
// and may also name a sessions directory itself.
func ReasonixRoot() string {
	return EnvPath("DEJA_REASONIX_ROOT", ReasonixStateRoot())
}

// reasonixConfiguredState reads `state = "<dir>"` from the [storage] table,
// which is how a user relocates the state root from the app. Only that key is
// read; a config that does not parse this way leaves the default in place.
func reasonixConfiguredState(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inStorage := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inStorage = line == "[storage]"
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !inStorage || !ok || strings.TrimSpace(key) != "state" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) < 2 || (val[0] != '"' && val[0] != '\'') || val[len(val)-1] != val[0] {
			return ""
		}
		return expandReasonixDir(val[1 : len(val)-1])
	}
	return ""
}

func expandReasonixDir(dir string) string {
	dir = os.ExpandEnv(strings.TrimSpace(dir))
	if dir == "~" {
		return Home()
	}
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		return filepath.Join(Home(), dir[2:])
	}
	return filepath.Clean(dir)
}

// reasonixLegacyRoots are the pre-relocation roots Reasonix still imports
// from: ~/.reasonix where it is not the home (Windows), and the OS config
// directory the Go rewrite used first. A pinned home or state root is an
// isolation boundary in Reasonix's own importer, and deja respects it too.
func reasonixLegacyRoots() []string {
	if os.Getenv("DEJA_REASONIX_ROOT") != "" || os.Getenv("REASONIX_HOME") != "" || os.Getenv("REASONIX_STATE_HOME") != "" {
		return nil
	}
	cands := []string{filepath.Join(Home(), ".reasonix")}
	if runtime.GOOS == "darwin" {
		cands = append(cands, filepath.Join(Home(), "Library", "Application Support", "reasonix"))
	}
	if runtime.GOOS != "windows" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			cands = append(cands, filepath.Join(x, "reasonix"))
		}
		cands = append(cands, filepath.Join(Home(), ".config", "reasonix"))
	}
	current := filepath.Clean(ReasonixRoot())
	seen := map[string]bool{current: true}
	var out []string
	for _, c := range cands {
		c = filepath.Clean(c)
		if seen[c] {
			continue
		}
		seen[c] = true
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// ReasonixRoots is the current root and every legacy root still on disk.
func ReasonixRoots() []string {
	return append([]string{ReasonixRoot()}, reasonixLegacyRoots()...)
}

// reasonixSessionDirs lists the directories under one root that hold
// transcripts: the global sessions/ and one per workspace.
func reasonixSessionDirs(root string) []string {
	dirs := []string{filepath.Join(root, "sessions")}
	projects, _ := filepath.Glob(filepath.Join(root, "projects", "*", "sessions"))
	dirs = append(dirs, projects...)
	if !dirExists(dirs[0]) && !dirExists(filepath.Join(root, "projects")) {
		// Pointed straight at a sessions directory.
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

// ReasonixSessionFiles lists the transcripts: the current root's, then each
// legacy root's whose id the current root does not already hold.
func ReasonixSessionFiles() []string {
	out := reasonixTranscriptsIn(ReasonixRoot())
	// Windows names are case-insensitive: the same session under two
	// spellings is one file there, and must be read once.
	key := filepath.Base
	if runtime.GOOS == "windows" {
		key = func(p string) string { return strings.ToLower(filepath.Base(p)) }
	}
	have := map[string]bool{}
	for _, p := range out {
		have[key(p)] = true
	}
	for _, root := range reasonixLegacyRoots() {
		for _, p := range reasonixTranscriptsIn(root) {
			if !have[key(p)] {
				have[key(p)] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// ReasonixSidecarFiles names everything else in the session directories —
// metadata, event logs, locks, context and recovery state, subagent logs — so
// doctor places them instead of counting them as transcripts it failed on.
func ReasonixSidecarFiles() []string {
	var out []string
	for _, root := range ReasonixRoots() {
		for _, dir := range reasonixSessionDirs(root) {
			out = append(out, walkFiles(dir, func(p string) bool {
				return filepath.Dir(p) != filepath.Clean(dir) || !isReasonixTranscriptName(filepath.Base(p))
			})...)
		}
	}
	return out
}

// ReasonixSessionDirsAll is every session directory doctor walks.
func ReasonixSessionDirsAll() []string {
	var out []string
	for _, root := range ReasonixRoots() {
		out = append(out, reasonixSessionDirs(root)...)
	}
	return out
}

// IsReasonixSession lets the registry claim a path for incremental ingest.
func IsReasonixSession(p string) bool {
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

func LoadReasonix() []model.Session {
	return parseFiles(ReasonixSessionFiles(), ParseReasonixFile)
}

// reasonixDialect is what Reasonix calls its tools (internal/tools/builtin).
var reasonixDialect = toolDialect{
	pathKey: "path",
	pathTools: map[string]bool{
		"read_file": true, "write_file": true, "edit_file": true, "multi_edit": true,
	},
	shellTool: "bash",
	editTools: map[string]bool{"edit_file": true, "multi_edit": true, "write_file": true},
	oldKey:    "old_string",
}

// reasonixMeta is the union of the current `.jsonl.meta` and the v0.x
// `.meta.json`; each file fills only its own half.
type reasonixMeta struct {
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Workspace   string    `json:"workspace_root"`
	CustomTitle string    `json:"custom_title"`
	TopicTitle  string    `json:"topic_title"`
	Name        string    `json:"name"`
	// v0.x
	LegacyWorkspace string `json:"workspace"`
	Summary         string `json:"summary"`
}

func readReasonixMeta(path string) reasonixMeta {
	var m reasonixMeta
	if b, err := os.ReadFile(path + ".meta"); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		var legacy reasonixMeta
		if json.Unmarshal(b, &legacy) == nil {
			m.LegacyWorkspace, m.Summary = legacy.LegacyWorkspace, legacy.Summary
		}
	}
	return m
}

// ReasonixWorkspace is the directory a session was worked in, from its
// metadata, or "" when neither sidecar names one.
func ReasonixWorkspace(path string) string {
	m := readReasonixMeta(path)
	if m.Workspace != "" {
		return m.Workspace
	}
	return m.LegacyWorkspace
}

// reasonixEventSpan is the first and last clock in a v0.x event log: `ts` on
// the v0.x events, `created_at` on the current ones.
func reasonixEventSpan(path string) (first, last time.Time) {
	events := strings.TrimSuffix(path, ".jsonl") + ".events.jsonl"
	_ = scanJSONLFromOffset(events, 0, func(m map[string]any) {
		t := parseTimeAny(m["ts"])
		if t.IsZero() {
			t = parseTimeAny(m["created_at"])
		}
		if t.IsZero() {
			return
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
		if t.After(last) {
			last = t
		}
	})
	return first, last
}

// ParseReasonixFile reads one transcript.
func ParseReasonixFile(path string) ([]model.Session, error) {
	meta := readReasonixMeta(path)
	s := model.Session{
		Harness: "reasonix",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	for _, t := range []string{meta.CustomTitle, meta.TopicTitle, meta.Name, meta.Summary} {
		if t = firstLineTrim(t); t != "" {
			s.Title = t
			break
		}
	}
	ws := meta.Workspace
	if ws == "" {
		ws = meta.LegacyWorkspace
	}
	switch {
	case ws != "":
		s.Project = claudeProjectName(pathToProjectKey(ws))
	case filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path)))) == "projects":
		s.Project = claudeProjectName(filepath.Dir(filepath.Dir(path)))
	}

	start, end := meta.CreatedAt, meta.UpdatedAt
	if start.IsZero() || end.IsZero() {
		first, last := reasonixEventSpan(path)
		if start.IsZero() {
			start = first
		}
		if end.IsZero() {
			end = last
		}
	}
	if start.IsZero() && end.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			start, end = fi.ModTime(), fi.ModTime()
		}
	}
	if start.IsZero() {
		start = end
	}

	// A line without createdAt sits one millisecond after the one before it,
	// so two identical turns stay two records (#3333).
	clock := start.Add(-time.Millisecond)
	err := scanJSONLFromOffset(path, 0, func(m map[string]any) {
		role, _ := m["role"].(string)
		if role == "" {
			return
		}
		if t := parseTimeAny(m["createdAt"]); !t.IsZero() {
			clock = t
		} else {
			clock = clock.Add(time.Millisecond)
		}
		ts := clock
		if host, _ := m["host_authored"].(bool); host {
			// The host composed it; the person did not say it.
			return
		}
		text := textFromContent(m["content"])
		if raw, _ := m["raw_content"].(string); strings.TrimSpace(raw) != "" && role != "assistant" {
			// The typed prompt before injected context, or the whole tool
			// result before it was cut for the model.
			text = strings.TrimSpace(raw)
		}
		switch role {
		case "user", "assistant":
			if text != "" {
				s.Touch(ts)
				s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: ts})
			}
			if role == "assistant" {
				for _, rec := range reasonixWorkRecords(m["tool_calls"], ts) {
					s.Touch(ts)
					s.Messages = append(s.Messages, rec)
				}
			}
		case "tool":
			if text != "" && IndexToolOutput() {
				s.Touch(ts)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(text), Time: ts})
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	s.Touch(start)
	s.Touch(end)
	return []model.Session{s}, err
}

// reasonixToolUses turns tool_calls into the tool_use blocks the dialect
// helpers read. The current store writes {id, name, arguments}; v0.x wrote
// OpenAI's {id, type, function: {name, arguments}}. arguments is a JSON string.
func reasonixToolUses(v any) []any {
	calls, _ := v.([]any)
	var out []any
	for _, c := range calls {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if fn, ok := m["function"].(map[string]any); ok {
			m = fn
		}
		name, _ := m["name"].(string)
		var in map[string]any
		switch a := m["arguments"].(type) {
		case string:
			_ = json.Unmarshal([]byte(a), &in)
		case map[string]any:
			in = a
		}
		if name == "" || in == nil {
			continue
		}
		out = append(out, map[string]any{"type": "tool_use", "name": name, "input": in})
	}
	return out
}

func reasonixWorkRecords(v any, ts time.Time) []model.Message {
	blocks := reasonixToolUses(v)
	if len(blocks) == 0 {
		return nil
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(blocks, reasonixDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(blocks, reasonixDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(blocks, reasonixDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, reasonixDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	return out
}
