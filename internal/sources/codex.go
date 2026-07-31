package sources

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
	// An appended rollout is parsed from where the last read stopped, so the
	// session_meta line at the top is never seen again and the id falls back
	// to the filename — which matches the real ThreadId in 0 of 28 rollouts on
	// this machine. The session then splits in two and every further turn
	// lands in the second half, undoing #635 for exactly the sessions someone
	// is still talking in. One line re-read is cheaper than carrying state.
	if offset > 0 {
		if id, cwd := codexRolloutHead(path); id != "" {
			s.ID = id
			if cwd != "" {
				s.Project = projectName(cwd)
			}
		}
	}
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
			// The ThreadId, not the SessionId. One rollout file is one thread;
			// a session groups every thread that branched from it, so keying on
			// session_id merges the whole fork tree into a single deja session
			// and every thread but one becomes unreachable. Measured by a
			// contributor on a fork-heavy store: 811 rollouts, 811 ThreadIds,
			// 74 SessionIds — 91% of threads collapsed (#635).
			//
			// payload.id equals the filename, so this agrees with the id
			// derived above; it is set explicitly because agreeing by accident
			// is not the same as agreeing on purpose.
			if id, ok := payload["id"].(string); ok {
				// Present and a string: the ThreadId, even if empty.
				if id != "" {
					s.ID = id
				}
			} else if _, present := payload["id"]; !present {
				// Absent, not malformed. Rollouts written before Codex split
				// the two carry only a SessionId, and without threads there is
				// nothing to collapse: keeping it preserves the identity those
				// sessions already have in existing indexes. A present-but-not-
				// a-string id is a shape deja does not understand, and falling
				// back to the SessionId there would silently reintroduce the
				// collapse (#635) — the filename-derived id stands instead.
				if id, _ := payload["session_id"].(string); id != "" {
					s.ID = id
				}
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
		if HarnessAuthored(role) {
			return
		}
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
	// The id is read with the comma-ok form on purpose: a record without a
	// call_id is not an error, and asserting it bare turns one into a panic
	// that takes the whole parse down.
	id, _ := payload["call_id"].(string)
	if m := codexExit.FindStringSubmatch(out); m != nil && id != "" {
		if code, err := strconv.Atoi(m[1]); err == nil && code > 0 {
			if i, ok := calls[id]; ok && i < len(s.Messages) {
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
	seen := map[string]bool{}
	removed := map[string][]string{}
	current := ""
	for _, line := range strings.Split(body, "\n") {
		if m := codexPatchFile.FindStringSubmatch(line); m != nil {
			current = strings.TrimSpace(m[1])
			if cwd != "" && !filepath.IsAbs(current) {
				current = filepath.Join(cwd, current)
			}
			if !seen[current] {
				seen[current] = true
				files = append(files, current)
			}
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

// codexRolloutHead reads the identity a rollout declares in its first record.
func codexRolloutHead(path string) (id, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// The meta record is the first line in every rollout Codex writes; a few
	// lines of slack costs nothing and covers a format that adds a preamble.
	for i := 0; i < 8 && sc.Scan(); i++ {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
				CWD       string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "session_meta" {
			continue
		}
		if rec.Payload.ID != "" {
			return rec.Payload.ID, rec.Payload.CWD
		}
		return rec.Payload.SessionID, rec.Payload.CWD
	}
	return "", ""
}
