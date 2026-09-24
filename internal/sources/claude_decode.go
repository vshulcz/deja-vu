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
	// IsMeta marks a user-role record Claude Code wrote itself — a loaded
	// skill's body, a prompt a cron job re-fired, the /fork notice, an image
	// placeholder. Indexed as the person's words, a skill body became 52
	// questions nobody asked on one machine (#3267).
	IsMeta bool `json:"isMeta"`
	// RequestID is the API call this record reports on. A store that appends a
	// snapshot per stream chunk repeats it, which is how a run of prefixes is
	// recognised as one reply (#3644).
	RequestID string `json:"requestId"`
	// UUID is the record's own id, the fallback identity when neither
	// requestId nor message.id is present.
	UUID string `json:"uuid"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	// ID identifies the API call a streaming snapshot belongs to, beside the
	// line's own requestId. Used only where a store writes snapshots.
	ID string `json:"id"`
}

func parseClaudeTypedFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeTypedWithScanner(path, func(fn func([]byte)) error {
		return scanJSONLBytes(path, offset, fn)
	})
}

// parseClaudeTypedWithScanner keeps normalization independent from where a
// bounded caller obtained its records. The normal file parser above remains
// the sole owner of filesystem I/O; compaction supplies its already-read
// memory slice so no unredacted temporary transcript reaches disk.
func parseClaudeTypedWithScanner(path string, scan func(func([]byte)) error) ([]model.Session, error) {
	return parseClaudeTypedWithOptions(path, scan, claudeParseOptions{Harness: "claude"})
}

// claudeParseOptions are the differences between the stores that share Claude
// Code's transcript format.
type claudeParseOptions struct {
	// Harness names the store on every session it produces.
	Harness string
	// CollapseSnapshots keeps one record per API call where the store appends a
	// snapshot per stream chunk, as Cherry Studio does (#3644).
	CollapseSnapshots bool
}

func parseClaudeTypedWithOptions(path string, scan func(func([]byte)) error,
	opts claudeParseOptions) ([]model.Session, error) {
	harness := opts.Harness
	if harness == "" {
		harness = "claude"
	}
	// index of the message a request id last wrote, for the collapse
	snapshotAt := map[string]int{}
	// Where each Bash call's record landed, so the result that arrives later
	// can stamp its outcome onto it.
	commandAt := map[string]int{}
	s := model.Session{
		Harness: harness,
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: claudeProjectName(claudeProjectDir(path)),
		Path:    path,
	}
	err := scan(func(line []byte) {
		var v claudeLine
		if json.Unmarshal(line, &v) != nil {
			diagMalformedLine(path)
			return
		}
		if v.Type != "user" && v.Type != "assistant" {
			return
		}
		if v.Type == "user" && v.IsMeta {
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
			// A snapshot run is one reply: the later record carries the longer
			// text, so it replaces the earlier rather than following it.
			if key := claudeCallIdentity(v); opts.CollapseSnapshots && key != "" {
				if at, ok := snapshotAt[key]; ok && at < len(s.Messages) &&
					s.Messages[at].Role == role {
					if len(txt) >= len(s.Messages[at].Text) {
						s.Messages[at].Text = txt
						s.Messages[at].Time = t
					}
					return
				}
				snapshotAt[key] = len(s.Messages)
			}
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
			if IndexWrites() {
				for _, w := range claudeWroteRecords(v.Message.Content) {
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range claudeCommands(v.Message.Content) {
					if cmd.ID != "" {
						commandAt[cmd.ID] = len(s.Messages)
					}
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd.Text, Time: t})
				}
				// The result arrives in a later record and names the call. A
				// transcript carries no exit code, so only the clean case is
				// stated, in the marker every other harness writes — nothing is
				// invented for a failure whose code nobody recorded.
				for _, res := range claudeToolOutcomes(v.Message.Content) {
					i, ok := commandAt[res.ID]
					if !ok || res.Error || i >= len(s.Messages) {
						continue
					}
					if !strings.Contains(s.Messages[i].Text, "  → exit ") {
						s.Messages[i].Text += "  → exit 0"
					}
					delete(commandAt, res.ID)
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	// A child run comes in as the task it was handed and the answer it came
	// back with, unless the reader asked for the whole thing (#3009).
	if IsSubagentPath(path) && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
		s.Messages = KeepSubagentTail(s.Messages)
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

// claudeWroteRecords returns "path\n<line hashes>" for every write in a turn:
// the new side of an Edit, of each sub-edit of a MultiEdit, and the whole
// content of a Write. The mirror of claudeEditSpans, which takes the old side.
func claudeWroteRecords(raw json.RawMessage) []string {
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
				NewString string `json:"new_string"`
				// A Write hands over the whole file, which is how a new file
				// enters a repository — and a commit that adds a file is the
				// case the replaced side can say nothing at all about.
				Content string `json:"content"`
				Edits   []struct {
					NewString string `json:"new_string"`
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
		written := []string{part.Input.NewString, part.Input.Content}
		for _, e := range part.Input.Edits {
			written = append(written, e.NewString)
		}
		for _, w := range written {
			if rec := WroteRecord(path, w); rec != "" {
				out = append(out, rec)
			}
		}
	}
	return out
}

// RoleSummary marks a harness's own digest of a conversation it compacted away.
//
// opencode writes one on the message — `summary: true`, with `mode` and `agent`
// both `compaction` — and deja indexed it as ordinary assistant speech: 1,906 of
// them across 23 sessions on one store, 3.97 MB of 20.24 MB of everything
// indexed in those sessions, 19.6%, and 42% in the worst one. Measured over 60
// real questions, none of them ever won a quoted line — so the cost was the read
// budget rather than the answers, and a fifth of the text those sessions offered
// to a per-session bound was speech nobody said (#3384).
//
// Kept rather than dropped: the summary is the only record of the half that was
// compacted away, and it is findable by asking for it. What goes is the claim
// that the agent said it.
const RoleSummary = "summary"

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
// The task runners — just, task, mise — are the same kind of entry as make,
// and they gate more than the command table: the point-of-action hook only
// speaks about a command the ingest kept, so on a repository driven by a
// justfile every deploy and every migration was invisible to it while the same
// work behind a Makefile was not. They are anchored to the start of a segment
// because each is an ordinary English word: `git grep task` names no runner.
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
	// The task runners are judged on statement boundaries rather than on every
	// pipe. Their names are ordinary words, and splitting on a bare `|` cuts
	// inside a quoted alternation too: `grep "worktree names\|task signal" f`
	// yields a fragment beginning "task signal" and nothing else about that
	// line says a runner ran. Nobody pipes into one, so `&&`, `||` and `;` are
	// the only places a run can begin.
	for _, seg := range statementChain.Split(cmd, -1) {
		if taskRunner.MatchString(seg) && !trivialCommand.MatchString(seg) {
			return true
		}
	}
	return false
}

// statementChain splits on the operators that start a new command, leaving a
// pipe inside a quoted argument alone.
var statementChain = regexp.MustCompile(`&&|\|\||;`)

// taskRunner is just, task and mise at the head of a statement — the same kind
// of entry as make, kept apart because each name is also an ordinary word.
var taskRunner = regexp.MustCompile(`^\s*(just|task|mise)\s`)

// commandChain splits a shell line on the operators that separate commands, so
// each side is judged on its own. Quoted operators are rare in real command
// logs and splitting on them at worst keeps one more command than it should.
var commandChain = regexp.MustCompile(`&&|\|\||;|\|`)

// claudeCommand is one Bash invocation and the call id its result will name.
type claudeCommand struct {
	ID, Text string
}

// claudeCommands pulls the shell commands worth keeping out of a message.
func claudeCommands(raw json.RawMessage) []claudeCommand {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []claudeCommand
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var part struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
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
		out = append(out, claudeCommand{ID: part.ID, Text: "$ " + part.Input.Command})
	}
	return out
}

// claudeToolOutcome is one tool_result and whether the harness marked it a
// failure. A Claude transcript records no exit code — `is_error` is all there
// is — so a result that is not an error is the only outcome that can be stated,
// and it is the one worth stating: it turns "this session ran X" into evidence
// that X worked here.
type claudeToolOutcome struct {
	ID    string
	Error bool
}

func claudeToolOutcomes(raw json.RawMessage) []claudeToolOutcome {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []claudeToolOutcome
	for _, item := range items {
		item = trimJSONSpace(item)
		if len(item) == 0 || item[0] != '{' {
			continue
		}
		var part struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
		}
		if json.Unmarshal(item, &part) != nil || part.Type != "tool_result" || part.ToolUseID == "" {
			continue
		}
		out = append(out, claudeToolOutcome{ID: part.ToolUseID, Error: part.IsError})
	}
	return out
}

// claudeCallIdentity is which API call a record reports on: the requestId when
// the store writes one, else the message id, else the record's own uuid. A
// store that appends a snapshot per stream chunk repeats the first two and
// changes the third, which is what makes the first two usable and the third a
// last resort (#3644).
func claudeCallIdentity(v claudeLine) string {
	if v.RequestID != "" {
		return "req:" + v.RequestID
	}
	if v.Message != nil && v.Message.ID != "" {
		return "msg:" + v.Message.ID
	}
	if v.UUID != "" {
		return "uuid:" + v.UUID
	}
	return ""
}
