package sources

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// hermesDialect is Hermes' tool vocabulary, read off its own schemas in
// tools/file_tools.py and tools/terminal_tool.py: the shell is `terminal`, the
// file tools take `path`, and patch in its default replace mode takes
// old_string/new_string like Claude's Edit. search_files' path is a directory
// to search, not a file the session touched, so it is left out.
var hermesDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"read_file": true, "write_file": true, "patch": true},
	shellTool: "terminal",
	editTools: map[string]bool{"patch": true, "write_file": true},
}

// hermesSession is one session being read, with what its rows refer back to.
type hermesSession struct {
	s    model.Session
	rows []hermesRow
	// commandAt is which messages hold the commands a call ran, by call id, so
	// the exit code on the `tool` row lands on its command.
	commandAt map[string][]int
	dropped   bool
}

// hermesRow is one row of the messages table, held until the session is
// complete: whether a live row is compaction's copy depends on the rows
// around it.
type hermesRow struct {
	role, text, calls, tool, callID string
	archived                        bool
	t                               time.Time
	// summary is the compaction summary the row carries, and text what is
	// left of the row after it.
	summary string
	copy    bool
	k       string
}

func newHermesSession(s model.Session) *hermesSession {
	return &hermesSession{s: s, commandAt: map[string][]int{}}
}

// hermesSummaryPrefixes open the message compaction writes between the head
// and tail it keeps: SUMMARY_PREFIX and its historical spellings, and
// LEGACY_SUMMARY_PREFIX (agent/context_compressor.py).
var hermesSummaryPrefixes = []string{"[CONTEXT COMPACTION", "[CONTEXT SUMMARY]:"}

// hermesSummaryEnd closes the summary. When the kept head ends on assistant
// and the tail starts on user, Hermes prepends the summary to the first tail
// message instead of writing a row of its own, and what follows the marker is
// that message (_merge_summary_into_tail).
const hermesSummaryEnd = "--- END OF CONTEXT SUMMARY"

func (h *hermesSession) row(r map[string]any) {
	row := hermesRow{
		role: str(r["role"]), text: hermesText(str(r["content"])), calls: str(r["tool_calls"]),
		tool: str(r["tool_name"]), callID: str(r["tool_call_id"]),
		archived: fmt.Sprint(r["compacted"]) == "1", t: hermesTime(r["timestamp"]),
	}
	if row.role != "tool" {
		for _, p := range hermesSummaryPrefixes {
			if !strings.HasPrefix(row.text, p) {
				continue
			}
			row.summary, row.text = row.text, ""
			if i := strings.Index(row.summary, hermesSummaryEnd); i >= 0 {
				rest := row.summary[i:]
				if j := strings.IndexByte(rest, '\n'); j >= 0 {
					row.text = strings.TrimSpace(rest[j+1:])
				}
				row.summary = strings.TrimSpace(row.summary[:i])
			}
			break
		}
	}
	h.rows = append(h.rows, row)
}

// key is what a copy of the row shares with its archived original: the text
// and call ids, not the results, which compaction replaces with stubs.
func (r *hermesRow) key() string {
	if r.k == "" {
		r.k = r.makeKey()
	}
	return r.k
}

func (r *hermesRow) makeKey() string {
	if r.role == "tool" && r.callID != "" {
		return "tool\x00" + r.callID
	}
	return r.role + "\x00" + r.text + "\x00" + strings.Join(hermesCallIDs(r.calls), " ")
}

// done reads the rows and hands the session back, without the command
// records of runs that never happened.
func (h *hermesSession) done() model.Session {
	h.markCopies()
	for _, r := range h.rows {
		if r.copy {
			continue
		}
		if r.summary != "" {
			// A restatement of turns the store still holds, kept where
			// `--role summary` reaches it and ordinary search does not.
			h.s.Touch(r.t)
			h.s.Messages = append(h.s.Messages, model.Message{Role: RoleSummary, Text: capParsedMessage(r.summary), Time: r.t})
		}
		if r.role == "tool" {
			h.result(r.text, r.tool, r.callID, r.t)
			continue
		}
		if r.text != "" {
			h.s.Touch(r.t)
			h.s.Messages = append(h.s.Messages, model.Message{Role: r.role, Text: capParsedMessage(r.text), Time: r.t})
		}
		if r.role == "assistant" {
			h.calls(r.calls, r.t)
		}
	}
	h.rows = nil
	if h.dropped {
		kept := h.s.Messages[:0]
		for _, m := range h.s.Messages {
			if m.Role != "" {
				kept = append(kept, m)
			}
		}
		h.s.Messages = kept
	}
	return h.s
}

// hermesCallList reads the tool_calls column. It is json.dumps of what the
// caller passed, so a single call can be an object rather than a list
// (hermes_state.py append_message).
func hermesCallList(raw string) []any {
	if raw == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return []any{m}
	}
	calls, _ := v.([]any)
	return calls
}

func hermesCallIDs(raw string) []string {
	var ids []string
	for _, c := range hermesCallList(raw) {
		if m, ok := c.(map[string]any); ok {
			if id, _ := m["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// calls turns an assistant row's OpenAI-style tool_calls into work records.
func (h *hermesSession) calls(raw string, t time.Time) {
	for _, c := range hermesCallList(raw) {
		id := ""
		if m, ok := c.(map[string]any); ok {
			id, _ = m["id"].(string)
		}
		blocks := reasonixToolUses([]any{c})
		if len(blocks) == 0 {
			continue
		}
		var records []model.Message
		if IndexToolPaths() {
			if p := toolPathsIn(blocks, hermesDialect); p != "" {
				records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
			}
		}
		if IndexWrites() {
			for _, w := range wroteRecordsIn(blocks, hermesDialect) {
				records = append(records, model.Message{Role: RoleWrote, Text: w, Time: t})
			}
		}
		if IndexEdits() {
			for _, span := range editSpansIn(blocks, hermesDialect) {
				records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
			}
		}
		records = append(records, hermesPatchRecords(blocks[0], t)...)
		var at []int
		if IndexCommands() {
			for _, cmd := range commandsIn(blocks, hermesDialect) {
				at = append(at, len(h.s.Messages)+len(records))
				records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
			}
		}
		if len(records) == 0 {
			continue
		}
		if id != "" && len(at) > 0 {
			h.commandAt[id] = at
		}
		h.s.Touch(t)
		h.s.Messages = append(h.s.Messages, records...)
	}
}

// result records a `tool` row. Every Hermes tool answers in JSON. What it
// says is usually in output (terminal), content (read_file), diff (patch),
// matches_text (search_files) or error, and then the rest is bookkeeping;
// other tools nest it — search_files' files, web_search's data, the results
// of web_extract and delegate_task — and when none of those keys is there,
// every string in it is kept.
// Keys starting with _ are hints to the model and never kept.
//
// terminal's exit_code rides on the command it answers. -1 is a command that
// never ran — denied, blocked, waiting on approval, invalid, failed to start
// (tools/terminal_tool.py) — so its command record is dropped: kept, it reads
// as a run that happened, and `→ exit -1` is not a status the index reads.
// Why it did not run stays in the tool output.
func (h *hermesSession) result(txt, tool, callID string, t time.Time) {
	if txt == "" || hermesCompressorStub(txt, tool) {
		return
	}
	if strings.HasPrefix(txt, "{") {
		var res map[string]any
		if json.Unmarshal([]byte(txt), &res) == nil {
			if code, ok := res["exit_code"]; ok {
				h.exit(callID, exitCode(code))
			}
			txt = hermesResultText(res)
		}
	}
	if txt == "" || !IndexToolOutput() {
		return
	}
	h.s.Touch(t)
	h.s.Messages = append(h.s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(txt), Time: t})
}

func (h *hermesSession) exit(callID string, code int) {
	// Other negative codes are a process killed by a signal after it started
	// (tools/environments/base.py); it ran, and the marker reads digits only.
	if code == 0 || callID == "" || (code < 0 && code != -1) {
		return
	}
	for _, i := range h.commandAt[callID] {
		if i >= len(h.s.Messages) || h.s.Messages[i].Role != RoleCommand {
			continue
		}
		if code == -1 {
			h.s.Messages[i].Role = ""
			h.dropped = true
			continue
		}
		h.s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
	}
	delete(h.commandAt, callID)
}

var hermesResultKeys = []string{"output", "content", "diff", "matches_text", "error"}

func hermesResultText(res map[string]any) string {
	var parts []string
	known := false
	for _, k := range hermesResultKeys {
		v, ok := res[k]
		known = known || ok
		parts = hermesLeaves(v, true, parts)
	}
	// Only a result that has none of those keys holds its text elsewhere; an
	// empty output is a quiet run, and what sits beside it is bookkeeping.
	if !known {
		parts = hermesLeaves(res, false, nil)
	}
	return strings.Join(parts, "\n")
}

// hermesLeaves appends the strings in v, in key order so a record reads the
// same on every parse. Numbers count only where the key says the value is
// the result — {"output": 42} — and not as the counts and indexes beside it.
func hermesLeaves(v any, numbers bool, out []string) []string {
	switch e := v.(type) {
	case string:
		if s := strings.TrimSpace(e); s != "" {
			out = append(out, s)
		}
	case float64:
		if numbers {
			out = append(out, strconv.FormatFloat(e, 'f', -1, 64))
		}
	case []any:
		for _, it := range e {
			out = hermesLeaves(it, numbers, out)
		}
	case map[string]any:
		keys := make([]string, 0, len(e))
		for k := range e {
			if !strings.HasPrefix(k, "_") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = hermesLeaves(e[k], numbers, out)
		}
	}
	return out
}

// hermesCompressorStub reports text Hermes' context compressor wrote in place
// of a result it cleared (agent/context_compressor.py): a fixed placeholder,
// or a one-line summary that opens with the tool's name in brackets.
func hermesCompressorStub(txt, tool string) bool {
	for _, p := range []string{
		"[Old tool output cleared to save context space]",
		"[Duplicate tool output",
		"[Result from earlier conversation",
		"[screenshot removed",
	} {
		if strings.HasPrefix(txt, p) {
			return true
		}
	}
	return tool != "" && strings.HasPrefix(txt, "["+tool+"]")
}

// hermesPatchHeader and hermesPatchMove are the file headers as Hermes' own
// V4A parser matches them (tools/patch_parser.py): the space after *** is
// optional, and Move File names two paths.
var (
	hermesPatchHeader = regexp.MustCompile(`^\*\*\*\s*(Update|Add|Delete)\s+File:\s*(.+)$`)
	hermesPatchMove   = regexp.MustCompile(`^\*\*\*\s*Move\s+File:\s*(.+?)\s*->\s*(.+)$`)
	hermesPatchEnd    = regexp.MustCompile(`^\*\*\*\s*End Patch`)
)

// hermesPatchRecords reads patch in its V4A mode, where the call carries a
// multi-file patch instead of a path and a span. The headers are rewritten to
// the spelling the apply_patch helpers read, so the spans come out the same.
func hermesPatchRecords(block any, t time.Time) []model.Message {
	b, _ := block.(map[string]any)
	if name, _ := b["name"].(string); name != "patch" {
		return nil
	}
	in, _ := b["input"].(map[string]any)
	patch, _ := in["patch"].(string)
	if patch == "" {
		return nil
	}
	var files []string
	seen := map[string]bool{}
	file := func(f string) {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	lines := strings.Split(patch, "\n")
	// After Delete File and Move File the parser has no current file and
	// passes over every line up to the next header, so those lines belong
	// to no file here either.
	orphan := false
	for i, line := range lines {
		switch {
		case hermesPatchHeader.MatchString(line):
			m := hermesPatchHeader.FindStringSubmatch(line)
			file(m[2])
			lines[i] = "*** " + m[1] + " File: " + strings.TrimSpace(m[2])
			orphan = m[1] == "Delete"
		case hermesPatchMove.MatchString(line):
			m := hermesPatchMove.FindStringSubmatch(line)
			file(m[1])
			file(m[2])
			// A header of its own ends the file before it.
			lines[i] = "*** Delete File: " + strings.TrimSpace(m[1])
			orphan = true
		case hermesPatchEnd.MatchString(line):
			lines[i] = "*** End Patch"
		case orphan:
			lines[i] = ""
		}
	}
	patch = strings.Join(lines, "\n")
	var out []model.Message
	if IndexToolPaths() && len(files) > 0 {
		out = append(out, model.Message{Role: RoleFiles, Text: strings.Join(files, "\n"), Time: t})
	}
	if IndexWrites() {
		for _, rec := range addedLinesOfPatch(patch) {
			out = append(out, model.Message{Role: RoleWrote, Text: rec, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range patchSpans(patch) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	return out
}
