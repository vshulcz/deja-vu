package sources

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// TRAE CLI 2.0 (traex, reporting itself as traecli 0.200.x) is a closed-source
// fork of codex-rs. It writes Codex rollouts — session_meta, event_msg,
// response_item, turn_context — under its own home rather than ~/.codex, so it
// is read the way Xcode's Codex store is, with three differences its rollouts
// carry and Codex's do not. The shapes below come from the reader botmux keeps
// for it (src/services/traex-transcript.ts and its tests); agentsview reads the
// same store through its Codex parser (internal/parser/traex.go).

// TraeHome is TRAE CLI's own state root. TRAE_HOME relocates it, the way
// botmux's traeHome() resolves it.
func TraeHome() string {
	return EnvPath("TRAE_HOME", filepath.Join(Home(), ".trae"))
}

// TraeRoot is the codex-rs store under that home: sessions/YYYY/MM/DD,
// archived_sessions and history.jsonl, one level down at cli/ where Codex has
// none. TRAECLI_HOME is TRAE's own name for this directory and moves the
// sessions and hooks.json with it (traecli 0.207.1: `TRAECLI_HOME=x traex
// archive <id>` writes x/archived_sessions). DEJA_TRAE_ROOT overrides both.
func TraeRoot() string {
	return EnvPath("DEJA_TRAE_ROOT", TraeCLIHome())
}

// TraeCLIHome is the directory TRAE itself keeps its state and hooks.json in,
// whatever deja is told to read.
func TraeCLIHome() string {
	return EnvPath("TRAECLI_HOME", filepath.Join(TraeHome(), "cli"))
}

// TraeSessionDirs are the directories TRAE's rollouts live in.
func TraeSessionDirs() []string { return codexSessionDirs(TraeRoot()) }

// TraeFiles lists the rollouts and the history file without parsing them.
func TraeFiles() []string {
	var files []string
	for _, dir := range TraeSessionDirs() {
		files = append(files, walkFiles(dir, codexRolloutWanted)...)
	}
	if hist := filepath.Join(TraeRoot(), "history.jsonl"); fileExists(hist) {
		files = append(files, hist)
	}
	return files
}

func underTraeSessions(p string) bool {
	for _, dir := range TraeSessionDirs() {
		if underCodexRoot(p, dir) {
			return true
		}
	}
	return false
}

func LoadTrae() []model.Session {
	var files []string
	for _, dir := range TraeSessionDirs() {
		files = append(files, walkFiles(dir, codexRolloutWanted)...)
	}
	ss := parseFiles(codexOnePerSession(files), ParseTraeRollout)
	// As for Codex: history.jsonl repeats the prompts of sessions that have a
	// rollout, so it only speaks for the sessions that have none.
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		seen[s.ID] = true
	}
	histPath := filepath.Join(TraeRoot(), "history.jsonl")
	if hist, err := ParseTraeHistory(histPath); len(storeRead(histPath, hist, err)) > 0 {
		for _, h := range hist {
			if !seen[h.ID] {
				ss = append(ss, h)
			}
		}
	}
	return ss
}

func ParseTraeRollout(path string) ([]model.Session, error) {
	return ParseTraeRolloutFromOffset(path, 0)
}

func ParseTraeRolloutFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseRolloutFile(path, offset, "trae")
}

func ParseTraeHistory(path string) ([]model.Session, error) {
	return ParseTraeHistoryFromOffset(path, 0)
}

func ParseTraeHistoryFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseRolloutHistory(path, offset, "trae")
}

// traeMirrorWindow is how far apart the two records of one prompt can be when
// one of them carries no turn id: botmux's TRAEX_LEGACY_USER_MIRROR_WINDOW_MS.
const traeMirrorWindow = 5 * time.Second

// traeTurns is what a TRAE rollout needs read differently from a Codex one.
//
// TRAE writes its own runtime injections — environment context, process-limit
// notices — as response_item messages under the user role, so that record is
// not evidence a person typed anything. A prompt is confirmed by event_msg
// user_message or, from 0.201.4, by event_msg item_completed carrying a
// UserMessage item; the two can mirror one prompt in either order, the older
// one sometimes without a turn id.
//
// The answer can arrive as a response_item, as a history_mutation item, as an
// item_completed AgentMessage, or as an agent_message event, and the first
// source present is the one kept. Tool calls and their results ride in
// history_mutation appends, with outputs as block lists rather than strings.
type traeTurns struct {
	users  []traeUser
	calls  map[string]bool
	rolled bool
	hist   []model.Message
	items  []model.Message
	events []model.Message
}

type traeUser struct {
	dialect, turn, text string
	id                  string // the item id; the same id is the same prompt
	at                  time.Time
	matched             bool
}

func newTraeTurns() *traeTurns { return &traeTurns{calls: map[string]bool{}} }

func (tr *traeTurns) record(s *model.Session, m, payload map[string]any, cwd string, t time.Time, calls map[string]int) {
	switch typ, _ := m["type"].(string); typ {
	case "history_mutation":
		// A replace re-sends history the file already holds.
		if op, _ := payload["operation"].(string); op != "append" {
			return
		}
		// From 0.208 the prompt is confirmed here and nowhere else: no
		// user_message, no item_completed UserMessage.
		dcs, _ := payload["display_completions"].([]any)
		for _, d := range dcs {
			dc, _ := d.(map[string]any)
			item, _ := dc["item"].(map[string]any)
			if it, _ := item["type"].(string); it == "UserMessage" {
				turn, _ := dc["turn_id"].(string)
				id, _ := item["id"].(string)
				tr.userID(s, "item", turn, id, traeItemText(item, "text"), t)
			}
		}
		items, _ := payload["items"].([]any)
		for _, it := range items {
			item, _ := it.(map[string]any)
			if item == nil {
				continue
			}
			if pt, _ := item["type"].(string); pt == "message" {
				// The user side is the same injection problem as below.
				if role, _ := item["role"].(string); role == "assistant" {
					if txt := textFromContent(item["content"]); txt != "" {
						tr.hist = append(tr.hist, model.Message{Role: "assistant", Text: txt, Time: t})
					}
				}
				continue
			}
			tr.tool(s, item, cwd, t, calls)
		}
	case "event_msg":
		turn, _ := payload["turn_id"].(string)
		switch pt, _ := payload["type"].(string); pt {
		case "user_message":
			msg, _ := payload["message"].(string)
			tr.user(s, "event", turn, msg, t)
		case "item_completed":
			item, _ := payload["item"].(map[string]any)
			switch it, _ := item["type"].(string); it {
			case "UserMessage":
				id, _ := item["id"].(string)
				tr.userID(s, "item", turn, id, traeItemText(item, "text"), t)
			case "AgentMessage":
				if txt := traeItemText(item, "Text", "text", "output_text"); txt != "" {
					tr.items = append(tr.items, model.Message{Role: "assistant", Text: txt, Time: t})
				}
			}
		case "agent_message":
			if msg, _ := payload["message"].(string); msg != "" {
				tr.events = append(tr.events, model.Message{Role: "assistant", Text: msg, Time: t})
			}
		}
	case "response_item":
		if pt, _ := payload["type"].(string); pt != "message" {
			tr.tool(s, payload, cwd, t, calls)
			return
		}
		if role, _ := payload["role"].(string); role == "assistant" {
			if txt := textFromContent(payload["content"]); txt != "" {
				s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: txt, Time: t})
				tr.rolled = true
			}
		}
	}
}

// mirrors reports whether a prompt is the other record of this one.
func (tr *traeUser) mirrors(dialect, turn, text string, t time.Time) bool {
	if tr.matched || tr.dialect == dialect || tr.text != text {
		return false
	}
	if tr.turn != "" && turn != "" {
		return tr.turn == turn
	}
	d := t.Sub(tr.at)
	return d <= traeMirrorWindow && d >= -traeMirrorWindow
}

// user records a prompt once, whichever of its two records arrives first.
func (tr *traeTurns) user(s *model.Session, dialect, turn, text string, t time.Time) {
	tr.userID(s, dialect, turn, "", text, t)
}

// userID is user for a record that carries its item id: display_completions
// and item_completed can both hold one prompt, under the same id.
func (tr *traeTurns) userID(s *model.Session, dialect, turn, id, text string, t time.Time) {
	if text == "" {
		return
	}
	if id != "" {
		for _, u := range tr.users {
			if u.id == id {
				return
			}
		}
	}
	for i := len(tr.users) - 1; i >= 0; i-- {
		if tr.users[i].mirrors(dialect, turn, text, t) {
			tr.users[i].matched = true
			return
		}
	}
	tr.users = append(tr.users, traeUser{dialect: dialect, turn: turn, text: text, id: id, at: t})
	s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
}

// tool reads a call or its result once, from whichever record carries it.
func (tr *traeTurns) tool(s *model.Session, item map[string]any, cwd string, t time.Time, calls map[string]int) {
	pt, _ := item["type"].(string)
	if id, _ := item["call_id"].(string); id != "" {
		key := pt + "\x00" + id
		if tr.calls[key] {
			return
		}
		tr.calls[key] = true
	}
	switch pt {
	case "function_call":
		if traeClaudeTool(s, item, t) {
			return
		}
		codexCall(s, traeShellCall(item), calls, cwd, t)
	case "function_call_output", "custom_tool_call_output":
		codexCallOutput(s, traeOutput(item), calls, t)
	case "custom_tool_call":
		codexPatch(s, item, cwd, t)
	}
}

// finish adds the answers from the first source that had any.
func (tr *traeTurns) finish(s *model.Session) {
	if tr.rolled {
		return
	}
	var answers []model.Message
	switch {
	case len(tr.hist) > 0:
		answers = tr.hist
	case len(tr.items) > 0:
		answers = tr.items
	default:
		answers = tr.events
	}
	if len(answers) == 0 {
		return
	}
	s.Messages = append(s.Messages, answers...)
	sort.SliceStable(s.Messages, func(i, j int) bool { return s.Messages[i].Time.Before(s.Messages[j].Time) })
}

// traeItemText joins an item_completed item's text blocks of the given types,
// with nothing between them, as botmux's itemCompletedUserText does.
func traeItemText(item map[string]any, types ...string) string {
	blocks, _ := item["content"].([]any)
	var b strings.Builder
	for _, bl := range blocks {
		m, _ := bl.(map[string]any)
		typ, _ := m["type"].(string)
		txt, ok := m["text"].(string)
		if !ok {
			continue
		}
		for _, want := range types {
			if typ == want {
				b.WriteString(txt)
				break
			}
		}
	}
	return b.String()
}

// traeClaudeTool reads a call made with Claude Code's tool set, which TRAE's
// binary carries next to Codex's (Bash, Edit, Write, Read and the rest), through
// the Claude dialect. Codex's names are lowercase and left to codexCall.
func traeClaudeTool(s *model.Session, item map[string]any, t time.Time) bool {
	name, _ := item["name"].(string)
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	in := map[string]any{}
	switch a := item["arguments"].(type) {
	case string:
		_ = json.Unmarshal([]byte(a), &in)
	case map[string]any:
		in = a
	}
	id, _ := item["call_id"].(string)
	s.Messages = append(s.Messages, codeBuddyWorkRecords([]any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}, t)...)
	return true
}

// traeShellCall turns TRAE's shell call into the shape codexCall reads. In
// history_mutation it is named exec and takes an argv under command, the
// script last: ["bash","-lc","rg --files src"].
func traeShellCall(item map[string]any) map[string]any {
	name, _ := item["name"].(string)
	if name != "exec" && name != "shell" {
		return item
	}
	args, _ := item["arguments"].(string)
	var in struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(args), &in) != nil || len(in.Command) == 0 {
		return item
	}
	cmd := strings.Join(in.Command, " ")
	if n := len(in.Command); n >= 3 && (in.Command[n-2] == "-lc" || in.Command[n-2] == "-c") {
		cmd = in.Command[n-1]
	}
	b, _ := json.Marshal(map[string]string{"cmd": cmd})
	out := make(map[string]any, len(item))
	for k, v := range item {
		out[k] = v
	}
	out["name"] = "exec_command"
	out["arguments"] = string(b)
	return out
}

// traeOutput flattens a tool result given as a block list to the string
// codexCallOutput reads; images and unknown blocks carry no text.
func traeOutput(item map[string]any) map[string]any {
	blocks, ok := item["output"].([]any)
	if !ok {
		return item
	}
	var b strings.Builder
	for _, bl := range blocks {
		m, _ := bl.(map[string]any)
		switch typ, _ := m["type"].(string); typ {
		case "input_text", "output_text", "text":
			if txt, _ := m["text"].(string); txt != "" {
				b.WriteString(txt)
			}
		}
	}
	out := make(map[string]any, len(item))
	for k, v := range item {
		out[k] = v
	}
	out["output"] = b.String()
	return out
}

// traeResumes sends a rollout back for a whole read when its tail holds the
// second record of something read before the offset: one prompt's other
// dialect, or a call history_mutation repeats. Read alone, the tail would
// store either one twice, a moment apart, which the ingest de-duplicator does
// not collapse. Codex's own rule about failed exits still holds.
func traeResumes(path string, offset int64) bool {
	if !codexResumes(path, offset) {
		return false
	}
	if offset <= 0 {
		return true
	}
	var tail [][]traeKey
	_ = scanJSONLBytes(path, offset, func(line []byte) { tail = append(tail, traeKeys(line)) })
	pending := false
	for _, ks := range tail {
		if len(ks) > 0 {
			pending = true
			break
		}
	}
	if !pending {
		return true
	}
	var all [][]traeKey
	_ = scanJSONLBytes(path, 0, func(line []byte) { all = append(all, traeKeys(line)) })
	headLen := len(all) - len(tail)
	if headLen <= 0 {
		return true
	}
	for _, hks := range all[:headLen] {
		for _, h := range hks {
			for _, tks := range tail {
				for _, k := range tks {
					if h.repeats(k) {
						return false
					}
				}
			}
		}
	}
	return true
}

type traeKey struct {
	call                string // type and call_id of a tool record
	dialect, turn, text string // a prompt
}

func (h traeKey) repeats(k traeKey) bool {
	if h.call != "" || k.call != "" {
		return h.call == k.call
	}
	if h.dialect == k.dialect || h.text != k.text {
		return false
	}
	return h.turn == "" || k.turn == "" || h.turn == k.turn
}

func traeKeys(line []byte) []traeKey {
	if !bytes.Contains(line, []byte("call_id")) && !bytes.Contains(line, []byte("user_message")) && !bytes.Contains(line, []byte("UserMessage")) {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	payload, _ := m["payload"].(map[string]any)
	if payload == nil {
		return nil
	}
	callKey := func(item map[string]any) []traeKey {
		pt, _ := item["type"].(string)
		if id, _ := item["call_id"].(string); id != "" {
			return []traeKey{{call: pt + "\x00" + id}}
		}
		return nil
	}
	turn, _ := payload["turn_id"].(string)
	switch typ, _ := m["type"].(string); typ {
	case "history_mutation":
		var out []traeKey
		dcs, _ := payload["display_completions"].([]any)
		for _, d := range dcs {
			dc, _ := d.(map[string]any)
			item, _ := dc["item"].(map[string]any)
			if it, _ := item["type"].(string); it == "UserMessage" {
				dturn, _ := dc["turn_id"].(string)
				out = append(out, traeKey{dialect: "display", turn: dturn, text: traeItemText(item, "text")})
			}
		}
		items, _ := payload["items"].([]any)
		for _, it := range items {
			if item, _ := it.(map[string]any); item != nil {
				out = append(out, callKey(item)...)
			}
		}
		return out
	case "response_item":
		return callKey(payload)
	case "event_msg":
		switch pt, _ := payload["type"].(string); pt {
		case "user_message":
			msg, _ := payload["message"].(string)
			return []traeKey{{dialect: "event", turn: turn, text: msg}}
		case "item_completed":
			item, _ := payload["item"].(map[string]any)
			if it, _ := item["type"].(string); it == "UserMessage" {
				return []traeKey{{dialect: "item", turn: turn, text: traeItemText(item, "text")}}
			}
		}
	}
	return nil
}
