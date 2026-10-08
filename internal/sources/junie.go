package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Junie is JetBrains' coding agent: the junie CLI, and the same agent the IDE
// runs through AI Assistant. Both keep one directory per session under
// $JUNIE_HOME, else ~/.junie (resolveJunieHomePath in the 3110.7 build):
//
//	~/.junie/sessions/index.jsonl                one line per session: id, projectDir, taskName
//	~/.junie/sessions/<session-id>/events.jsonl  the session's event log
//	~/.junie/sessions/<session-id>/state.json    the agent's last state
//
// An events.jsonl line is {"kind", "timestampMs", …}. UserPromptEvent carries
// the person's words under prompt; the agent's work arrives as
// SessionA2uxEvent{event:{agentEvent:{kind, stepId, …}}}, the AUI blocks
// aui.go folds. A headless run (`junie "task"`) writes no UserPromptEvent, and
// the task's words are only in state.json, as the issue description.
// Measured on Junie CLI 3110.7 (26.9.7).

// JunieHome is $JUNIE_HOME, else ~/.junie.
func JunieHome() string {
	return EnvPath("JUNIE_HOME", filepath.Join(Home(), ".junie"))
}

// JunieRoot is the sessions directory. DEJA_JUNIE_ROOT replaces it.
func JunieRoot() string {
	return EnvPath("DEJA_JUNIE_ROOT", filepath.Join(JunieHome(), "sessions"))
}

// JunieSessionFiles lists every session's events.jsonl.
func JunieSessionFiles() []string {
	entries, err := os.ReadDir(JunieRoot())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(JunieRoot(), e.Name(), "events.jsonl")
		if fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

// JunieSidecarFiles are the store's own files beside the logs: the session
// index, and each session's state.json and transcript.md, a rendering of the
// same log.
func JunieSidecarFiles() []string {
	out := []string{filepath.Join(JunieRoot(), "index.jsonl")}
	for _, p := range JunieSessionFiles() {
		dir := filepath.Dir(p)
		out = append(out, filepath.Join(dir, "state.json"), filepath.Join(dir, "transcript.md"))
	}
	return out
}

func isJunieSession(p string) bool {
	return hasBase(p, "events.jsonl") && filepath.Dir(filepath.Dir(p)) == filepath.Clean(JunieRoot())
}

// JunieSessionEvents is the event log of a session id, "" when there is none.
// A hook is told the id and nothing else about where the session lives.
func JunieSessionEvents(sessionID string) string {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return ""
	}
	p := filepath.Join(JunieRoot(), sessionID, "events.jsonl")
	if !fileExists(p) {
		return ""
	}
	return p
}

func LoadJunie() []model.Session {
	return parseFiles(JunieSessionFiles(), ParseJunieFile)
}

// ParseJunieFile reads one session's events.jsonl.
func ParseJunieFile(path string) ([]model.Session, error) {
	s, err := parseJunieEvents(path, time.Time{})
	if err != nil || len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, nil
}

// junieIndexEntry is a session's line in sessions/index.jsonl.
type junieIndexEntry struct {
	SessionID  string `json:"sessionId"`
	CreatedAt  int64  `json:"createdAt"`
	ProjectDir string `json:"projectDir"`
	TaskName   string `json:"taskName"`
}

func junieIndexFor(sessionDir string) junieIndexEntry {
	id := filepath.Base(sessionDir)
	f, err := os.Open(filepath.Join(filepath.Dir(sessionDir), "index.jsonl"))
	if err != nil {
		return junieIndexEntry{}
	}
	defer f.Close()
	var found junieIndexEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e junieIndexEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.SessionID == id {
			found = e
		}
	}
	return found
}

// parseJunieEvents reads a session's log. before, when set, stops at the first
// event at or after it: a compaction capture reads the turns up to the
// compaction and no further.
func parseJunieEvents(path string, before time.Time) (model.Session, error) {
	dir := filepath.Dir(path)
	idx := junieIndexFor(dir)
	s := model.Session{Harness: "junie", ID: filepath.Base(dir), Path: path}
	var events []auiEvent
	var title, cwd string
	prompts := 0
	err := scanJSONL(path, func(m map[string]any) {
		at := junieTime(m["timestampMs"])
		if !before.IsZero() && !at.IsZero() && !at.Before(before) {
			return
		}
		switch m["kind"] {
		case "UserPromptEvent":
			text := firstString(m, "presentablePrompt", "prompt")
			events = append(events, auiEvent{Kind: auiPrompt, Body: map[string]any{"prompt": text}, At: at})
			prompts++
		case "SessionA2uxEvent":
			ev, _ := m["event"].(map[string]any)
			ae, _ := ev["agentEvent"].(map[string]any)
			kind, _ := ae["kind"].(string)
			switch kind {
			case "AgentTaskNameUpdatedEvent":
				if title == "" {
					title, _ = ae["name"].(string)
				}
			case "CurrentDirectoryUpdatedEvent":
				if d, _ := ae["currentDirectory"].(string); d != "" && cwd == "" {
					cwd = d
				}
			default:
				events = append(events, auiEvent{Kind: kind, Body: ae, At: at})
			}
		}
	})
	if err != nil {
		return s, err
	}
	project := idx.ProjectDir
	if project == "" {
		project = cwd
	}
	if prompts == 0 {
		if text := junieHeadlessTask(filepath.Join(dir, "state.json")); text != "" {
			at := junieTime(idx.CreatedAt)
			if len(events) > 0 && (at.IsZero() || events[0].At.Before(at)) {
				at = events[0].At
			}
			events = append([]auiEvent{{Kind: auiPrompt, Body: map[string]any{"prompt": text}, At: at}}, events...)
		}
	}
	for _, msg := range auiRecords(events, project) {
		s.Touch(msg.Time)
		s.Messages = append(s.Messages, msg)
	}
	if project != "" {
		s.Project = projectName(project)
	}
	if title == "" {
		title = idx.TaskName
	}
	s.Title = firstLineTrim(title)
	return s, nil
}

// junieHeadlessTask is the task a headless run was given, from the issue
// description in state.json. Junie puts what its hooks answered in front of
// it, which is not the person's words.
func junieHeadlessTask(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Event struct {
			AgentEvent struct {
				Blob string `json:"blob"`
			} `json:"agentEvent"`
		} `json:"event"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Event.AgentEvent.Blob == "" {
		return ""
	}
	var blob struct {
		LastAgentState struct {
			Issue struct {
				Description string `json:"description"`
			} `json:"issue"`
		} `json:"lastAgentState"`
	}
	if json.Unmarshal([]byte(doc.Event.AgentEvent.Blob), &blob) != nil {
		return ""
	}
	return strings.TrimSpace(withoutAdditionalContext(blob.LastAgentState.Issue.Description))
}

// JunieProjectDir is the project a session ran in, from sessions/index.jsonl.
func JunieProjectDir(sessionID string) string {
	p := JunieSessionEvents(sessionID)
	if p == "" {
		return ""
	}
	return JunieSessionProject(p)
}

// JunieSessionProject is the project of the session whose events.jsonl is at
// path.
func JunieSessionProject(path string) string {
	return junieIndexFor(filepath.Dir(path)).ProjectDir
}

// junieTime reads a millisecond timestamp, which scanJSONL hands over as a
// json.Number.
func junieTime(v any) time.Time {
	var ms int64
	switch n := v.(type) {
	case json.Number:
		ms, _ = n.Int64()
	case float64:
		ms = int64(n)
	case int64:
		ms = n
	}
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
