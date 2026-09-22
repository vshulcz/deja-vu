package sources

import (
	"fmt"
	"strings"
	"time"
)

// opencode 2.0 renamed the tables deja reads. `session` became `session_v2`,
// and `message` and `part` were folded into one `session_message` table whose
// `type` column carries what the role used to be and whose `data` blob carries
// the parts an assistant turn used to keep in rows of its own. A store on the
// new schema answered every query with `no such table: session`, so the whole
// harness read as unreadable and none of its sessions reached the index
// (#3924).
//
// Both schemas are read. opencode 1.x is still what most machines have, and a
// store migrated in place keeps neither set of tables around for long.

// opencodeV2 reports whether a store is on opencode 2.x's schema. The question
// is asked of sqlite_master rather than by trying a query and reading the
// error, because a query that fails for another reason — a locked store, a
// truncated file — would answer it wrong.
func opencodeV2(db string) bool {
	b, err := sqliteOutput(db, `select count(*) from sqlite_master where type='table' and name='session_v2'`)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

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
	`where sm.type='user' and json_extract(sm.data,'$.text') is not null) `

// opencodeV2Query returns the 2.x reader, emitting the same row object the 1.x
// projection does so one loop reads both.
func opencodeV2Query(where string, limit int) string {
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	return opencodeV2Parts +
		`select json_object(` +
		`'id',cast(s.id as text),'directory',cast(s.directory as text),` +
		`'time_created',s.time_created,'time_updated',s.time_updated,` +
		`'role',case p.role when 'assistant' then 'assistant' else 'user' end,` +
		`'text',json_extract(p.data,'$.text'),` +
		`'path',json_extract(p.data,'$.state.input.filePath'),` +
		`'cmd',json_extract(p.data,'$.state.input.command'),` +
		`'patch',json_extract(p.data,'$.state.input.patchText'),` +
		// What a command printed moved from `$.state.output`, a string, to
		// `$.state.content`, the list of blocks the tool returned. Only the text
		// ones, and only for bash, for the reason the 1.x reader gives: a file
		// read is the bulk of a store and the weakest thing in it.
		`'out',case when json_extract(p.data,'$.name')='bash' then (` +
		`select group_concat(json_extract(c.value,'$.text'),char(10)) from json_each(p.data,'$.state.content') c ` +
		`where json_extract(c.value,'$.type')='text') end,` +
		`'exit',json_extract(p.data,'$.state.metadata.exit'),` +
		`'pt',coalesce(json_extract(p.data,'$.time.start'),json_extract(p.data,'$.time.created')),` +
		`'mt',json_extract(p.mdata,'$.time.created'),` +
		`'mc',p.mc) ` +
		`from session_v2 s join parts p on p.sid=s.id ` +
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
func opencodeSessionTable(db string) string {
	if opencodeV2(db) {
		return "session_v2"
	}
	return "session"
}
