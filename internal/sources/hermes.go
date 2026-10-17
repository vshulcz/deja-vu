package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// HermesHome is the Hermes root: profiles, plugins and config.yaml live here.
// HERMES_HOME is Hermes's own switch and moves install, parse and doctor
// together; DEJA_HERMES_HOME overrides it for deja alone (#3203).
func HermesHome() string {
	return EnvPath("DEJA_HERMES_HOME", EnvPath("HERMES_HOME", filepath.Join(Home(), ".hermes")))
}

// Hermes keeps one SQLite store per profile under ~/.hermes/profiles/<name>,
// so a user running several profiles has several stores and all of them count.
func HermesProfilesRoot() string {
	if p := os.Getenv("DEJA_HERMES_PROFILES_ROOT"); p != "" {
		return p
	}
	return filepath.Join(HermesHome(), "profiles")
}

// HermesDBs lists every profile store that exists and has content. A single
// store can be forced with DEJA_HERMES_DB, which is what the tests use.
func HermesDBs() []string {
	if p := os.Getenv("DEJA_HERMES_DB"); p != "" {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return []string{p}
		}
		return nil
	}
	var out []string
	// 0.17 keeps one store at the root; older builds shard it per profile.
	// Both shapes exist in the wild, so both are looked for.
	if db := filepath.Join(HermesHome(), "state.db"); nonEmptyFile(db) {
		out = append(out, db)
	}
	entries, err := os.ReadDir(HermesProfilesRoot())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := filepath.Join(HermesProfilesRoot(), e.Name(), "state.db")
		if nonEmptyFile(db) {
			out = append(out, db)
		}
	}
	return out
}

// HermesSessionFiles is the store list the indexer stats for changes. The
// Postgres store, when opted in, rides along as a token the index fingerprints
// instead of stats.
func HermesSessionFiles() []string {
	files := HermesDBs()
	if dsn := HermesPGDSN(); dsn != "" {
		files = append(files, HermesPGStorePath(dsn))
	}
	return files
}

func LoadHermes() []model.Session {
	var out []model.Session
	for _, db := range HermesDBs() {
		ss, _ := ParseHermesDB(db)
		out = append(out, ss...)
	}
	if dsn := HermesPGDSN(); dsn != "" {
		ss, _ := ParseHermesPG(dsn, 0)
		out = append(out, ss...)
	}
	return out
}

func ParseHermesDB(db string) ([]model.Session, error) {
	return parseHermesDBWhere(db, "")
}

// ParseHermesDBSince reads only what changed, so a re-index of an unchanged
// profile costs one query instead of the whole history. timestamp is REAL
// seconds since the epoch in Hermes' schema.
//
// A second back from the watermark, which is the guard grok's reader spells in
// milliseconds (#2150). The column is REAL but a store may write whole seconds
// into it, and then every message sharing the watermark's second compares equal
// to it and a strict `>` leaves it out for good — measured on such a store, 0
// of 2 came back. The cost is re-reading one second of history, and those turns
// are already held (#2075).
func ParseHermesDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return parseHermesDBWhere(db, "")
	}
	// By session, so the session comes back whole: what a store parsed from its
	// watermark hands back replaces what the index holds for that key, and a
	// return of the newest turn alone takes the earlier ones with it (#2075).
	// The whole-second floor is then harmless — it can only re-offer a message
	// the pass was going to replace anyway.
	return parseHermesDBWhere(db, fmt.Sprintf(
		" and session_id in (select session_id from messages where timestamp > %d)",
		t.Add(-time.Second).Unix()))
}

func parseHermesDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	cols := hermesColumns(db)
	// Rewind takes turns back with active=0; compaction archives the turns it
	// summarised with active=0 and compacted=1, and Hermes' own search still
	// reads those. A store with active alone has only the first.
	live, archived := "", ""
	switch {
	case cols["active"] && cols["compacted"]:
		live = " and (active = 1 or compacted = 1)"
		archived = `,'compacted',compacted`
	case cols["active"]:
		live = " and active = 1"
	}
	// json_object rather than the shell's -json mode, which is quadratic in
	// what it escapes — see sqliteRows. In insertion order, as Hermes reads
	// its own sessions (get_messages): a row's timestamp can be the
	// platform's event time, and the rows compaction writes as one batch have
	// to stay together (#4296).
	q := `select json_object('session_id',cast(session_id as text),'role',cast(role as text),` +
		`'content',cast(content as text),'timestamp',timestamp` + archived + `) from messages ` +
		`where role in ('user','assistant') and content is not null and content <> ''` + live + where +
		` order by session_id,id`
	if cols["tool_calls"] && cols["tool_call_id"] && cols["tool_name"] {
		// A tool-call row has no content, only tool_calls, and the result lands
		// on a `tool` row; both carry the session's work (#4242).
		q = `select json_object('session_id',cast(session_id as text),'role',cast(role as text),` +
			`'content',cast(content as text),'timestamp',timestamp,` +
			`'tool_calls',cast(tool_calls as text),'tool_call_id',cast(tool_call_id as text),` +
			`'tool_name',cast(tool_name as text)` + archived + `) from messages ` +
			`where role in ('user','assistant','tool') and ((content is not null and content <> '')` +
			` or (tool_calls is not null and tool_calls <> ''))` + live + where +
			` order by session_id,id`
	}
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	out, err := decodeHermesArray(dec, hermesProfile(db), db)
	if err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if len(out) == 0 {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			return nil, fmt.Errorf("hermes: query failed, the store schema may have changed: %w", err)
		}
		return nil, err
	}
	applyHermesMeta(out, hermesSessionMeta(db))
	return out, nil
}

// applyHermesMeta stamps each session with what the sessions table says:
// its project from the directory it ran in, and who started it. A subagent's
// run folds under the session that spawned it, as Claude Code's sidechains
// do, and a cron job's run stays searchable but is not what a person asked
// (#4948).
func applyHermesMeta(out []model.Session, meta map[string]hermesMeta) {
	for i := range out {
		m := meta[out[i].ID]
		if m.Cwd != "" {
			out[i].Project = projectName(m.Cwd)
		}
		// A branch is a copy of its source's turns under a new id, so recall
		// asked from it leaves the source out (#5141). Its row has no cwd; it
		// ran where its source did.
		if b := m.Branched; b != "" && m.Source != "subagent" {
			out[i].Kind, out[i].Parent = "fork", b
			if m.Cwd == "" && meta[b].Cwd != "" {
				out[i].Project = projectName(meta[b].Cwd)
			}
			continue
		}
		switch m.Source {
		case "subagent":
			if m.Parent != "" {
				out[i].Kind, out[i].Parent = "subagent", m.Parent
			}
		case "cron":
			out[i].Kind = "cron"
		}
	}
}

// decodeHermesArray reads {session_id,role,content,timestamp} rows into
// sessions — with tool_calls, tool_call_id and tool_name when the query asked
// for them — either as a json array — Postgres json_agg, with dec positioned
// just past the opening '[' and left just past the closing ']' — or as the
// bare stream of objects the sqlite3 reader produces. project and path stamp
// every session.
//
// Both queries order by session, so a session is finished when the next one
// starts and its bookkeeping goes with it.
func decodeHermesArray(dec *json.Decoder, project, path string) ([]model.Session, error) {
	var out []model.Session
	var cur *hermesSession
	finish := func() {
		if cur == nil {
			return
		}
		// No title here: the index derives one (the person's first line,
		// a greeting giving way to the next turn, the agent's line when
		// nobody typed). Titling in the parser skipped the greeting rule, so
		// a session opened with "hi" listed as "hi" (#3241, #3251).
		if s := cur.done(); len(s.Messages) > 0 {
			out = append(out, s)
		}
		cur = nil
	}
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		id := str(r["session_id"])
		if id == "" {
			continue
		}
		if cur == nil || cur.s.ID != id {
			finish()
			cur = newHermesSession(model.Session{Harness: "hermes", ID: id, Project: project, Path: path})
		}
		cur.row(r)
	}
	finish()
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

// hermesColumns names the columns of the messages table. Every Hermes schema
// seen has the tool columns, but naming a missing one fails the whole query,
// and a store without them still has its prose to give.
func hermesColumns(db string) map[string]bool {
	out, err := sqliteOutput(db, "pragma table_info(messages)")
	if err != nil {
		return nil
	}
	cols := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		// cid|name|type|notnull|dflt_value|pk
		if f := strings.SplitN(line, "|", 3); len(f) == 3 {
			cols[f[1]] = true
		}
	}
	return cols
}

// hermesContentJSON is the prefix Hermes stores structured content under —
// a multimodal message's list of parts (hermes_state.py _encode_content).
const hermesContentJSON = "\x00json:"

// hermesText is a row's content as text: the text parts of a multimodal
// message, never the base64 of its images.
func hermesText(content string) string {
	if !strings.HasPrefix(content, hermesContentJSON) {
		return strings.TrimSpace(content)
	}
	var v any
	if json.Unmarshal([]byte(content[len(hermesContentJSON):]), &v) != nil {
		return ""
	}
	parts, _ := v.([]any)
	if m, ok := v.(map[string]any); ok {
		parts = []any{m}
	}
	var out []string
	for _, p := range parts {
		switch e := p.(type) {
		case string:
			out = append(out, e)
		case map[string]any:
			if e["type"] != nil && e["type"] != "text" {
				continue
			}
			if hermesMediaPlaceholder[str(e["text"])] {
				// What compaction leaves where it took an image out
				// (agent/context_compressor.py); it says nothing.
				continue
			}
			if s, _ := e["text"].(string); s != "" {
				out = append(out, s)
			} else if s, _ := e["text_summary"].(string); s != "" {
				out = append(out, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var hermesMediaPlaceholder = map[string]bool{
	"[Attached image — stripped after compression]": true,
	"[screenshot removed to save context]":          true,
}

// hermesTime reads Hermes' REAL epoch seconds. The shared parser handles
// integer epochs and RFC 3339, but a fractional second arrives as 1785000000.5
// and would otherwise land as the zero time — which sorts as ancient and never
// surfaces in recall.
func hermesTime(v any) time.Time {
	var secs float64
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return parseTimeAny(v)
		}
		secs = f
	case float64:
		secs = n
	default:
		return parseTimeAny(v)
	}
	if secs <= 0 {
		return time.Time{}
	}
	sec := int64(secs)
	return time.Unix(sec, int64((secs-float64(sec))*1e9)).UTC()
}

func nonEmptyFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

// hermesMeta is what Hermes' sessions table says about one session: the
// directory it was recorded in, who started it (cli, cron, subagent, a
// messaging platform) and the session that spawned it.
type hermesMeta struct {
	Cwd    string `json:"cwd"`
	Source string `json:"source"`
	Parent string `json:"parent"`
	// Branched is model_config._branched_from: /branch copies the
	// conversation into a new session and marks it with the one it came from
	// (hermes_cli/cli_commands_mixin.py, 0.17.0).
	Branched string `json:"branched"`
}

// hermesSessionMeta reads hermesMeta for every session. Stamped with the
// profile alone, every session fell into one project, and the prompt hook —
// which ranks the payload's project only — never served a Hermes session in
// the directory it was about (#3257). Best effort: a store from before the
// table or a column keeps what it has.
func hermesSessionMeta(db string) map[string]hermesMeta {
	return hermesSessionMetaWhere(db, "")
}

// hermesSessionMetaWhere is hermesSessionMeta for the rows a where clause
// keeps, "" for all of them.
func hermesSessionMetaWhere(db, where string) map[string]hermesMeta {
	cols := map[string]bool{}
	if out, err := sqliteOutput(db, `select name from pragma_table_info('sessions')`); err == nil {
		for _, c := range strings.Fields(string(out)) {
			cols[c] = true
		}
	}
	if !cols["id"] {
		return nil
	}
	fields := `'id',cast(id as text)`
	for col, key := range map[string]string{"cwd": "cwd", "source": "source", "parent_session_id": "parent"} {
		if cols[col] {
			fields += fmt.Sprintf(`,'%s',cast(%s as text)`, key, col)
		}
	}
	if cols["model_config"] {
		// json_valid first: one row of something else would fail the query
		// and lose every session's cwd with it.
		fields += `,'branched',case when json_valid(model_config) then cast(json_extract(model_config,'$._branched_from') as text) end`
	}
	out, err := sqliteOutput(db, `select json_object(`+fields+`) from sessions`+where)
	if err != nil {
		return nil
	}
	rows, err := sqliteObjects[struct {
		ID string `json:"id"`
		hermesMeta
	}](out)
	if err != nil {
		return nil
	}
	meta := make(map[string]hermesMeta, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			continue
		}
		meta[r.ID] = hermesMeta{Cwd: strings.TrimSpace(r.Cwd), Source: strings.TrimSpace(r.Source), Parent: strings.TrimSpace(r.Parent), Branched: strings.TrimSpace(r.Branched)}
	}
	return meta
}

// hermesSessionID is the id Hermes gives a CLI session and a /branch:
// 20261018_013350_8f1cbe.
var hermesSessionID = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]+$`)

// HermesSessionHead is what the sessions table says about one session, read
// off the store for a session the index has not seen yet: a branch's first
// turns come before any build, and its recall answered with its source
// (#5141). Who it is, not its messages; false when no store holds the id.
func HermesSessionHead(id string) (model.Session, bool) {
	if !hermesSessionID.MatchString(id) {
		return model.Session{}, false
	}
	for _, db := range HermesDBs() {
		m, ok := hermesSessionMetaWhere(db, ` where id = '`+id+`'`)[id]
		if !ok {
			continue
		}
		s := []model.Session{{ID: id, Harness: "hermes"}}
		applyHermesMeta(s, map[string]hermesMeta{id: m})
		return s[0], true
	}
	return model.Session{}, false
}

// hermesProfile names the session's project after the profile directory when
// the store says nothing about where the session was recorded.
func hermesProfile(db string) string {
	name := filepath.Base(filepath.Dir(db))
	// The root store has no profile directory to be named after.
	if name == filepath.Base(HermesHome()) {
		return "hermes"
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "hermes"
	}
	return name
}

// HermesResumeProfile is the profile `hermes -p` has to name for a session
// read from this store to be found, or "" when the plain command finds it.
// `hermes --resume` looks only in the active profile's store, so a session
// recorded under `hermes -p work` came back "Session not found" (#4248). A
// profile's session is always named, so the command does not depend on which
// one is active; a sticky `hermes profile use work` makes the root store need
// naming too, as `default`. dir says the name is a directory under profiles/,
// for the caller to check it is one Hermes takes.
//
// The root is worked out the way Hermes' get_default_hermes_root does: a
// HERMES_HOME under `profiles/` is a profile, which `hermes -p work` exports
// to everything it runs, deja included, and the root is two levels up. In that
// mode the root's store is always named. Like Hermes, the profile and its name
// are read off the path as written, absolute and cleaned, so a profile that is
// a symlink to another disk is still that profile; symlinks are resolved only
// to tell whether two directories are the same one, so a symlinked or relative
// home still matches. A store that is neither the root's nor a profile's, or a
// Postgres one, keeps the plain command.
func HermesResumeProfile(db string) (name string, dir bool) {
	if IsHermesPGStore(db) {
		return "", false
	}
	root, inProfile := hermesAbs(HermesHome()), false
	if filepath.Base(filepath.Dir(root)) == "profiles" {
		root, inProfile = filepath.Dir(filepath.Dir(root)), true
	}
	profiles := filepath.Join(root, "profiles")
	if p := os.Getenv("DEJA_HERMES_PROFILES_ROOT"); p != "" {
		profiles = hermesAbs(p)
	}
	store := hermesAbs(filepath.Dir(db))
	if sameDir(filepath.Dir(store), profiles) {
		return filepath.Base(store), true
	}
	if !sameDir(store, root) {
		return "", false
	}
	if inProfile {
		return "default", false
	}
	b, err := os.ReadFile(filepath.Join(root, "active_profile"))
	if err != nil {
		return "", false
	}
	if active := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff")); active != "" && active != "default" {
		return "default", false
	}
	return "", false
}

// hermesAbs is p absolute and cleaned, symlinks left as written.
func hermesAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// sameDir reports whether two directories are the same one: as written, or
// once their symlinks are followed.
func sameDir(a, b string) bool {
	return samePath(a, b) || samePath(hermesResolved(a), hermesResolved(b))
}

// hermesResolved is p absolute, cleaned and with its symlinks followed: those
// of the deepest part that exists, with the rest joined back on, so a store
// whose directory is gone resolves the same way as the home it sat in.
func hermesResolved(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(dir) == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// samePath compares two cleaned paths the way the filesystem does: Windows
// ignores case.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// HermesStoreLacks reports whether the Hermes store a session was read from no
// longer has it — taken out with `hermes sessions delete`, which leaves
// `hermes --resume` answering "Session not found" (#4250). False whenever that
// cannot be told: a Postgres store, no file, or a read that fails. The sessions
// table is what resume looks the id up in; a store from before it is asked
// through its messages.
func HermesStoreLacks(db, id string) bool {
	if IsHermesPGStore(db) || !nonEmptyFile(db) {
		return false
	}
	query := func(q string) (string, bool) {
		cmd, stop := sqliteReadCmd(db, q)
		defer stop()
		b, err := cmd.Output()
		return strings.TrimSpace(string(b)), err == nil
	}
	names, ok := query(`select name from sqlite_master where type='table' and name in ('sessions','messages')`)
	if !ok || names == "" {
		return false
	}
	q := fmt.Sprintf(`select count(*) from messages where session_id='%s'`, sqlEscape(id))
	if strings.Contains(" "+strings.Join(strings.Fields(names), " ")+" ", " sessions ") {
		q = fmt.Sprintf(`select count(*) from sessions where id='%s'`, sqlEscape(id))
	}
	n, ok := query(q)
	return ok && n == "0"
}
