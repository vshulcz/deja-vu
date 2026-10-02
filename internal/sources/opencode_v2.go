package sources

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// opencode 2.0 moved the tables deja reads. Sessions live in `session_v2`, and
// a session's turns move out of `message` and `part` into one `session_message`
// table, whose `type` column carries what the role used to be and whose `data`
// blob carries an assistant turn's parts. The migration that brings this in
// (20260804233008_loose_psylocke) renames `session` to `session_v2` only on a
// store from the 2.0 prerelease lineage; on a 1.x store it creates `session_v2`
// beside `session` (below), and drops and rebuilds `session_message` with a
// foreign key to `session_v2`; on the prerelease path the rename carries the
// existing key along. So a turn in `session_message` always has a `session_v2`
// row.
//
// Both layouts are read, because of how a store that has moved fails: every
// query finds the old tables gone and the harness errors with `no such table:
// session` (#3924), or — on a build mid-migration, where the old tables are
// still there and empty — it reports a store with no sessions at all, which
// nothing would complain about. Both halves are covered by asking where the
// turns actually are rather than which tables exist.
//
// Measured against opencode 2.0.12 (`@opencode/cli`) run in a throwaway home:
// the store has `session_v2`, `session_message`, `session_inbox` and
// `session_pending`, and no `session`, `message` or `part`.
//
// A store upgraded from 1.x keeps both (#4151). The 2.0 migration creates
// `session_v2` beside the old `session` and never drops `session`, `message` or
// `part`; the v1 migration (packages/core/src/database/v1-migration.bun.ts)
// copies each old session into `session_v2` under the same id and writes its
// turns into `session_message`, one session per transaction. Until that has
// run for a session, its turns are only in `message` and `part`. So such a
// store is read both ways, and a session with turns in `session_message` is
// read from there only: that copy is the one opencode keeps writing to.

// opencodeSchema says where a store keeps its sessions and its turns.
type opencodeSchema struct {
	v2           bool   // the conversation is in session_message
	sessionTable string // session_v2 on a 2.0 store, session before it
	// legacy is a 2.0 store that still holds 1.x sessions in session,
	// message and part, read beside the 2.0 ones.
	legacy bool
	// rowsStamped says message and part carry time_updated and part names its
	// session, turnsStamped that session_message carries time_updated: the
	// since clause asks those where they are there (#4207).
	rowsStamped, turnsStamped bool
}

// opencodeSchemaCache keeps one answer per store file, because the schema is
// asked for again by every read beside the main projection — the newest id, the
// parents, the titles, the counts — and each answer costs a sqlite3 process.
// Keyed on the file's size and modification time, so a store that changes under
// a long-running process is asked again.
var opencodeSchemaCache sync.Map

// opencodeSchemaOf answers that question once per store file.
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
	// The tables, and which of the turn tables carry time_updated, in one
	// process. pragma_table_info on a missing table is empty, not an error.
	b, err := sqliteOutput(db, `select group_concat(n) from (`+
		`select name n from sqlite_master where type='table' `+
		`and name in ('session','session_v2','session_message','message','part')`+
		` union all select 'message.time_updated' from pragma_table_info('message') where name='time_updated'`+
		` union all select 'part.'||name from pragma_table_info('part') where name in ('time_updated','session_id')`+
		` union all select 'session_message.time_updated' from pragma_table_info('session_message') where name='time_updated')`)
	if err != nil {
		return out
	}
	have := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(string(b)), ",") {
		have[name] = true
	}
	out.rowsStamped = have["message.time_updated"] && have["part.time_updated"] && have["part.session_id"]
	out.turnsStamped = have["session_message.time_updated"]
	if have["session_v2"] && !have["session"] {
		out.sessionTable = "session_v2"
	}
	if !have["session_message"] {
		return out
	}
	// A 1.18.3 store can hold only switch events here while message and part
	// still hold every turn, so count only the row types this reader can project.
	n, err := sqliteOutput(db, `select count(*) from session_message where type in `+opencodeTurnTypes)
	if err != nil {
		return out
	}
	out.v2 = strings.TrimSpace(string(n)) != "0"
	if out.v2 && have["session_v2"] && have["session"] && have["message"] && have["part"] {
		out.sessionTable = "session_v2"
		out.legacy = true
	}
	return out
}

// opencodeTurnTypes are the session_message rows the 2.x reader projects. A
// session with any of them is read from there.
const opencodeTurnTypes = `('user','assistant','compaction')`

// opencodeNotMoved keeps the 1.x reader off a session whose turns are already
// in session_message, so a session the v1 migration copied is read once.
const opencodeNotMoved = ` and not exists (select 1 from session_message x ` +
	`where x.session_id=s.id and x.type in ` + opencodeTurnTypes + `)`

// opencodeSessionTables are the tables the reads beside the projection ask:
// one, or on a store that holds both layouts the old one and then the new, so
// a session in both takes its name and parent from the copy opencode updates.
func opencodeSessionTables(db string) []string {
	sc := opencodeSchemaOf(db)
	if sc.legacy {
		return []string{"session", sc.sessionTable}
	}
	return []string{sc.sessionTable}
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
		`'path',case when json_extract(p.data,'$.name') in ('read') then ` +
		`coalesce(json_extract(p.data,'$.state.input.filePath'),json_extract(p.data,'$.state.input.path')) end,` +
		`'editpath',coalesce(json_extract(p.data,'$.state.input.filePath'),json_extract(p.data,'$.state.input.path')),` +
		`'cmd',json_extract(p.data,'$.state.input.command'),` +
		`'patch',json_extract(p.data,'$.state.input.patchText'),` +
		// 2.0's main editing tool is `edit`, and it carries both sides of the
		// change. The 1.x reader only ever saw `apply_patch`, so a store where
		// the edits came through this tool had nothing for `deja restore`,
		// `deja files` or blame.
		`'old',json_extract(p.data,'$.state.input.oldString'),` +
		`'new',json_extract(p.data,'$.state.input.newString'),` +
		// What a command printed moved from `$.state.output`, a string, to
		// `$.state.content`, the list of blocks the tool returned. Only the text
		// ones, and only for bash, for the reason the 1.x reader gives: a file
		// read is the bulk of a store and the weakest thing in it.
		`'out',case when json_extract(p.data,'$.name') in ('bash','shell') then (` +
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
		// 2.0 renamed two of the three: `bash` is `shell` and `apply_patch` is
		// `patch`. Both spellings are read, because a store written before the
		// rename keeps the old ones.
		`and json_extract(p.data,'$.name') in ('read','bash','shell','apply_patch','patch','edit')))` +
		where + ` order by s.id,p.mc,p.ord` + lim
}

// opencodeV2SinceWhere picks the 2.x sessions touched after the watermark, read
// whole, for the reason opencodeSinceWhere gives. A turn's row is rewritten as
// its content streams in, so time_updated is what moves; a store without the
// column is asked for the turn's own stamps, under `$.time.start` for a tool
// and `$.time.created` for a turn.
func opencodeV2SinceWhere(db string, t time.Time) string {
	touched := fmt.Sprintf("select session_id from session_message where %s or %s",
		newerThanEpoch("time_created", t), newerThanEpoch("time_updated", t))
	if !opencodeSchemaOf(db).turnsStamped {
		rfc := sqlEscape(t.UTC().Format(time.RFC3339Nano))
		at := "coalesce(json_extract(x.data,'$.time.start'),json_extract(x.data,'$.time.created'))"
		touched = fmt.Sprintf("select x.sid from parts x where %s or x.mc > '%s' or %s or %s > '%s'",
			newerThanEpoch("x.mc", t), rfc, newerThanEpoch(at, t), at, rfc)
	}
	return opencodeSessionTouched(opencodeSessionTable(db), t, touched)
}

// opencodeSessionTable is the table sessions live in, for the reads beside the
// main projection — the newest id, the parents, the titles, the counts. On a
// store that holds both layouts it is the 2.0 one; opencodeSessionTables names
// both.
func opencodeSessionTable(db string) string { return opencodeSchemaOf(db).sessionTable }

// OpencodeStoreIsV2 reports whether the store at db keeps its turns in the 2.0
// layout. install reads it to pick the plugin shape: 2.0 loads only a default
// export, 1.x only a named one, and the wrong file is refused outright.
func OpencodeStoreIsV2(db string) bool { return opencodeSchemaOf(db).v2 }
