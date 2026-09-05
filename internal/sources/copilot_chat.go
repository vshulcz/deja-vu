package sources

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
		files = append(files, walkFiles(root, copilotChatFile)...)
	}
	return copilotChatDropJSONSiblings(files)
}

func LoadCopilotChat() []model.Session {
	return parseFiles(CopilotChatSessionFiles(), ParseCopilotChatFile)
}

func ParseCopilotChatFile(path string) ([]model.Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
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
	for _, raw := range strings.Split(string(data), "\n") {
		line := string(trimJSONSpace([]byte(raw)))
		if line == "" {
			continue
		}
		n++
		var entry map[string]any
		d := json.NewDecoder(strings.NewReader(line))
		d.UseNumber()
		if d.Decode(&entry) != nil {
			diagMalformedLine(path)
			return nil, false
		}
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
	return []model.Session{s}, nil
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

func copilotChatProjectFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" {
		return ""
	}
	p := u.Path
	if u.Scheme == "file" {
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return projectName(p)
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return projectName(p)
}

func copilotChatAppendRequest(s *model.Session, req map[string]any) {
	t := parseTimeAny(req["timestamp"])
	if user := copilotChatUserText(req["message"]); strings.TrimSpace(user) != "" {
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: user, Time: t})
	}
	at := parseTimeAny(req["responseTimestamp"])
	if at.IsZero() {
		at = t
	}
	var speech []string
	var extras []model.Message
	copilotChatWalkResponse(req["response"], at, &speech, &extras)
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
		for _, part := range r {
			copilotChatWalkPart(part, t, speech, extras)
		}
	}
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
	case "inlineReference":
		if !IndexToolPaths() {
			return
		}
		if p := copilotChatRefPath(m["inlineReference"]); p != "" {
			*extras = append(*extras, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
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
		if len(paths) > 0 {
			*extras = append(*extras, model.Message{Role: RoleFiles, Text: strings.Join(paths, "\n"), Time: t})
		}
	}
	if !IndexCommands() {
		return
	}
	data, _ := m["toolSpecificData"].(map[string]any)
	if data == nil {
		return
	}
	if cmd := copilotChatTerminalCommand(data); cmd != "" && worthIndexing(cmd) {
		*extras = append(*extras, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
	}
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
