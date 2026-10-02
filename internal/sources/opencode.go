package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpencodeDB mirrors upstream's path logic: XDG_DATA_HOME is honored on Linux
// only (opencode's xdg-basedir dependency ignores it elsewhere).
func OpencodeDB() string {
	if p := os.Getenv("DEJA_OPENCODE_DB"); p != "" {
		return p
	}
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "opencode", "opencode.db")
		}
	}
	return filepath.Join(Home(), ".local", "share", "opencode", "opencode.db")
}

func LoadOpencode() []model.Session {
	// A store that would not open — locked past the timeout by the agent
	// using it, or unreadable — is reported like a transcript that would not
	// parse, so the pass says so and does not record the store as read.
	ss, err := ParseOpencodeDBWhere(OpencodeDB(), "", 0)
	diagFileError(OpencodeDB(), err)
	// And the diff store beside it, which is the only record of what most of
	// those sessions changed (#3791). A full pass goes through Load rather
	// than through the file kinds, so registering the kind alone left this
	// store unread on every rebuild — which is how this was found.
	return withOpencodeDiffs(ss)
}

// withOpencodeDiffs folds each session's diff records into the session the
// database gave, by id.
//
// Not as sessions of their own: handing back a second session with the same id
// goes down the collision path, which exists for two different conversations
// that happen to share an id. It filed the pair under one project, dropped the
// other's, and printed "1 session shares an id with another transcript" — a
// line that would read one thousand on this machine. The diff is not another
// conversation, it is the same one's account of what it changed.
//
// A diff whose session the database no longer holds is left out: opencode
// prunes the database and leaves the diff, and with no conversation beside it
// there is nothing to list or blame it on.
func withOpencodeDiffs(ss []model.Session) []model.Session {
	files := OpencodeDiffFiles()
	if len(files) == 0 {
		return ss
	}
	at := map[string]int{}
	for i, s := range ss {
		at[s.ID] = i
	}
	for _, p := range files {
		parsed, err := ParseOpencodeDiff(p)
		if err != nil {
			// One file's problem: a diff opencode is in the middle of writing
			// must not cost the store its other sessions.
			diagFileError(p, err)
			continue
		}
		for _, d := range parsed {
			i, ok := at[d.ID]
			if !ok {
				// A diff whose session the database no longer holds. It is not
				// a session of its own: there is no conversation in it, no
				// project and no title, so every screen that lists sessions
				// would carry a blank row, and blame would answer a question
				// about a line with a session nobody can read. The file stays
				// on disk for whenever that id comes back.
				continue
			}
			ss[i].Messages = append(ss[i].Messages, d.Messages...)
			// The session keeps its own times; a diff is written at the end
			// and would otherwise move the session's last-updated past what
			// the conversation says.
		}
	}
	return ss
}

func LoadOpencodeMatching(q string) []model.Session {
	where := fmt.Sprintf(" and lower(p.data) like '%%%s%%' escape '\\'", sqlEscape(sqlLikeEscape(strings.ToLower(q))))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 5000)
	return ss
}

func LoadOpencodeRecent(n int) []model.Session {
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), "", n*20)
	return ss
}

func LoadOpencodeSince(t time.Time) []model.Session {
	ss, _ := ParseOpencodeDBSince(OpencodeDB(), t)
	return ss
}

func LoadOpencodePrefix(p string) []model.Session {
	where := fmt.Sprintf(" and s.id like '%s%%' escape '\\'", sqlEscape(sqlLikeEscape(p)))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 0)
	return ss
}

// OpencodeStoreLacks reports whether the store a CLI session of this harness
// lives in holds no session row with this id — deleted with `opencode session
// delete`, or pruned. False whenever that cannot be told: no store, or a read
// that fails. The row is asked for directly, in each session table the store
// has, rather than through the projection, which skips sessions it has
// nothing to read from and takes seconds on a long one.
func OpencodeStoreLacks(harness, id string) bool {
	db := OpencodeDB()
	if harness == "kilocode" {
		db = KiloDB()
	}
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return false
	}
	query := func(q string) (string, bool) {
		cmd, stop := sqliteReadCmd(db, q)
		defer stop()
		b, err := cmd.Output()
		return strings.TrimSpace(string(b)), err == nil
	}
	names, ok := query(`select name from sqlite_master where type='table' and name in ('session','session_v2')`)
	if !ok || names == "" {
		return false
	}
	for _, table := range strings.Fields(names) {
		n, ok := query(fmt.Sprintf(`select count(*) from %s where id='%s'`, table, sqlEscape(id)))
		if !ok || n != "0" {
			return false
		}
	}
	return true
}

func ParseOpencodeDB(db string) ([]model.Session, error) {
	return ParseOpencodeDBWhere(db, "", 0)
}

func ParseOpencodeDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseOpencodeDBWhere(db, "", 0)
	}
	return parseOpencodeSchemaDBSince("opencode", db, t)
}

// parseOpencodeSchemaDBSince is the since read for every store in this schema.
// Each layout is bounded by its own columns, and a store holding both reads
// both.
func parseOpencodeSchemaDBSince(harness, db string, t time.Time) ([]model.Session, error) {
	// A row can be stamped a moment before the pass that missed it, and the
	// comparison is a strict >. A session read twice replaces itself, so going
	// back costs a re-read and nothing else (#4207).
	t = t.Add(-opencodeSinceSlack)
	return parseOpencodeLayouts(harness, db, opencodeSinceWhere(db, t), opencodeV2SinceWhere(db, t), 0)
}

// opencodeSinceSlack is how far before the watermark a since read starts.
const opencodeSinceSlack = 5 * time.Second

// opencodeSinceWhere picks the sessions touched after the watermark and reads
// each of them whole. Shared with the other stores in this schema — Kilo's CLI
// database is one (#3643).
//
// Whole, because opencode creates a reply's message and text part, time.start
// set, before the text streams in: a pass that read the part empty stamped a
// watermark past both, and bounding rows by when they were created never asked
// for the text again (#4207). time_updated is what moves when the text lands,
// and a session read again replaces what the index holds for it
// (rereadsWholeSessions), so the turns it already had are not added twice.
func opencodeSinceWhere(db string, t time.Time) string {
	// Each a column of its own, so the subquery reads row headers and never a
	// blob: 3–5 s on a 3.8 GB store, against 9 s for the row-level clause.
	touched := fmt.Sprintf("select session_id from message where %s or %s union "+
		"select session_id from part where %s",
		newerThanEpoch("time_created", t), newerThanEpoch("time_updated", t),
		newerThanEpoch("time_updated", t))
	if !opencodeSchemaOf(db).rowsStamped {
		// A store from before the columns: the stamps a row is created with.
		rfc := sqlEscape(t.UTC().Format(time.RFC3339Nano))
		touched = fmt.Sprintf("select m2.session_id from message m2 join part p2 on p2.message_id=m2.id "+
			"where %s or m2.time_created > '%s' or %s or json_extract(p2.data,'$.time.start') > '%s'",
			newerThanEpoch("m2.time_created", t), rfc,
			newerThanEpoch("json_extract(p2.data,'$.time.start')", t), rfc)
	}
	return opencodeSessionTouched("session", t, touched)
}

// opencodeSessionTouched bounds a read to the sessions in table whose own stamp
// moved past t, and those touched names. One list for s.id to be looked up in:
// an OR beside it made SQLite scan every part instead.
func opencodeSessionTouched(table string, t time.Time, touched string) string {
	return fmt.Sprintf(" and s.id in (select id from %s where %s or time_updated > '%s' union %s)",
		table, newerThanEpoch("time_updated", t), sqlEscape(t.UTC().Format(time.RFC3339Nano)), touched)
}

func ParseOpencodeDBWhere(db, where string, limit int) ([]model.Session, error) {
	return parseOpencodeSchemaDB("opencode", db, where, limit)
}

// parseOpencodeSchemaDB reads a store in OpenCode's message schema. Kilo's CLI
// writes that schema too — tokscale reads it through the same path under
// `OpenCodeSchemaConfig::kilo` — so the harness a session belongs to is the
// only difference (#3643).
func parseOpencodeSchemaDB(harness, db, where string, limit int) ([]model.Session, error) {
	return parseOpencodeLayouts(harness, db, where, where, limit)
}

// parseOpencodeLayouts reads a store with the clause each layout needs: where1
// for the 1.x projection, where2 for the 2.x one. A store upgraded from 1.x
// holds both, and each of its sessions is read from one of them (#4151).
func parseOpencodeLayouts(harness, db, where1, where2 string, limit int) ([]model.Session, error) {
	// The sqlite3 CLI CREATES a missing database file on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	schema := opencodeSchemaOf(db)
	by := map[string]*model.Session{}
	rows := 0
	if !schema.v2 || schema.legacy {
		w := where1
		if schema.legacy {
			w = opencodeNotMoved + w
		}
		n, err := readOpencodeRows(harness, db, opencodeV1Query(harness, w, limit), by)
		if err != nil {
			return nil, err
		}
		rows += n
	}
	if schema.v2 {
		n, err := readOpencodeRows(harness, db, opencodeV2Query(schema.sessionTable, where2, limit), by)
		if err != nil {
			return nil, err
		}
		rows += n
	}
	if rows == 0 {
		return nil, nil
	}
	var out []model.Session
	for _, s := range by {
		out = append(out, *s)
	}
	// A subagent run is its own session with parent_id naming the spawner —
	// 922 of 1472 on one store; read without it, every child listed as a
	// person's own session (#3301). Read beside the rows, best effort: a store
	// from before the column keeps its sessions standalone.
	if parents := opencodeParents(db); len(parents) > 0 {
		for i := range out {
			if p := parents[out[i].ID]; p != "" {
				out[i].Kind = "subagent"
				out[i].Parent = p
			}
		}
	}
	// opencode names every session, and for the 922 subagent runs of one real
	// store that name is the only short thing about them — the first user line
	// there is the whole brief (#3315). Read beside the rows, not in the main
	// projection: a store from before the column would fail the whole query
	// and take the harness with it.
	if titles := opencodeTitles(db); len(titles) > 0 {
		for i := range out {
			if t := titles[out[i].ID]; t != "" {
				out[i].Title = t
			}
		}
	}
	return out, nil
}

// opencodeV1Query is the 1.x projection: sessions in `session`, turns in
// `message`, their content in `part`.
func opencodeV1Query(harness, where string, limit int) string {
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	// Narrow projection: shipping full m.data/p.data JSON blobs through the
	// sqlite3 pipe on multi-GB stores takes minutes; extracting just the
	// needed scalars keeps the dump to tens of MB and seconds.
	// The row is assembled by SQLite's own json_object rather than by the
	// sqlite3 shell's -json mode, which is quadratic in what it escapes and
	// turned tool output into a hang — see sqliteRows.
	q := `select json_object(` +
		`'id',cast(s.id as text),'directory',cast(s.directory as text),` +
		`'time_created',s.time_created,'time_updated',s.time_updated,` +
		`'role',json_extract(m.data,'$.role'),` +
		`'text',json_extract(p.data,'$.text'),` +
		// The text opencode wrote itself under the user role — "Continue if
		// you have next steps…", "The following tool was executed by the
		// user" — carries this flag; 338 of them indexed as the person's words
		// on one store (#3299).
		`'synthetic',json_extract(p.data,'$.synthetic'),` +
		// A part opencode does not feed to the model at all. A compression
		// plugin's status line carries it — `▣ DCP | -100.3K removed, +9K
		// summary — Compression #1` — and those are filed under the user role,
		// so 376 of them on one store were indexed as the person's words. The
		// flag is exact there: every ignored part on that store is one of these
		// banners, and none of the 4,420 real user parts carries it (#3515).
		`'ignored',json_extract(p.data,'$.ignored'),` +
		// opencode's own digest of the turns it compacted away, marked on the
		// message rather than the part. Indexed under RoleSummary: searchable,
		// and not the agent talking (#3384).
		//
		// The type, not the value. Current opencode writes an object of file
		// diffs under the same key — 5,018 of them on a 3.4 GB store, 107 MB
		// summed over the rows this projection ships, and one value of 15.3 MB
		// on another store. None of it can make a truthiness test pass, so all
		// of it was read, piped and parsed to decide nothing (#3556). `true`
		// and a non-zero integer still read as set; a text "1" or "true"
		// survives the truncation, and longer text was never truthy.
		`'summary',case json_type(m.data,'$.summary') ` +
		`when 'true' then 1 ` +
		`when 'integer' then json_extract(m.data,'$.summary') ` +
		`when 'text' then substr(json_extract(m.data,'$.summary'),1,8) end,` +
		`'path',` + opencodeV1Path(harness) + `,` +
		`'cmd',` + opencodeV1Command(harness) + `,` +
		`'patch',json_extract(p.data,'$.state.input.patchText'),` +
		opencodeV1EditFields(harness) +
		// The output of a bash call and its exit status. Only bash: `read`
		// output is 119 MB of file contents on this store against 49 MB of
		// command output, and #547 measured file bodies as the weakest slice
		// deja could index. What a command printed is where the errors live.
		`'out',case when json_extract(p.data,'$.tool') in ('bash','Bash') ` +
		`then json_extract(p.data,'$.state.output') end,` +
		`'exit',json_extract(p.data,'$.state.metadata.exit'),` +
		`'pt',json_extract(p.data,'$.time.start'),` +
		`'mt',json_extract(m.data,'$.time.created'),` +
		// The row's own column, which is what the since clause filters on. A
		// store that fills it and leaves the blob's time out handed back
		// messages dated to the year zero (#2086).
		`'mc',m.time_created) ` +
		`from session s join message m on m.session_id=s.id join part p on p.message_id=m.id ` +
		// The type test is written twice on purpose. json_extract on its own
		// parses every blob in the table — parts average ~12 KB and are mostly
		// tool output — while the substring test reads only the head of the
		// row, which for a large blob means SQLite never follows the overflow
		// pages. `"type"` sits within the first 80 bytes of every part opencode
		// writes. The exact test still decides: the cheap one only avoids
		// parsing rows that cannot match. Measured on a 2.8 GB store: 6.9s to
		// 5.6s, byte-identical output.
		// Read calls join the text parts: they are where the answer to "which
		// file was this session about" lives, and prose mentions are not a
		// substitute — measured at 43% present and wrong more often than right.
		// The same two-test trick applies, with `"tool":"read"` as the cheap
		// gate. Measured on a 3 GB store: 5.79s to 6.60s for 26,466 paths.
		`where ((instr(substr(p.data,1,120),'"type":"text"')>0 ` +
		`and json_extract(p.data,'$.type')='text')` +
		` or (instr(substr(p.data,1,200),'"tool":"read"')>0 ` +
		`and json_extract(p.data,'$.tool')='read')` +
		// bash and apply_patch are the other half of what a session did: 20,425
		// commands and 6,021 patches on this store against 26,466 reads. Same
		// two-test shape — the cheap substring gate first, so json_extract never
		// parses a blob that cannot match.
		` or (instr(substr(p.data,1,200),'"tool":"bash"')>0 ` +
		`and json_extract(p.data,'$.tool')='bash')` +
		` or (instr(substr(p.data,1,200),'"tool":"apply_patch"')>0 ` +
		`and json_extract(p.data,'$.tool')='apply_patch')` + opencodeV1Tools(harness) + `)` +
		where + ` order by s.id,m.time_created,p.id` + lim
	return q
}

// opencodeV1EditFields read the two sides of an edit. opencode's own edit
// takes {filePath, oldString, newString} and its write {filePath, content};
// the 1.x reader took neither, so a session whose changes went through them
// had nothing for restore or blame (#4495). ZCode's tool parts sit in the same
// schema under Claude Code's names and arguments, Edit {file_path, old_string,
// new_string} and Write {file_path, content} (#4428).
func opencodeV1EditFields(harness string) string {
	path := `when json_extract(p.data,'$.tool') in ('edit','write') then json_extract(p.data,'$.state.input.filePath') `
	old := `json_extract(p.data,'$.state.input.oldString')`
	nw := `json_extract(p.data,'$.state.input.newString'),` +
		`case when json_extract(p.data,'$.tool')='write' then json_extract(p.data,'$.state.input.content') end`
	if harness == "zcode" {
		path += `when json_extract(p.data,'$.tool') in ('Edit','Write') then json_extract(p.data,'$.state.input.file_path') `
		old = `coalesce(` + old + `,json_extract(p.data,'$.state.input.old_string'))`
		nw += `,json_extract(p.data,'$.state.input.new_string'),` +
			`case when json_extract(p.data,'$.tool')='Write' then json_extract(p.data,'$.state.input.content') end`
	}
	if harness == "kilocode" {
		// Kilo CLI's notebook_edit {path, action, kind, source}: insert and
		// replace write source into a cell; delete and create write none of
		// it (#4534).
		path += `when json_extract(p.data,'$.tool')='notebook_edit' then json_extract(p.data,'$.state.input.path') `
		nw += `,case when json_extract(p.data,'$.tool')='notebook_edit' and json_extract(p.data,'$.state.input.action') in ('insert','replace') ` +
			`then json_extract(p.data,'$.state.input.source') end`
	}
	return `'editpath',case ` + path + `end,` +
		`'old',` + old + `,` +
		`'new',coalesce(` + nw + `),` +
		// An edit the tool refused — ZCode's "File has not been read yet",
		// opencode's error state — changed nothing, and is not recorded as if
		// it had.
		`'refused',case when json_extract(p.data,'$.state.status')='error' then 1 end,`
}

// opencodeV1Tools admits the edit and write parts beside read, bash and
// apply_patch, with the same cheap gate in front of the exact test; and for
// ZCode its Claude-named tools, so opencode's own query gains no clause for
// them; read as opencode's were, ZCode sessions were text alone (#4428).
func opencodeV1Tools(harness string) string {
	names := []string{"edit", "write"}
	if harness == "zcode" {
		names = append(names, "Bash", "Read", "Edit", "Write")
	}
	if harness == "kilocode" {
		// Kilo CLI 7.8's own tools beside opencode's (#4534).
		names = append(names, "background_process", "notebook_edit", "notebook_read")
	}
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, ` or (instr(substr(p.data,1,200),'"tool":"%s"')>0 and json_extract(p.data,'$.tool')='%s')`, name, name)
	}
	return b.String()
}

// opencodeV1Command is the command a part ran. Kilo CLI's background_process
// takes one only to start or monitor a process; its other actions name a
// process by id (#4534).
func opencodeV1Command(harness string) string {
	cmd := `json_extract(p.data,'$.state.input.command')`
	if harness != "kilocode" {
		return cmd
	}
	return `case when json_extract(p.data,'$.tool')='background_process' ` +
		`and coalesce(json_extract(p.data,'$.state.input.action'),'') not in ('start','monitor') then null else ` + cmd + ` end`
}

// opencodeV1Path is the file a read opened: `filePath` in opencode's read,
// `file_path` in ZCode's Read, and the notebook Kilo CLI's notebook_read and
// notebook_edit name under `path` (#4534).
func opencodeV1Path(harness string) string {
	if harness == "kilocode" {
		return `case when json_extract(p.data,'$.tool')='read' then json_extract(p.data,'$.state.input.filePath') ` +
			`when json_extract(p.data,'$.tool') in ('notebook_read','notebook_edit') then json_extract(p.data,'$.state.input.path') end`
	}
	if harness != "zcode" {
		return `case when json_extract(p.data,'$.tool')='read' then json_extract(p.data,'$.state.input.filePath') end`
	}
	return `coalesce(json_extract(p.data,'$.state.input.filePath'),case when json_extract(p.data,'$.tool')='Read' ` +
		`then json_extract(p.data,'$.state.input.file_path') end)`
}

// readOpencodeRows runs one projection and folds its rows into by, keyed by
// session id. It returns how many rows came back.
func readOpencodeRows(harness, db, q string, by map[string]*model.Session) (int, error) {
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	// What sqlite3 says when it refuses, not merely that it did. "exit status
	// 1" is what a person was asked to report, and it names neither a renamed
	// column nor a locked database nor a file that is not a database (#1642).
	var whyNot bytes.Buffer
	cmd.Stderr = &whyNot
	dec, err := sqliteRows(cmd)
	if err != nil {
		return 0, err
	}
	rows := 0
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return rows, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		id, _ := r["id"].(string)
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			dir, _ := r["directory"].(string)
			s = &model.Session{Harness: harness, ID: id, Project: projectName(dir), Path: dir, Started: parseTimeAny(r["time_created"]), Updated: parseTimeAny(r["time_updated"])}
			by[id] = s
		}
		role := str(r["role"])
		txt := str(r["text"])
		if opencodeSynthetic(r["summary"]) {
			// The summary is the only record of the half that was compacted
			// away, so it is kept — under its own role, where an ordinary
			// question does not reach it and `--role summary` does.
			role = RoleSummary
		}
		if opencodeSynthetic(r["synthetic"]) || opencodeSynthetic(r["ignored"]) {
			continue
		}
		// A read call carries no text, only the file it opened. Recorded under
		// the files role so it can answer "which files" without competing in
		// ordinary search.
		if path := str(r["path"]); path != "" && IndexToolPaths() {
			t := partTime(r)
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: kiloNotebookPath(harness, path, s.Path), Time: t})
			// A notebook edit names its file and writes a cell, so it goes on
			// to the edit below.
			if str(r["old"]) == "" && str(r["new"]) == "" {
				continue
			}
		}
		if cmd := str(r["cmd"]); cmd != "" {
			t := partTime(r)
			out := str(r["out"])
			if r["exit"] == nil && harness == "zcode" {
				// ZCode records no exit field and writes a failed run's code
				// Claude's way, "Exit code N" as the output's first line, and
				// only for a non-zero N (#4536).
				head, rest, _ := strings.Cut(out, "\n")
				if code, ok := statusCode(head, "Exit code ", ""); ok && code != 0 {
					r["exit"] = float64(code)
					out = rest
				}
			}
			if IndexCommands() && worthIndexing(cmd) {
				// A non-zero exit rides with the command. opencode records it on
				// 99% of runs, which is the one thing Claude's transcripts
				// cannot give: there the verdict has to be inferred from output
				// text and only ~17% of runs can be scored. Zero is left off —
				// it is the common case, and saying so on 18,000 records would
				// be noise rather than information.
				line := "$ " + cmd
				if code := exitCode(r["exit"]); code > 0 {
					line += fmt.Sprintf("  → exit %d", code)
				}
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: line, Time: t})
			}
			// The output is a separate record under the same role Claude's tool
			// results use, so `--role tool-output` means the same thing on both.
			if out != "" && IndexToolOutput() {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
			}
			continue
		}
		// An `edit` call hands back the text it replaced and the text it wrote,
		// so both sides are recorded the way every other harness's edit is.
		if old, nw := str(r["old"]), str(r["new"]); old != "" || nw != "" {
			if opencodeSynthetic(r["refused"]) {
				continue
			}
			path := kiloNotebookPath(harness, str(r["editpath"]), s.Path)
			t := partTime(r)
			if path != "" && old != "" && IndexEdits() {
				span := old
				if len(span) > editSpanMax {
					span = span[:editSpanMax]
				}
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: path + "\n" + span, Time: t})
			}
			if path != "" && nw != "" && IndexWrites() {
				if rec := WroteRecord(path, nw); rec != "" {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: rec, Time: t})
				}
			}
			continue
		}
		if patch := str(r["patch"]); patch != "" {
			t := partTime(r)
			if IndexEdits() {
				for _, span := range patchSpans(patch) {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: t})
				}
			}
			// apply_patch is opencode's main editing tool, and the payload it
			// already ships carries both sides of the change — so the written
			// side costs nothing more to read.
			if IndexWrites() {
				for _, rec := range addedLinesOfPatch(patch) {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: rec, Time: t})
				}
			}
			continue
		}
		if txt == "" {
			continue
		}
		txt = capParsedMessage(txt)
		// Whatever carries the stamp: the part's, then the message blob's, then
		// the message row's column — and failing all three the conversation's
		// own start, which is the fallback cursor takes for a bubble without a
		// stamp. The year zero is not a time anyone asked deja to record, and
		// it reaches `show --json` as it is.
		t := parseTimeAny(r["pt"])
		if t.IsZero() {
			t = parseTimeAny(r["mt"])
		}
		if t.IsZero() {
			t = parseTimeAny(r["mc"])
		}
		if t.IsZero() {
			t = s.Started
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return rows, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			return 0, fmt.Errorf("opencode: query failed, the store schema may have changed: %w",
				withStderr(err, &whyNot))
		}
		return rows, err
	}
	return rows, nil
}

// kiloNotebookPath puts a Kilo CLI path relative to the session directory
// onto it: the notebook tools take one relative to the request directory, where
// opencode's own tools take absolute paths (#4534).
func kiloNotebookPath(harness, path, dir string) string {
	if harness != "kilocode" {
		return path
	}
	return resolveToolPath(path, dir)
}

// opencodeParents maps a session id to its parent's, for the sessions that
// have one. The column arrived with subagents; a query that fails is a store
// without it, and nothing is stamped.
func opencodeParents(db string) map[string]string {
	var m map[string]string
	for _, table := range opencodeSessionTables(db) {
		cmd, stopRead := sqliteReadCmd(db, `select json_object('id',id,'parent_id',parent_id) from `+
			table+` where parent_id is not null and parent_id <> ''`)
		b, err := cmd.Output()
		stopRead()
		if err != nil || len(b) == 0 {
			continue
		}
		rows, err := sqliteObjects[struct {
			ID     string `json:"id"`
			Parent string `json:"parent_id"`
		}](b)
		if err != nil {
			continue
		}
		if m == nil {
			m = make(map[string]string, len(rows))
		}
		for _, r := range rows {
			if r.ID != "" && r.Parent != "" && r.ID != r.Parent {
				m[r.ID] = r.Parent
			}
		}
	}
	return m
}

func OpencodeCounts() (sessions, messages int, err error) {
	if fi, e := os.Stat(OpencodeDB()); e != nil || fi.Size() == 0 {
		return 0, 0, nil
	}
	q := "select (select count(*) from session),(select count(*) from part where json_extract(data,'$.type')='text')"
	switch sc := opencodeSchemaOf(OpencodeDB()); {
	case sc.legacy:
		// Both layouts, each session once: the ids the v1 migration copied
		// are in both session tables, and their turns count where the parser
		// reads them from.
		q = "select (select count(*) from (select id from session union select id from " + sc.sessionTable + "))," +
			"(select count(*) from part p join message m on m.id=p.message_id join session s on s.id=m.session_id " +
			"where json_extract(p.data,'$.type')='text'" + opencodeNotMoved + ")" +
			"+(select count(*) from session_message where type in ('user','assistant'))"
	case sc.v2:
		// A 2.x turn holds its parts in its own blob, so the second figure is
		// the turns that carry words rather than the text parts under them.
		q = "select (select count(*) from " + sc.sessionTable +
			"),(select count(*) from session_message where type in ('user','assistant'))"
	}
	cmd, stopRead := sqliteReadCmd(OpencodeDB(), q)
	defer stopRead()
	b, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	f := strings.Split(strings.TrimSpace(string(b)), "|")
	if len(f) == 2 {
		_, _ = fmt.Sscanf(f[0], "%d", &sessions)
		_, _ = fmt.Sscanf(f[1], "%d", &messages)
	}
	return
}

// sqlEscape doubles single quotes for embedding in a SQL literal. It does not
// add the surrounding quotes — the caller does, because half the call sites
// build a LIKE pattern around the value. It was called sqlQuote, and that name
// cost two bugs in one day: both times the quotes were left off and sqlite
// rejected the query, which the parsers then reported as an empty store.
func sqlEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }

// sqlLikeEscape neutralises the LIKE wildcards % and _ (and the escape
// character itself) so a search term is matched literally. The pattern must be
// built with `escape '\'`; without this an id like `a_b` matched `axb` and a
// query with a `%` degenerated into a full-table scan. The caller still runs
// the result through sqlEscape for the surrounding quotes.
func sqlLikeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func str(v any) string { s, _ := v.(string); return s }
func parseNestedTime(m map[string]any, k, sub string) time.Time {
	if x, ok := m[k].(map[string]any); ok {
		return parseTimeAny(x[sub])
	}
	return time.Time{}
}

// ParseOpencodeNewest reads only the most recent session. deja doctor asks
// one question of a store — does it parse into sessions — and answering it by
// reading a multi-gigabyte database took 6.5 seconds of an 8 second report.
func ParseOpencodeNewest(db string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// The newest session that has turns. opencode writes the session row
	// before the first message, so a session opened and left empty is often
	// the newest, and reading it alone told doctor the store parsed to zero
	// (#4151). Both lookups ride opencode's own session_id indexes.
	sc := opencodeSchemaOf(db)
	v1 := "select id,time_created from session s where exists (select 1 from message m where m.session_id=s.id)"
	v2 := "select id,time_created from " + sc.sessionTable + " s where exists (select 1 from session_message x " +
		"where x.session_id=s.id and x.type in " + opencodeTurnTypes + ")"
	from := v1
	switch {
	case sc.legacy:
		// Either layout: a store mid-migration can have its latest work in
		// the old tables or the new.
		from = v1 + " union all " + v2
	case sc.v2:
		from = v2
	}
	q := "select id from (" + from + ") order by time_created desc limit 1"
	probe, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	var whyNot bytes.Buffer
	probe.Stderr = &whyNot
	out, err := probe.Output()
	if err != nil {
		// This is the first thing doctor runs against the store, so it is the
		// one whose complaint a person is shown. "exit status 1" named nothing
		// (#1642).
		return nil, withStderr(err, &whyNot)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return nil, nil
	}
	return ParseOpencodeDBWhere(db, " and s.id='"+sqlEscape(id)+"'", 0)
}

// opencodeSynthetic reads either of opencode's two "this is not the
// conversation" flags off a sqlite3 -json row — `synthetic` for text opencode
// wrote under the user role, `ignored` for a part it never sends — across the
// shapes that come back: 1, a json.Number, or a bool.
func opencodeSynthetic(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case json.Number:
		return x.String() != "0" && x.String() != ""
	case string:
		return x == "1" || x == "true"
	}
	return false
}

// opencodeTitles maps a session id to the name opencode gave it, for the names
// worth having. A store without the column stamps nothing.
func opencodeTitles(db string) map[string]string {
	var out map[string]string
	for _, table := range opencodeSessionTables(db) {
		cmd, stopRead := sqliteReadCmd(db, `select json_object('id',id,'title',title) from `+
			table+` where title is not null and title <> ''`)
		b, err := cmd.Output()
		stopRead()
		if err != nil || len(b) == 0 {
			continue
		}
		rows, err := sqliteObjects[struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}](b)
		if err != nil {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(rows))
		}
		for _, r := range rows {
			t := strings.TrimSpace(r.Title)
			if r.ID == "" || opencodeThinTitle(t) {
				continue
			}
			out[r.ID] = t
		}
	}
	return out
}

// opencodeThinTitle reports whether opencode's own name for a session says
// less than its first line would: its "New session - <timestamp>" placeholder
// (31 of 1472 on a real store), or a name of two words or fewer and short —
// "done" ×12, "ok", "Greeting" ×11 there, where the first line is the better
// name. Same shape as Continue's placeholders (#3274).
func opencodeThinTitle(t string) bool {
	if strings.HasPrefix(t, "New session - ") || t == "New session" {
		return true
	}
	return len(strings.Fields(t)) <= 2 && len([]rune(t)) <= 12
}

// partTime prefers the part's own timestamp and falls back to the message's.
func partTime(r map[string]any) time.Time {
	t := parseTimeAny(r["pt"])
	if t.IsZero() {
		t = parseTimeAny(r["mt"])
	}
	return t
}

// patchSpans turns an apply_patch payload into the same "path\nreplaced bytes"
// records a Claude Edit produces, so `deja restore` does not have to care which
// harness did the work. Only removed lines count: the added ones are on disk.
func patchSpans(patch string) []string {
	var out []string
	path := ""
	var removed []string
	flush := func() {
		if path != "" && len(removed) > 0 {
			span := strings.Join(removed, "\n")
			if len(span) > editSpanMax {
				span = span[:editSpanMax]
			}
			out = append(out, path+"\n"+span)
		}
		removed = nil
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Update File:"),
			strings.HasPrefix(line, "*** Delete File:"),
			strings.HasPrefix(line, "*** Add File:"):
			flush()
			path = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		case strings.HasPrefix(line, "@@"), line == "*** End Patch":
			flush()
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed = append(removed, line[1:])
		}
	}
	flush()
	return out
}

// IndexToolOutput reports whether what a command printed is indexed. On by
// default: it is where errors live, and every feature that reasons about what
// went wrong needs it. Off is for people who want a smaller index — measured at
// 49 MB of bash output on a 1150-session store.
func IndexToolOutput() bool { return os.Getenv("DEJA_INDEX_TOOL_OUTPUT") != "0" }

// exitCode reads the status opencode records for a command. sqlite3 -json
// hands numbers back as json.Number or float64 depending on the shape.
func exitCode(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0
		}
		return i
	}
	return 0
}

// sqliteStderrMax bounds what a failing sqlite3 is quoted saying. Its first
// line names the cause; the rest is the query it was handed, which is a
// screenful and says nothing a reader does not already have.
const sqliteStderrMax = 200

// withStderr turns "exit status 1" into what the tool actually said.
func withStderr(err error, buf *bytes.Buffer) error {
	msg := strings.TrimSpace(buf.String())
	if msg == "" {
		return err
	}
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > sqliteStderrMax {
		msg = strings.TrimSpace(msg[:sqliteStderrMax]) + "…"
	}
	return fmt.Errorf("%s (%w)", msg, err)
}

// ExitStderrLine is the first line sqlite3 wrote to stderr before it exited,
// or "" when the error carries none: "database is locked" says more than
// "exit status 5".
func ExitStderrLine(err error) string {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(ee.Stderr), "\n", 2)[0])
	return line
}
