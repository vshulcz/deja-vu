package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Devin CLI (`devin`, Cognition's local agent) keeps every session in one
// SQLite store, <data>/devin/cli/sessions.db. A session is a row of
// `sessions` plus its nodes in `message_nodes` — but the table in order is
// not the conversation: the agent rebuilds its context chain whenever the
// system prefix changes, so the store keeps every copy, several of them
// sharing their `message_id`s, and `sessions.main_chain_id` names the head
// of the live one. Reading backwards over `parent_node_id` from that head
// and keeping one copy of each `message_id` gives the conversation the
// model saw.
//
//	sessions(id, working_directory, backend_type, model, agent_mode,
//	         created_at, last_activity_at, title, main_chain_id, …, hidden)
//	message_nodes(session_id, node_id, parent_node_id, chat_message,
//	              created_at, metadata)
//	subagent_heads(session_id, agent_id, chain_node_id, updated_at)
//	prompt_history(session_id, content, timestamp, is_shell)
//
// Every timestamp is unix seconds. A node's `chat_message` is
//
//	{"message_id","role":"system|user|assistant|tool","content":<string>,
//	 "tool_calls":[{"id","name","arguments","index","kind"}],
//	 "tool_call_id","metadata":{"extensions":{
//	   "chisel/terminal_output":{"exit":{"exit_code"}},
//	   "chisel/tool_result_meta":{"success","kind"}}}}
//
// and its `metadata` column marks the rebuilt scaffolding with
// `is_system_prefix: true`; injected context — a hook's, a completed
// subagent's report — carries no flag and is kept.
//
// Subagent runs share the parent's node table. Builds that write
// `subagent_heads` declare each run's stable `agent_id` and the node its
// chain ends on; builds that never did leave only the chains the main one
// cannot reach, which the leaf heuristic walks. `run_subagent` is not
// resumable, so a subagent's id is `<session>:<agent_id>` when declared and
// `<session>:<first message id of the part no earlier session carried>`
// when it is not, and its Parent the session id.
//
// Read off Devin CLI 3000.11.3; `devin --resume <id>` takes the sessions
// row's id. On Windows the store lives under %LOCALAPPDATA%\devin; Linux and
// macOS share the XDG data directory, ~/.local/share/devin by default.

// devinDataDir is Devin CLI's local data directory, the platform's own.
func devinDataDir() string {
	switch runtime.GOOS {
	case "windows":
		app := os.Getenv("LOCALAPPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Local")
		}
		return filepath.Join(app, "devin")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(Home(), ".local", "share")
	}
	return filepath.Join(data, "devin")
}

// DevinSessionsDB is the session store. DEJA_DEVIN_DB replaces it.
func DevinSessionsDB() string {
	return EnvPath("DEJA_DEVIN_DB", filepath.Join(devinDataDir(), "cli", "sessions.db"))
}

// DevinLegacySessionsDB is the name older builds used for the same store;
// the binary still names both, so an un-migrated data directory is still read.
func DevinLegacySessionsDB() string {
	return filepath.Join(devinDataDir(), "cli", "cli_sessions.db")
}

// devinSummariesDir is where the summarizer agent writes
// <session_id>.md beside the store — the only record of what a compaction
// dropped.
func devinSummariesDir(db string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(db)), "summaries")
}

// DevinFiles is every store path deja would read when it exists.
func DevinFiles() []string {
	var out []string
	for _, p := range []string{DevinSessionsDB(), DevinLegacySessionsDB()} {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			out = append(out, p)
		}
	}
	return out
}

// LoadDevin reads every Devin store that exists. A store that would not
// read — locked past the timeout by the agent using it, or a schema sqlite3
// refused — is reported like a transcript that would not parse, so the pass
// says so and does not record the store as read.
func LoadDevin() []model.Session {
	var all []model.Session
	for _, p := range DevinFiles() {
		s, err := ParseDevinDB(p)
		all = append(all, dbRead(p, s, err)...)
	}
	return all
}

// devinDBMatch is the FileKind matcher for the Devin store: the candidates
// DevinFiles walks, no other path.
func devinDBMatch(p string) bool {
	return p == DevinSessionsDB() || p == DevinLegacySessionsDB()
}

// ParseDevinDB reads every session in the store.
func ParseDevinDB(db string) ([]model.Session, error) {
	return parseDevinDBWhere(db, "")
}

// ParseDevinDBSince reads the sessions active after t, whole: a session row
// always re-exports its full chain, so what comes back replaces it in the
// index. last_activity_at is in seconds, where kiro's is in milliseconds.
func ParseDevinDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseDevinDB(db)
	}
	return parseDevinDBWhere(db, fmt.Sprintf(" where last_activity_at > %d", t.Add(-time.Second).Unix()))
}

func parseDevinDBWhere(db, where string) ([]model.Session, error) {
	// The sqlite3 CLI creates a missing database on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// `hidden` landed after the first builds that wrote this store; a column
	// the query names but the schema lacks fails the whole read. Probe once
	// and emit a literal for what is missing — but only on a clean negative:
	// a probe that errors (a locked file, say) leaves `hidden` in the query
	// so the main read reports the real failure instead of indexing hidden
	// sessions behind a literal 0.
	hidden := "hidden"
	if devinDBColumnMissing(db, "sessions", "hidden") {
		hidden = "0"
	}
	// subagent_heads is the newer schema's own record of which chains are
	// subagent runs; probe the table once per store rather than per session.
	hasSubHeads := devinDBTableExists(db, "subagent_heads")
	q := `select json_object('id',id,'dir',working_directory,'title',title,` +
		`'created',created_at,'updated',last_activity_at,'head',main_chain_id,'hidden',` + hidden + `)` +
		` from sessions` + where + ` order by last_activity_at`
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	var out []model.Session
	rows := 0
	for dec.More() {
		var r struct {
			ID      string `json:"id"`
			Dir     string `json:"dir"`
			Title   string `json:"title"`
			Created int64  `json:"created"`
			Updated int64  `json:"updated"`
			Head    *int64 `json:"head"`
			Hidden  int64  `json:"hidden"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		// A hidden session is one the harness itself no longer lists —
		// `devin` marks it rather than deleting it. Indexing it would
		// resurrect sessions their owner removed from view.
		if r.ID == "" || r.Hidden != 0 {
			continue
		}
		s := model.Session{Harness: "devin", ID: r.ID, Path: db, Title: r.Title}
		if r.Dir != "" {
			s.Project = projectName(r.Dir)
		}
		nodes, err := devinSessionNodes(db, r.ID)
		if err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		sides := devinSessionChains(&s, nodes, r.Head, devinSubagentHeads(db, r.ID, hasSubHeads))
		devinPromptHistory(&s, db)
		if len(s.Messages) == 0 && len(nodes) == 0 {
			continue
		}
		if r.Created > 0 {
			s.Touch(time.Unix(r.Created, 0).UTC())
		}
		if r.Updated > 0 {
			s.Touch(time.Unix(r.Updated, 0).UTC())
		}
		devinAttachSummary(&s, db)
		out = append(out, s)
		out = append(out, sides...)
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// A store without the tables yet — devin creates them lazily —
			// is an empty store, not a schema change to report.
			if !devinDBHasTable(db) {
				return nil, nil
			}
			return nil, schemaChanged("devin", err)
		}
		return nil, err
	}
	return out, nil
}

// devinDBHasTable reports whether the store has sessions yet. A query that
// cannot run says true, so a store sqlite3 refuses is still reported.
func devinDBHasTable(db string) bool {
	out, err := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='sessions'")
	return err != nil || strings.TrimSpace(string(out)) != "0"
}

// devinNode is one message_nodes row.
type devinNode struct {
	nodeID  int64
	parent  int64
	hasPar  bool // parent_node_id is NULL on chain roots
	msgID   string
	msg     map[string]any
	created int64
}

// devinSessionNodes loads every node of one session, in row order.
func devinSessionNodes(db, sid string) ([]devinNode, error) {
	q := `select json_object('node',node_id,'parent',parent_node_id,'mid',` +
		`json_extract(chat_message,'$.message_id'),'msg',json_extract(chat_message,'$'),'created',created_at,` +
		`'prefix',coalesce(json_extract(metadata,'$.is_system_prefix'),0))` +
		` from message_nodes where session_id = '` + strings.ReplaceAll(sid, "'", "''") +
		`' order by node_id`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return nil, err
	}
	type row struct {
		Node    int64           `json:"node"`
		Parent  *int64          `json:"parent"`
		Mid     string          `json:"mid"`
		Msg     json.RawMessage `json:"msg"`
		Created int64           `json:"created"`
		Prefix  int             `json:"prefix"`
	}
	rows, err := sqliteObjects[row](out)
	if err != nil {
		return nil, err
	}
	nodes := make([]devinNode, 0, len(rows))
	for _, r := range rows {
		n := devinNode{nodeID: r.Node, msgID: r.Mid, created: r.Created}
		if r.Parent != nil {
			n.parent, n.hasPar = *r.Parent, true
		}
		if json.Unmarshal(r.Msg, &n.msg) != nil {
			continue
		}
		n.msg["__prefix__"] = r.Prefix != 0
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// devinChain is the nodes of one chain, oldest first, deduped by
// message_id.
func devinChain(nodes map[int64]devinNode, head int64) []devinNode {
	var rev []devinNode
	seen := map[string]bool{}
	for id, guard := head, 0; guard < len(nodes)+1; guard++ {
		n, ok := nodes[id]
		if !ok {
			break
		}
		if n.msgID == "" || !seen[n.msgID] {
			seen[n.msgID] = true
			rev = append(rev, n)
		}
		if !n.hasPar {
			break
		}
		id = n.parent
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// devinSubHead is one row of subagent_heads: a run's stable agent id and
// the chain node it currently ends on.
type devinSubHead struct {
	Agent string `json:"agent"`
	Node  int64  `json:"node"`
}

// devinSubagentHeads reads the session's declared subagent runs. Builds that
// predate the table have none — the caller probes that once per store, and
// a query failure here reads as empty: the leaf heuristic below still finds
// their chains.
func devinSubagentHeads(db, sid string, table bool) []devinSubHead {
	if !table {
		return nil
	}
	out, err := sqliteOutput(db, `select json_object('agent',agent_id,'node',chain_node_id)`+
		` from subagent_heads where session_id = '`+strings.ReplaceAll(sid, "'", "''")+`'`)
	if err != nil {
		return nil
	}
	heads, err := sqliteObjects[devinSubHead](out)
	if err != nil {
		return nil
	}
	return heads
}

// devinDBTableExists reports whether the store names the table at all.
func devinDBTableExists(db, table string) bool {
	out, err := sqliteOutput(db, `select count(*) from sqlite_master where type='table' and name='`+table+`'`)
	return err == nil && strings.TrimSpace(string(out)) != "0"
}

// devinDBColumnMissing reports a confirmed absence — older sessions.db
// schemas predate `hidden`, and naming a missing column in the sessions
// query fails the whole store read. A probe that itself fails answers
// false, leaving the column in the query so the real error surfaces there.
func devinDBColumnMissing(db, table, column string) bool {
	out, err := sqliteOutput(db, `select count(*) from pragma_table_info('`+table+`') where name='`+column+`'`)
	return err == nil && strings.TrimSpace(string(out)) == "0"
}

// devinSessionChains fills s.Messages from the session's chains: the main
// one from head (or the newest node when the row names none), then every
// chain the main one does not reach — the subagent runs — appended as their
// own messages on sub-sessions the caller appends after s. Declared
// subagent_heads rows name runs directly; the leaf heuristic covers builds
// that never wrote the table and whatever it misses.
func devinSessionChains(s *model.Session, nodes []devinNode, head *int64, heads []devinSubHead) []model.Session {
	byID := make(map[int64]devinNode, len(nodes))
	children := map[int64]bool{}
	var maxID int64 = -1
	for _, n := range nodes {
		byID[n.nodeID] = n
		if n.nodeID > maxID {
			maxID = n.nodeID
		}
		if n.hasPar {
			children[n.parent] = true
		}
	}
	h := maxID
	if head != nil {
		h = *head
	}
	main := devinChain(byID, h)
	mainNodes := map[int64]bool{}
	emitted := map[string]bool{}
	emittedNode := map[int64]bool{}
	// A node is covered once a session carried it — by node_id, or by
	// message_id when it has one: a rebuilt copy is another node_id under
	// the same message_id, and an empty message_id marks nothing.
	covered := func(n devinNode) bool {
		return emittedNode[n.nodeID] || n.msgID != "" && emitted[n.msgID]
	}
	var exits commandExits
	if IndexCommands() {
		exits = commandExits{}
	}
	devinEmitChain(s, main, exits)
	for _, n := range main {
		mainNodes[n.nodeID] = true
		emittedNode[n.nodeID] = true
		if n.msgID != "" {
			emitted[n.msgID] = true
		}
	}

	// A chain the main one does not reach is a conversation of its own: the
	// subagent a run_subagent spawned. Roots are nodes with no parent; each
	// such leaf's chain is walked once — the deepest leaf first, so the
	// copy whose build ran longest is the one emitted and the rest have
	// nothing left to say (every one of their message_ids is already out).
	// A branch off a chain already emitted — an edited ask, a forked run —
	// contributes only the suffix past the node it left from. Leaves,
	// deepest first: a side chain's best copy ends on the leaf its last
	// build wrote, which is the one with the highest node_id under that
	// root.
	var leaves []devinNode
	for _, n := range nodes {
		if mainNodes[n.nodeID] || children[n.nodeID] {
			continue
		}
		leaves = append(leaves, n)
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].nodeID > leaves[j].nodeID })
	// Declared subagent runs first: the table names each run by an agent id
	// that survives a rebuild of its chain, which the message-derived id
	// below cannot do. Their chains are walked here so the leaf pass sees
	// their message_ids as spoken for.
	var sides []model.Session
	emitSub := func(chain []devinNode, id string) bool {
		if len(chain) == 0 {
			return false
		}
		sub := model.Session{
			Harness: "devin", Kind: "subagent",
			ID:      s.ID + ":" + id,
			Parent:  s.ID,
			Project: s.Project,
			Path:    s.Path,
		}
		subExits := commandExits{}
		if !IndexCommands() {
			subExits = nil
		}
		devinEmitChain(&sub, chain, subExits)
		if len(sub.Messages) == 0 {
			return false
		}
		for _, n := range chain {
			emittedNode[n.nodeID] = true
			if n.msgID != "" {
				emitted[n.msgID] = true
			}
		}
		sides = append(sides, sub)
		return true
	}
	for _, hd := range heads {
		emitSub(devinChain(byID, hd.Node), hd.Agent)
	}
	for _, leaf := range leaves {
		chain := devinChain(byID, leaf.nodeID)
		// Cut the chain at the first node an earlier session carried: what
		// remains is the branch's own part, and the sub-session is keyed on
		// its first message — a node without a message_id falls back to its
		// node_id.
		i := len(chain)
		for i > 0 && !covered(chain[i-1]) {
			i--
		}
		suffix := chain[i:]
		if len(suffix) == 0 {
			continue
		}
		// A rewind or an edit can leave a side chain whose messages got new
		// ids — it looks like a run nobody declared. It still carries a
		// conversation the harness wrote, and dropping it loses it quietly.
		id := shortDevinID(suffix[0].msgID)
		if suffix[0].msgID == "" {
			id = "n" + strconv.FormatInt(suffix[0].nodeID, 10)
		}
		emitSub(suffix, id)
	}
	return sides
}

// devinEmitChain appends one chain's messages to s. A chain's side output —
// subagent sessions — keeps the same shape.
func devinEmitChain(s *model.Session, chain []devinNode, exits commandExits) {
	for _, n := range chain {
		t := time.Unix(n.created, 0).UTC()
		role, _ := n.msg["role"].(string)
		switch role {
		case "user", "assistant":
			txt := devinContentText(n.msg["content"])
			if strings.TrimSpace(txt) != "" {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
			}
			if role != "assistant" {
				break
			}
			calls := devinToolCalls(n.msg["tool_calls"])
			from := len(s.Messages)
			if work := devinWorkRecords(calls, t); len(work) > 0 {
				s.Touch(t)
				s.Messages = append(s.Messages, work...)
			}
			if exits != nil {
				exits.note(s.Messages, from, commandCallsIn(calls, devinDialect))
			}
		case "tool":
			if IndexToolOutput() {
				if txt := devinContentText(n.msg["content"]); strings.TrimSpace(txt) != "" {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(txt), Time: t})
				}
			}
			if exits != nil {
				if id, _ := n.msg["tool_call_id"].(string); id != "" {
					if code, ok := devinExitCode(n.msg); ok {
						exits.stamp(s.Messages, id, "", code)
					}
				}
			}
		case "system":
			// The prefix scaffolding — the system prompt and the skills
			// catalogue re-emitted at every turn — is marked is_system_prefix
			// and dropped; what carries no mark is context the session was
			// handed, which is the record of what the model was told. It rides
			// on the same switch tool output does — it is the same kind of
			// record (role tool_output), and a user who asked for none of it
			// does not get only the branch that names a tool.
			if pref, _ := n.msg["__prefix__"].(bool); pref {
				break
			}
			if IndexToolOutput() {
				if txt := devinContentText(n.msg["content"]); strings.TrimSpace(txt) != "" {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(txt), Time: t})
				}
			}
		}
	}
}

// devinToolCalls is a chat_message's tool_calls in the tool_use shape the
// work-record readers take.
func devinToolCalls(v any) []any {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []any
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		args := devinCallArgs(m["arguments"])
		if name == "" || args == nil {
			continue
		}
		call := map[string]any{"type": "tool_use", "name": name, "input": args}
		if id, _ := m["id"].(string); id != "" {
			call["id"] = id
		}
		out = append(out, call)
	}
	return out
}

// devinCallArgs accepts a call's arguments as an object or a JSON-encoded
// string — some builds persist the serialized form, and silently dropping
// the call loses its whole work record.
func devinCallArgs(v any) map[string]any {
	switch a := v.(type) {
	case map[string]any:
		return a
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(a), &m) == nil && m != nil {
			return m
		}
	}
	return nil
}

// devinExitCode reads a shell result's code off the tool node's metadata:
// extensions → chisel/terminal_output → exit.exit_code.
func devinExitCode(msg map[string]any) (int, bool) {
	meta, _ := msg["metadata"].(map[string]any)
	ext, _ := meta["extensions"].(map[string]any)
	term, _ := ext["chisel/terminal_output"].(map[string]any)
	exit, _ := term["exit"].(map[string]any)
	switch n := exit["exit_code"].(type) {
	case float64:
		return int(n), true
	case json.Number:
		c, err := n.Int64()
		return int(c), err == nil
	}
	return 0, false
}

// devinContentText reads a chat_message's content: a string, or a content
// array whose text parts join.
func devinContentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var out []string
		for _, it := range c {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["text"].(string); t != "" {
				out = append(out, t)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// devinPromptHistory adds the commands a REPL session ran inline, the rows
// prompt_history marks is_shell: they are work the session did that no
// message records.
func devinPromptHistory(s *model.Session, db string) {
	if !IndexCommands() {
		return
	}
	q := `select json_object('content',content,'ts',timestamp) from prompt_history` +
		` where session_id = '` + strings.ReplaceAll(s.ID, "'", "''") + `' and is_shell = 1 order by id`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return
	}
	type row struct {
		Content string `json:"content"`
		TS      int64  `json:"ts"`
	}
	rows, err := sqliteObjects[row](out)
	if err != nil {
		return
	}
	for _, r := range rows {
		cmd := strings.TrimSpace(r.Content)
		if !worthIndexing(cmd) {
			continue
		}
		var t time.Time
		if r.TS > 0 {
			t = time.Unix(r.TS, 0).UTC()
			s.Touch(t)
		}
		s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
	}
}

// devinAttachSummary files the summarizer's <session_id>.md under the
// session it describes, where it exists. Written beside the store, not in
// it, so it is the one record of the turns a compaction folded away.
func devinAttachSummary(s *model.Session, db string) {
	b, err := os.ReadFile(filepath.Join(devinSummariesDir(db), s.ID+".md"))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return
	}
	s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: capParsedMessage(string(b))})
}

// devinWorkRecords turns one batch of tool calls into work records, the way
// the other readers do for their own dialects.
func devinWorkRecords(calls []any, t time.Time) []model.Message {
	if len(calls) == 0 {
		return nil
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(calls, devinDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, devinDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, devinDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(calls, devinDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
	return out
}

// devinDialect is Devin's tool vocabulary. Its tools name Claude Code's
// arguments — file_path, old_string/new_string, content — under its own
// names: exec for the shell, edit/write/apply_patch/notebook_edit for file
// changes, read/notebook_read for reads. Read off live hook payloads on
// Devin CLI 3000.11.3.
var devinDialect = toolDialect{
	pathKey:    "file_path",
	pathKeyAlt: "notebook_path",
	pathTools: map[string]bool{
		"read": true, "write": true, "edit": true, "apply_patch": true,
		"notebook_edit": true, "notebook_read": true,
	},
	shellTools: map[string]bool{"exec": true},
	editTools:  map[string]bool{"edit": true, "write": true, "apply_patch": true, "notebook_edit": true},
}

// shortDevinID is the stable tail a sub-session is keyed on.
func shortDevinID(mid string) string {
	if len(mid) > 8 {
		return mid[:8]
	}
	if mid != "" {
		return mid
	}
	return "sub"
}

// DevinSessionDir is the directory a sessions row ran in — the column
// `devin` itself resumes from, so it is what `deja resume` cds to. "" when
// the row is not there or sqlite3 cannot answer.
func DevinSessionDir(db, id string) string {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return ""
	}
	q := `select json_object('dir',working_directory) from sessions where id = '` +
		strings.ReplaceAll(id, "'", "''") + `' limit 1`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return ""
	}
	type row struct {
		Dir string `json:"dir"`
	}
	rows, err := sqliteObjects[row](out)
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Dir
}
