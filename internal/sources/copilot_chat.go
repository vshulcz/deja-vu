package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// VS Code Copilot Chat keeps one session per file under the editor's User
// folder, distinct from Copilot CLI (~/.copilot/session-state). Cursor and
// Windsurf are not hosts: they do not ship Copilot Chat.
//
//	workspaceStorage/<id>/chatSessions/<sessionId>.jsonl
//	globalStorage/emptyWindowChatSessions/<sessionId>.jsonl
//
// A sibling .json is the pre-1.109 flat form. VS Code reads the log first, so
// when both exist the index lists only the .jsonl.

func CopilotChatRoots() []string {
	if list := os.Getenv("DEJA_COPILOT_CHAT_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	hosts := []string{"Code", "Code - Insiders", "VSCodium"}
	var bases []string
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(Home(), "Library", "Application Support")
		for _, host := range hosts {
			bases = append(bases, filepath.Join(app, host, "User"))
		}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		for _, host := range hosts {
			bases = append(bases, filepath.Join(app, host, "User"))
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		for _, host := range hosts {
			bases = append(bases, filepath.Join(cfg, host, "User"))
		}
	}
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

func CopilotChatSessionFiles() []string {
	var files []string
	for _, root := range CopilotChatRoots() {
		dirs := copilotChatStoreDirs(root)
		if dirs == nil {
			// A root laid out in none of the shapes below: walk it, as this
			// always did. Costs what it costs, and finds what is there.
			files = append(files, walkFiles(root, copilotChatFile)...)
			continue
		}
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := filepath.Join(dir, e.Name())
				if e.Type().IsRegular() && copilotChatFile(p) {
					files = append(files, p)
				}
			}
		}
	}
	return copilotChatDropJSONSiblings(files)
}

// copilotChatStoreDirs names the directories transcripts live in, instead of
// walking the User folder to find them. It returns nil when the root has none
// of VS Code's own top-level directories, which is the signal to walk instead.
//
// The root belongs to the editor, and almost none of it is ours: extension
// caches, the editor's history, per-workspace state. Measured on a working
// machine, the walk read 2,643 workspace directories to reach 28 that hold
// chat sessions — 72 ms of the 95 ms every search spends discovering the stores
// of all twenty-five harnesses, and by a wide margin the largest single term in
// it. Asking for `<hash>/chatSessions` directly turns each of those workspace
// directories from a directory read into one failed open (#3167).
func copilotChatStoreDirs(root string) []string {
	sessionDirs := func(base string) []string {
		var out []string
		out = append(out, filepath.Join(base, "globalStorage", "emptyWindowChatSessions"))
		entries, err := os.ReadDir(filepath.Join(base, "workspaceStorage"))
		if err != nil {
			return out
		}
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, filepath.Join(base, "workspaceStorage", e.Name(), "chatSessions"))
				// The extension's own transcripts, which on a newer Copilot
				// Chat are the only place the history is (copilot_agent.go,
				// #3637). One more failed open per workspace directory, which
				// is the same term #3167 replaced the walk with.
				out = append(out, filepath.Join(base, "workspaceStorage", e.Name(), copilotAgentParentDir, copilotAgentDirName))
			}
		}
		return out
	}
	known := false
	for _, name := range []string{"workspaceStorage", "globalStorage", "profiles"} {
		if fi, err := os.Stat(filepath.Join(root, name)); err == nil && fi.IsDir() {
			known = true
			break
		}
	}
	if !known {
		return nil
	}
	dirs := sessionDirs(root)
	// A named profile keeps its own copy of both, one level down.
	if profiles, err := os.ReadDir(filepath.Join(root, "profiles")); err == nil {
		for _, e := range profiles {
			if e.IsDir() {
				dirs = append(dirs, sessionDirs(filepath.Join(root, "profiles", e.Name()))...)
			}
		}
	}
	return dirs
}

func LoadCopilotChat() []model.Session {
	return parseFiles(CopilotChatSessionFiles(), ParseCopilotChatFile)
}

func ParseCopilotChatFile(path string) ([]model.Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The extension's own transcript is a different log in a different
	// directory — an event stream rather than the delta state below (#3637).
	if copilotAgentTranscript(path) {
		return parseCopilotAgentTranscript(path, data)
	}
	if strings.EqualFold(filepath.Ext(path), ".jsonl") {
		state, ok := copilotChatReplay(path, data)
		if !ok {
			return nil, nil
		}
		return copilotChatSession(path, state)
	}
	data = trimJSONSpace(data)
	if len(data) == 0 {
		diagMalformedLine(path)
		return nil, nil
	}
	var state map[string]any
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.UseNumber()
	if err := d.Decode(&state); err != nil {
		diagMalformedLine(path)
		return nil, nil
	}
	return copilotChatSession(path, state)
}

func copilotChatFile(p string) bool {
	if copilotAgentTranscript(p) {
		return true
	}
	ext := filepath.Ext(p)
	if ext != ".json" && ext != ".jsonl" {
		return false
	}
	parent := filepath.Base(filepath.Dir(p))
	return parent == "chatSessions" || parent == "emptyWindowChatSessions"
}

func copilotChatMatch(p string) bool {
	if !copilotChatFile(p) {
		return false
	}
	sep := string(filepath.Separator)
	for _, root := range CopilotChatRoots() {
		if strings.HasPrefix(p, root+sep) {
			return true
		}
	}
	return false
}

func copilotChatDropJSONSiblings(files []string) []string {
	hasLog := make(map[string]bool)
	for _, p := range files {
		if strings.HasSuffix(p, ".jsonl") {
			hasLog[strings.TrimSuffix(p, ".jsonl")] = true
		}
	}
	var out []string
	for _, p := range files {
		if strings.HasSuffix(p, ".json") && hasLog[strings.TrimSuffix(p, ".json")] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// The log is not append-only in the offset sense: a later Initial replaces
// state, Push can truncate, and compaction rewrites the whole file. Re-parse
// from zero; do not use scanJSONLFromOffset, which skips a bad line and keeps
// going on partial state. VS Code throws and drops the session.
func copilotChatReplay(path string, data []byte) (map[string]any, bool) {
	var state any
	n := 0
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := string(trimJSONSpace([]byte(raw)))
		if line == "" {
			continue
		}
		var entry map[string]any
		d := json.NewDecoder(strings.NewReader(line))
		d.UseNumber()
		if d.Decode(&entry) != nil {
			// VS Code appends while a reply streams: a last line with no
			// newline yet is a write in progress, not a broken file, and the
			// lines before it are the session (#4229).
			if i == len(lines)-1 && n > 0 {
				break
			}
			diagMalformedLine(path)
			return nil, false
		}
		n++
		kind, ok := numberVal(entry["kind"])
		if !ok {
			diagMalformedLine(path)
			return nil, false
		}
		switch kind {
		case 0:
			state = entry["v"]
		case 1, 2, 3:
			if state == nil {
				diagMalformedLine(path)
				return nil, false
			}
			k, _ := entry["k"].([]any)
			var applyOK bool
			switch kind {
			case 1:
				applyOK = copilotChatApplySet(state, k, entry["v"], false)
			case 3:
				applyOK = copilotChatApplySet(state, k, nil, true)
			default:
				applyOK = copilotChatApplyPush(state, k, entry)
			}
			if !applyOK {
				diagMalformedLine(path)
				return nil, false
			}
		default:
			diagMalformedLine(path)
			return nil, false
		}
	}
	if n == 0 {
		diagMalformedLine(path)
		return nil, false
	}
	m, ok := state.(map[string]any)
	if !ok {
		diagMalformedLine(path)
		return nil, false
	}
	return m, true
}

func copilotChatApplySet(state any, path []any, val any, del bool) bool {
	if len(path) == 0 {
		return true
	}
	parent, ok := copilotChatWalk(state, path[:len(path)-1])
	if !ok {
		return false
	}
	key := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return false
		}
		if del {
			delete(p, k)
			return true
		}
		p[k] = val
		return true
	case []any:
		n, ok := numberVal(key)
		if !ok || n < 0 || int(n) >= len(p) {
			return false
		}
		p[n] = val
		return true
	}
	return false
}

func copilotChatApplyPush(state any, path []any, entry map[string]any) bool {
	if len(path) == 0 {
		return false
	}
	var values []any
	if _, ok := entry["v"]; ok {
		sl, ok := entry["v"].([]any)
		if !ok {
			return false
		}
		values = sl
	}
	var start *int
	if _, ok := entry["i"]; ok {
		n, ok := numberVal(entry["i"])
		if !ok {
			return false
		}
		i := int(n)
		start = &i
	}
	parent, ok := copilotChatWalk(state, path[:len(path)-1])
	if !ok {
		return false
	}
	key := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return false
		}
		arr, _ := p[k].([]any)
		if start != nil {
			arr = copilotChatPadTrunc(arr, *start)
		}
		p[k] = append(arr, values...)
		return true
	case []any:
		n, ok := numberVal(key)
		if !ok || n < 0 || int(n) >= len(p) {
			return false
		}
		arr, _ := p[n].([]any)
		if start != nil {
			arr = copilotChatPadTrunc(arr, *start)
		}
		p[n] = append(arr, values...)
		return true
	}
	return false
}

func copilotChatPadTrunc(arr []any, i int) []any {
	if i < 0 {
		i = 0
	}
	if i <= len(arr) {
		return arr[:i]
	}
	for len(arr) < i {
		arr = append(arr, nil)
	}
	return arr
}

func copilotChatWalk(state any, path []any) (any, bool) {
	cur := state
	for _, key := range path {
		next, ok := copilotChatIndex(cur, key)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func copilotChatIndex(parent any, key any) (any, bool) {
	switch p := parent.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return nil, false
		}
		v, exists := p[k]
		return v, exists
	case []any:
		n, ok := numberVal(key)
		if !ok || n < 0 || int(n) >= len(p) {
			return nil, false
		}
		return p[n], true
	}
	return nil, false
}

func copilotChatSession(path string, state map[string]any) ([]model.Session, error) {
	id, _ := state["sessionId"].(string)
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	s := model.Session{
		Harness: "copilot-chat",
		ID:      id,
		Path:    path,
		Project: copilotChatProject(path, state),
		Title:   copilotChatTitle(state),
		Started: parseTimeAny(state["creationDate"]),
	}
	s.Touch(s.Started)
	for _, raw := range copilotChatSlice(state["requests"]) {
		req, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		copilotChatAppendRequest(&s, req)
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	appendChatEditedFiles(&s, path, id)
	return []model.Session{s}, nil
}

// appendChatEditedFiles adds the files this chat edited, from the state VS Code
// keeps beside the transcript:
//
//	workspaceStorage/<ws>/chatEditingSessions/<session id>/state.json
//
// The transcript names a path only when the text does, so a chat that edited
// eleven files left one `files` record. Measured on this machine: 48 of those
// state files, whose entries carry 145 resources between them (#3381).
func appendChatEditedFiles(s *model.Session, path, id string) {
	if !IndexToolPaths() || id == "" {
		return
	}
	ws := filepath.Dir(filepath.Dir(path))
	b, err := os.ReadFile(filepath.Join(ws, "chatEditingSessions", id, "state.json"))
	if err != nil {
		return
	}
	var state struct {
		RecentSnapshot struct {
			Entries []struct {
				Resource any `json:"resource"`
			} `json:"entries"`
		} `json:"recentSnapshot"`
	}
	if json.Unmarshal(b, &state) != nil {
		return
	}
	seen := map[string]bool{}
	var paths []string
	add := func(res any) {
		p := chatResourcePath(res)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, e := range state.RecentSnapshot.Entries {
		add(e.Resource)
	}
	if len(paths) == 0 {
		return
	}
	at := s.Messages[len(s.Messages)-1].Time
	s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(paths, "\n"), Time: at})
}

// chatResourcePath reads the path out of a VS Code URI. The state file writes
// a percent-encoded URI string, decoded exactly once here, or an object whose
// path is already decoded and is not decoded again.
func chatResourcePath(res any) string {
	var p string
	switch v := res.(type) {
	case string:
		if strings.Contains(v, "://") {
			u, err := url.Parse(v)
			if err != nil || u.Scheme == "" {
				return ""
			}
			p = u.Path
			if u.Scheme == "file" && u.Host != "" {
				p = "//" + u.Host + u.Path
			}
		} else {
			p = v
		}
	case map[string]any:
		p, _ = v["path"].(string)
	}
	if strings.ContainsAny(p, "\n\r") {
		return ""
	}
	return strings.TrimSpace(p)
}

func copilotChatTitle(state map[string]any) string {
	if _, ok := state["version"]; !ok {
		return ""
	}
	var title string
	if v, ok := numberVal(state["version"]); ok && v == 2 {
		title, _ = state["computedTitle"].(string)
	} else {
		title, _ = state["customTitle"].(string)
	}
	if title == "" {
		return ""
	}
	return firstLineTrim(title)
}

func copilotChatProject(path string, state map[string]any) string {
	if p := copilotChatProjectFromWorkspace(path); p != "" {
		return p
	}
	if wd, _ := state["workingDirectory"].(string); wd != "" {
		if p := copilotChatProjectFromURI(wd); p != "" {
			return p
		}
	}
	return "-"
}

func copilotChatProjectFromWorkspace(sessionPath string) string {
	ws := filepath.Join(filepath.Dir(filepath.Dir(sessionPath)), "workspace.json")
	b, err := os.ReadFile(ws)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	for _, k := range []string{"folder", "workspace"} {
		s, _ := m[k].(string)
		if p := copilotChatProjectFromURI(s); p != "" {
			return p
		}
	}
	return ""
}

// CopilotChatWorkspaceDir is the folder (or .code-workspace file) the chat at
// sessionPath belongs to, as a local path, or "" for an empty-window chat or a
// remote workspace. VS Code keeps chat history per workspace, so this is what
// has to be open for the chat to be listed again.
func CopilotChatWorkspaceDir(sessionPath string) string {
	// chatSessions/<id>.json sits two levels under the storage hash,
	// GitHub.copilot-chat/transcripts/<id>.jsonl three.
	ws := filepath.Dir(filepath.Dir(sessionPath))
	if filepath.Base(ws) == "GitHub.copilot-chat" {
		ws = filepath.Dir(ws)
	}
	b, err := os.ReadFile(filepath.Join(ws, "workspace.json"))
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	for _, k := range []string{"folder", "workspace"} {
		s, _ := m[k].(string)
		p, ok := fileURIPath(s)
		if !ok {
			continue
		}
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return filepath.FromSlash(p)
	}
	return ""
}

func copilotChatProjectFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	if p, ok := fileURIPath(uri); ok {
		return projectName(p)
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" {
		return ""
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return ""
	}
	return projectName(p)
}

func copilotChatAppendRequest(s *model.Session, req map[string]any) {
	t := parseTimeAny(req["timestamp"])
	if user := copilotChatUserText(req["message"]); strings.TrimSpace(user) != "" {
		s.Touch(t)
		system, _ := req["isSystemInitiated"].(bool)
		confirmation, _ := req["confirmation"].(string)
		// Some turns VS Code raises itself: a background terminal completion
		// carries the terminal's output as the message, and a confirmation
		// carries the button's own label. The turn happened, so it still
		// times the session and its work is still recorded, but nobody typed
		// the text and recall must not quote it back as though someone had.
		if !system && strings.TrimSpace(confirmation) == "" {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: user, Time: t})
		}
	}
	at := parseTimeAny(req["responseTimestamp"])
	if at.IsZero() {
		at = t
	}
	var speech []string
	var extras []model.Message
	copilotChatWalkResponse(req["response"], at, &speech, &extras)
	extras = append(extras, copilotChatRoundEdits(req, at)...)
	if txt := strings.TrimSpace(strings.Join(speech, "")); txt != "" {
		s.Touch(at)
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: txt, Time: at})
	}
	if len(extras) > 0 {
		s.Touch(at)
		s.Messages = append(s.Messages, extras...)
	}
}

func copilotChatUserText(v any) string {
	switch m := v.(type) {
	case string:
		return m
	case map[string]any:
		s, _ := m["text"].(string)
		return s
	}
	return ""
}

func copilotChatWalkResponse(v any, t time.Time, speech *[]string, extras *[]model.Message) {
	switch r := v.(type) {
	case string:
		if r != "" {
			*speech = append(*speech, r)
		}
	case []any:
		for _, part := range copilotChatFoldEditFences(r) {
			copilotChatWalkPart(part, t, speech, extras)
		}
	}
}

// copilotChatFoldEditFences turns the empty code fence agent mode writes around
// an edit into the file's name. The parts are a fence, a codeblockUri, the
// textEditGroup and a closing fence; VS Code draws a pill naming the file
// there, and read as text the reply had an empty fence in the middle of it
// (#4590). A fence with code in it is left alone, and the edit parts are kept
// for the records they leave.
func copilotChatFoldEditFences(parts []any) []any {
	out := make([]any, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		if fold, end, ok := copilotChatEditFence(parts, i); ok {
			out = append(out, fold...)
			i = end
			continue
		}
		out = append(out, parts[i])
	}
	return out
}

// copilotChatEditFence reads an empty fence around an edit starting at parts[i]:
// what to put in its place, and the index of the part that closes it.
func copilotChatEditFence(parts []any, i int) ([]any, int, bool) {
	text, ok := copilotChatValue(parts[i])
	if !ok || i+1 >= len(parts) {
		return nil, 0, false
	}
	before, ok := copilotChatCutOpenFence(text)
	if !ok {
		return nil, 0, false
	}
	cb, _ := parts[i+1].(map[string]any)
	if kind, _ := cb["kind"].(string); kind != "codeblockUri" {
		return nil, 0, false
	}
	name := copilotChatBaseName(copilotChatRefPath(cb["uri"]))
	if name == "" {
		return nil, 0, false
	}
	k := i + 2
	for k < len(parts) && !copilotChatSpeaks(parts[k]) {
		k++
	}
	if k >= len(parts) {
		return nil, 0, false
	}
	closing, _ := copilotChatValue(parts[k])
	after, ok := copilotChatCutCloseFence(closing)
	if !ok {
		return nil, 0, false
	}
	fold := []any{before + "\n" + name + "\n"}
	fold = append(fold, parts[i+1:k]...)
	if after != "" {
		fold = append(fold, after)
	}
	return fold, k, true
}

// copilotChatValue is the markdown a speech part carries.
func copilotChatValue(part any) (string, bool) {
	switch x := part.(type) {
	case string:
		return x, true
	case map[string]any:
		if kind, _ := x["kind"].(string); kind != "" {
			return "", false
		}
		v, ok := x["value"].(string)
		return v, ok
	}
	return "", false
}

// copilotChatSpeaks reports whether a part puts text in the reply.
func copilotChatSpeaks(part any) bool {
	if _, ok := copilotChatValue(part); ok {
		return true
	}
	m, _ := part.(map[string]any)
	kind, _ := m["kind"].(string)
	return kind == "markdownVuln" || kind == "inlineReference"
}

// copilotChatCutOpenFence takes a fence off the end of a chunk: the line
// "```" or "```lang", last in it.
func copilotChatCutOpenFence(text string) (string, bool) {
	t := strings.TrimRight(text, " \t\r\n")
	nl := strings.LastIndex(t, "\n")
	line := strings.TrimSpace(t[nl+1:])
	if !strings.HasPrefix(line, "```") || strings.Contains(line[3:], "`") {
		return "", false
	}
	if nl < 0 {
		return "", true
	}
	return strings.TrimRight(t[:nl], "\r\n"), true
}

// copilotChatCutCloseFence takes a bare "```" line off the start of a chunk.
func copilotChatCutCloseFence(text string) (string, bool) {
	line, rest, _ := strings.Cut(strings.TrimLeft(text, " \t\r\n"), "\n")
	if strings.TrimSpace(line) != "```" {
		return "", false
	}
	return rest, true
}

// copilotChatBaseName is a file's name out of a path or URI path, whichever
// separator it was written with.
func copilotChatBaseName(p string) string {
	// A URI written as a string keeps its escapes; the object form's path
	// does not. The scheme is not part of the name: file:/// is the root, and
	// untitled:Untitled-1 is a buffer VS Code calls Untitled-1. A one-letter
	// scheme is a Windows drive.
	if u, err := url.Parse(p); err == nil && len(u.Scheme) > 1 {
		if u.Opaque != "" {
			p = u.Opaque
		} else {
			p = u.Path
		}
	} else if strings.Contains(p, "://") {
		if u, err := url.PathUnescape(p); err == nil {
			p = u
		}
	}
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	if base == "/" || base == "." {
		return ""
	}
	return base
}

func copilotChatWalkPart(part any, t time.Time, speech *[]string, extras *[]model.Message) {
	if s, ok := part.(string); ok {
		if s != "" {
			*speech = append(*speech, s)
		}
		return
	}
	m, ok := part.(map[string]any)
	if !ok {
		return
	}
	kind, _ := m["kind"].(string)
	switch kind {
	case "":
		if val, _ := m["value"].(string); val != "" {
			*speech = append(*speech, val)
		}
	case "markdownVuln":
		if c, _ := m["content"].(map[string]any); c != nil {
			if val, _ := c["value"].(string); val != "" {
				*speech = append(*speech, val)
			}
		}
	case "thinking", "progressMessage", "warning", "info", "systemNotification":
		return
	case "toolInvocationSerialized":
		copilotChatTool(m, t, extras)
	case "textEditGroup":
		copilotChatEdits(m, t, extras)
	case "inlineReference":
		// VS Code draws the reference as a name in the middle of the
		// sentence; without it the reply read "The bug is in , line 40:"
		// (#4589).
		if name := copilotChatRefName(m); name != "" {
			*speech = append(*speech, name)
		}
		if !IndexToolPaths() {
			return
		}
		if p := copilotChatRefPath(m["inlineReference"]); p != "" {
			*extras = append(*extras, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
}

// copilotChatRefName is what VS Code shows for an inlineReference: the part's
// own name when it has one, a symbol's name, or the file's base name.
func copilotChatRefName(m map[string]any) string {
	if n, _ := m["name"].(string); strings.TrimSpace(n) != "" {
		return n
	}
	ref, _ := m["inlineReference"].(map[string]any)
	if n, _ := ref["name"].(string); strings.TrimSpace(n) != "" {
		return n
	}
	p := copilotChatRefPath(m["inlineReference"])
	if p == "" {
		return ""
	}
	return copilotChatBaseName(p)
}

// copilotChatEdits reads the written side of a Copilot Chat edit.
//
// A `textEditGroup` part carries the file as a uri and the edits as ranges
// plus the text that replaced each one. The new text is there in full; the old
// text is not — the range is all that says what was there — so this is the
// written side only. The replaced side is in the edit call's arguments, read by
// copilotChatRoundEdits. Counted on a local store: 655 edit groups across 34
// session files, none of which reached the index, so `deja files`, `restore`
// and line-level blame were silent for every Copilot Chat user (#595).
func copilotChatEdits(m map[string]any, t time.Time, extras *[]model.Message) {
	path := copilotChatRefPath(m["uri"])
	if path == "" {
		return
	}
	if IndexToolPaths() {
		*extras = append(*extras, model.Message{Role: RoleFiles, Text: path, Time: t})
	}
	if !IndexWrites() {
		return
	}
	// The edits arrive as a list of lists — one inner list per revision of the
	// same group, and the empty one that closes it.
	var written []string
	for _, group := range copilotChatSlice(m["edits"]) {
		for _, e := range copilotChatSlice(group) {
			edit, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if s, _ := edit["text"].(string); s != "" {
				written = append(written, s)
			}
		}
	}
	if rec := WroteRecord(path, strings.Join(written, "\n")); rec != "" {
		*extras = append(*extras, model.Message{Role: RoleWrote, Text: rec, Time: t})
	}
}

func copilotChatTool(m map[string]any, t time.Time, extras *[]model.Message) {
	if IndexToolPaths() {
		var paths []string
		for _, d := range copilotChatSlice(m["resultDetails"]) {
			if p := copilotChatRefPath(d); p != "" {
				paths = append(paths, p)
			}
		}
		// VS Code keeps no resultDetails for copilot_readFile: the file is
		// only in the uris of the message the call showed. readFile alone —
		// the other tools' uris are directories, every file with a problem in
		// the workspace, or the extension's own memory files (#4492).
		if id, _ := m["toolId"].(string); len(paths) == 0 && id == "copilot_readFile" {
			paths = copilotChatMessageURIs(m)
		}
		if len(paths) > 0 {
			*extras = append(*extras, model.Message{Role: RoleFiles, Text: strings.Join(paths, "\n"), Time: t})
		}
	}
	data, _ := m["toolSpecificData"].(map[string]any)
	if data == nil {
		return
	}
	cmd := copilotChatTerminalCommand(data)
	if cmd == "" || !worthIndexing(cmd) {
		return
	}
	// A terminal call keeps how the command ended and what it printed beside
	// the command line, terminalCommandState.exitCode and
	// terminalCommandOutput.text; only the line was read (#4493).
	if IndexCommands() {
		line := "$ " + cmd
		if st, ok := data["terminalCommandState"].(map[string]any); ok {
			if code, ok := piExitCode(st["exitCode"]); ok {
				line += fmt.Sprintf("  → exit %d", code)
			}
		}
		*extras = append(*extras, model.Message{Role: RoleCommand, Text: line, Time: t})
	}
	if IndexToolOutput() {
		out, _ := data["terminalCommandOutput"].(map[string]any)
		if text, _ := out["text"].(string); strings.TrimSpace(text) != "" {
			*extras = append(*extras, model.Message{Role: RoleToolOutput, Text: capParsedMessage(strings.TrimSpace(text)), Time: t})
		}
	}
}

// copilotChatMessageURIs is the files a tool's past-tense message names, or
// its invocation message's when it has none, in a stable order.
func copilotChatMessageURIs(m map[string]any) []string {
	for _, k := range []string{"pastTenseMessage", "invocationMessage"} {
		msg, _ := m[k].(map[string]any)
		uris, _ := msg["uris"].(map[string]any)
		keys := make([]string, 0, len(uris))
		for k := range uris {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			if u, _ := uris[k].(map[string]any); u != nil {
				if scheme, _ := u["scheme"].(string); scheme != "" && scheme != "file" {
					continue
				}
			}
			if p := copilotChatRefPath(uris[k]); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func copilotChatTerminalCommand(data map[string]any) string {
	if cl, ok := data["commandLine"].(map[string]any); ok {
		for _, k := range []string{"toolEdited", "userEdited", "original"} {
			if s, _ := cl[k].(string); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	if s, _ := data["command"].(string); strings.TrimSpace(s) != "" {
		return s
	}
	return ""
}

func copilotChatRefPath(v any) string {
	switch x := v.(type) {
	case string:
		if strings.ContainsAny(x, "\n\r") {
			return ""
		}
		return x
	case map[string]any:
		if p, _ := x["path"].(string); p != "" {
			if strings.ContainsAny(p, "\n\r") {
				return ""
			}
			return p
		}
		if p := copilotChatRefPath(x["uri"]); p != "" {
			return p
		}
		return copilotChatRefPath(x["location"])
	}
	return ""
}

func copilotChatSlice(v any) []any {
	s, _ := v.([]any)
	return s
}
