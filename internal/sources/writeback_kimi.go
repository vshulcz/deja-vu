package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kimi Code keeps a session in sessions/<workDirKey>/<sessionId>/: state.json
// names the directory it ran in, agents/main/wire.jsonl is the transcript, and
// session_index.jsonl at the top lists every session. Deleting one removes the
// directory and appends a tombstone to the index, so writing it back takes
// the two files and, when the index's last word on the id is that tombstone,
// one entry appended after it, the line Kimi itself appends for a new session.
// `kimi --session` opens a session only from the directory state.json names.
// The index does not keep that directory; Kimi's workspaces.json does, under
// the key the session's path is filed by.

func init() {
	registerWriteBack("kimi", func() []string { return []string{KimiRoot()} }, renderKimi)
	registerWriteBackExtras("kimi", kimiWriteBackExtras)
}

// kimiWriteBackDirs splits a main-agent wire.jsonl path into the store home,
// the session directory and the workDir key, or ok=false.
func kimiWriteBackDirs(s model.Session) (home, sessionDir, key string, ok bool) {
	p := s.Path
	if filepath.Base(p) != "wire.jsonl" || filepath.Base(filepath.Dir(p)) != "main" ||
		filepath.Base(filepath.Dir(filepath.Dir(p))) != "agents" {
		return "", "", "", false
	}
	sessionDir = kimiSessionDirOf(p)
	if filepath.Base(sessionDir) != s.ID {
		return "", "", "", false
	}
	bucket := filepath.Dir(sessionDir)
	sessions := filepath.Dir(bucket)
	if filepath.Base(sessions) != "sessions" {
		return "", "", "", false
	}
	return filepath.Dir(sessions), sessionDir, filepath.Base(bucket), true
}

// kimiWorkDir is the directory Kimi recorded for a session: workspaces.json
// under the session's key, else the newest index entry for the id.
func kimiWorkDir(home, key, id string) string {
	var ws struct {
		Workspaces map[string]struct {
			Root string `json:"root"`
		} `json:"workspaces"`
	}
	if b, err := os.ReadFile(filepath.Join(home, "workspaces.json")); err == nil && json.Unmarshal(b, &ws) == nil {
		if r := ws.Workspaces[key].Root; r != "" {
			return r
		}
	}
	dir := ""
	kimiIndexEach(home, func(e kimiIndexEntry) {
		if e.SessionID == id && e.WorkDir != "" {
			dir = e.WorkDir
		}
	})
	return dir
}

type kimiIndexEntry struct {
	SessionID  string `json:"sessionId"`
	SessionDir string `json:"sessionDir"`
	WorkDir    string `json:"workDir"`
	Deleted    bool   `json:"deleted"`
}

func kimiIndexEach(home string, fn func(kimiIndexEntry)) {
	f, err := os.Open(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var e kimiIndexEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.SessionID != "" {
			fn(e)
		}
	}
}

func renderKimi(s model.Session, turns []model.Message) (string, []byte, error) {
	home, _, key, ok := kimiWriteBackDirs(s)
	if !ok {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no sessions/<dir>/<id>/agents/main/wire.jsonl path for this session, which is where Kimi keeps a session"}
	}
	if kimiWorkDir(home, key, s.ID) == "" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the directory the session ran in is not recorded anywhere left: Kimi opens a session only from there and kept it in the session's state.json, which went with the session, and neither workspaces.json nor session_index.jsonl names it"}
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	lines := []any{map[string]any{"type": "metadata", "protocol_version": "1.4", "created_at": start.UnixMilli()}}
	for _, m := range turns {
		msg := map[string]any{
			"role":      m.Role,
			"content":   []any{map[string]any{"type": "text", "text": m.Text}},
			"toolCalls": []any{},
		}
		if m.Role == "user" {
			msg["origin"] = map[string]any{"kind": "user"}
		}
		lines = append(lines, map[string]any{"type": "context.append_message", "message": msg, "time": m.Time.UnixMilli()})
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

func kimiWriteBackExtras(s model.Session, turns []model.Message, f WriteBackFile) (sidecars, appends []WriteBackPart, err error) {
	home, sessionDir, key, _ := kimiWriteBackDirs(s)
	workDir := kimiWorkDir(home, key, s.ID)
	start, end := turns[0].Time, turns[len(turns)-1].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	if s.Updated.After(end) {
		end = s.Updated
	}
	lastPrompt := ""
	for _, m := range turns {
		if m.Role == "user" {
			lastPrompt = m.Text
		}
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = firstLineRunes(lastPrompt, 200)
	}
	state := map[string]any{
		"createdAt":     writeBackStamp(start),
		"updatedAt":     writeBackStamp(end),
		"title":         title,
		"isCustomTitle": false,
		"agents": map[string]any{"main": map[string]any{
			"homedir":       filepath.Join(sessionDir, "agents", "main"),
			"type":          "main",
			"parentAgentId": nil,
		}},
		"custom":     map[string]any{},
		"workDir":    workDir,
		"lastPrompt": lastPrompt,
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	sidecars = []WriteBackPart{{Path: filepath.Join(sessionDir, "state.json"), Data: append(b, '\n')}}
	// Listed when the index's last word on the id is not an entry for it:
	// after Kimi's own delete that is a tombstone, which hides the session
	// from `kimi --session` even with its directory back.
	live := false
	kimiIndexEach(home, func(e kimiIndexEntry) {
		if e.SessionID == s.ID {
			live = !e.Deleted && e.SessionDir != ""
		}
	})
	if !live {
		line, err := jsonLines([]any{map[string]any{"sessionId": s.ID, "sessionDir": sessionDir, "workDir": workDir}})
		if err != nil {
			return nil, nil, err
		}
		appends = []WriteBackPart{{Path: filepath.Join(home, "session_index.jsonl"), Data: line}}
	}
	return sidecars, appends, nil
}

// firstLineRunes is the first line of s, cut to n runes.
func firstLineRunes(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
