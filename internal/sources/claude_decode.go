package sources

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The generic scanner decodes every transcript line into map[string]any behind a
// per-line json.Decoder. On a real corpus that path allocated 4.4 GB of the
// 8.6 GB a full rebuild spends, and 96.9% of it came from this parser alone —
// claude transcripts are the bulk of most stores.
//
// Four fields are wanted out of a line that carries twenty. Declaring them costs
// −39% time and −89% bytes against decoding into a map, measured on a real line;
// reusing the decoder instead, which is the obvious fix, buys 3%.
//
// The two fields whose type varies stay raw and are decoded by hand, because
// that variance is the reason the generic path existed:
//
//	timestamp:  "2026-01-02T03:04:05Z"  or  1767323045
//	content:    "plain text"            or  [{"type":"text","text":"…"}, …]
type claudeLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp json.RawMessage `json:"timestamp"`
	Message   *claudeMessage  `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func parseClaudeTypedFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "claude",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: claudeProjectName(claudeProjectDir(path)),
		Path:    path,
	}
	err := scanJSONLBytes(path, offset, func(line []byte) {
		var v claudeLine
		if json.Unmarshal(line, &v) != nil {
			diagMalformedLine(path)
			return
		}
		if v.Type != "user" && v.Type != "assistant" {
			return
		}
		if v.SessionID != "" {
			s.ID = v.SessionID
		}
		t := claudeTime(v.Timestamp)
		s.Touch(t)
		role := v.Type
		txt := ""
		if v.Message != nil {
			if v.Message.Role != "" {
				role = v.Message.Role
			}
			var toolOut bool
			txt, toolOut = claudeTextKind(v.Message.Content)
			if toolOut {
				role = RoleToolOutput
			}
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
		if v.Message != nil {
			if IndexToolPaths() {
				if p := claudeToolPaths(v.Message.Content); p != "" {
					s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range claudeCommands(v.Message.Content) {
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd, Time: t})
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// claudeTime accepts the two shapes a transcript uses. Numbers keep going
// through unixGuess rather than being read as float64: the generic scanner set
// UseNumber for exactly this reason, and losing it would shift timestamps
// silently.
func claudeTime(raw json.RawMessage) time.Time {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		return time.Time{}
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return time.Time{}
	}
	i, err := n.Int64()
	if err != nil {
		return time.Time{}
	}
	return unixGuess(i)
}

// claudeText joins the text a message carries. Items that are not objects, or
// objects with neither key, are skipped rather than failing the line — a
// transcript mixing shapes must not cost the whole message.
func claudeText(raw json.RawMessage) string {
	txt, _ := claudeTextKind(raw)
	return txt
}

// RoleToolOutput marks text a tool produced. Claude Code files tool results
// inside `user` messages, so without this the index labels 89% of its user-role
// content as if a person had typed it — 33,027 of 37,077 messages, 77.8 MB
// (#559). The text is worth keeping and searching; only the attribution was
// wrong.
const RoleToolOutput = "tool-output"

// claudeTextKind joins a message's text and reports whether what it joined came
// from tool results rather than from speech. A message mixing both counts as
// speech: the person said something, and the tool output rode along with it.
func claudeTextKind(raw json.RawMessage) (string, bool) {
	var sawToolResult, sawSpeech bool
	raw = trimJSONSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		return s, false
	}
	if raw[0] != '[' {
		return "", false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return "", false
	}
	var b strings.Builder
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var part struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content string `json:"content"`
		}
		if json.Unmarshal(item, &part) != nil {
			continue
		}
		chunk := part.Text
		if chunk == "" {
			chunk = part.Content
		}
		if part.Type == "tool_result" {
			sawToolResult = true
		} else if chunk != "" {
			sawSpeech = true
		}
		if chunk == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(chunk)
	}
	return b.String(), sawToolResult && !sawSpeech
}

func trimJSONSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// scanJSONLBytes hands each line to the caller without copying it or building a
// decoder for it. The slice is only valid for the duration of the call.
func scanJSONLBytes(path string, offset int64, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := trimJSONSpace(line); len(trimmed) > 0 {
			fn(trimmed)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// RoleFiles marks a record that lists the files a turn touched, rather than
// anything said. It exists because the reliable answer to "which file is this
// session about" is what the agent opened and edited, and that lives in
// tool_use inputs — measured three times over: #542 works with these paths and
// not with prose mentions, #531 fails its kill condition without them, and #537
// has no material at all.
const RoleFiles = "files"

// IndexToolPaths reports whether file paths from tool calls are indexed. On by
// default: a feature behind a flag does not exist for the people who would
// benefit from it, and this one costs 2% of the index. `DEJA_INDEX_PATHS=0`
// turns it off for anyone who wants the smaller index back.
//
// This is the cheapest slice of #547 — 0.6 MB of path text across a 644 MB
// corpus, against 13.6 MB for commands and 80 MB for the bodies of files that
// were read — and three features rest on it: #542, #531 and #534.
func IndexToolPaths() bool { return os.Getenv("DEJA_INDEX_PATHS") != "0" }

// pathTools are the calls whose input names a file. Bash is deliberately absent:
// a path inside a shell command is guesswork, and guessing is what made the
// prose-mention approach unusable.
var pathTools = map[string]bool{"Read": true, "Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// claudeToolPaths returns the distinct file paths a message's tool calls name,
// one per line, or "" when it names none.
func claudeToolPaths(raw json.RawMessage) string {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return ""
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var out []string
	seen := map[string]bool{}
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var part struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				FilePath string `json:"file_path"`
			} `json:"input"`
		}
		if json.Unmarshal(item, &part) != nil {
			continue
		}
		if part.Type != "tool_use" || !pathTools[part.Name] || part.Input.FilePath == "" {
			continue
		}
		if seen[part.Input.FilePath] {
			continue
		}
		seen[part.Input.FilePath] = true
		out = append(out, part.Input.FilePath)
	}
	return strings.Join(out, "\n")
}

// RoleCommand marks a command that ran. Tool *output* has always been indexed —
// Claude files it under the user role, which is why the index holds megabytes of
// test output — but the invocation that produced it was dropped, so output sat
// there with nothing saying what it came from.
const RoleCommand = "command"

// IndexCommands reports whether commands are indexed. On by default, because a
// flag nobody enables is a feature nobody has; `DEJA_INDEX_COMMANDS=0` turns it
// off. Only the commands worth keeping — see worthIndexing.
func IndexCommands() bool { return os.Getenv("DEJA_INDEX_COMMANDS") != "0" }

// worthIndexing keeps the commands that say what happened — a test run, a build,
// a deploy, a git or gh operation — and drops navigation. Measured on a real
// corpus: 5,051 of 23,774 commands, 3.5 MB of 13.1 MB.
//
// The dropped ones are not merely cheap to store, they are actively bad to keep:
// `cat internal/index/index.go` matches a query about the index and answers
// nothing.
var meaningfulCommand = regexp.MustCompile(`\b(go (test|build|vet|run)|golangci-lint|pytest|npm (run )?(test|build)|yarn |cargo |make\b|gh (pr|run|release|issue|workflow)|git (commit|push|rebase|merge|revert|tag|bisect)|docker|kubectl|terraform|deja )`)

var trivialCommand = regexp.MustCompile(`^\s*(ls|cd|pwd|cat|head|tail|echo|grep|rg|find|which|wc|sed|awk|sleep|mkdir|rm|cp|mv|chmod|export|source|touch|open|printf)\b`)

func worthIndexing(cmd string) bool {
	return meaningfulCommand.MatchString(cmd) && !trivialCommand.MatchString(cmd)
}

// claudeCommands pulls the shell commands worth keeping out of a message.
func claudeCommands(raw json.RawMessage) []string {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var part struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		}
		if json.Unmarshal(item, &part) != nil {
			continue
		}
		if part.Type != "tool_use" || part.Name != "Bash" || part.Input.Command == "" {
			continue
		}
		if !worthIndexing(part.Input.Command) {
			continue
		}
		out = append(out, "$ "+part.Input.Command)
	}
	return out
}
