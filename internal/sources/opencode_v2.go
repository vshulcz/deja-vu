package sources

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// opencode is moving its conversation out of the `message` and `part` tables
// into one `session_message` table, whose `type` column carries what the role
// used to be and whose `data` blob carries an assistant turn's parts. The new
// table is created and empty in the dev build of 2026-09-21; the turns are
// still in the old pair there, and `select name from sqlite_master` on that
// build lists both.
//
// Read before the switch is thrown, because of how a store that has moved
// fails: the old query finds the old tables gone and every read of the harness
// errors with `no such table: session` (#3924), or finds them present and empty
// and reports a store with no sessions at all, which nothing would complain
// about. Both halves are covered by asking where the turns actually are.
//
// The store in the report also had the sessions in `session_v2` rather than
// `session`. No build here writes that name, so it is accepted and not
// insisted on.

// opencodeSchemaOf reads which tables a store actually has, and which of them
// hold the conversation. Measured against the dev build of 2026-09-21: the
// session table is still `session`, `session_message` is created and empty, and
// `message`/`part` carry the turns — so the question cannot be "does a table
// exist". A store where the old tables are still there but empty would read as
// a store with no sessions, which is the quiet half of #3924.
//
// The report that opened the issue had `session_v2` and no `session` at all, so
// both names are accepted for the sessions themselves.
type opencodeSchema struct {
	v2           bool   // the conversation is in session_message
	sessionTable string // session, or session_v2 where a store has that
}

// opencodeSchemaCache keeps one answer per store file, because the schema is
// asked for again by every read beside the main projection — the newest id, the
// parents, the titles, the counts — and each answer costs a sqlite3 process.
// Keyed on the file's size and modification time, so a store that changes under
// a long-running process is asked again.
var opencodeSchemaCache sync.Map

func opencodeSchemaOf(db string) opencodeSchema {
	key := db
	if fi, err := os.Stat(db); err == nil {
		key = fmt.Sprintf("%s|%d|%d", db, fi.Size(), fi.ModTime().UnixNano())
	}
	if v, ok := opencodeSchemaCache.Load(key); ok {
		return v.(opencodeSchema)
	}
	got := readOpencodeSchema(db)
	opencodeSchemaCache.Store(key, got)
	return got
}

func readOpencodeSchema(db string) opencodeSchema {
	out := opencodeSchema{sessionTable: "session"}
	b, err := sqliteOutput(db, `select group_concat(name) from sqlite_master where type='table' `+
		`and name in ('session','session_v2','session_message')`)
	if err != nil {
		return out
	}
	have := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(string(b)), ",") {
		have[name] = true
	}
	if have["session_v2"] && !have["session"] {
		out.sessionTable = "session_v2"
	}
	if !have["session_message"] {
		return out
	}
	// The table exists in a store that has never used it. What decides is
	// whether the turns are in there.
	n, err := sqliteOutput(db, `select count(*) from session_message`)
	if err != nil {
		return out
	}
	out.v2 = strings.TrimSpace(string(n)) != "0"
	return out
}

// opencodeV2 reports whether the conversation in a store is kept the 2.x way.
func opencodeV2(db string) bool { return opencodeSchemaOf(db).v2 }

// opencodeV2Parts is the shape the rest of the parser already reads: one row
// per part, with the message's own columns beside it.
//
// A user turn keeps its text at the top of `data` and has no parts at all, so
// it is wrapped as the single text part it would have been on the old schema.
// An assistant turn holds its parts under `$.content`. The other message types
// — `synthetic`, `system`, `idle`, `shell`, `skill`, `model-switched` and the
// rest — are opencode talking to itself, which is what `$.synthetic` marked on
// the old schema and what the parser dropped there too.
const opencodeV2Parts = `with parts as (` +
	`select sm.id as mid, sm.session_id as sid, sm.type as role, sm.time_created as mc, sm.data as mdata, ` +
	`j.value as data, j.key as ord ` +
	`from session_message sm, json_each(sm.data,'$.content') j ` +
	`where sm.type='assistant' and json_type(sm.data,'$.content')='array' ` +
	`union all ` +
	`select sm.id, sm.session_id, sm.type, sm.time_created, sm.data, ` +
	`json_object('type','text','text',json_extract(sm.data,'$.text'),'time',json_extract(sm.data,'$.time')), 0 ` +
	`from session_message sm ` +
	`where sm.type='user' and json_extract(sm.data,'$.text') is not null ` +
	`union all ` +
	// What opencode compacted away, kept the way the old schema's summary
	// message is: searchable, and not the agent talking.
	`select sm.id, sm.session_id, sm.type, sm.time_created, sm.data, ` +
	`json_object('type','text','text',json_extract(sm.data,'$.summary'),'time',json_extract(sm.data,'$.time')), 0 ` +
	`from session_message sm ` +
	`where sm.type='compaction' and json_extract(sm.data,'$.summary') is not null) `

// opencodeV2Query returns the 2.x reader, emitting the same row object the 1.x
// projection does so one loop reads both.
func opencodeV2Query(sessionTable, where string, limit int) string {
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	return opencodeV2Parts +
		`select json_object(` +
		`'id',cast(s.id as text),'directory',cast(s.directory as text),` +
		`'time_created',s.time_created,'time_updated',s.time_updated,` +
		`'role',case p.role when 'assistant' then 'assistant' else 'user' end,` +
		`'summary',case p.role when 'compaction' then 1 end,` +
		`'text',json_extract(p.data,'$.text'),` +
		`'path',coalesce(json_extract(p.data,'$.state.input.filePath'),json_extract(p.data,'$.state.input.path')),` +
		`'cmd',json_extract(p.data,'$.state.input.command'),` +
		`'patch',json_extract(p.data,'$.state.input.patchText'),` +
		// What a command printed moved from `$.state.output`, a string, to
		// `$.state.content`, the list of blocks the tool returned. Only the text
		// ones, and only for bash, for the reason the 1.x reader gives: a file
		// read is the bulk of a store and the weakest thing in it.
		`'out',case when json_extract(p.data,'$.name')='bash' then (` +
		`select group_concat(json_extract(c.value,'$.text'),char(10)) from json_each(p.data,'$.state.content') c ` +
		`where json_extract(c.value,'$.type')='text') end,` +
		`'exit',coalesce(json_extract(p.data,'$.state.structured.exit'),json_extract(p.data,'$.state.metadata.exit')),` +
		`'pt',coalesce(json_extract(p.data,'$.time.start'),json_extract(p.data,'$.time.created')),` +
		`'mt',json_extract(p.mdata,'$.time.created'),` +
		`'mc',p.mc) ` +
		`from ` + sessionTable + ` s join parts p on p.sid=s.id ` +
		`where (json_extract(p.data,'$.type')='text' ` +
		// A tool call names itself under `$.name` now, where 1.x wrote
		// `$.tool`. The same three are read: what was opened, what was run,
		// what was changed.
		`or (json_extract(p.data,'$.type')='tool' ` +
		`and json_extract(p.data,'$.name') in ('read','bash','apply_patch')))` +
		where + ` order by s.id,p.mc,p.ord` + lim
}

// opencodeV2SinceWhere bounds a 2.x read to what changed after the watermark.
// The message's own column is `p.mc` here, and a part's stamp is written under
// `$.time.start` by a tool and `$.time.created` by a turn.
func opencodeV2SinceWhere(t time.Time) string {
	rfc := sqlEscape(t.UTC().Format(time.RFC3339Nano))
	return fmt.Sprintf(" and (%s or p.mc > '%s' or %s or coalesce(json_extract(p.data,'$.time.start'),json_extract(p.data,'$.time.created')) > '%s')",
		newerThanEpoch("p.mc", t), rfc,
		newerThanEpoch("coalesce(json_extract(p.data,'$.time.start'),json_extract(p.data,'$.time.created'))", t), rfc)
}

// opencodeSessionTable is the table sessions live in, for the reads beside the
// main projection — the newest id, the parents, the titles, the counts.
func opencodeSessionTable(db string) string { return opencodeSchemaOf(db).sessionTable }
