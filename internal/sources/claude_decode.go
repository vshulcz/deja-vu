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
	"sync"
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
	// A sidechain file is a subagent's own run. Every line in it repeats the
	// parent's sessionId, so keying on that folded the child's work into the
	// parent and one of the two won (#1384). agentId is what identifies the
	// child, and attributionAgent names which agent it was.
	IsSidechain      bool   `json:"isSidechain"`
	AgentID          string `json:"agentId"`
	AttributionAgent string `json:"attributionAgent"`
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
		if v.IsSidechain && v.AgentID != "" {
			// The child is its own session, spawned by the parent whose id
			// every one of these lines carries.
			s.ID = v.AgentID
			s.Kind = "sidechain"
			s.Parent = v.SessionID
			if v.AttributionAgent != "" {
				s.Agent = v.AttributionAgent
			}
		} else if v.SessionID != "" {
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
			if IndexEdits() {
				for _, e := range claudeEditSpans(v.Message.Content) {
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: e, Time: t})
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
	// The directory a session was started from is a weak guess at what it was
	// about; the files it touched are a strong one.
	if p := projectFromPaths(s.Messages); p != "" {
		s.Project = p
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
	// Both shapes go through parseTimeAny so the claude fast path reads the
	// same timestamp variants every other parser does: a stringified or
	// fractional epoch, or an RFC3339 that dropped its zone or seconds. It used
	// to accept only RFC3339 strings and whole-number epochs, so those turns
	// lost their date to the zero time and sorted as "-".
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}
		}
		return parseTimeAny(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return time.Time{}
	}
	return parseTimeAny(n)
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
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &part) != nil {
			continue
		}
		chunk := part.Text
		if chunk == "" {
			chunk = claudeContentText(part.Content)
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

// claudeContentText reads an item's content field. Most tool results carry it
// as a plain string, but an MCP tool's result is an array of blocks with the
// text one level down — decoding it as a string failed the whole item, so
// every MCP tool output vanished from the index (76 records on one real
// store). Blocks without text, images among them, contribute nothing.
func claudeContentText(raw json.RawMessage) string {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	if raw[0] != '[' {
		return ""
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var block struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(item, &block) != nil || block.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(block.Text)
	}
	return b.String()
}

func trimJSONSpace(b []byte) []byte {
	// A UTF-8 BOM on the first line of a transcript makes its JSON invalid, so
	// the opening turn — usually the session's own question — was dropped while
	// the rest of the session indexed (some Windows editors write the mark).
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
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

// jsonlReaders keeps the megabyte read buffer alive between files. A history
// is thousands of transcripts, and allocating the buffer per file made the
// buffer itself half of everything a cold index build allocated — 3.7 GB of
// churn over 3745 sessions, for one buffer's worth of actual working memory.
var jsonlReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 1024*1024) }}

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
	r := jsonlReaders.Get().(*bufio.Reader)
	// Reset before returning it as well as after taking it: a pooled reader
	// must not keep the last file open through its reference.
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(f)
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
		// One path per line, split back apart by the index: a path carrying a
		// newline arrives as two files the session never touched (#2042).
		if seen[part.Input.FilePath] || strings.ContainsAny(part.Input.FilePath, "\n\r") {
			continue
		}
		seen[part.Input.FilePath] = true
		out = append(out, part.Input.FilePath)
	}
	return strings.Join(out, "\n")
}

// RoleEdit holds a span an agent replaced, with the file it belonged to on the
// first line. It is the one part of a transcript that cannot be reconstructed
// from anything else: `old_string` is the exact bytes that stopped existing,
// and on this corpus it is 3,836 spans across 862 files for 1.09 MB.
const RoleEdit = "edit"

// IndexEdits reports whether replaced spans are indexed. On by default for the
// same reason as paths: a recovery feature nobody enabled recovers nothing.
func IndexEdits() bool { return os.Getenv("DEJA_INDEX_EDITS") != "0" }

// editSpanMax bounds a single stored span. The measured maximum is 8.5 KB and
// p90 is 578 B; the bound is there so one pathological patch cannot put a
// megabyte of someone else's file into the index.
const editSpanMax = 32 * 1024

// claudeEditSpans returns "path\nreplaced bytes" for every edit in a turn,
// including the sub-edits of a MultiEdit call.
func claudeEditSpans(raw json.RawMessage) []string {
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
			Input struct {
				FilePath  string `json:"file_path"`
				OldString string `json:"old_string"`
				Edits     []struct {
					OldString string `json:"old_string"`
				} `json:"edits"`
			} `json:"input"`
		}
		if json.Unmarshal(item, &part) != nil || part.Type != "tool_use" {
			continue
		}
		path := part.Input.FilePath
		if path == "" {
			continue
		}
		// "path\nspan" cannot hold a path with a newline in it, and restore
		// hands the overflow back as file content (#2042). editSpansIn, the
		// reference parser this one must agree with, drops the same edits.
		if strings.ContainsAny(path, "\n\r") {
			continue
		}
		spans := []string{part.Input.OldString}
		for _, e := range part.Input.Edits {
			spans = append(spans, e.OldString)
		}
		for _, span := range spans {
			if span == "" {
				continue
			}
			if len(span) > editSpanMax {
				span = span[:editSpanMax]
			}
			out = append(out, path+"\n"+span)
		}
	}
	return out
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

// meaningfulCommand is an allowlist on purpose. Inverting it — index anything
// that is not trivial — was measured on an 85,623-command store: 98% of them
// pass, 6.3 MB, and the families it lets through are `if`, `for`, `const`,
// `def` — code from multi-line scripts, not commands anyone ran.
//
// It stays an allowlist and gets wider instead. The narrow version covered one
// ecosystem: `git status` and `git log` missed it 2,270 times, and a person
// working in LaTeX, Python or the JVM saw nothing at all. Widening it takes the
// same store from 6,770 commands to 12,661, and 1.03 MB to 1.41 MB.
//
// The service family — brew services, systemctl, launchctl, service — is here
// for the same reason go mod tidy is: "start the thing that is not running" is
// the answer to a refused connection, and it was the one remedy the list did
// not carry while `brew install`, two words away, was carried (#2373).
var meaningfulCommand = regexp.MustCompile(`\b(go (test|build|vet|run|mod|get|install|generate|work|clean)|brew (install|upgrade|uninstall|reinstall|tap|services)|systemctl|launchctl|service [a-z0-9_.-]+ (start|stop|restart|reload|status)|golangci-lint|gofmt|pytest|python3? -m|uv (run|pip)|pip install|ruff|mypy|npm|npx|pnpm|yarn|bun |cargo |make\b|cmake|ctest|ninja|meson|bazel|buck2|gh (pr|run|release|issue|workflow|api)|git [a-z-]+|docker|kubectl|helm|terraform|psql|mysql|latexmk|mvn|gradle|dotnet|swift (build|test)|bundle exec|rails|rake|rspec|phpunit|composer (install|update|require)|tox|nox|jest|vitest|playwright|cypress|deno|tsc|sbt|stack (build|test|run|exec)|cabal|ghc|dune|zig|nix|rustc|clang\+\+|clang|g\+\+|gcc|pdm run|poetry run|deja )`)

var trivialCommand = regexp.MustCompile(`^\s*(ls|cd|pwd|cat|head|tail|echo|grep|rg|find|which|wc|sed|awk|sleep|mkdir|rm|cp|mv|chmod|export|source|touch|open|printf)\b`)

// worthIndexing keeps the commands that say what happened — a test run, a build,
// a deploy, a git or gh operation — and drops navigation. It also keeps the
// subcommands that change a build's inputs (`go mod tidy`, `go get`, `brew
// install`): those are the remedies `deja fix` exists to name, and enumerating
// only `go test|build|vet|run` meant the failure was indexed and the repair was
// not (#1635). On the corpus that set
// the first allowlist, roughly a fifth of command text was worth keeping (5k of
// 24k); the list has since grown to cover more build and test toolchains
// (bazel, jest, deno, sbt, cabal, zig, compilers) that were being dropped.
//
// The dropped ones are not merely cheap to store, they are actively bad to keep:
// `cat internal/index/index.go` matches a query about the index and answers
// nothing.
func worthIndexing(cmd string) bool {
	// One line only: a multi-line command is a heredoc or a pasted script, and
	// what it says about the work is already in what it produced.
	if strings.Contains(cmd, "\n") {
		return false
	}
	// Judge each chained segment, not the whole string. `cd repo && go test` is
	// a test run, not navigation — the leading `cd` must not sink it. Splitting
	// on the shell operators also keeps `grep "go test" log` correctly dropped:
	// its `go test` is an argument, not a segment of its own.
	for _, seg := range commandChain.Split(cmd, -1) {
		if meaningfulCommand.MatchString(seg) && !trivialCommand.MatchString(seg) {
			return true
		}
	}
	return false
}

// commandChain splits a shell line on the operators that separate commands, so
// each side is judged on its own. Quoted operators are rare in real command
// logs and splitting on them at worst keeps one more command than it should.
var commandChain = regexp.MustCompile(`&&|\|\||;|\|`)

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
