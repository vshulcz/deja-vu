package sources

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The agent transcript Cursor writes carries every tool call and none of the
// results: no command output, no exit code, no MCP answer. The results are in
// the chat's store.db beside it (see cursor_chats.go for the layout), so a
// Cursor session was indexed as a list of commands nobody knew the outcome of
// — `deja fix`, the post-command hook and `deja tests` read exactly that
// (#4187). The transcript's tool_use parts carry no id, so a call is paired
// with the store's by its tool name and arguments, the earliest unused first;
// the store also holds calls the transcript leaves out (GetDynamicTools), so
// position alone would pair the wrong ones.

// cursorToolResult is one call from the store with what it returned.
type cursorToolResult struct {
	key  string // tool name and canonical arguments
	text string
	used bool
}

// cursorToolResults is a chat's calls in the order they were made.
type cursorToolResults []*cursorToolResult

// take returns the result of the earliest unused call with this name and
// these arguments.
func (rs cursorToolResults) take(name string, args map[string]any) (string, bool) {
	k := cursorCallKey(name, args)
	for _, r := range rs {
		if !r.used && r.key == k {
			r.used = true
			return r.text, true
		}
	}
	return "", false
}

func cursorCallKey(name string, args map[string]any) string {
	b, _ := json.Marshal(args) // map keys marshal sorted
	return name + "\x00" + string(b)
}

// cursorStoreFor finds the store.db of the chat a transcript belongs to: the
// chat id is the transcript's name.
func cursorStoreFor(transcript string) string {
	id := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	if id == "" || strings.ContainsAny(id, `*?[\/`) {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(CursorCLIRoot(), "chats", "*", id, "store.db"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// cursorStoreResults reads a chat store's tool calls and their results in
// message order. Anything it cannot read — a store that is encrypted, a
// tree in another shape — yields nothing, and the session keeps what the
// transcript has.
func cursorStoreResults(db string) cursorToolResults {
	rows, err := cursorQuery(db, `SELECT json_object('v', CAST(value AS TEXT)) FROM meta`)
	if err != nil {
		return nil
	}
	root := ""
	for _, r := range rows {
		v, _ := r["v"].(string)
		b, err := hex.DecodeString(strings.TrimSpace(v))
		if err != nil {
			b = []byte(v)
		}
		var meta struct {
			Root string `json:"latestRootBlobId"`
		}
		if json.Unmarshal(b, &meta) == nil && meta.Root != "" {
			root = meta.Root
			break
		}
	}
	if root == "" || !isHexID(root) {
		return nil
	}
	blobs, err := cursorQuery(db, `SELECT json_object('id', id, 'h', hex(data)) FROM blobs`)
	if err != nil {
		return nil
	}
	data := make(map[string][]byte, len(blobs))
	for _, r := range blobs {
		id, _ := r["id"].(string)
		h, _ := r["h"].(string)
		if b, err := hex.DecodeString(h); err == nil {
			data[strings.ToLower(id)] = b
		}
	}
	var out cursorToolResults
	calls := map[string]*cursorToolResult{}
	for _, child := range protoChildIDs(data[strings.ToLower(root)]) {
		var msg struct {
			Content []struct {
				Type       string          `json:"type"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Args       map[string]any  `json:"args"`
				Result     json.RawMessage `json:"result"`
			} `json:"content"`
		}
		if json.Unmarshal(data[child], &msg) != nil {
			continue
		}
		for _, p := range msg.Content {
			switch {
			case p.ToolCallID == "":
			case p.Type == "tool-call" && calls[p.ToolCallID] == nil:
				r := &cursorToolResult{key: cursorCallKey(p.ToolName, p.Args)}
				calls[p.ToolCallID] = r
				out = append(out, r)
			case p.Type == "tool-result" && calls[p.ToolCallID] != nil:
				calls[p.ToolCallID].text = cursorResultText(p.Result)
			}
		}
	}
	return out
}

func isHexID(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 64
}

// protoChildIDs reads the root blob: protobuf whose repeated field 1 holds the
// 32-byte digests of the chat's messages in order. Other fields are skipped by
// their wire type; a malformed blob ends the walk where it breaks.
func protoChildIDs(b []byte) []string {
	var out []string
	for len(b) > 0 {
		tag, n := protoVarint(b)
		if n == 0 {
			return out
		}
		b = b[n:]
		field, wire := tag>>3, tag&7
		switch wire {
		case 0:
			_, n := protoVarint(b)
			if n == 0 {
				return out
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return out
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return out
			}
			b = b[4:]
		case 2:
			l, n := protoVarint(b)
			if n == 0 || uint64(len(b)-n) < l {
				return out
			}
			v := b[n : n+int(l)]
			b = b[n+int(l):]
			if field == 1 && len(v) == 32 {
				out = append(out, hex.EncodeToString(v))
			}
		default:
			return out
		}
	}
	return out
}

func protoVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// cursorResultText is a result as text: a string as it is, anything else as
// its JSON.
func cursorResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// cursorShellExit reads the status Cursor writes at the top of a Shell
// result: "Exit code: 1".
var cursorShellExit = regexp.MustCompile(`^Exit code: (\d+)`)

func cursorExitCode(text string) (int, bool) {
	m := cursorShellExit.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// cursorShellOutput keeps what a command printed out of the result Cursor
// writes around it:
//
//	Exit code: 0
//
//	Command output:
//
//	```
//	hi
//	```
//
//	Command completed in 420 ms.
//
//	Shell state (cwd, env vars) persists for subsequent calls.
//
// The output is cut at the last fence, so a fence the command printed itself
// stays in.
func cursorShellOutput(text string) string {
	t := strings.TrimSpace(text)
	i := strings.Index(t, "Command output:")
	if i < 0 {
		return t
	}
	t = strings.TrimSpace(t[i+len("Command output:"):])
	if strings.HasPrefix(t, "```") {
		t = t[len("```"):]
		if j := strings.LastIndex(t, "```"); j >= 0 {
			t = t[:j]
		}
	}
	return strings.TrimSpace(t)
}

func cursorCallsTools(content any) bool {
	items, _ := content.([]any)
	for _, it := range items {
		if _, _, ok := toolPart(it, cursorDialect); ok {
			return true
		}
	}
	return false
}

// cursorToolTurn records one assistant turn's commands and, from the store,
// what each call returned: a failed command's exit status on the command, and
// the output itself as tool output.
func cursorToolTurn(s *model.Session, content any, results cursorToolResults, at time.Time) {
	items, _ := content.([]any)
	for _, it := range items {
		name, in, ok := toolPart(it, cursorDialect)
		if !ok {
			continue
		}
		shell := cursorDialect.isShellTool(name)
		cmdAt := -1
		if shell && IndexCommands() {
			for _, cmd := range commandStrings(in, cursorDialect) {
				if worthIndexing(cmd) {
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: at})
					cmdAt = len(s.Messages) - 1
				}
			}
		}
		out, ok := results.take(name, in)
		if !ok {
			continue
		}
		if shell {
			if code, ok := cursorExitCode(out); ok && code > 0 && cmdAt >= 0 {
				s.Messages[cmdAt].Text += fmt.Sprintf("  → exit %d", code)
			}
			out = cursorShellOutput(out)
		}
		if out = strings.TrimSpace(out); out != "" && IndexToolOutput() {
			s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(out), Time: at})
		}
	}
}
