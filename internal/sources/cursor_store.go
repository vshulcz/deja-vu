package sources

import (
	"bytes"
	"encoding/hex"
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

// The agent transcript Cursor writes carries every tool call and none of the
// results: no command output, no exit code, no MCP answer. The results are in
// the chat's store.db beside it (see cursor_chats.go for the layout), so a
// Cursor session was indexed as a list of commands nobody knew the outcome of
// — `deja fix`, the post-command hook and `deja tests` read exactly that
// (#4187).
//
// The transcript's tool_use parts carry no id, so a call is paired with the
// store's by its tool name and arguments. The store also holds calls the
// transcript leaves out (GetDynamicTools), so position alone would pair the
// wrong ones; and the store is only the chat's latest branch, so a call it
// lost to a rewind must not take the result of a later identical one. Pairing
// walks both in order and only forward: each transcript call takes the next
// matching store call after the last one paired.

// cursorToolResult is one call from the store with what it returned.
type cursorToolResult struct {
	key  string // tool name and canonical arguments
	text string
}

// cursorToolResults is a chat's calls in the order they were made, and how far
// pairing has got through them.
type cursorToolResults struct {
	calls []cursorToolResult
	next  int
}

// take returns the result of the next call after the last one paired with
// this name and these arguments.
func (rs *cursorToolResults) take(name string, args map[string]any) (string, bool) {
	if rs == nil {
		return "", false
	}
	k := cursorCallKey(name, args)
	for i := rs.next; i < len(rs.calls); i++ {
		if rs.calls[i].key == k {
			rs.next = i + 1
			return rs.calls[i].text, true
		}
	}
	return "", false
}

// cursorCallKey is a call's name and arguments as one comparable string. Both
// sides decode numbers as json.Number, so `1e21` marshals back as written on
// each, and map keys marshal sorted.
func cursorCallKey(name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	return name + "\x00" + string(b)
}

// cursorDiscoveryTools are calls Cursor makes to list what an MCP server
// offers. Their results are the servers' own descriptions — deja's among them
// — and indexing those would put deja's text in every session that used it.
var cursorDiscoveryTools = map[string]bool{"GetMcpTools": true, "GetDynamicTools": true}

// cursorStoreFor finds the store.db of the chat a transcript belongs to: the
// chat id is the transcript's name. A chat id under two workspace hashes — a
// copied ~/.cursor — takes the store written last.
func cursorStoreFor(transcript string) string {
	id := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ""
	}
	chats := filepath.Join(CursorCLIRoot(), "chats")
	best := ""
	var bestTime time.Time
	for _, bucket := range cursorChatBuckets(chats, id) {
		p := filepath.Join(chats, bucket, id, "store.db")
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if best == "" || fi.ModTime().After(bestTime) {
			best, bestTime = p, fi.ModTime()
		}
	}
	return best
}

// cursorStoreResults reads a chat store's tool calls and their results in
// message order. Anything it cannot read — a store that is encrypted, a tree
// in another shape — yields nothing, and the session keeps what the
// transcript has.
func cursorStoreResults(db string) *cursorToolResults {
	if db == "" {
		return nil
	}
	root := cursorStoreRoot(db)
	if root == "" {
		return nil
	}
	rows, err := cursorQuery(db, `SELECT json_object('h', hex(data)) FROM blobs WHERE lower(id) = '`+root+`'`)
	if err != nil || len(rows) == 0 {
		return nil
	}
	h, _ := rows[0]["h"].(string)
	rootBlob, err := hex.DecodeString(h)
	if err != nil {
		return nil
	}
	// Only the messages that hold a tool part, as text: the store keeps every
	// root the chat ever had, and each lists every message before it, so
	// reading all of it grows with the square of the chat's length. The
	// filter is the bytes `"tool-`.
	rows, err = cursorQuery(db, `SELECT json_object('id', lower(id), 'd', CAST(data AS TEXT)) FROM blobs WHERE instr(data, X'22746F6F6C2D') > 0`)
	if err != nil {
		return nil
	}
	data := make(map[string]string, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(string)
		d, _ := r["d"].(string)
		data[id] = d
	}
	out := &cursorToolResults{}
	calls := map[string]int{}
	for _, child := range protoChildIDs(rootBlob) {
		d, ok := data[child]
		if !ok {
			continue
		}
		var msg struct {
			Content []struct {
				Type       string          `json:"type"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Args       map[string]any  `json:"args"`
				Result     json.RawMessage `json:"result"`
			} `json:"content"`
		}
		dec := json.NewDecoder(strings.NewReader(d))
		dec.UseNumber()
		if dec.Decode(&msg) != nil {
			continue
		}
		for _, p := range msg.Content {
			if p.ToolCallID == "" {
				continue
			}
			i, seen := calls[p.ToolCallID]
			switch {
			case p.Type == "tool-call" && !seen:
				calls[p.ToolCallID] = len(out.calls)
				out.calls = append(out.calls, cursorToolResult{key: cursorCallKey(p.ToolName, p.Args)})
			case p.Type == "tool-result" && seen && !cursorDiscoveryTools[p.ToolName]:
				out.calls[i].text = cursorResultText(p.Result)
			}
		}
	}
	return out
}

// cursorStoreRoot is the id of the chat's latest root blob, from meta's
// hex-encoded JSON.
func cursorStoreRoot(db string) string {
	rows, err := cursorQuery(db, `SELECT json_object('v', CAST(value AS TEXT)) FROM meta`)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		v, _ := r["v"].(string)
		b, err := hex.DecodeString(strings.TrimSpace(v))
		if err != nil {
			b = []byte(v)
		}
		var meta struct {
			Root string `json:"latestRootBlobId"`
		}
		if json.Unmarshal(b, &meta) == nil && isHexID(meta.Root) {
			return strings.ToLower(meta.Root)
		}
	}
	return ""
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
	return string(bytes.TrimSpace(raw))
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
// The fence is cut at its own length, from the end, so a fence the command
// printed itself stays in.
func cursorShellOutput(text string) string {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "Command output:"); i >= 0 {
		t = strings.TrimSpace(t[i+len("Command output:"):])
	} else if loc := cursorShellExit.FindStringIndex(t); loc != nil {
		t = strings.TrimSpace(t[loc[1]:])
	}
	if n := len(t) - len(strings.TrimLeft(t, "`")); n >= 3 {
		fence := t[:n]
		t = t[n:]
		if j := strings.LastIndex(t, fence); j >= 0 {
			t = t[:j]
		}
	}
	return strings.TrimSpace(t)
}

// cursorCallsTools reports whether a turn holds a tool call.
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
// what each call returned: the exit status on the command, as Claude's are,
// and the output itself as tool output.
func cursorToolTurn(s *model.Session, content any, results *cursorToolResults, at time.Time) {
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
			if code, ok := cursorExitCode(out); ok && cmdAt >= 0 {
				s.Messages[cmdAt].Text += fmt.Sprintf("  → exit %d", code)
			}
			out = cursorShellOutput(out)
		}
		if out = strings.TrimSpace(out); out != "" && IndexToolOutput() {
			s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(out), Time: at})
		}
	}
}
