package sources

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// gooseSchema is which of the columns the reader leans on this store has.
// Each arrived in a later goose migration, and naming one an older store lacks
// fails the whole query, so they are probed rather than assumed.
type gooseSchema struct {
	sessionType bool // sessions.session_type
	parent      bool // sessions.parent_session_id, the edge summon writes
	messageID   bool // messages.message_id, which a fork copies
}

func gooseColumns(db string) gooseSchema {
	var g gooseSchema
	if out, err := sqliteOutput(db, "pragma table_info(sessions)"); err == nil {
		g.sessionType = bytes.Contains(out, []byte("session_type"))
		g.parent = bytes.Contains(out, []byte("parent_session_id"))
	}
	if out, err := sqliteOutput(db, "pragma table_info(messages)"); err == nil {
		g.messageID = bytes.Contains(out, []byte("message_id"))
	}
	return g
}

// gooseSubagentTypes are goose's words for a session its summon extension
// spawned, spelled both ways across versions.
var gooseSubagentTypes = []string{"subagent", "sub_agent"}

func gooseSubagentType(t string) bool {
	return slices.Contains(gooseSubagentTypes, t)
}

// gooseFork is a session `goose session --resume --fork` (or an ACP fork)
// made: copy_session gives it a new id and copies the source's messages under
// their own message ids and times, and records no parent anywhere (1.46).
type gooseFork struct {
	source string
	copied map[string]bool
}

// gooseForks finds every fork in the store by the message ids it shares with
// a session created before it. The source is the earlier session it shares
// the most with: a fork of a fork shares its own source's turns too, and a
// second fork of the same source shares at most what the first copied.
// A tie goes to the older session. A copy is the same id with the same
// content: `--fork --edit` keeps the ids of the messages it rewrote.
//
// Without this a fork read as a second author of everything its source did:
// the copies carry the source's writes at the source's times, so blame
// credited whichever of the two it saw first (#5233).
//
// where is the reader's own window, so an incremental pass looks only at the
// sessions it is about to return.
func gooseForks(db string, g gooseSchema, where string) map[string]gooseFork {
	if !g.messageID {
		return nil
	}
	only := ""
	if where != "" {
		only = ` and m.session_id in (select s.id from sessions s join messages m on m.session_id=s.id where 1=1` + where + `)`
	}
	q := `select json_object('fork',cast(m.session_id as text),'source',cast(m2.session_id as text),` +
		`'ids',group_concat(cast(m.message_id as text), char(31))) ` +
		`from messages m join messages m2 on m2.message_id = m.message_id and m2.content_json = m.content_json and m2.session_id <> m.session_id ` +
		`join sessions s on s.id = m.session_id join sessions s2 on s2.id = m2.session_id ` +
		`where m.message_id is not null and m.message_id <> '' and s2.rowid < s.rowid` + only + ` ` +
		`group by m.session_id, m2.session_id order by m.session_id, count(distinct m.message_id) desc, s2.rowid`
	out, err := sqliteOutput(db, q)
	if err != nil || len(out) == 0 {
		return nil
	}
	forks := map[string]gooseFork{}
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		var r struct {
			Fork   string `json:"fork"`
			Source string `json:"source"`
			IDs    string `json:"ids"`
		}
		if json.Unmarshal(line, &r) != nil || r.Fork == "" || r.Source == "" {
			continue
		}
		if _, ok := forks[r.Fork]; ok {
			continue // ordered best first
		}
		copied := map[string]bool{}
		for id := range strings.SplitSeq(r.IDs, "\x1f") {
			copied[id] = true
		}
		forks[r.Fork] = gooseFork{source: r.Source, copied: copied}
	}
	return forks
}

// gooseTypeFilter keeps out of recall what goose wrote for itself: Goose
// stores subagent turns and scheduled runs in the same table as the reader's
// own sessions.
//
// Stated as what to exclude rather than what to keep. #2874 named the three
// types a person reaches goose through today, which fixed the report; written
// that way round, a type goose adds next is dropped silently and nothing says
// why — the failure that produced #2873 in the first place. So: a type is a
// person's work until goose is known to write it for itself.
//
// A sub-agent run that names the session that spawned it comes in, the way a
// Claude Code sidechain does: it folds under its parent, stays out of the
// session-start digest, and is the only record of the files it wrote, so with
// it skipped blame answered "no indexed session wrote this line" for every one
// of them (#5232). DEJA_INCLUDE_SUBAGENTS=0 keeps them all out, as before.
func gooseTypeFilter(g gooseSchema) string {
	if !g.sessionType {
		return ""
	}
	quote := func(ts []string) string {
		q := make([]string, 0, len(ts))
		for _, t := range ts {
			q = append(q, "'"+t+"'")
		}
		return strings.Join(q, ",")
	}
	f := " and (s.session_type is null or s.session_type not in (" + quote(gooseMachineTypes) + "))"
	sub := " and (s.session_type is null or s.session_type not in (" + quote(gooseSubagentTypes) + ")"
	if g.parent && !SubagentsOff() {
		sub += " or coalesce(s.parent_session_id,'') <> ''"
	}
	return f + sub + ")"
}

// gooseMachineTypes are the session types goose writes for its own work rather
// than the reader's: a run its scheduler started.
//
// Read off goose's own enum — user, scheduled, sub_agent, hidden, terminal,
// gateway, acp — rather than guessed. `hidden` is not on this list although
// the name suggests it: goose writes it for `goose run --no-session`, the
// reader working without keeping a session, and for two wizards that store no
// conversation and fall out of the role join anyway. `gateway` is a person
// reaching goose from a chat app. Both are history someone made. Sub-agents
// are gooseSubagentTypes.
var gooseMachineTypes = []string{"scheduled"}
