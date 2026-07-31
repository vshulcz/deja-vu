package sources

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodexHome is where the Codex CLI itself keeps state; CODEX_HOME relocates
// the whole tree (config.toml, sessions, history). Install and doctor use it.
func CodexHome() string {
	return EnvPath("CODEX_HOME", filepath.Join(Home(), ".codex"))
}

// CodexRoot is the session-reading root; DEJA_CODEX_ROOT overrides it without
// affecting where install writes.
func CodexRoot() string {
	return EnvPath("DEJA_CODEX_ROOT", CodexHome())
}

func LoadCodex() []model.Session {
	root := CodexRoot()
	files := walkFiles(filepath.Join(root, "sessions"), codexRolloutWanted)
	ss := parseFiles(files, ParseCodexRollout)
	if hist, _ := ParseCodexHistory(filepath.Join(root, "history.jsonl")); len(hist) > 0 {
		ss = append(ss, hist...)
	}
	return ss
}

func codexRolloutWanted(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && strings.Contains(filepath.Base(p), "rollout-")
}

// CodexFiles lists the rollout transcripts (plus history.jsonl when present)
// without parsing them — a cheap count for diagnostics.
func CodexFiles() []string {
	root := CodexRoot()
	files := walkFiles(filepath.Join(root, "sessions"), codexRolloutWanted)
	if hist := filepath.Join(root, "history.jsonl"); fileExists(hist) {
		files = append(files, hist)
	}
	return files
}

func ParseCodexHistory(path string) ([]model.Session, error) {
	return ParseCodexHistoryFromOffset(path, 0)
}

func ParseCodexHistoryFromOffset(path string, offset int64) ([]model.Session, error) {
	var out []model.Session
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		id, _ := m["session_id"].(string)
		txt, _ := m["text"].(string)
		if id == "" || txt == "" {
			return
		}
		t := parseTimeAny(m["ts"])
		out = append(out, model.Session{Harness: "codex", ID: id, Project: "history", Path: path, Started: t, Updated: t, Messages: []model.Message{{Role: "user", Text: txt, Time: t}}})
	})
	return out, err
}

func ParseCodexRollout(path string) ([]model.Session, error) {
	return ParseCodexRolloutFromOffset(path, 0)
}

func ParseCodexRolloutFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "codex", ID: strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "rollout-"), ".jsonl"), Project: projectName(filepath.Dir(path)), Path: path}
	// A command and its exit code arrive in separate records joined by call_id,
	// so the command line is annotated after the fact — the same shape opencode
	// gets for free from a column.
	calls := map[string]int{}
	cwd := ""
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		payload, _ := m["payload"].(map[string]any)
		if payload == nil {
			return
		}
		if typ, _ := m["type"].(string); typ == "session_meta" {
			if id, _ := payload["session_id"].(string); id != "" {
				s.ID = id
			}
			if c, _ := payload["cwd"].(string); c != "" {
				cwd = c
				s.Project = projectName(c)
			}
			return
		}
		switch pt, _ := payload["type"].(string); pt {
		case "function_call":
			codexCall(&s, payload, calls, t)
			return
		case "function_call_output", "custom_tool_call_output":
			codexCallOutput(&s, payload, calls, t)
			return
		case "custom_tool_call":
			codexPatch(&s, payload, cwd, t)
			return
		}
		role, _ := payload["role"].(string)
		txt := textFromContent(payload["content"])
		if txt == "" {
			if msg, _ := payload["message"].(string); msg != "" {
				role = "user"
				txt = msg
			}
		}
		if role != "" && txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// Codex records a shell run as a function_call named exec_command whose
// arguments are a JSON string, and its result as a separate function_call_output
// joined by call_id. Both were read off rollouts the Codex CLI had just
// written; an earlier note in #595 that Codex carries no exit code came from a
// sample that happened to be all MCP calls.
func codexCall(s *model.Session, payload map[string]any, calls map[string]int, t time.Time) {
	if name, _ := payload["name"].(string); name != "exec_command" {
		return
	}
	args, _ := payload["arguments"].(string)
	var in struct {
		Cmd string `json:"cmd"`
	}
	if json.Unmarshal([]byte(args), &in) != nil || in.Cmd == "" {
		return
	}
	if !IndexCommands() || !worthIndexing(in.Cmd) {
		return
	}
	s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + in.Cmd, Time: t})
	if id, _ := payload["call_id"].(string); id != "" {
		calls[id] = len(s.Messages) - 1
	}
}

// codexExit reads the outcome Codex prints above the output. exec_command says
// "Process exited with code N" on every run including zero; apply_patch says
// "Exit code: N".
var codexExit = regexp.MustCompile(`(?m)^(?:Process exited with code|Exit code:) (\d+)`)

func codexCallOutput(s *model.Session, payload map[string]any, calls map[string]int, t time.Time) {
	out, _ := payload["output"].(string)
	if out == "" {
		return
	}
	if m := codexExit.FindStringSubmatch(out); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil && code > 0 {
			if i, ok := calls[payload["call_id"].(string)]; ok && i < len(s.Messages) {
				s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
			}
		}
	}
	if !IndexToolOutput() {
		return
	}
	// Everything above "Output:" is Codex's own framing — a chunk id, a wall
	// time, a token count. Keeping it would put four lines of plumbing in front
	// of every result in the index.
	if i := strings.Index(out, "\nOutput:\n"); i >= 0 {
		out = out[i+len("\nOutput:\n"):]
	}
	if out = strings.TrimSpace(out); out == "" {
		return
	}
	s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
}

// codexPatchFile matches the file headers of the apply_patch format Codex uses
// for every edit it makes; it has no Read or Edit tool of its own.
var codexPatchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$`)

// codexPatch turns an apply_patch call into the files it touched and the lines
// it removed. Paths in the patch are relative to the session's cwd.
func codexPatch(s *model.Session, payload map[string]any, cwd string, t time.Time) {
	if name, _ := payload["name"].(string); name != "apply_patch" {
		return
	}
	body, _ := payload["input"].(string)
	if body == "" {
		return
	}
	var files []string
	removed := map[string][]string{}
	current := ""
	for _, line := range strings.Split(body, "\n") {
		if m := codexPatchFile.FindStringSubmatch(line); m != nil {
			current = strings.TrimSpace(m[1])
			if cwd != "" && !filepath.IsAbs(current) {
				current = filepath.Join(cwd, current)
			}
			files = append(files, current)
			continue
		}
		// A removed line, not the "--- a/x" header of a unified diff: this
		// format has no such header, so a single leading minus is unambiguous.
		if current != "" && strings.HasPrefix(line, "-") {
			removed[current] = append(removed[current], strings.TrimPrefix(line, "-"))
		}
	}
	if len(files) == 0 {
		return
	}
	if IndexToolPaths() {
		s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(files, "\n"), Time: t})
	}
	if !IndexEdits() {
		return
	}
	for _, f := range files {
		span := strings.Join(removed[f], "\n")
		if span == "" {
			continue
		}
		if len(span) > editSpanMax {
			span = span[:editSpanMax]
		}
		s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: f + "\n" + span, Time: t})
	}
}
