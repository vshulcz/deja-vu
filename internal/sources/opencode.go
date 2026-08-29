package sources

import (
	"bytes"
	"encoding/json"
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

// SQLite3Available reports whether the sqlite3 CLI the opencode parser
// shells out to is on PATH.
func SQLite3Available() bool {
	_, err := exec.LookPath("sqlite3")
	return err == nil
}

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
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), "", 0)
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

func ParseOpencodeDB(db string) ([]model.Session, error) {
	return ParseOpencodeDBWhere(db, "", 0)
}

func ParseOpencodeDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseOpencodeDBWhere(db, "", 0)
	}
	rfc := sqlEscape(t.UTC().Format(time.RFC3339Nano))
	where := fmt.Sprintf(" and (%s or m.time_created > '%s' or %s or json_extract(p.data,'$.time.start') > '%s')",
		newerThanEpoch("m.time_created", t), rfc,
		newerThanEpoch("json_extract(p.data,'$.time.start')", t), rfc)
	return ParseOpencodeDBWhere(db, where, 0)
}

func ParseOpencodeDBWhere(db, where string, limit int) ([]model.Session, error) {
	// The sqlite3 CLI CREATES a missing database file on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	// Narrow projection: shipping full m.data/p.data JSON blobs through the
	// sqlite3 pipe on multi-GB stores takes minutes; extracting just the
	// needed scalars keeps the dump to tens of MB and seconds.
	q := `select s.id,s.directory,s.time_created,s.time_updated,` +
		`json_extract(m.data,'$.role') as role,` +
		`json_extract(p.data,'$.text') as text,` +
		`json_extract(p.data,'$.state.input.filePath') as path,` +
		`json_extract(p.data,'$.state.input.command') as cmd,` +
		`json_extract(p.data,'$.state.input.patchText') as patch,` +
		// The output of a bash call and its exit status. Only bash: `read`
		// output is 119 MB of file contents on this store against 49 MB of
		// command output, and #547 measured file bodies as the weakest slice
		// deja could index. What a command printed is where the errors live.
		`case when json_extract(p.data,'$.tool')='bash' ` +
		`then json_extract(p.data,'$.state.output') end as out,` +
		`json_extract(p.data,'$.state.metadata.exit') as exit,` +
		`json_extract(p.data,'$.time.start') as pt,` +
		`json_extract(m.data,'$.time.created') as mt,` +
		// The row's own column, which is what the since clause filters on. A
		// store that fills it and leaves the blob's time out handed back
		// messages dated to the year zero (#2086).
		`m.time_created as mc ` +
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
		`and json_extract(p.data,'$.tool')='apply_patch'))` + where + ` order by s.id,m.time_created,p.id` + lim
	cmd := exec.Command("sqlite3", "-readonly", "-json", db, ".timeout 5000", q)
	// What sqlite3 says when it refuses, not merely that it did. "exit status
	// 1" is what a person was asked to report, and it names neither a renamed
	// column nor a locked database nor a file that is not a database (#1642).
	var whyNot bytes.Buffer
	cmd.Stderr = &whyNot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		waitErr := cmd.Wait()
		if err == io.EOF {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("opencode: query failed, the store schema may have changed: %w",
					withStderr(waitErr, &whyNot))
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	by := map[string]*model.Session{}
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		id, _ := r["id"].(string)
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			dir, _ := r["directory"].(string)
			s = &model.Session{Harness: "opencode", ID: id, Project: projectName(dir), Path: dir, Started: parseTimeAny(r["time_created"]), Updated: parseTimeAny(r["time_updated"])}
			by[id] = s
		}
		role := str(r["role"])
		txt := str(r["text"])
		// A read call carries no text, only the file it opened. Recorded under
		// the files role so it can answer "which files" without competing in
		// ordinary search.
		if path := str(r["path"]); path != "" && IndexToolPaths() {
			t := partTime(r)
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: path, Time: t})
			continue
		}
		if cmd := str(r["cmd"]); cmd != "" {
			t := partTime(r)
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
			if out := str(r["out"]); out != "" && IndexToolOutput() {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
			}
			continue
		}
		if patch := str(r["patch"]); patch != "" && IndexEdits() {
			t := partTime(r)
			for _, span := range patchSpans(patch) {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: t})
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
	if _, err := dec.Token(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	var out []model.Session
	for _, s := range by {
		out = append(out, *s)
	}
	return out, nil
}

func OpencodeCounts() (sessions, messages int, err error) {
	if fi, e := os.Stat(OpencodeDB()); e != nil || fi.Size() == 0 {
		return 0, 0, nil
	}
	cmd := exec.Command("sqlite3", "-readonly", OpencodeDB(), ".timeout 5000", "select (select count(*) from session),(select count(*) from part where json_extract(data,'$.type')='text')")
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
	probe := exec.Command("sqlite3", "-readonly", db, ".timeout 5000",
		"select id from session order by time_created desc limit 1")
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
