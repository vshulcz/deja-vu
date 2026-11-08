package sources

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// parseClaudeGenericFromOffset is the previous implementation, kept as the
// reference the typed parser is proved against — including on a real store,
// where the shapes nobody thought of live.
func parseClaudeGenericFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: claudeProjectNameFor(path), Path: path}
	// Where each Bash call's record landed, so the result that arrives in a
	// later record can stamp its outcome onto it.
	commandAt := map[string][]int{}
	forkedFrom := ""
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if ff, ok := m["forkedFrom"].(map[string]any); ok && forkedFrom == "" {
			forkedFrom, _ = ff["sessionId"].(string)
		}
		if typ != "user" && typ != "assistant" {
			return
		}
		if meta, _ := m["isMeta"].(bool); meta && typ == "user" {
			// The harness's own user-role record (#3267); the typed parser
			// skips it the same way.
			return
		}
		side, _ := m["isSidechain"].(bool)
		agent, _ := m["agentId"].(string)
		if side && agent != "" {
			s.ID = agent
			s.Kind = "sidechain"
			s.Parent, _ = m["sessionId"].(string)
			if name, _ := m["attributionAgent"].(string); name != "" {
				s.Agent = name
			}
		} else if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		role := typ
		txt := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				role = r
			}
			var toolOut bool
			txt, toolOut = textFromContentKind(msg["content"])
			if toolOut {
				role = RoleToolOutput
			}
		}
		if summary, _ := m["isCompactSummary"].(bool); summary && typ == "user" && role == "user" {
			role = RoleSummary
		}
		if role == "user" {
			txt = UnwrapPastedContent(txt)
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
		if msg, ok := m["message"].(map[string]any); ok {
			if IndexToolPaths() {
				if p := toolPathsFromContent(msg["content"]); p != "" {
					s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexEdits() {
				for _, e := range editSpansFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: e, Time: t})
				}
			}
			if IndexWrites() {
				for _, w := range wroteRecordsFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range commandCallsFromContent(msg["content"]) {
					if cmd.ID != "" {
						// One call can carry a batch of commands, and the
						// filter drops some of them, so the indexes under one
						// id are neither one nor contiguous.
						commandAt[cmd.ID] = append(commandAt[cmd.ID], len(s.Messages))
					}
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd.Text, Time: t})
				}
				// The same stamp the typed parser writes, from the same
				// fields.
				for _, res := range toolOutcomesFromContent(msg["content"]) {
					if _, ok := commandAt[res.ID]; !ok || !res.Known {
						continue
					}
					commandExits(commandAt).stamp(s.Messages, res.ID, "", res.Code)
					delete(commandAt, res.ID)
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	markClaudeBranch(&s, forkedFrom)
	// The same cut the typed parser makes: this is the reference it is proved
	// against, and a difference here reads as a parser bug (#3009).
	if IsSubagentPath(path) && !SubagentsWhole() {
		s.Messages = KeepSubagentTail(s.Messages)
	}
	resolveDirProject(&s)
	return []model.Session{s}, err
}

// claudeText joins the text a message carries. Items that are not objects, or
// objects with neither key, are skipped rather than failing the line — a
// transcript mixing shapes must not cost the whole message.
func claudeText(raw json.RawMessage) string {
	txt, _ := claudeTextKind(raw)
	return txt
}

// claudeTextKind joins a message's text and reports whether what it joined came
// from tool results rather than from speech. A message mixing both counts as
// speech: the person said something, and the tool output rode along with it.
func claudeTextKind(raw json.RawMessage) (string, bool) {
	return claudeTextKindIn(splitClaudeContent(raw))
}

// claudeToolPaths returns the distinct file paths a message's tool calls name,
// one per line, or "" when it names none.
func claudeToolPaths(raw json.RawMessage) string { return claudeToolPathsIn(splitClaudeContent(raw)) }

// claudeEditSpans returns "path\nreplaced bytes" for every edit in a turn,
// including the sub-edits of a MultiEdit call.
func claudeEditSpans(raw json.RawMessage) []string { return claudeEditSpansIn(splitClaudeContent(raw)) }

// claudeWroteRecords returns "path\n<line hashes>" for every write in a turn:
// the new side of an Edit, of each sub-edit of a MultiEdit, and the whole
// content of a Write. The mirror of claudeEditSpans, which takes the old side.
func claudeWroteRecords(raw json.RawMessage) []string {
	return claudeWroteRecordsIn(splitClaudeContent(raw))
}

// Most update lines are large tool events. Filter them before JSON decoding so
// indexing cost is dominated by reading the log rather than materializing tool
// payloads that will be discarded.
func scanGrokUpdates(path string, fn func(grokUpdateEvent)) error {
	return scanGrokUpdatesFrom(path, 0, fn)
}

// notesParses counts the parses that actually read the file, for the test that
// pins the memo.
func notesParses() int {
	notesMemo.Lock()
	defer notesMemo.Unlock()
	return notesMemo.parses
}

func parseNestedTime(m map[string]any, k, sub string) time.Time {
	if x, ok := m[k].(map[string]any); ok {
		return parseTimeAny(x[sub])
	}
	return time.Time{}
}

// opencodeV2 reports whether the conversation in a store is kept the 2.x way.
func opencodeV2(db string) bool { return opencodeSchemaOf(db).v2 }

// toolPathsFromContent is claudeToolPaths for the reference parser, which walks
// decoded maps rather than raw JSON. The two must agree or the differential
// test in #502 stops meaning anything.
func toolPathsFromContent(v any) string { return toolPathsIn(v, claudeDialect) }

// editSpansFromContent is claudeEditSpans for the reference parser.
func editSpansFromContent(v any) []string { return editSpansIn(v, claudeDialect) }

// wroteRecordsFromContent is claudeWroteRecords for the reference parser: the
// written side of the same calls editSpansIn reads the replaced side of.
func wroteRecordsFromContent(v any) []string { return wroteRecordsIn(v, claudeDialect) }

// commandsFromContent is claudeCommands for the reference parser.
func commandsFromContent(v any) []string { return commandsIn(v, claudeDialect) }

// commandCallsFromContent is commandsFromContent with the call id each command
// will be answered under, so the reference parser can stamp an outcome the same
// way the typed one does. The two must agree or the differential test in #502
// stops meaning anything.
func commandCallsFromContent(v any) []claudeCommand { return commandCallsIn(v, claudeDialect) }

// toolOutcomesFromContent is claudeToolOutcomes for the reference parser.
func toolOutcomesFromContent(v any) []claudeToolOutcome {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []claudeToolOutcome
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if id == "" {
			continue
		}
		bad, _ := m["is_error"].(bool)
		out = append(out, claudeOutcome(id, bad, m["content"]))
	}
	return out
}

func ReadGeminiCompaction(path, nativeSessionID string) (CompactionTranscript, bool, error) {
	return ReadGeminiCompactionIfNew(path, nativeSessionID, nil)
}

func ReadZCodeCompaction(sessionID string) (CompactionTranscript, bool, error) {
	return ReadZCodeCompactionIfNew(sessionID, nil)
}

func ReadCodeWhaleCompaction(workspace string, since time.Time) (CompactionTranscript, bool, error) {
	return ReadCodeWhaleCompactionIfNew(workspace, since, nil)
}

func GooseTurnFailure(sessionID string) string {
	return GooseTurn(sessionID).Failure
}

func ReadJunieCompaction(sessionID string) (CompactionTranscript, bool, error) {
	return ReadJunieCompactionFromTurnIfNew(sessionID, JunieTurn(sessionID), nil)
}

func LifecycleKeys(m map[string]Lifecycle) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// StaleCandidateNotes returns accepted notes whose cited files were touched in
// sessions strictly newer than the note's timestamp.
func StaleCandidateNotes(notes []PromotedNote, fileLastTouched map[string]time.Time) []StaleCandidate {
	if len(notes) == 0 || len(fileLastTouched) == 0 {
		return nil
	}
	normTouched := make(map[string]time.Time, len(fileLastTouched)*2)
	for path, t := range fileLastTouched {
		norm := strings.ToLower(filepath.ToSlash(path))
		if t.After(normTouched[norm]) {
			normTouched[norm] = t
		}
		base := filepath.Base(norm)
		if t.After(normTouched[base]) {
			normTouched[base] = t
		}
	}
	var out []StaleCandidate
	for _, n := range notes {
		if n.State != "accepted" || n.At.IsZero() {
			continue
		}
		for _, ref := range NoteFileRefs(n) {
			key := strings.ToLower(filepath.ToSlash(ref))
			touchedAt := normTouched[key]
			if touchedAt.IsZero() {
				touchedAt = normTouched[filepath.Base(key)]
			}
			if touchedAt.After(n.At) {
				out = append(out, StaleCandidate{Note: n, File: ref, TouchedAt: touchedAt})
				break
			}
		}
	}
	return out
}

// LoadOpencodeMatching reads the OpenCode sessions whose text contains q.
func LoadOpencodeMatching(q string) []model.Session {
	where := fmt.Sprintf(" and lower(p.data) like '%%%s%%' escape '\\'", sqlEscape(sqlLikeEscape(strings.ToLower(q))))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 5000)
	return ss
}

// LoadOpencodeSince reads the OpenCode sessions that changed after t.
func LoadOpencodeSince(t time.Time) []model.Session {
	ss, _ := ParseOpencodeDBSince(OpencodeDB(), t)
	return ss
}

// PiProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "--Users-x-projects-app--" for .../sessions/--Users-x-projects-app--/s.jsonl.
func PiProjectDirBase(path string) string {
	dir := projectDir(PiRoot(), path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// AllHarnesses is every harness deja knows how to read, whatever DEJA_STORES
// says. The set `--harness` accepts, and the number the documentation counts:
// silencing a store for one run does not make deja a tool that reads fewer
// harnesses.
func AllHarnesses() []Harness { return allHarnesses() }

// HomeRoots returns Home() followed by any additional readable home
// directories listed in DEJA_HOME_ROOTS (separated by filepath.ListSeparator,
// e.g. "/home/alice:/home/bob" on shared multi-user servers). Relative paths
// and unreadable directories are skipped so a restrictive peer home never
// fails the caller's pass (#5759, #5773).
func HomeRoots() []string {
	var out []string
	if h := Home(); h != "" {
		out = append(out, filepath.Clean(h))
	}
	for _, h := range extraHomeRoots() {
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}
