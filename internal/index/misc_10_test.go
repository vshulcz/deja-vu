package index

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRussianFillerDoesNotAnchor(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", "починил ретраи очереди доставки для отчетов")
	mk("b", "давай сделай все красиво и покажи мне это")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The filler-only session must not outrank content on a question full of
	// Russian glue: every meaningful word points at session a.
	got, err := Search(dir, query.Options{Query: "давай посмотри как мы делали ретраи доставки", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "a" {
		t.Fatalf("content session must rank first, got %d sessions", len(got))
	}
	terms := RelevanceTerms("давай сделай все по шагам и говори мне")
	for _, term := range terms {
		if term == "давай" || term == "сделай" || term == "говори" {
			t.Fatalf("filler survived RelevanceTerms: %v", terms)
		}
	}
}

func TestRussianInflectionFolds(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"net1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"что то у меня с сетью локально всё отваливается"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "net1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// A decoy that has the exact nominative "сеть" but nothing else relevant.
	line2 := `{"type":"user","sessionId":"dec1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"локальная сеть офиса работает стабильно"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "dec1.jsonl"), []byte(line2), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Search(dir, query.Options{Query: "сеть отваливается почему", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "net1" {
		t.Fatalf("inflected сетью must be reachable from сеть and outrank the decoy, got %d", len(got))
	}
}

func TestEnsureHonorsHarnessScope(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"c1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"claude only content"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second harness's store exists and must NOT be ingested.
	piRoot := filepath.Join(tmp, "pi")
	if err := os.MkdirAll(piRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	piLine := `{"type":"session","id":"p1","timestamp":"2026-01-02T03:04:05Z"}` + "\n" +
		`{"type":"message","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"text","text":"pi session must stay out"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(piRoot, "p1.jsonl"), []byte(piLine), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_PI_ROOT", piRoot)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range m.Sessions {
		if meta.Harness != "claude" {
			t.Fatalf("claude-scoped Ensure ingested harness %q (session %s)", meta.Harness, meta.ID)
		}
	}
	if len(m.Sessions) != 1 {
		t.Fatalf("sessions = %d, want the single claude fixture", len(m.Sessions))
	}
}

func TestDateTokensMakeSessionsFindableByMonth(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, ts, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("jun", "2023-06-10T10:00:00Z", "fixed the camping stove regression")
	mk("sep", "2023-09-03T10:00:00Z", "tuned the winter tent lineup")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Search(dir, query.Options{Query: "camping june", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "jun" {
		t.Fatalf("month token did not surface the June session: %+v", got)
	}
}

// The relevance tier took the global top 50 and only then applied the search
// scope, so a filtered search returned nothing whenever the unfiltered head
// was full of sessions from another harness or project — the deeper matches
// the filter was asking for had already been cut.
func TestRelevanceTierAppliesScopeBeforeTruncation(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// 60 Claude sessions matching three of the four query terms, so they all
	// outrank the Codex one and fill the top 50 on their own.
	for i := range 60 {
		line := fmt.Sprintf(`{"type":"user","sessionId":"c%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"kubernetes autoscaler telemetry notes %d"}}`, i, i) + "\n"
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("c%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A session holding the fourth term and nothing else, so every query term
	// resolves (the close tier drops terms it cannot resolve and ANDs the
	// rest) while no session holds all four — which is what drops the ladder
	// down to the relevance tier.
	if err := os.WriteFile(filepath.Join(proj, "d1.jsonl"),
		[]byte(`{"type":"user","sessionId":"d1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"dashboards"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One Codex session matching two of the terms — a genuine answer for a
	// harness-scoped search, and ranked below every Claude session.
	sess := filepath.Join(codexRoot, "sessions", "2026", "01", "02")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	roll := `{"type":"session_meta","payload":{"id":"cx1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/app"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"kubernetes autoscaler runbook"},"timestamp":"2026-01-02T03:04:06Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "rollout-2026-01-02T03-04-05-cx1.jsonl"), []byte(roll), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// No session holds all four terms, so the ladder falls to the relevance
	// tier — which is where the truncation happened.
	const q = "kubernetes autoscaler telemetry dashboards"
	all, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Tier != query.TierRelevance {
		t.Fatalf("fixture resolved through tier %q, want the relevance tier", all.Tier)
	}
	got, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, Harness: "codex", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) == 0 {
		t.Fatal("harness-scoped relevance search returned nothing; the match was cut by truncation before the scope filter ran")
	}
	for _, s := range got.Sessions {
		if s.Harness != "codex" {
			t.Fatalf("scope leaked: got a %s session", s.Harness)
		}
	}
}

// smallPassStore writes n Claude Code sessions of four turns each, every turn
// a question, a command and its output, and builds an index over them. It
// returns the index and a function that rewrites session i shorter.
func smallPassStore(t *testing.T, n int) (string, func(i int)) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	session := func(i, turns int) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		var bodySb26 strings.Builder
		for turn := range turns {
			ts := fmt.Sprintf("%02d:%02d", turn, i)
			tid := fmt.Sprintf("t%d", turn)
			bodySb26.WriteString(parityClaudeLine(id, ts+"Z", "user", fmt.Sprintf(`"why does step %d of the migration hang"`, turn)) +
				parityClaudeLine(id, ts+"Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate STEP=`+strconv.Itoa(turn)+`"}}]`) +
				parityClaudeLine(id, ts+"Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`))
		}
		body += bodySb26.String()
		p := filepath.Join(proj, id+".jsonl")
		parityWrite(t, p, body, false)
		later := time.Now().Add(time.Duration(i+10*(4-turns)) * time.Second)
		_ = os.Chtimes(p, later, later)
	}
	for i := range n {
		session(i, 4)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	return inc, func(i int) { session(i, 3) }
}

// A pass that rewrites one session of many reads that session's records, not
// the whole log: the rest stay where they are, and the offset table says
// where. Walking the log was a second of a 20-session pass at 40,000.
func TestAReplacingPassReadsOnlyTheSessionsItChanges(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	walked := storedRecordsWalked.Load()
	shorten(5)
	parityPass(t, inc, false)
	walked = storedRecordsWalked.Load() - walked
	if walked == 0 {
		t.Fatal("no replacing pass ran, so this measures nothing")
	}
	// The changed session's twelve records, out of n*12.
	if walked > 12 {
		t.Errorf("the pass walked %d stored records, want the changed session's 12", walked)
	}
	sameAsRebuild(t, inc)

	// The control: without the offset table the pass walks the log.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	walked = storedRecordsWalked.Load()
	shorten(6)
	parityPass(t, inc, false)
	if walked = storedRecordsWalked.Load() - walked; walked < (n-1)*12 {
		t.Errorf("with no offset table the pass walked %d records, want them all, so the counter misses the walk", walked)
	}
	sameAsRebuild(t, inc)
}

// The command table and the session facts are mined again only from the
// sessions a pass changed, the first pass after a build included.
func TestAReplacingPassMinesOnlyTheChangedSessionsCommands(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	mined := commandRecordsMined.Load()
	shorten(3)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined > 3 {
		t.Errorf("the command tables read %d command records, want the changed session's 3", mined)
	}
	if len(ReadCommands(inc)) == 0 || len(ReadSessionFacts(inc)) != n {
		t.Fatalf("commands %v, facts for %d sessions: this measures nothing", ReadCommands(inc), len(ReadSessionFacts(inc)))
	}
	sameAsRebuild(t, inc)

	// The control: without the state every command is read again.
	if err := os.Remove(commandsStatePath(inc)); err != nil {
		t.Fatal(err)
	}
	mined = commandRecordsMined.Load()
	shorten(4)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined < (n-2)*4 {
		t.Errorf("with no state the tables read %d command records, want them all, so the counter misses the walk", mined)
	}
	sameAsRebuild(t, inc)
}

// Resolving a schema store's database stats files, and a pass met every
// transcript's path for the first time: a second of stats on 40,000 of them.
func TestAReplacingPassResolvesTheSchemaStoresOnce(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	var asked atomic.Int64
	saved := opencodeSchemaDBs
	t.Cleanup(func() { opencodeSchemaDBs = saved })
	counting := map[string]func() string{}
	for h, db := range saved {
		counting[h] = func() string { asked.Add(1); return db() }
	}
	opencodeSchemaDBs = counting
	// Walked whole, as a pass that cannot name its sessions walks it, so
	// every transcript's records are judged.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	shorten(2)
	parityPass(t, inc, false)
	if got, most := asked.Load(), int64(4*len(saved)); got > most {
		t.Errorf("the pass resolved the schema databases %d times, want at most %d", got, most)
	}
}

// A word the dead records shared with live ones is known live from the start
// of its postings; reading each one whole made the scrub cost the size of the
// common words' postings.
func TestTheScrubReadsTheStartOfALiveWordsPostings(t *testing.T) {
	const n = 60
	inc, shorten := smallPassStore(t, n)
	// Dead records from earlier passes raise the size below which a word's
	// postings are read at all.
	for i := range n / 6 {
		shorten(i)
	}
	parityPass(t, inc, false)
	probed := postingBytesProbed.Load()
	shorten(n - 1)
	parityPass(t, inc, false)
	probed = postingBytesProbed.Load() - probed
	if probed > 4*liveProbeBytes {
		t.Errorf("the scrub read %d bytes of postings, want no more than the start of each word's", probed)
	}
	sameAsRebuild(t, inc)
}

// The facts are carried with the state they were written beside. A facts
// file that went missing is not read as "no session ran anything".
func TestCarriedCommandFactsMatchTheirState(t *testing.T) {
	inc, shorten := smallPassStore(t, 6)
	if err := os.Remove(sessionFactsPath(inc)); err != nil {
		t.Fatal(err)
	}
	shorten(1)
	parityPass(t, inc, false)
	if got := len(ReadSessionFacts(inc)); got != 6 {
		t.Errorf("facts for %d sessions after the pass, want 6", got)
	}
	sameAsRebuild(t, inc)
}

// A store that changed in the same pass carries the sessions it handed back,
// not the whole log: someone running opencode or grok changes its database on
// most passes (#5476).
func TestAPassThatAlsoChangesAStoreReadsOnlyTheSessionsItChanged(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	const n = 12
	inc, shorten := smallPassStore(t, n)
	tmp := filepath.Dir(inc)
	oc, grok := filepath.Join(tmp, "opencode.db"), filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_OPENCODE_DB", oc)
	t.Setenv("DEJA_GROK_DB", grok)
	clock := time.Now().Add(-2 * time.Hour).UnixMilli()
	// Turn i of session n at minute 10i+n: each one past the slack a since
	// read goes back, and a later turn past every earlier one.
	ocMsg := func(n, i int) string {
		at := clock + int64(10*i+n)*60_000
		return fmt.Sprintf(`insert into message values('oc%[1]d-m%[2]d','oc%[1]d',%[3]d,%[3]d,'{"role":"user","time":{"created":%[3]d}}');
insert into part values('oc%[1]d-p%[2]d','oc%[1]d-m%[2]d','oc%[1]d',%[3]d,%[3]d,'{"type":"text","text":"why does the pool %[1]d step %[2]d hang"}');
update session set time_updated=%[3]d where id='oc%[1]d';
`, n, i, at)
	}
	grokMsg := func(n, i int) string {
		return fmt.Sprintf(`INSERT INTO messages VALUES ('g%[1]d',%[2]d,'user','{"content":"why does the queue %[1]d step %[2]d stall"}','2026-07-27T10:%02[3]d:00.000Z');
`, n, i, 10*i+n)
	}
	ocSQL := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
`
	grokSQL := `CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
`
	var grokSQLSb204 strings.Builder
	var ocSQLSb204 strings.Builder
	for i := range 6 {
		ocSQLSb204.WriteString(fmt.Sprintf("insert into session values('oc%d','/w/app','pool %d',%d,%d);\n", i, i, clock, clock) + ocMsg(i, 0) + ocMsg(i, 1))
		grokSQLSb204.WriteString(fmt.Sprintf("INSERT INTO sessions VALUES ('g%d','w','queue %d','/work/api','2026-07-27T10:00:00.000Z');\n", i, i) + grokMsg(i, 0) + grokMsg(i, 1))
	}
	grokSQL += grokSQLSb204.String()
	ocSQL += ocSQLSb204.String()
	paritySQL(t, oc, ocSQL)
	paritySQL(t, grok, grokSQL)
	touch := func(p string, s int) {
		at := time.Now().Add(time.Duration(s) * time.Minute)
		_ = os.Chtimes(p, at, at)
	}
	parityPass(t, inc, false)
	if !HasSession(inc, "oc3") || !HasSession(inc, "g3") {
		t.Fatal("the stores were not indexed, so this measures nothing")
	}

	walked := storedRecordsWalked.Load()
	shorten(5)
	paritySQL(t, oc, ocMsg(3, 2))
	paritySQL(t, grok, grokMsg(3, 2))
	touch(oc, 1)
	touch(grok, 1)
	parityPass(t, inc, false)
	walked = storedRecordsWalked.Load() - walked
	// The transcript's twelve records, and in each store the changed
	// session and the one at the watermark, which a since read takes again.
	if walked == 0 || walked > 12+2*(2+2) {
		t.Errorf("the pass walked %d stored records, want at most the changed sessions' 20 of %d", walked, n*12+24)
	}
	sameAsRebuild(t, inc)

	// A store change alone reads the session it changed.
	walked = storedRecordsWalked.Load()
	paritySQL(t, oc, ocMsg(1, 3))
	touch(oc, 2)
	parityPass(t, inc, false)
	if walked = storedRecordsWalked.Load() - walked; walked == 0 || walked > 2+3 {
		t.Errorf("an opencode message walked %d stored records, want at most the changed session's and the watermark's 5", walked)
	}
	sameAsRebuild(t, inc)
}

// A title the reader took from the store skipped the rule that a derived one
// has had since #790, so a harness whose title model answered with the reply
// left "ok" and "47" in every listing while the question sat one line below —
// 43 of the 60 sessions from three real stores on this machine (#3328).
func TestAThinSourceTitleGivesWayToTheQuestion(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "dshproj", ID: "28341f5a", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle about the checkout worker dropping connections", Time: at},
			{Role: "assistant", Text: "ok", Time: at.Add(time.Second)},
		},
	}
	// Cut to sixty runes like every derived title.
	if got := metaForSession(s).Title; got != "what did we settle about the checkout worker dropping connec\u2026" {
		t.Errorf("title = %q, want the person's question", got)
	}
}

// And it stands as it is when the session holds nothing better: the goose row
// titled "say ok" is honest, because "say ok" is the whole turn.
func TestAThinSourceTitleStaysWhenTheTurnIsThinToo(t *testing.T) {
	at := time.Date(2026, 7, 27, 18, 30, 0, 0, time.UTC)
	s := model.Session{
		Harness: "goose", Project: "deja-vu", ID: "20260727_2", Title: "say ok",
		Messages: []model.Message{
			{Role: "user", Text: "say ok", Time: at},
			{Role: "assistant", Text: "ok", Time: at.Add(time.Second)},
		},
	}
	if got := metaForSession(s).Title; got != "say ok" {
		t.Errorf("title = %q, want the name the store gave it", got)
	}
}

// A short title that names the work is not thin and is not touched.
func TestAShortSourceTitleThatNamesTheWorkIsKept(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "cline", Project: "api", ID: "c1", Title: "cap the retries",
		Messages: []model.Message{
			{Role: "user", Text: "the checkout worker drops connections under load, cap the retries", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "cap the retries" {
		t.Errorf("title = %q, want the store's own name", got)
	}
}

// A sentence in a script that does not space its words is not thin: eight
// runes of Chinese say why the test failed, and a rune-length rule alone
// replaced it with a longer, worse turn (review of #3328).
func TestACJKTitleThatSaysSomethingIsNotThin(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "opencode", Project: "api", ID: "cjk1", Title: "为什么测试失败了",
		Messages: []model.Message{
			{Role: "user", Text: "please look into this in more detail and explain step by step what happened", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "为什么测试失败了" {
		t.Errorf("title = %q, want the store's own name", got)
	}
	// A greeting in the same script still is thin.
	s.Title = "你好"
	s.ID = "cjk2"
	if got := metaForSession(s).Title; got == "你好" {
		t.Errorf("title = %q, want the turn under a two-character greeting", got)
	}
}

// Korean writes its words apart, so a one-word acknowledgement is thin the way
// "ok" is, and a sentence in it is not (review of #3328).
func TestKoreanIsCountedByItsWordsNotItsSyllables(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "opencode", Project: "api", ID: "ko1", Title: "고마워",
		Messages: []model.Message{
			{Role: "user", Text: "the checkout worker drops connections under load", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "the checkout worker drops connections under load" {
		t.Errorf("title = %q, want the question — a one-word thanks names nothing", got)
	}
	s.Title = "결제 워커가 연결을 끊는 이유"
	s.ID = "ko2"
	if got := metaForSession(s).Title; got != "결제 워커가 연결을 끊는 이유" {
		t.Errorf("title = %q, want the store's own name — it is a whole sentence", got)
	}
}

// The turn that takes over must be something a person said. A resume preamble
// is the harness's, and notAsked has rejected it for the repeat counter all
// along (review of #3328).
func TestAWidenedTitleIsNeverTheHarnessOwnPreamble(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "api", ID: "pre1", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "This session is being continued from a previous conversation that ran out of context. The conversation is summarized below:", Time: at},
			{Role: "user", Text: "cap the retries at three and log the last attempt", Time: at.Add(time.Minute)},
		},
	}
	got := metaForSession(s).Title
	if strings.Contains(got, "This session is being continued") {
		t.Errorf("title = %q, want the person's own turn", got)
	}
	if got != "cap the retries at three and log the last attempt" {
		t.Errorf("title = %q, want the sentence under the preamble", got)
	}
}

// The phrase a shell record uses is not a reason to reject a person's
// question: notAsked looks for "no visible output" anywhere in a turn, which
// is right for counting repeated plumbing and wrong for naming a session
// (second review of #3328).
func TestAQuestionThatSaysNoVisibleOutputCanStillNameTheSession(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "api", ID: "nvo1", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "why does the button have no visible output when clicked, please debug", Time: at},
		},
	}
	// Cut to sixty runes like every widened title.
	if got := metaForSession(s).Title; got != "why does the button have no visible output when clicked, ple\u2026" {
		t.Errorf("title = %q, want the question", got)
	}
}

// A title the source authored — a cursor composer name, a goose description, a
// zed thread summary — reached the one-line surfaces whole: 384 characters
// with the line breaks still in it, so one session printed several rows of
// `deja last` and the text after a break began "[claude · …", which is that
// listing's own format. Derived titles have been collapsed and bounded since
// they existed; this is the same bound on the other branch.
func TestSourceTitleIsBoundedLikeADerivedOne(t *testing.T) {
	msg := model.Message{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once, needs jitter"}
	for _, tc := range []struct {
		name  string
		title string
	}{
		{"multiline", "retry queue jitter\n[claude · fake · 2026-08-19 · deadbeef] a line in the listing's own format"},
		{"long", strings.Repeat("the retry queue stalls on staging and the workers wake together ", 6)},
		{"tabbed", "retry\tqueue\tjitter\tand\tbackoff"},
		{"carriage return", "retry queue jitter\r[claude · fake] rewound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := metaForSession(model.Session{
				Harness: "cursor", ID: "c1", Project: "p", Title: tc.title,
				Messages: []model.Message{msg},
			})
			if strings.ContainsAny(m.Title, "\n\r\t") {
				t.Errorf("the title still breaks the line: %q", m.Title)
			}
			// 61 is the ceiling, not the length: 60 characters plus the mark,
			// and TrimSpace can leave it shorter.
			if n := utf8.RuneCountInString(m.Title); n > 61 {
				t.Errorf("the title is %d characters: %q", n, m.Title)
			}
		})
	}
}

// What the source said still wins over what deja would derive, and a title
// that already fits arrives untouched — no mark claiming something was cut.
func TestSourceTitleShortEnoughIsUnchanged(t *testing.T) {
	const title = "retry queue jitter"
	m := metaForSession(model.Session{
		Harness: "cursor", ID: "c1", Project: "p", Title: title,
		Messages: []model.Message{{Role: "user", Text: "something else entirely"}},
	})
	if m.Title != title {
		t.Errorf("metaForSession rewrote a title that fits: %q", m.Title)
	}
	if m.AgentTitle {
		t.Error("a source title is not an agent title")
	}
}

// A derived title is unchanged by this: it was already collapsed and cut.
func TestDerivedTitleUnchanged(t *testing.T) {
	m := metaForSession(model.Session{
		Harness: "claude", ID: "d1", Project: "p",
		Messages: []model.Message{{Role: "user", Text: strings.Repeat("the retry queue stalls on staging ", 8)}},
	})
	if n := utf8.RuneCountInString(m.Title); n > 61 {
		t.Errorf("derived title is %d characters: %q", n, m.Title)
	}
	if !strings.HasSuffix(m.Title, "…") {
		t.Errorf("a cut derived title lost its mark: %q", m.Title)
	}
}

// A promoted note's title ends in its state, and the state is what the
// one-line surfaces read it for. The bound must not cut it off, and a state
// change must still be visible to whatever compares the two.
func TestPromotedNoteTitleKeepsItsState(t *testing.T) {
	long := strings.Repeat("the retry queue stalls on staging ", 3)
	for _, state := range []string{"accepted", "rejected", "superseded"} {
		title := strings.TrimSpace(long) + " [" + state + "]"
		m := metaForSession(model.Session{
			Harness: "deja", ID: "deja-2026-08-19-app", Project: "p", Title: title,
			Messages: []model.Message{{Role: "user", Text: "[" + state + "] the retry queue needs jitter"}},
		})
		if !strings.HasSuffix(m.Title, "["+state+"]") {
			t.Errorf("the state was cut off the note title: %q", m.Title)
		}
	}
	// And a note whose state changes has a different title, so the row is
	// rewritten rather than left saying the old one.
	one := metaForSession(model.Session{Harness: "deja", ID: "n", Title: strings.TrimSpace(long) + " [accepted]"})
	two := metaForSession(model.Session{Harness: "deja", ID: "n", Title: strings.TrimSpace(long) + " [rejected]"})
	if one.Title == two.Title {
		t.Errorf("two states produced one title: %q", one.Title)
	}
}

// An import writes the manifest, and until now it wrote the build time with
// it. Anything asking "is a store behind?" compares the newest file in that
// store against the build time, so on a machine that syncs on a timer — this
// one imports from a peer under launchd — every store read as freshly indexed
// while local transcripts sat unread. That is the surface half of #3747: ten
// transcripts the index had never read, and doctor saying nothing.
func TestAnImportDoesNotClaimTheStoresWereRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	session := `{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the pool hands out dead connections after a deploy"}}
{"type":"assistant","sessionId":"aaa1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the proxy closes idle backends at five minutes"}}
`
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	read := ManifestSourcesReadAt(dir)
	if read.IsZero() {
		t.Fatal("a build that walked the stores recorded no time for it")
	}
	builtBefore := ManifestBuiltAt(dir)

	// A peer's records, imported the way `deja sync` imports them.
	in := filepath.Join(tmp, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := SyncRecord{
		Harness: "claude", SessionID: "peer1", Project: "pool", Role: "user",
		Text: "the peer machine hit the same dead connection after its deploy", Time: time.Now(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "peer.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if n, err := Import(dir, in); err != nil || n == 0 {
		t.Fatalf("import wrote %d records: %v", n, err)
	}

	built := ManifestBuiltAt(dir)
	after := ManifestSourcesReadAt(dir)
	if !after.Equal(read) {
		t.Errorf("the import moved the sources-read time from %s to %s; it read no local transcript",
			read.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	}
	// And this is the mechanism, stated as an assertion: the import does move
	// the build time, which is what made every store look freshly read.
	if !built.After(builtBefore) {
		t.Errorf("the import left the build time at %s; the whole point is that it moves it",
			built.Format(time.RFC3339Nano))
	}

	// And a pass that does walk the stores moves it.
	if err := os.WriteFile(filepath.Join(root, "bbb2.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if again := ManifestSourcesReadAt(dir); !again.After(read) {
		t.Errorf("a pass that read the stores left the read time at %s", again.Format(time.RFC3339Nano))
	}
}

// An older store has no read time in it, and the answer for those is the build
// time rather than the zero value — which would report every store behind.
func TestAStoreWithoutTheFieldFallsBackToTheBuildTime(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"),
		[]byte(`{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"an older store, written before the field existed"}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.SourcesReadAt = time.Time{}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got, want := ManifestSourcesReadAt(dir), ManifestBuiltAt(dir); !got.Equal(want) {
		t.Errorf("a store with no read time answered %s; the build time is %s",
			got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// The bootstrap shape: an import on a machine that has never indexed writes a
// manifest with no files at all, so that the next pass ingests everything
// (#1307). Nothing local has been read there, and answering with the import's
// own clock is what made doctor call a store current when it had not been
// opened.
func TestAnIndexBuiltOnlyFromImportsHasReadNothing(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		Version:  version,
		Format:   onDiskFormat,
		Files:    map[string]FileState{},
		Sessions: map[string]SessionMeta{},
		BuiltAt:  time.Now(),
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := ManifestSourcesReadAt(dir); !got.IsZero() {
		t.Errorf("an import-built index says it read the stores at %s", got.Format(time.RFC3339Nano))
	}
	if built := ManifestBuiltAt(dir); built.IsZero() {
		t.Error("the same index has no build time, so the two are not being told apart")
	}
}

// writeSpanFixture writes the given records back to back and returns their
// offsets in write order, which is the order the scan sees them in.
func writeSpanFixture(t *testing.T, texts []string) (string, []int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tables := newRecordTables()
	offs := make([]int64, 0, len(texts))
	for i, s := range texts {
		off, err := writeRecord(f, Record{
			Key:        "k",
			SourcePath: "p",
			Role:       "user",
			Text:       s,
			Time:       time.Unix(int64(1700000000+i), 0).UTC(),
		}, tables)
		if err != nil {
			t.Fatal(err)
		}
		offs = append(offs, off)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path, offs
}

func collectSpan(t *testing.T, path string, offs []int64) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var got []string
	if err := eachRecordAt(f, offs, newRecordTables(), func(r Record) { got = append(got, r.Text) }); err != nil {
		t.Fatalf("eachRecordAt over valid data: %v", err)
	}
	return got
}

// TestEachRecordAtMatchesIndividualReads is the property that matters: reading
// a run of records in coalesced spans must return exactly what reading them one
// at a time returns. The mix is deliberate — small records that share a span, a
// record far larger than the read-ahead window, and one pushed past spanGapMax
// so a new span has to start.
func TestEachRecordAtMatchesIndividualReads(t *testing.T) {
	texts := []string{
		"short one",
		"short two",
		strings.Repeat("a long record that cannot fit in the speculative window ", 4000),
		"after the long one",
		strings.Repeat("filler to push the next record past the gap limit ", 1200),
		"in a second span",
	}
	path, offs := writeSpanFixture(t, texts)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(offs))
	for _, off := range offs {
		r, err := readRecordAt(f, off, newRecordTables())
		if err != nil {
			t.Fatalf("readRecordAt(%d): %v", off, err)
		}
		want = append(want, r.Text)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got := collectSpan(t, path, offs)
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d differs: got %d bytes, want %d", i, len(got[i]), len(want[i]))
		}
	}
}

// TestEachRecordAtReportsAnUnreadableFile checks the part that would fail
// quietly: a file that cannot be read must surface the IO error, not hand back a
// clean-looking empty result. A closed file is a real IO error, not corruption,
// so it must propagate as itself and not be mistaken for a store to rebuild.
func TestEachRecordAtReportsAnUnreadableFile(t *testing.T) {
	path, offs := writeSpanFixture(t, []string{"one", "two", "three"})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	n := 0
	err = eachRecordAt(f, offs, newRecordTables(), func(Record) { n++ })
	if err == nil {
		t.Fatal("a closed file was read as an empty result")
	}
	if IsCorrupt(err) {
		t.Fatalf("a closed file was misreported as index corruption: %v", err)
	}
	if n != 0 {
		t.Fatalf("closed file yielded %d records", n)
	}
}

// TestDecodeRecordInRejectsOutOfRange covers the bounds the span decoder is
// responsible for; each of these has to fall back to a single read rather than
// decode whatever bytes happen to follow.
func TestDecodeRecordInRejectsOutOfRange(t *testing.T) {
	b := []byte{4, 0, 0, 0, 1, 2}
	for _, rel := range []int{-1, len(b), len(b) - 2} {
		if _, ok := decodeRecordIn(b, rel, newRecordTables()); ok {
			t.Fatalf("rel=%d decoded out of range", rel)
		}
	}
	huge := []byte{255, 255, 255, 255}
	if _, ok := decodeRecordIn(huge, 0, newRecordTables()); ok {
		t.Fatal("oversized length decoded")
	}
}

// TestReadRecordAtCorruptHeader covers the two ways a header can be wrong: a
// file that ends inside it, and a length that claims more than any record is
// allowed to be. Both are index corruption, and both have to be reported
// rather than turned into a decode of arbitrary bytes.
func TestReadRecordAtCorruptHeader(t *testing.T) {
	dir := t.TempDir()

	short := filepath.Join(dir, "short.bin")
	if err := os.WriteFile(short, []byte{1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(short)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readRecordAt(f, 0, newRecordTables()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial header err=%v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	huge := filepath.Join(dir, "huge.bin")
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr, ^uint32(0))
	if err = os.WriteFile(huge, hdr, 0o600); err != nil {
		t.Fatal(err)
	}
	f2, err := os.Open(huge)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f2.Close() }()
	if _, err := readRecordAt(f2, 0, newRecordTables()); !errors.Is(err, errCorruptIndex) {
		t.Fatalf("oversized length err=%v", err)
	}
}

// spillJobs is a corpus with a shared vocabulary, sessions that come back out
// of order, tool records, and more messages than one worker batch.
func spillJobs() []tokenJob {
	jobs := make([]tokenJob, 0, 5000)
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := range 5000 {
		sid := uint32(i/7 + 1)
		if i%11 == 0 {
			sid = uint32(3) // a session that steps back
		}
		text := fmt.Sprintf("the build failed on runSpillWorker step %d with error code %d 日本語のテスト", i%97, i%13)
		jobs = append(jobs, tokenJob{text: text, offset: off, sid: sid, when: when.Add(time.Duration(i) * time.Hour), tool: i%3 == 0})
		off += int64(len(text) + 40)
	}
	return jobs
}

func spillDirFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

// distinctJobs are messages of words that never repeat, enough of them that a
// worker has to spill before its batch ends.
func distinctJobs() []tokenJob {
	jobs := make([]tokenJob, 0, 300)
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := range 300 {
		var b strings.Builder
		for w := range 1000 {
			fmt.Fprintf(&b, "w%dx%d ", i, w)
		}
		jobs = append(jobs, tokenJob{text: b.String(), offset: off, sid: uint32(i + 1), when: when})
		off += int64(b.Len())
	}
	return jobs
}

// The two-pass build must write the buckets the in-memory path writes, byte for
// byte, whatever the spill file looks like in between.
func TestSpilledBucketsMatchTheInMemoryOnes(t *testing.T) {
	t.Run("shared vocabulary", func(t *testing.T) { checkSpillMatchesMemory(t, spillJobs()) })
	t.Run("early spill", func(t *testing.T) { checkSpillMatchesMemory(t, distinctJobs()) })
}

func checkSpillMatchesMemory(t *testing.T, jobs []tokenJob) {
	t.Helper()
	mem := bucketPostings{}
	for _, j := range jobs {
		addIndexKeys(mem, j.text, j.offset, j.sid, j.when, j.tool)
	}
	memDir := filepath.Join(t.TempDir(), "mem")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeBucketsConcurrent(memDir, mem); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	sp, err := newSpiller(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := sp.bucketCount(), len(mem); got != want {
		t.Fatalf("bucketCount = %d, want %d", got, want)
	}
	spDir := filepath.Join(tmp, "buckets")
	if err := os.MkdirAll(spDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sp.writeBuckets(spDir); err != nil {
		t.Fatal(err)
	}

	want, got := spillDirFiles(t, memDir), spillDirFiles(t, spDir)
	if len(got) != len(want) {
		t.Fatalf("%d bucket files, want %d", len(got), len(want))
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !bytes.Equal(got[n], want[n]) {
			t.Errorf("bucket %s differs from the in-memory build", n)
		}
	}
}

// A posting used to carry its whole token and a full offset into the spill:
// 2.4 GB for a 12k-session store, and the workers spent the phase in write(2)
// (#5526). A token is written once per batch now.
func TestSpillWritesATokenOncePerBatch(t *testing.T) {
	jobs := spillJobs()
	postings, tokBytes := 0, 0
	for _, j := range jobs {
		eachIndexKey(j.text, j.when, func(tok string) {
			postings++
			tokBytes += len(tok)
		})
	}
	tmp := t.TempDir()
	sp, err := newSpiller(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.flushWriters(); err != nil {
		t.Fatal(err)
	}
	spilled := 0
	for _, b := range spillDirFiles(t, sp.dir) {
		spilled += len(b)
	}
	// Less than the token bytes alone, which every posting used to repeat.
	if spilled >= tokBytes {
		t.Fatalf("spilled %d bytes for %d postings (%d bytes of tokens): tokens are written per posting", spilled, postings, tokBytes)
	}
}

// A sqlite3 on PATH that prints nothing read the opencode store as empty and
// the index pass said nothing about it. It is now skipped by name, is not
// recorded as a tool the index had, and repairing it counts as gaining one.
func TestIndexNamesABrokenSQLite3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	db := filepath.Join(home, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "sqlite3")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)

	var out bytes.Buffer
	loadProgress("opencode", &out)
	want := "deja: opencode: skipped — sqlite3 at " + stub + " did not answer a probe query (no output)"
	if !strings.Contains(out.String(), want) {
		t.Errorf("index narration:\n%s\nwant %q", out.String(), want)
	}

	if fp := mergedToolFingerprint(""); !strings.Contains(fp, "sqlite3=false") {
		t.Errorf("broken sqlite3 recorded as present: %s", fp)
	}
	if toolsChanged(Manifest{ToolFingerprint: toolFingerprint(false, true)}) {
		t.Error("a broken sqlite3 read as a tool newly gained")
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"deja\":1}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sources.ResetSQLite3Probe()
	if !toolsChanged(Manifest{ToolFingerprint: toolFingerprint(false, true)}) {
		t.Error("a repaired sqlite3 did not ask for a pass")
	}
}

// The early slice of a first build records how many sessions the build found,
// so an answer from it can say the oldest are what is missing (#5390).
func TestTheEarlySliceKnowsWhatItIsASliceOf(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	ss := make([]model.Session, 0, 450)
	for i := range 450 {
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("session %d", i)}},
		})
	}
	publishNewestFirst(dir, ss, io.Discard)
	c := SnapshotCoverage(dir)
	if c.Sessions != partialPublishSessions || c.PartialOf != 450 {
		t.Errorf("the slice reads as %d sessions of %d, want %d of 450", c.Sessions, c.PartialOf, partialPublishSessions)
	}

	// A complete index is not a slice of anything.
	full := filepath.Join(tmp, "full")
	if err := os.MkdirAll(filepath.Join(full+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(full+".tmp", full, ss, map[string]FileState{"/x.jsonl": {}}, ""); err != nil {
		t.Fatal(err)
	}
	if c := SnapshotCoverage(full); c.PartialOf != 0 || c.Sessions != 450 {
		t.Errorf("a complete index reads as %d sessions of %d", c.Sessions, c.PartialOf)
	}
}

// A refresh handed to the warmup reports how many transcripts it left unread
// and how many the walk found.
func TestAHandedOffRefreshCountsWhatIsPending(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(i int, text string) {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", i, text)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		write(i, "a short turn")
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	inlineAppendMax = 1 << 10
	t.Cleanup(func() { inlineAppendMax = 8 << 20 })
	for i := 3; i < 5; i++ {
		write(i, strings.Repeat("tool output ", 200))
	}
	c, err := EnsureForSearchCoverage(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Stale || c.Pending != 2 || c.Found != 5 || c.Sessions != 3 || c.At.IsZero() {
		t.Errorf("coverage %+v, want stale, 2 of 5 pending, 3 sessions, a date", c)
	}
}

// EnsureForSearchStale decides whether a hook can answer from the index it has
// while a rebuild happens behind it. Getting it wrong either blocks a session
// start on a full rebuild or serves an index that is missing the answer.
func TestEnsureForSearchStaleDecidesWhatToServe(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	path := filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"))

	// No index yet: nothing sensible to serve stale, so it must build.
	stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("an absent index was reported as servable")
	}
	if !HasManifest(dir) {
		t.Fatal("no index was built")
	}

	// Nothing changed: no work, nothing stale.
	if stale, err = EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || stale {
		t.Fatalf("unchanged store: stale=%v err=%v", stale, err)
	}

	// A plain append is cheap enough to absorb synchronously.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	if stale, err = EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || stale {
		t.Fatalf("append should be absorbed in place: stale=%v err=%v", stale, err)
	}

	// A removed file cannot be absorbed by appending: the caller has to
	// rebuild, and until it does the current snapshot is what gets served.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stale, err = EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("a removed transcript did not ask for a rebuild")
	}
}

// A rewound file must not take the append path here either, or the hook
// serves an index holding text the transcript no longer has.
func TestEnsureForSearchStaleRefusesToAppendOntoARewind(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	path := filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "ORIGINAL"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeLines(t, path,
		claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN"),
		claudeLine("s1", "2026-01-01T00:02:00Z", "second"),
		claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("a rewritten prefix was absorbed as an append; the old text would stay indexed")
	}
}

// RecentProjects is how the session-start digest picks what to show: the most
// recent sessions of the projects the working tree points at. Matching is by
// substring because project names arrive in different shapes per harness, and
// a session must never be listed twice when two names both match it.
func TestRecentProjectsRanksAndDeduplicates(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	for _, tc := range []struct{ project, id, ts, text string }{
		{"alpha", "a1", "2026-01-01T00:01:00Z", "oldest alpha"},
		{"alpha", "a2", "2026-03-01T00:01:00Z", "newest alpha"},
		{"beta", "b1", "2026-02-01T00:01:00Z", "only beta"},
	} {
		writeLines(t, filepath.Join(claude, tc.project, tc.id+".jsonl"), claudeLine(tc.id, tc.ts, tc.text))
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	// Newest first, and the per-name cap applies to each name separately.
	got, err := RecentProjects(dir, []string{"alpha"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("got %+v, want only the newest alpha session", ids(got))
	}

	// Two names that both match the same session must not list it twice —
	// the digest would spend its budget saying the same thing.
	both, err := RecentProjects(dir, []string{"alpha", "alph"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range both {
		if seen[s.ID] {
			t.Fatalf("session %s listed twice: %v", s.ID, ids(both))
		}
		seen[s.ID] = true
	}
	if len(both) != 2 {
		t.Fatalf("got %v, want both alpha sessions once each", ids(both))
	}

	// An unknown project yields nothing rather than everything, or a fresh
	// checkout would recall the whole machine.
	if got, err := RecentProjects(dir, []string{"gamma"}, 5); err != nil || len(got) != 0 {
		t.Fatalf("unknown project returned %v (%v)", ids(got), err)
	}
}

func ids(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// RecentProjectsUnder adds the sessions whose work happened inside the
// caller's checkout, whatever project name they were recorded under, and only
// when the caller stands in a checkout: an ordinary directory that happens to
// hold other people's projects speaks for none of them (#2040).
func TestRecentProjectsUnderNeedsACheckout(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	repo := filepath.Join(home, "src", "app")
	sub := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	touched := filepath.Join(sub, "pool.go")
	writeLines(t, filepath.Join(claude, "tool", "t1.jsonl"),
		claudeLineAt("t1", "2026-03-01T00:01:00Z", "the pool ran dry", sub),
		claudeEditAt("t1", "2026-03-01T00:02:00Z", touched, sub))
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	// The name the session was recorded under does not reach the root.
	if got, err := RecentProjects(dir, []string{"app"}, 5); err != nil || len(got) != 0 {
		t.Fatalf("RecentProjects(app) = %v, %v; want nothing", ids(got), err)
	}
	// Standing in the checkout, the work under it comes back.
	got, err := RecentProjectsUnder(dir, []string{"app"}, repo, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("got %v, want the subdirectory session", ids(got))
	}
	// A directory that is not a checkout speaks for nothing under it, and
	// neither does the home directory itself.
	for _, root := range []string{filepath.Join(home, "src"), home, "/", ".", "", "  "} {
		got, err := RecentProjectsUnder(dir, []string{"app"}, root, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("root %q recalled %v; want nothing", root, ids(got))
		}
	}
}

// claudeLineAt and claudeEditAt are claudeLine with a working directory and a
// file edit: what a session touched is what RecentProjectsUnder reads.
func claudeLineAt(sid, ts, text, cwd string) string {
	return `{"type":"user","sessionId":"` + sid + `","timestamp":"` + ts + `","cwd":` + jsonStr(cwd) +
		`,"message":{"role":"user","content":"` + text + `"}}` + "\n"
}

func claudeEditAt(sid, ts, path, cwd string) string {
	return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + ts + `","cwd":` + jsonStr(cwd) +
		`,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"e1","input":{"file_path":` +
		jsonStr(path) + `,"old_string":"a","new_string":"b"}}]}}` + "\n"
}

func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// stemMatches keeps eight forms, in the order they were built: the English
// suffix forms first, the developer synonyms, then the Cyrillic ones. The
// fullest thing a store can hold is a Russian paradigm, and that is seven
// forms — one slot under the cap, with the single-letter endings that carry
// the lemma last in the order, so the lemma is what a tighter cap would cut.
// Nothing said so, and for a Russian query those forms are the whole answer.
func TestAFullRussianParadigmFitsUnderTheFormCap(t *testing.T) {
	for _, tc := range []struct {
		term  string
		lemma string
		forms []string
	}{
		{"настройки", "настройка", []string{
			"настройка", "настройки", "настройке", "настройку", "настройкой", "настройками", "настройках"}},
		{"миграции", "миграция", []string{
			"миграция", "миграции", "миграцию", "миграцией", "миграциях", "миграциями", "миграций"}},
		{"обработчика", "обработчик", []string{
			"обработчик", "обработчика", "обработчику", "обработчиком", "обработчике", "обработчики", "обработчиков"}},
	} {
		// A catalog like a real one: what a transcript would contain, and none
		// of the stubs the expander invents — indexKeys stores tokens, never
		// expansions, so "настройкиed" is in no catalog anywhere.
		catalog := map[string]bool{}
		for _, w := range tc.forms {
			catalog[w] = true
		}
		got := stemMatches(tc.term, catalog)
		if len(got) != len(tc.forms) {
			t.Errorf("stemMatches(%q) kept %d of %d forms: %q", tc.term, len(got), len(tc.forms), got)
		}
		// The lemma above all: it is what the reader would search next, and it
		// is last in the build order.
		if !slices.Contains(got, tc.lemma) {
			t.Errorf("stemMatches(%q) does not reach the lemma %q: %q", tc.term, tc.lemma, got)
		}
	}
}

// The line for a store given up says how long it sat and how to wait instead
// (#5517).
func TestStallNarration(t *testing.T) {
	for _, c := range []struct {
		idle []time.Duration
		want string
	}{
		{[]time.Duration{10 * time.Minute}, "no progress for 10m0s; DEJA_STORE_TIMEOUT=0 waits"},
		{[]time.Duration{0}, "a read in the same directory stopped moving; DEJA_STORE_TIMEOUT=0 waits"},
		{[]time.Duration{time.Minute, 2 * time.Minute}, "2 reads given up, no progress for 2m0s; DEJA_STORE_TIMEOUT=0 waits"},
	} {
		if got := stallNarration(c.idle); got != c.want {
			t.Errorf("stallNarration(%v) = %q, want %q", c.idle, got, c.want)
		}
	}
}

// Nothing unread, or no index yet, carries nothing.
func TestUnreadStoresCarryWithNothingToCarry(t *testing.T) {
	dir := t.TempDir()
	if got := unreadStoresCarry(dir, "", nil, nil); got != nil {
		t.Errorf("no unread stores carried %d sessions", len(got))
	}
	if got := unreadStoresCarry(filepath.Join(dir, "none"), "", map[string]bool{"/x/state.db": true}, nil); got != nil {
		t.Errorf("no index carried %d sessions", len(got))
	}
}

// A store that read part of what it holds and gave up on the rest says both.
func TestAStoreWithSessionsAndAStalledReadSaysBoth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	setHome(t, home)
	hermes := filepath.Join(home, ".hermes")
	t.Setenv("DEJA_HERMES_HOME", hermes)
	t.Setenv("DEJA_HERMES_DB", "")
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	for _, db := range []string{filepath.Join(hermes, "state.db"), filepath.Join(hermes, "profiles", "b", "state.db")} {
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The profile store stalls, the root one answers.
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"json_object('deja',1)"*) echo '{"deja":1}'; exit 0;;
*"from messages"*"order by session_id"*) ;;
*) exit 0;;
esac
case "$*" in
*profiles*) exec sleep 30;;
esac
printf '{"session_id":"h1","role":"user","content":"partneedle","timestamp":1790000001}\n'
`
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)
	sources.DiagSnapshot()
	t.Cleanup(func() { sources.DiagSnapshot() })

	var out bytes.Buffer
	ss := loadProgress("hermes", &out)
	if len(ss) != 1 {
		t.Fatalf("read %d sessions, want the root store's one", len(ss))
	}
	want := "deja: hermes: 1 session, 1 message — 1 path could not be read at all — no progress for 300ms; DEJA_STORE_TIMEOUT=0 waits"
	if !strings.Contains(out.String(), want) {
		t.Errorf("narration:\n%s\nwant %q", out.String(), want)
	}

	// Built, then asked what to carry for the root store as if unread.
	if err := os.Remove(filepath.Join(hermes, "profiles", "b", "state.db")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_STORE_TIMEOUT", "5s")
	root := filepath.Join(hermes, "state.db")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "hermes", true, nil); err != nil {
		t.Fatal(err)
	}
	unread := map[string]bool{root: true}
	if got := unreadStoresCarry(dir, "hermes", unread, nil); len(got) != 1 || got[0].ID != "h1" {
		t.Errorf("carried %v, want h1", got)
	}
	if got := unreadStoresCarry(dir, "hermes", unread, []model.Session{{Harness: "hermes", ID: "h1"}}); len(got) != 0 {
		t.Errorf("carried %d sessions this pass already read", len(got))
	}
	if got := unreadStoresCarry(dir, "claude", unread, nil); len(got) != 0 {
		t.Errorf("a claude pass carried %d hermes sessions", len(got))
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if got := unreadStoresCarry(dir, "", unread, nil); len(got) != 0 {
		t.Errorf("a store gone since the walk carried %d sessions", len(got))
	}
}

// A Kimi or Qwen sub-agent's id was its parent's with the agent's name after
// it, so the parent's whole id was a prefix of every child's, and a prefix
// opens the newest session it matches: `deja resume q-1` reached a child
// (#4483).
func TestAWholeIDOpensThatSessionNotANewerOneItPrefixes(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	project := filepath.Join(root, "projects", "-tmp-proj")
	write(t, filepath.Join(project, "chats", "q-1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"parent question"}]}}`+"\n")
	write(t, filepath.Join(project, "subagents", "q-1", "agent-a1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:05:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"trace the backoff jitter"}]}}`+"\n")
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := FindByPrefix(dir, "q-1")
	if err != nil || !ok {
		t.Fatalf("q-1 resolved to nothing: %v", err)
	}
	if s.ID != "q-1" {
		t.Errorf("the whole id q-1 opened %s", s.ID)
	}
	if n := PrefixMatches(dir, "q-1"); n != 1 {
		t.Errorf("q-1 counts %d matches, want the one it names", n)
	}
	// Control: the child is still reached by its own id.
	if s, ok, _ := FindByPrefix(dir, "agent-a1"); !ok || s.Parent != "q-1" {
		t.Errorf("agent-a1 opened %+v", s)
	}
}

// English doubles a final consonant before -ing and -ed, and the ladder
// only stripped the suffix: "logging" reached "logg" and stopped, so recall
// asked about "structured logging" missed the session that says "log" and
// "logs" (#4785).
func TestSuffixFormsUndoTheDoubledConsonant(t *testing.T) {
	for _, tc := range []struct {
		word string
		want []string
	}{
		{"logging", []string{"log"}},
		{"logged", []string{"log"}},
		{"pinned", []string{"pin"}},
		{"committing", []string{"commit"}},
		{"commit", []string{"committing", "committed"}},
	} {
		got := suffixForms(tc.word)
		for _, want := range tc.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q did not reach %q: %v", tc.word, want, got)
			}
		}
	}
	// A short word goes through the plural-only branch; the doubled forms
	// are the inflections it has.
	catalog := map[string]bool{"logging": true, "logged": true, "logs": true}
	if got := stemMatches("log", catalog); !slices.Contains(got, "logging") || !slices.Contains(got, "logged") {
		t.Errorf("log did not reach logging and logged: %v", got)
	}
}

// Only a doubled consonant before -ing or -ed is undone. Before -er the
// spelling cannot tell an agent noun from a word of its own: letter is not
// let's. "falling" keeps "fall", and a vowel ending doubles nothing.
func TestSuffixFormsLeaveUndoubledWordsAlone(t *testing.T) {
	for w, bad := range map[string]string{"letter": "let", "matter": "mat", "summer": "sum", "butter": "but", "dinner": "din"} {
		if slices.Contains(suffixForms(w), bad) {
			t.Errorf("%s reached %s: %v", w, bad, suffixForms(w))
		}
	}
	for _, w := range []string{"let", "sum", "bug"} {
		if slices.Contains(doubledForms(w), w+w[len(w)-1:]+"er") {
			t.Errorf("%s expanded to an -er form: %v", w, doubledForms(w))
		}
	}
	if slices.Contains(suffixForms("user"), "us") {
		t.Errorf("user reached us: %v", suffixForms("user"))
	}
	if !slices.Contains(suffixForms("falling"), "fall") {
		t.Errorf("falling lost fall: %v", suffixForms("falling"))
	}
	for _, w := range []string{"see", "go", "show", "fix", "play"} {
		if f := doubledForms(w); f != nil {
			t.Errorf("doubledForms(%q) = %v, want none", w, f)
		}
	}
}

// The word-form ladder had no y <-> ies/ied step, so a consonant+y word and
// its plural could never reach each other: `deja "retry"` answered "no matches
// in 20 indexed sessions" over a store whose transcripts all say "retries"
// (#1079).
func TestSuffixFormsCrossTheYIesBoundary(t *testing.T) {
	for _, tc := range []struct {
		word string
		want []string
	}{
		{"retry", []string{"retries", "retried"}},
		{"query", []string{"queries", "queried"}},
		{"proxy", []string{"proxies", "proxied"}},
		{"retries", []string{"retry"}},
		{"queries", []string{"query"}},
		{"proxies", []string{"proxy"}},
		{"queried", []string{"query"}},
	} {
		got := suffixForms(tc.word)
		for _, want := range tc.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q did not expand to %q: %v", tc.word, want, got)
			}
		}
	}
}

// A word ending in vowel+y takes a plain -s. Turning "key" into "kies" would
// spend variant slots on forms no transcript contains.
func TestSuffixFormsLeaveVowelYAlone(t *testing.T) {
	for _, word := range []string{"key", "day", "play", "boy"} {
		for _, bad := range []string{word[:len(word)-1] + "ies", word[:len(word)-1] + "ied"} {
			if slices.Contains(suffixForms(word), bad) {
				t.Errorf("%q expanded to %q; vowel+y pluralises with -s", word, bad)
			}
		}
		if !slices.Contains(suffixForms(word), word+"s") {
			t.Errorf("%q lost its plain plural", word)
		}
	}
}

func TestConsonantY(t *testing.T) {
	for _, w := range []string{"retry", "query", "proxy", "city"} {
		if base, ok := consonantY(w); !ok || base != w[:len(w)-1] {
			t.Errorf("consonantY(%q) = %q, %v", w, base, ok)
		}
	}
	for _, w := range []string{"key", "day", "boy", "guy", "y", "by", "index"} {
		if base, ok := consonantY(w); ok {
			t.Errorf("consonantY(%q) = %q, true — want false", w, base)
		}
	}
}

// A compaction summary names every topic the session touched. Served to an
// ordinary question it carried a marathon into answers about things the session
// never discussed: on 453 real recall questions, 231 first-hit quotes came from
// summaries alone. Asked for by name it is still there (#3384).
func TestTheCompactionSummaryIsServedOnlyWhenAskedFor(t *testing.T) {
	if recordServable(roleSummary, query.Options{}) {
		t.Error("an ordinary query is served a compaction summary")
	}
	if !recordServable(roleSummary, query.Options{Role: roleSummary}) {
		t.Error("--role summary stopped serving summaries")
	}
	if !recordServable("user", query.Options{}) {
		t.Error("an ordinary query lost the person's words")
	}
}

// A failing test is the most common thing an agent hits in a Go repo — 2,318
// of the 6,744 error lines on the store this was measured against — and it was
// rejected by name, listed with the shapes that identify nothing. The test name
// identifies plenty: it is stable across runs and across machines, which is
// more than most error strings manage.
func TestANamedTestFailureIsFriction(t *testing.T) {
	line, ok := FrictionLine("--- FAIL: TestGrokSinceHandlesEveryStampShape (0.09s)")
	if !ok {
		t.Fatal("a failing test is not read as friction")
	}
	// The duration is this run's, not the failure's. Left in, every run is its
	// own piece of friction and none of them ever reaches a second session.
	if line != "--- FAIL: TestGrokSinceHandlesEveryStampShape" {
		t.Errorf("the run's duration stayed in the line: %q", line)
	}
	other, _ := FrictionLine("    --- FAIL: TestGrokSinceHandlesEveryStampShape (12.40s)")
	if other != line {
		t.Errorf("two runs of the same failing test do not compare equal: %q vs %q", other, line)
	}
}

// The summary lines name nothing, which is why they were rejected in the first
// place, and they stay rejected.
func TestTheBareFailSummariesAreStillNotFriction(t *testing.T) {
	for _, l := range []string{
		"FAIL",
		"FAIL\tgithub.com/vshulcz/deja-vu/internal/index\t20.4s",
		"--- FAIL",
		`t.Fatalf("--- FAIL: %s", name)`,
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line that identifies nothing was read as friction: %q", l)
		}
	}
}

// The classifier decides whether a command could have been the remedy, and
// several git subcommands are a read or a write depending on the argument.
func TestGitSubcommandsThatDependOnTheirArgument(t *testing.T) {
	for _, cmd := range []string{
		"git stash list | head -3",
		"git worktree list",
		"git tag -l",
		"git remote -v",
	} {
		if !InspectionCommand(cmd) {
			t.Errorf("a read was classified as a change: %q", cmd)
		}
	}
	// Going to look at CI is the miner's question, not the hook's: it is where
	// an agent goes after a failure, not what it did about it.
	if !investigationCommand("until gh pr checks 2059; do sleep 20; done") {
		t.Error("a poll loop around a read was classified as a remedy")
	}
	for _, cmd := range []string{
		"git stash push -u -m wip",
		"git worktree add /tmp/wt main",
		"git tag v1.2.3",
		"git remote add origin git@github.com:x/y",
	} {
		if InspectionCommand(cmd) {
			t.Errorf("a change was classified as a read: %q", cmd)
		}
	}
}

// cjkIndexKeys is what keyer.cjkKeys emits, as strings.
func cjkIndexKeys(s string, emit func(tok string)) {
	var k keyer
	k.cjkKeys(s, func(b []byte) { emit(string(b)) })
}

func load(h string) []model.Session { return loadProgress(h, nil) }

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{compactions: compactionsForRebuild(dir, readTombstones())})
}

// textKeys emits every key the text contributes, repeats included.
func textKeys(text string, emit func(tok string)) {
	var k keyer
	k.textKeys(text, func(b []byte) { emit(string(b)) })
}

// topTouchedFiles returns the files a session worked on most, busiest first.
func topTouchedFiles(ms []model.Message) []string {
	paths, _ := topTouchedCounted(ms)
	return paths
}

func sameProject(recorded, cwd string) bool { return projectDistance(recorded, cwd) >= 0 }

func stemPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	return stemPostingsFor(dir, terms, phrases, 0)
}

func intersectSubstringPostings(dir string, bare []string) ([]posting, error) {
	posts, _, err := intersectSubstringPostingsDetailed(dir, bare)
	return posts, err
}

// indexKeys collects what textKeys emits, in order and with its repeats. The
// build path streams the keys instead; this is what the tests read, so they
// read the same emitter the build does.
//
// Bigram keys fold Traditional to Simplified so the same word written either
// way lands on one key and a query in one script reaches content in the other.
// Folding only the key keeps the stored text untouched. The emitter folds runs
// in place and dedupes folded pairs — cheaper than folding each emitted
// bigram, and every caller collapses repeated keys anyway (#492).
func indexKeys(s string) []string {
	var out []string
	textKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

// identifierParts is what keyer.identifierParts emits, as words.
func identifierParts(s string) []string {
	var out []string
	var k keyer
	k.identifierParts(s, func(b []byte) { out = append(out, string(b[1:])) })
	return out
}

func splitCompound(word []rune, out *[]string) {
	var k keyer
	k.splitCompound(word, func(b []byte) { *out = append(*out, string(b[1:])) })
}

func recordMatchesQuery(r Record, o query.Options) bool {
	return recordMatchesQueryVariants(r, o, nil)
}

func recordMatchesQueryVariants(r Record, o query.Options, variants map[string][]string) bool {
	return newRecordMatcher(o, variants).matches(r)
}

func sessionTitle(s model.Session) string {
	t, _ := sessionTitleFrom(s)
	return t
}

func writeRecord(f *os.File, r Record, t *recordTables) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b := encodeRecord(r, t)
	if len(b) > 1<<31 {
		return 0, errors.New("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // a record is far below 4 GiB
	if _, err = f.Write(hdr[:]); err != nil {
		return 0, err
	}
	_, err = f.Write(b)
	return off, err
}

// Search returns the sessions in the index at dir that answer o.
func Search(dir string, o query.Options) ([]model.Session, error) {
	r, err := SearchDetailed(dir, o)
	return r.Sessions, err
}

func CommandSettled(dir, cmd string, projects []string, allow func(string) bool) string {
	return CommandSettledOutside(dir, cmd, projects, allow, "")
}

func CommandSettledOutside(dir, cmd string, projects []string, allow func(string) bool, self string) string {
	return CommandSettledUsableOutside(dir, cmd, projects, allow, nil, self)
}

func Lineage(dir string, ids map[string]bool, heads ...model.Session) map[string]bool {
	out, _ := lineage(dir, ids, heads, nil)
	return out
}

func LineageWhole(dir string, ids map[string]bool, wholes ...model.Session) map[string]bool {
	out, _ := lineage(dir, ids, nil, wholes)
	return out
}

func OlderFormat(dir string) bool { return FormatDirection(dir) != 0 }

func RecentProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	project = strings.ToLower(project)
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		p := strings.ToLower(meta.Project)
		if p == project || (project != "" && strings.Contains(p, project)) {
			metas = append(metas, meta)
		}
	}
	metas = metasNotIgnored(metas)
	sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

func RecentProjects(dir string, projects []string, perName int) ([]model.Session, error) {
	return RecentProjectsUnder(dir, projects, "", perName)
}

func FirstMatch(dir string, queries []string, limit int) ([]model.Session, string, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, "", err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, "", fmt.Errorf("manifest: %w", err)
	}
	if !recordsReadable(dir, m) {
		return nil, "", fmt.Errorf("%w: records.bin size does not match the manifest (crash-truncated or uncommitted tail)", errCorruptIndex)
	}
	for _, q := range queries {
		keys := queryKeys(q)
		if len(keys) == 0 {
			continue
		}
		posts, err := intersectPostings(dir, retrievalKeys(keys))
		if err != nil {
			return nil, "", fmt.Errorf("postings: %w", err)
		}
		o := query.Options{Query: q}
		posts = cutPostingsBySession(posts, m, o)
		if len(posts) == 0 {
			continue
		}
		ss, err := scanRecords(dir, m, o, postingOffsets(posts))
		if err != nil {
			return nil, "", fmt.Errorf("records: %w", err)
		}
		if len(ss) == 0 {
			continue
		}
		if len(ss) > limit {
			ss = ss[:limit]
		}
		return ss, q, nil
	}
	return nil, "", nil
}

func SpanInventory(dir string) (spans, files int, err error) {
	return SpanInventoryWhere(dir, nil)
}

// Every shape here was taken from a real output record on a 2,700-session
// store, including the three that used to be read the wrong way round.
func TestARunnersVerdictIsReadFromItsOwnLine(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want TestVerdict
	}{
		{"go pass", "ok  \tgithub.com/me/app/internal/fetch\t1.2s", TestPassed},
		{"go cached", "ok  \tgithub.com/me/app\t(cached)", TestPassed},
		{"go fail", "--- FAIL: TestRetry (0.01s)\nFAIL\tgithub.com/me/app\t0.9s\nFAIL", TestFailed},
		{"build failed", "FAIL\tgithub.com/me/app [build failed]\nFAIL", TestFailed},
		{"vet", "# github.com/me/app\nvet: internal/x/x.go:7:2: \"fmt\" imported and not used", TestFailed},
		{"compile error under an echo", "ok\n# github.com/me/app\ninternal/x/x.go:12:3: undefined: q", TestFailed},
		{"no test files", "?   \tgithub.com/me/app/cmd\t[no test files]", TestPassed},
		{"pytest pass", "===== 41 passed in 2.10s =====", TestPassed},
		{"pytest fail", "===== 2 failed, 39 passed in 2.40s =====", TestFailed},
		{"jest fail", "Tests:       3 failed, 18 passed, 21 total", TestFailed},
		{"cargo fail", "error[E0308]: mismatched types", TestFailed},
		{"cargo pass", "test result: ok. 12 passed; 0 failed", TestPassed},
		{"wrapper", "Running: go test -json ./...\nGo test: 756 passed in 5 packages", TestPassed},
		// The agent filtered its own run and printed a sentinel instead.
		{"filtered", "DONE", TestNoVerdict},
		{"silent build", "", TestNoVerdict},
		// A git push says `ok main` and a shell audit says `ok  friends.sh`.
		// Neither is a test run, and both were read as one.
		{"git push", "To https://github.com/me/app.git\n   c10e92f..3830e85  main -> main\nok main", TestNoVerdict},
		{"script audit", "ok        friends.sh\nNO FALLBACK status-all.sh", TestNoVerdict},
		{"git apply", "ok\nerror: No valid patches in input (allow with \"--allow-empty\")", TestNoVerdict},
	}
	for _, c := range cases {
		if got := TestRunVerdict(c.out); got != c.want {
			t.Errorf("%s: read %q, want %q", c.name, got, c.want)
		}
	}
}

// The store keeps one command record for a command line a session ran twice,
// and both outputs. Counting only the first output made every repeated
// `go test ./...` a single run — 6,000 of 18,645 on a real store (#539).
func TestASessionRepeatingOneCommandIsCountedTwice(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, minute int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(minute) * time.Minute)}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl", Updated: at,
		Messages: []model.Message{
			msg(roleCommand, "$ go test ./internal/fetch -count=1", 0),
			msg(roleToolOutput, "--- FAIL: TestRetryBudget (0.01s)\nFAIL\tgithub.com/me/app\t0.9s\nFAIL", 1),
			msg(roleToolOutput, "ok  \tgithub.com/me/app/internal/fetch\t1.1s", 2),
			// Filtered: the verdict never reached the transcript.
			msg(roleCommand, `$ go test ./... 2>&1 | grep -E "^FAIL"; echo DONE`, 3),
			msg(roleToolOutput, "DONE", 4),
		},
	}})
	h, err := ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 3 || h.Failed != 1 || h.Passed != 1 || h.NoVerdict != 1 {
		t.Fatalf("want 3 runs one of each band, got %+v", h)
	}
	if h.Tools["go test"] != 2 {
		t.Errorf("the runners behind the series are wrong: %v", h.Tools)
	}
	if h.Days != 1 || len(h.Weeks) != 1 || h.Weeks[0].Runs != 3 {
		t.Errorf("the series does not hold one week of three runs: %+v", h.Weeks)
	}
	if len(h.Repeats) != 0 {
		t.Errorf("a test that failed on one day was called a repeat: %+v", h.Repeats)
	}
}

// Mining the named failures from every output, not only from the ones that
// answer a command deja matched: `make check` and a shell script both print
// `--- FAIL:` lines, and the test named in them is the same test.
func TestANamedFailureIsMinedFromAnyOutput(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	day := func(d int) time.Time { return time.Date(2026, 3, d, 10, 0, 0, 0, time.UTC) }
	out := func(name string, d int) model.Message {
		return model.Message{Role: roleToolOutput, Time: day(d),
			Text: "--- FAIL: " + name + " (0.02s)\nFAIL\tgithub.com/me/app\t0.4s\nFAIL"}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s2", Harness: "claude", Project: "work/app", Path: "/tmp/s2.jsonl", Updated: day(6),
		Messages: []model.Message{
			out("TestKeepsComingBack", 4),
			out("TestKeepsComingBack", 6),
			out("TestFixedThatAfternoon", 4),
			out("TestFixedThatAfternoon", 4),
		},
	}})
	h, err := ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 0 {
		t.Fatalf("outputs with no command before them were counted as runs: %+v", h)
	}
	if len(h.Repeats) != 1 {
		t.Fatalf("want one repeat-failing test, got %+v", h.Repeats)
	}
	r := h.Repeats[0]
	if r.Name != "TestKeepsComingBack" || r.Days != 2 || r.Failures != 2 || r.Project != "work/app" {
		t.Errorf("wrong row: %+v", r)
	}
	if !r.LastFail.Equal(day(6)) {
		t.Errorf("the newest failure is %s, want %s", r.LastFail, day(6))
	}
}

// testTables builds a record table covering records assembled by hand, so a
// test can round-trip through the log without standing up a manifest.
func testTables(recs ...Record) *recordTables {
	t := newRecordTables()
	for _, r := range recs {
		t.intern(r.Key)
		t.intern(r.SourcePath)
	}
	return t
}

// mustTables loads the table an index on disk was written with.
func mustTables(t interface{ Fatal(...any) }, dir string) *recordTables {
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

// tokenHits answers what the ranker actually asks — does this message carry any
// of these words — without building the message's whole token set. It has to
// tokenise identically, or the ranking changes with the shortcut.
func TestTokenHitsAgreesWithTokens(t *testing.T) {
	texts := []string{
		"The retry budget was capped at three attempts",
		"café vs café: the same word, composed and decomposed",
		"كَتَبَ and हिन्दी carry marks that continue a word",
		"internal/index/fixpair.go:146 _ = writeGob(fixesPath(tmp), kept)",
		"MiXeD CaSe and UPPER CASE and lower",
		"hyphen-joined and under_scored tokens",
		strings.Repeat("a", 70) + " " + strings.Repeat("b", 130),
		"один два три ЧЕТЫРЕ",
		"",
		"a b c",
		"日本語のトークン化",
	}
	for _, text := range texts {
		all := tokens(text)
		want := map[string]bool{}
		for _, tk := range all {
			want[tk] = true
		}
		// Every token the full tokeniser found must be reported.
		got := tokenHits(text, want)
		for _, tk := range all {
			if !got[tk] {
				t.Errorf("tokenHits(%.30q) missed %q", text, tk)
			}
		}
		if len(got) != len(want) {
			t.Errorf("tokenHits(%.30q) found %d tokens, tokens found %d", text, len(got), len(want))
		}
		// And a word the text does not carry is not invented.
		if hit := tokenHits(text, map[string]bool{"quokkabloom": true}); len(hit) != 0 {
			t.Errorf("tokenHits(%.30q) claimed a word the text lacks: %v", text, hit)
		}
	}
}

// The wanted set is what bounds the answer: a token in the text that nobody
// asked about is not reported.
func TestTokenHitsReportsOnlyWhatWasAsked(t *testing.T) {
	got := tokenHits("the retry budget was capped at three", map[string]bool{"budget": true, "scheduler": true})
	if !got["budget"] || got["scheduler"] || len(got) != 1 {
		t.Fatalf("want budget alone, got %v", got)
	}
	if tokenHits("anything at all", nil) != nil {
		t.Error("an empty question got an answer")
	}
}

// Forgetting is what people use on things they specifically do not want kept,
// and the record of it lived in exactly one file. Lose ~/.config — a wiped
// directory, a migration, a changed XDG_CONFIG_HOME — and the next rebuild
// brings those sessions back from source history that is still on disk.
func TestTombstonesSurviveLosingTheConfigDirectory(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	idx := filepath.Join(home, "idx")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("DEJA_INDEX_DIR", idx)
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"claude:sensitive-1": true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(tombstoneMirrorPath("")); err != nil {
		t.Fatalf("no copy beside the index: %v", err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("tombstone not readable at all")
	}

	// The config directory is gone. What was forgotten must stay forgotten.
	if err := os.RemoveAll(cfg); err != nil {
		t.Fatal(err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("losing the config directory resurrected a forgotten session")
	}

	// And the other way round: the index can be rebuilt from scratch at any
	// time, so the config copy has to carry it alone too.
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"claude:sensitive-1": true}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(idx); err != nil {
		t.Fatal(err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("losing the index directory resurrected a forgotten session")
	}
}

// Unforget is the deliberate reversal and must clear both copies, or the
// session comes back on one rebuild and vanishes on the next.
func TestUnforgetClearsBothCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	if err := os.MkdirAll(filepath.Join(home, "idx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"a": true, "b": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"b": true}); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	if got["a"] {
		t.Fatal("a removed tombstone survived in the mirror")
	}
	if !got["b"] {
		t.Fatal("the remaining tombstone was lost")
	}
}

// The mirror beside the index was written only by forget, and every pass that
// writes a new index deleted it with the old one. With the config copy gone
// too, the second rebuild after a forget brought the session back (#5052).
func TestForgetSurvivesRebuildsWithoutTheConfigDirectory(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"),
		claudeLine("sess-1", "2026-01-02T03:04:05Z", "forgotten needle"))
	dir := DefaultDir()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if r, err := Forget(dir, ForgetOptions{Session: "sess-1"}); err != nil || r.Sessions != 1 {
		t.Fatalf("forget=%#v err=%v", r, err)
	}
	if err := os.RemoveAll(filepath.Join(tmp, "home", ".config")); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tombstoneMirrorPath(dir)); err != nil {
			t.Fatalf("rebuild %d dropped the tombstone mirror: %v", i+1, err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		for key := range m.Sessions {
			t.Fatalf("rebuild %d brought back %s", i+1, key)
		}
	}
}

// A mirror lost earlier is written again from the config copy by the next
// swap, so either copy alone keeps the pair in force.
func TestSwapWritesTheMirrorFromTheConfigCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	dir := filepath.Join(home, "idx")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appendTombstones([]string{"claude:gone"}); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(home, "next")
	if err := os.MkdirAll(next, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, next); err != nil {
		t.Fatal(err)
	}
	if !readTombstoneFile(tombstoneMirrorPath(dir))["claude:gone"] {
		t.Fatal("the swapped-in index has no mirror of the config copy")
	}
}

// Hook text waiting for its session lives in <index>/deferred, which no build
// writes, so a rebuild between the hook that could not speak and the one that
// could deleted it (#5052).
func TestSwapKeepsDeferredHookText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	dir := filepath.Join(home, "idx")
	waiting := filepath.Join(dir, "deferred", "abc", "0start")
	if err := os.MkdirAll(filepath.Dir(waiting), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(waiting, []byte("digest"), 0o600); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(home, "next")
	if err := os.MkdirAll(next, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, next); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(waiting); err != nil || string(b) != "digest" {
		t.Fatalf("deferred text after the swap: %q, %v", b, err)
	}
}

// A process killed between a key and its newline leaves the file ending
// mid-line. Losing that key is the accepted cost of appending without a lock
// (#1029) — taking the next forget down with it is not: the new key was glued
// onto the partial one, so the authoritative copy held neither, and only the
// mirror beside the index still knew. Take the index away and the forgotten
// session comes back (#2195).
func TestATornTombstoneLineDoesNotSwallowTheNextForget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(privacyDir(), "tombstones")
	if err := os.MkdirAll(privacyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("claude:torn-half"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := appendTombstones([]string{"claude:b1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Read back from this file alone: the mirror is a second copy for the case
	// where this one is gone, and it must not be what makes this pass.
	if got := readTombstoneFile(path); !got["claude:b1"] {
		t.Errorf("the forget after a torn line is not in the file it is written to:\n%s", raw)
	}
	if strings.Contains(string(raw), "claude:torn-halfclaude:b1") {
		t.Errorf("the new key was appended onto the partial line:\n%s", raw)
	}
	// The interrupted key is still lost as a key and kept as a line: what it
	// says is not a session anyone forgot, and rewriting it away is a decision
	// for the reader, not for the writer that found it.
	if lines := strings.Count(string(raw), "\n"); lines != 2 {
		t.Errorf("file holds %d newlines, want 2:\n%s", lines, raw)
	}
}

// A transcript its agent deleted is kept in the manifest and missing from the
// listing on every run after. Once a pass has kept it, that alone is not a
// change to index.
func TestManifestFreshWithAKeptTranscript(t *testing.T) {
	store := t.TempDir()
	live := filepath.Join(store, "live.jsonl")
	gone := filepath.Join(store, "gone.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := FileState{Size: 3}
	m := Manifest{Version: version, KeptDated: true, ToolFingerprint: toolFingerprint(sources.SQLite3Available(), sources.ZstdAvailable()),
		Files: map[string]FileState{live: st, gone: st, syncImportPath: {}}}
	listed := map[string]FileState{live: st}
	if manifestFresh(m, listed, "") {
		t.Error("a deletion no pass has seen read as fresh")
	}
	m.Files[gone] = FileState{Size: 3, Kept: true}
	if !manifestFresh(m, listed, "") {
		t.Error("a kept transcript made the index stale")
	}
	if manifestFresh(m, map[string]FileState{live: st, filepath.Join(store, "new.jsonl"): st}, "") {
		t.Error("a new file beside a kept one counted as fresh")
	}
	// Put back, it is read again so the mark comes off.
	if err := os.WriteFile(gone, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if manifestFresh(m, listed, "") || manifestFresh(m, map[string]FileState{live: st, gone: st}, "") {
		t.Error("a kept transcript back on disk counted as fresh")
	}
}

// A store skipped because an external CLI was missing is not stale by its file
// state — the transcripts did not change — so the next run said "index is up to
// date" and the store stayed out of recall, after doctor had told the user to
// install the tool and run exactly that (#1760).
func TestInstallingTheMissingToolMakesTheIndexStale(t *testing.T) {
	if !sources.SQLite3Available() && !sources.ZstdAvailable() {
		if runtime.GOOS == "windows" {
			t.Skip("neither sqlite3 nor zstd on PATH")
		}
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "zstd"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	without := toolFingerprint(false, false)
	sqliteOnly := toolFingerprint(true, false)
	both := toolFingerprint(true, true)
	if without == sqliteOnly || sqliteOnly == both || without == both {
		t.Fatalf("the fingerprint does not separate the three states: %q %q %q", without, sqliteOnly, both)
	}

	m := Manifest{Version: version, KeptDated: true, Files: map[string]FileState{}, ToolFingerprint: without}
	if manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built without the tools is current on a machine that now has them")
	}
	m.ToolFingerprint = toolFingerprint(sources.SQLite3Available(), sources.ZstdAvailable())
	if !manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built with today's tools is not current")
	}

	// An index from before this field existed is not rebuilt for it: an empty
	// fingerprint says nothing was recorded, not that the tools were absent.
	m.ToolFingerprint = ""
	if !manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built by an older deja is rebuilt for a field it never wrote")
	}
}

// A hook runs with whatever PATH its harness has, which is often minimal. If
// losing sight of a tool counted as a change, that hook and a terminal with
// both tools would trade full rebuilds forever; and if the hook's build erased
// what the terminal knew, the next terminal run would read it as a tool newly
// gained and rebuild again.
func TestAToolLostDoesNotStartARebuildLoop(t *testing.T) {
	both := toolFingerprint(true, true)
	none := toolFingerprint(false, false)

	t.Setenv("PATH", "")
	if toolsChanged(Manifest{ToolFingerprint: both}) {
		t.Error("losing a tool counts as a change, so a minimal-PATH hook forces a rebuild")
	}
	if got := mergedToolFingerprint(both); got != both {
		t.Errorf("a build with no tools erased what the index knew: %q", got)
	}
	if !toolsChanged(Manifest{ToolFingerprint: none}) == false {
		t.Error("with no tools at all, nothing is newly readable")
	}
	if got := mergedToolFingerprint(""); got != none {
		t.Errorf("a first build with no tools recorded %q", got)
	}
}

// An index created by `deja sync import` records the tools too: a blank
// fingerprint reads as "written by an older deja", and the store skipped for a
// missing CLI would never be re-read once it was installed.
func TestAnImportedIndexRecordsTheTools(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.ToolFingerprint == "" {
		t.Error("an index built by sync import records no tools, so installing one never re-reads a skipped store")
	}
}

func TestPostingRoundTripCarriesTheToolBit(t *testing.T) {
	in := []posting{
		{Off: 10, Sid: 1},
		{Off: 4096, Sid: 1, Tool: true},
		{Off: 1 << 20, Sid: 70000, Tool: true},
		{Off: 1<<20 + 3, Sid: 70000},
	}
	got := decodePostings(encodePostings(in))
	if len(got) != len(in) {
		t.Fatalf("got %d postings, want %d", len(got), len(in))
	}
	for i, p := range got {
		if p != in[i] {
			t.Fatalf("posting %d = %+v, want %+v", i, p, in[i])
		}
	}
}

func TestCapKeepsSpeechBeforeToolRecords(t *testing.T) {
	// One session, four sentences and ninety-six command lines. Reading only
	// half the session's postings must not spend the whole budget on commands.
	posts := make([]posting, 0, 100)
	for i := range 4 {
		posts = append(posts, posting{Off: int64(i), Sid: 7})
	}
	for i := range 96 {
		posts = append(posts, posting{Off: int64(100 + i), Sid: 7, Tool: true})
	}
	got := capPostingsPerSession(posts, 50)
	speech, tool := 0, 0
	for _, p := range got {
		if p.Tool {
			tool++
		} else {
			speech++
		}
	}
	if speech != 4 {
		t.Fatalf("kept %d of 4 speech postings", speech)
	}
	if want := 46; tool != want {
		t.Fatalf("kept %d tool postings, want %d", tool, want)
	}
}

func TestCapSpendsEverythingOnSpeechWhenThereIsEnoughOfIt(t *testing.T) {
	posts := make([]posting, 0, 160)
	for i := range 80 {
		posts = append(posts, posting{Off: int64(i), Sid: 3})
	}
	for i := range 80 {
		posts = append(posts, posting{Off: int64(1000 + i), Sid: 3, Tool: true})
	}
	got := capPostingsPerSession(posts, 20)
	for _, p := range got {
		if p.Tool {
			t.Fatal("speech alone fills the budget, no tool posting should survive")
		}
	}
	if len(got) != 20 {
		t.Fatalf("kept %d postings, want 20", len(got))
	}
}

func TestCapLeavesSmallSessionsAlone(t *testing.T) {
	posts := []posting{
		{Off: 1, Sid: 2, Tool: true},
		{Off: 2, Sid: 2},
	}
	if got := capPostingsPerSession(posts, 64); len(got) != 2 {
		t.Fatalf("a session under the bound keeps every posting, got %d", len(got))
	}
}

func TestIsToolRole(t *testing.T) {
	for _, r := range []string{roleFiles, roleCommand, roleToolOutput} {
		if !isToolRole(r) {
			t.Errorf("%q should be a tool role", r)
		}
	}
	for _, r := range []string{"user", "assistant", "developer"} {
		if isToolRole(r) {
			t.Errorf("%q is speech", r)
		}
	}
}

func TestTokenizedPartIndexesOnlyThePathOfASpan(t *testing.T) {
	span := "/w/pkg/retry.go\nfunc retry() error {\n\treturn nil\n}"
	if got := tokenizedPart(roleEdit, span); got != "/w/pkg/retry.go" {
		t.Fatalf("got %q, want the path alone", got)
	}
	if got := tokenizedPart("user", span); got != span {
		t.Fatal("speech is indexed whole")
	}
	// A span with no body still has to yield its path rather than nothing.
	if got := tokenizedPart(roleEdit, "/w/only.go"); got != "/w/only.go" {
		t.Fatalf("got %q", got)
	}
}

func TestTopTouchedFilesRanksAndFiltersAgentFiles(t *testing.T) {
	ms := []model.Message{
		{Role: roleFiles, Text: "/w/app.go\n/w/app.go"},
		{Role: roleFiles, Text: "/w/app.go\n/w/util.go\n/Users/x/.claude/notes.md\n/w/build.log"},
		{Role: "user", Text: "/w/never.go"},
	}
	got := topTouchedFiles(ms)
	if len(got) != 2 || got[0] != "/w/app.go" || got[1] != "/w/util.go" {
		t.Fatalf("got %v, want the busiest repository files only", got)
	}
	if topTouchedFiles(nil) != nil {
		t.Fatal("no file records, nothing stored")
	}
}

func TestTopTouchedFilesIsCapped(t *testing.T) {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "/w/f%02d.go\n", i)
	}
	if got := topTouchedFiles([]model.Message{{Role: roleFiles, Text: b.String()}}); len(got) != touchedFileCap {
		t.Fatalf("stored %d paths, want the cap of %d", len(got), touchedFileCap)
	}
}

// Tool output is indexed whole below a threshold and filtered above it. A short
// output is usually the answer to something; a 50 KB build log is progress
// nobody searches, and indexing all of it took the commonest word on a
// 1150-session store from 22 ms to 57 ms.
func TestSignalLinesKeepsShortOutputWhole(t *testing.T) {
	short := "--- FAIL: TestRetry (0.01s)\n    retry_test.go:42: got 3 want 4\nFAIL\tpkg\t0.1s"
	if got := tokenizedPart(roleToolOutput, short); got != short {
		t.Fatalf("short output must be indexed whole:\n%s", got)
	}
}

func TestSignalLinesFiltersLongLogs(t *testing.T) {
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, "downloading github.com/example/module/v%d\n", i)
	}
	b.WriteString("--- FAIL: TestRetry (0.01s)\n")
	b.WriteString("    retry_test.go:42: got 3 want 4\n")
	got := tokenizedPart(roleToolOutput, b.String())
	if len(got) >= b.Len() {
		t.Fatalf("a long log should shrink: %d of %d bytes", len(got), b.Len())
	}
	// The verdict and the line explaining it both survive.
	if !strings.Contains(got, "--- FAIL: TestRetry") {
		t.Error("the verdict must be indexed")
	}
	if !strings.Contains(got, "retry_test.go:42") {
		t.Error("the line after a verdict is what it was about")
	}
	if strings.Contains(got, "downloading github.com/example/module/v200") {
		t.Error("progress lines are what the filter exists to drop")
	}
}

func TestSignalLinesFallsBackWhenNothingMatches(t *testing.T) {
	// A long output with no error-shaped line still has to be findable.
	quiet := strings.Repeat("all quiet on the build front\n", 500)
	got := tokenizedPart(roleToolOutput, quiet)
	if got == "" {
		t.Fatal("indexing nothing makes the record unreachable")
	}
	// A literal, not signalFloor+signalTail: written in terms of the constant
	// it is meant to bound, this assertion passes for any value of signalTail
	// — including signalFloor*8. The tail is pinned from the other side by
	// TestSignalLinesKeepsBothEndsWhenNothingMatches, but a bound that cannot
	// discriminate should not look like one.
	if len(got) > 13000 {
		t.Fatalf("the fallback should be bounded, got %d bytes", len(got))
	}
}

func TestSignalLinesLeavesOtherRolesAlone(t *testing.T) {
	long := strings.Repeat("a sentence someone actually typed. ", 500)
	if got := tokenizedPart("assistant", long); got != long {
		t.Fatal("speech is indexed whole however long it is")
	}
}

// An output that matches none of the signal substrings used to be indexed by
// its head alone, and the head is the least informative part of exactly those
// records. Measured on a 1153-session store: 394 outputs match no signal line,
// 262 are long enough to have a tail, and 186 of those carry a token the head
// does not — text no query could reach (#614).
func TestSignalLinesKeepsBothEndsWhenNothingMatches(t *testing.T) {
	var b strings.Builder
	b.WriteString("HEADTOKEN starts here\n")
	for b.Len() < signalFloor*2 {
		b.WriteString("ordinary chatter about nothing in particular\n")
	}
	b.WriteString("TAILTOKEN the vendor said the model name is not valid\n")
	got := signalLines(b.String())

	if !strings.Contains(got, "HEADTOKEN") {
		t.Error("dropped the head")
	}
	if !strings.Contains(got, "TAILTOKEN") {
		t.Error("dropped the tail, so the end of a long output stays unreachable")
	}
	// In order: a reader of the indexed text should meet the output the way it
	// was written. Reversing the splice indexes the same content, so only an
	// assertion catches it.
	if strings.Index(got, "HEADTOKEN") > strings.Index(got, "TAILTOKEN") {
		t.Errorf("tail spliced ahead of head: %.60q", got)
	}
	if !strings.HasPrefix(got, "HEADTOKEN") {
		t.Errorf("the head is no longer the prefix: %.60q", got)
	}
	// Both ends, not the whole thing: the byte saving is the reason the filter
	// exists.
	if len(got) >= b.Len() {
		t.Fatalf("kept %d of %d bytes — the filter stopped filtering", len(got), b.Len())
	}
	if len(got) > signalFloor+signalTail+64 {
		t.Fatalf("kept %d bytes, budget is %d", len(got), signalFloor+signalTail)
	}
}

// Short enough to fit in head plus tail: keep it whole rather than splicing
// two overlapping halves together.
func TestSignalLinesKeepsAShortUnmatchedOutputWhole(t *testing.T) {
	var b strings.Builder
	b.WriteString("nothing here matches a signal substring\n")
	for b.Len() < signalFloor+100 {
		b.WriteString("more ordinary chatter\n")
	}
	in := b.String()
	if got := signalLines(in); got != in {
		t.Fatalf("spliced a %d-byte output that fits in %d", len(in), signalFloor+signalTail)
	}
}

// A matched output is unchanged by any of this.
func TestSignalLinesUnchangedWhenSomethingMatches(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- FAIL: TestThing\n    thing_test.go:12: wrong\n")
	for b.Len() < signalFloor*2 {
		b.WriteString("filler line\n")
	}
	got := signalLines(b.String())
	if !strings.Contains(got, "--- FAIL: TestThing") || !strings.Contains(got, "thing_test.go:12") {
		t.Fatalf("lost the verdict: %q", got)
	}
	if strings.Contains(got, "filler line") {
		t.Error("kept the filler around a matched line")
	}
}

func TestEachToolOutputStreamsOnlyToolRecords(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-stream")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var got []Record
	var harness string
	if err := EachToolOutput(dir, func(m SessionMeta, r Record) {
		got = append(got, r)
		harness = m.Harness
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one tool-output record, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "shellcheck") {
		t.Fatalf("wrong record: %q", got[0].Text)
	}
	// The session is what makes the record countable — a caller clustering
	// errors needs to know which session and which harness hit it.
	if harness != "claude" {
		t.Fatalf("harness = %q", harness)
	}
	if got[0].Time.IsZero() {
		t.Fatal("a record with no time cannot be dated on a report")
	}
}

func TestEachToolOutputOnMissingStore(t *testing.T) {
	if err := EachToolOutput(filepath.Join(t.TempDir(), "nope"), func(SessionMeta, Record) {}); err == nil {
		t.Fatal("a missing store should report an error")
	}
}

// recordsForKey skips the body of records belonging to other sessions. It must
// still return exactly what a full decode of the log would have returned for
// this one — the shortcut is a decode it avoids, not a record it drops.
func TestRecordsForKeyMatchesAFullScan(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-keys")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"k1", "k2"} {
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first turn in ` + sid + `"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer for ` + sid + `"}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records.bin")
	tbl := tablesFromManifest(m)
	for key := range m.Sessions {
		var want []Record
		if err := eachRecord(path, tbl, func(r Record) {
			if r.Key == key {
				want = append(want, r)
			}
		}); err != nil {
			t.Fatal(err)
		}
		got, err := recordsForKey(path, tbl, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || len(want) == 0 {
			t.Fatalf("%s: got %d records, full scan found %d", key, len(got), len(want))
		}
		for i := range want {
			if got[i].Text != want[i].Text || got[i].Role != want[i].Role || !got[i].Time.Equal(want[i].Time) {
				t.Fatalf("%s record %d differs: %#v vs %#v", key, i, got[i], want[i])
			}
		}
	}
}

// A replaced span is served only when asked for by role, so it never reaches
// a caller loading sessions the ordinary way — the inventory needs its own
// pass, and that is the whole reason this function exists.
func TestSpanInventoryCountsWhatRestoreCanHandBack(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-spans")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	edit := func(sid, path, old string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-07-20T10:00:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + path +
			`","old_string":"` + old + `","new_string":"replacement"}}]}}` + "\n"
	}
	// Two sessions, three spans, two distinct files — the same file edited in
	// both sessions must count once.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"fix it"}}`+"\n"+
			edit("s1", "/w/pool.go", "old pool code")+
			edit("s1", "/w/retry.go", "old retry code")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"), []byte(
		`{"type":"user","sessionId":"s2","cwd":"/w","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"again"}}`+"\n"+
			edit("s2", "/w/pool.go", "another old pool")), 0o600); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	spans, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if spans != 3 {
		t.Fatalf("spans = %d, want 3", spans)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2 — the same file in two sessions is one file", files)
	}
	// The ordinary load path cannot see them, which is why this exists.
	ss, err := SearchWithRecovery(dir, query.Options{All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == roleEdit {
				t.Fatal("a span reached ordinary retrieval; SpanInventory would be unnecessary")
			}
		}
	}
}

func TestSpanInventoryOnAStoreWithNoSpans(t *testing.T) {
	if _, _, err := SpanInventory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("no index should report an error rather than zero")
	}
}

// SpanInventory guards against a span whose first line is empty, and that
// guard cannot be exercised through a parser: both extractors drop an edit
// with no path, so no such record can be written. In real-world stores, // 0 edit records with an empty first line, 0 with no newline at all.
//
// So this pins the invariant the guard rests on rather than the guard itself.
// If a future parser starts recording pathless spans, the file count here
// changes and this fails — which is the moment the guard starts mattering.
func TestSpanInventorySkipsSpansWithNoPath(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-blank")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// old_string carrying a leading newline makes the stored span begin with
	// its own newline once the path line is written above it.
	body := `{"type":"user","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"edit it"}}` + "\n" +
		`{"type":"assistant","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"","old_string":"orphan span with no path","new_string":"x"}}]}}` + "\n" +
		`{"type":"assistant","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/real.go","old_string":"real old bytes","new_string":"y"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "b1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("files = %d, want the one span that names a file", files)
	}
}

// A manifest entry becomes a session in one place. Two constructors existed
// and only one carried Touched, so a caller reading it from Recent got an
// empty slice on every session and no error — silence, which is how it cost a
// wrong measurement before it was noticed (#633).
func TestRecentCarriesTouched(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-touch")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"t1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"look at the pool"}}` + "\n" +
		`{"type":"assistant","sessionId":"t1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/pool.go"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "t1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The manifest holds it...
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || len(metas[0].Touched) == 0 {
		t.Fatalf("manifest carries no Touched: %#v", metas)
	}
	// ...and every path that hands back a session must too.
	recent, err := Recent(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 {
		t.Fatalf("sessions = %d", len(recent))
	}
	if len(recent[0].Touched) == 0 {
		t.Fatal("Recent dropped Touched, so a caller sees an empty slice and no error")
	}
	if recent[0].Touched[0] != "/w/pool.go" {
		t.Fatalf("Touched = %v", recent[0].Touched)
	}
}

// Notes are stored under the harness "deja" and narrated during indexing as
// "notes", so `deja index` printed "notes: 5 sessions" and `--harness notes`
// answered "no sessions match" and exited 0. A filter that rejects the name
// the tool just printed is a silent miss.
func TestHarnessFilterAcceptsTheNameItPrints(t *testing.T) {
	for _, c := range []struct {
		stored, want string
		ok           bool
	}{
		{"deja", "notes", true},
		{"deja", "deja", true},
		{"claude", "claude", true},
		{"claude", "notes", false},
		{"notes", "deja", false},
		{"codex", "claude", false},
	} {
		if got := harnessMatches(c.stored, c.want); got != c.ok {
			t.Errorf("harnessMatches(%q, %q) = %v, want %v", c.stored, c.want, got, c.ok)
		}
	}
}

// `deja help` documents `--role <user|assistant|tool>` while the stored name
// is "tool-output", so the documented spelling matched nothing at all —
// silently, with a healthy exit code.
func TestRoleFilterAcceptsTheDocumentedName(t *testing.T) {
	for _, c := range []struct {
		stored, want string
		ok           bool
	}{
		{"tool-output", "tool", true},
		{"tool-output", "tool-output", true},
		{"user", "user", true},
		{"assistant", "tool", false},
		{"files", "tool", false},
		{"command", "command", true},
	} {
		if got := roleMatches(c.stored, c.want); got != c.ok {
			t.Errorf("roleMatches(%q, %q) = %v, want %v", c.stored, c.want, got, c.ok)
		}
	}
}

// A fork's source is cut at the fork point, and Touched has no times: only a
// files record stamped after the cut says the source worked on the file since
// (#5341). One that names no such record is kept counted.
func TestTouchedAfterReadsTheFilesRecordTimes(t *testing.T) {
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Minute)}
	}
	ss := []model.Session{{Harness: "claude", ID: "src", Project: "p", Messages: []model.Message{
		turn("user", "flush the ledger", 0), turn(roleFiles, "/work/app/ledger.go", 1),
		turn("user", "check the notes", 10), turn(roleFiles, "/work/app/notes.md", 11),
	}}}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	meta := SessionMeta{Harness: "claude", ID: "src"}
	cut := at.Add(5 * time.Minute)
	for _, c := range []struct {
		name, path string
		at         time.Time
		want       bool
	}{
		{"touched only before the cut", "/work/app/ledger.go", cut, false},
		{"control: the same file with the cut before its edit", "/work/app/ledger.go", at, true},
		{"touched after the cut", "/work/app/notes.md", cut, true},
		{"no record names it", "/work/app/other.go", cut, true},
	} {
		if got := TouchedAfter(dir, meta, c.path, c.at); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if !TouchedAfter(filepath.Join(t.TempDir(), "missing"), meta, "/work/app/ledger.go", cut) {
		t.Error("with no index the session should stay counted")
	}
}

// A session records what it worked on so the line before an edit can say how
// many sessions touched this file. Six was enough to describe a session and too
// few to answer that question: measured over 334 files an agent really edited,
// 257 had been touched by no session at all as far as the index knew, while 268
// were spoken about in sessions the store holds.
func TestASessionRecordsTheFilesItReallyWorkedOn(t *testing.T) {
	var ms []model.Message
	for i := range 30 {
		// Recurrence decides the order, so give the later files fewer touches:
		// what is pinned is that the tail is kept at all, not that it is ranked
		// differently than before.
		for range 30 - i {
			ms = append(ms, model.Message{
				Role: roleFiles,
				Text: fmt.Sprintf("internal/index/file%02d.go", i),
			})
		}
	}
	got, hits := topTouchedCounted(ms)
	if len(got) < 20 {
		t.Errorf("a session that worked on 30 files recorded %d of them", len(got))
	}
	if len(got) > touchedFileCap {
		t.Errorf("recorded %d files, the cap is %d", len(got), touchedFileCap)
	}
	if len(hits) != len(got) {
		t.Errorf("%d paths carry %d counts", len(got), len(hits))
	}
	// The most-touched file still leads: the cap decides how long the tail is,
	// not what the head is.
	if got[0] != "internal/index/file00.go" {
		t.Errorf("the file worked on most is not first: %q", got[0])
	}
}

// And the bound still bounds: a session that touched hundreds does not carry
// hundreds into every hook that reads the manifest.
func TestTheRecordedListIsStillBounded(t *testing.T) {
	ms := make([]model.Message, 0, 500)
	for i := range 500 {
		ms = append(ms, model.Message{Role: roleFiles, Text: fmt.Sprintf("pkg/f%03d.go", i)})
	}
	got, _ := topTouchedCounted(ms)
	if len(got) != touchedFileCap {
		t.Errorf("recorded %d files of 500, want the cap of %d", len(got), touchedFileCap)
	}
}

// An agent isolated in a Claude Code worktree edits the repository's own
// source under <repo>/.claude/worktrees/<name>/. Those paths were dropped as the
// agent's bookkeeping, so the session had no Touched files and blame never
// listed it (#4164). The scratch files under .claude/ still stay out.
func TestFilesInAClaudeWorktreeCountAsTouched(t *testing.T) {
	wt := "/repo/.claude/worktrees/agent-a1/apps/promptidea/idea.go"
	nested := "/repo/.claude/worktrees/agent-a1/.claude/worktrees/agent-b2/pkg/pool.go"
	ms := []model.Message{
		{Role: roleFiles, Text: wt},
		{Role: roleFiles, Text: wt},
		{Role: roleFiles, Text: "/Users/x/.claude/projects/p/notes.md"},
		{Role: roleFiles, Text: "/repo/.claude/worktrees/agent-a1/.git/HEAD"},
		{Role: roleFiles, Text: "/repo/.claude/worktrees/agent-a1/build.log"},
		{Role: roleFiles, Text: "/repo/.claude/worktrees/agent-a1/node_modules/x/index.js"},
		{Role: roleFiles, Text: nested},
		{Role: roleFiles, Text: nested},
	}
	got := topTouchedFiles(ms)
	if len(got) != 2 || got[0] != wt && got[1] != wt {
		t.Fatalf("touched = %q, want the two worktree source files", got)
	}
}

// Sync keeps both copies when a session id exists on two machines, and the
// imported row keeps the id it had there (#1049). Nothing connected the two, so
// a reader saw one session's two histories as two unrelated sessions with
// contradictory conclusions (#1775).
func TestTwinsAcrossMachinesFindEachOther(t *testing.T) {
	metas := []SessionMeta{
		{ID: "dup-2", Harness: "claude", Project: "proj"},
		{ID: "imported-d5b1285ecbfe", Harness: "claude", Project: "imported:proj", OrigID: "dup-2"},
		{ID: "other", Harness: "claude", Project: "proj"},
		{ID: "imported-elsewhere", Harness: "claude", Project: "imported:proj", OrigID: "never-here"},
		{ID: "dup-2", Harness: "codex", Project: "proj"},
	}
	twins := TwinSessions(metas)

	if got := twins["claude:dup-2"]; len(got) != 1 || got[0] != "claude:imported-d5b1285ecbfe" {
		t.Errorf("the local copy points at %q", got)
	}
	if got := twins["claude:imported-d5b1285ecbfe"]; len(got) != 1 || got[0] != "claude:dup-2" {
		t.Errorf("the imported copy points at %q", got)
	}
	// Two other machines holding one session are both named, rather than one
	// of them silently winning the map.
	three := TwinSessions([]SessionMeta{
		{ID: "dup-3", Harness: "claude"},
		{ID: "imported-aaa", Harness: "claude", OrigID: "dup-3"},
		{ID: "imported-bbb", Harness: "claude", OrigID: "dup-3"},
	})
	if got := three["claude:dup-3"]; len(got) != 2 {
		t.Errorf("the local copy names %q, want both imports", got)
	}

	// A session with no counterpart, an import whose origin is not here, and
	// the same id under another harness are all left alone.
	for _, key := range []string{"claude:other", "claude:imported-elsewhere", "codex:dup-2"} {
		if got, ok := twins[key]; ok {
			t.Errorf("%s was paired with %q", key, got)
		}
	}
}

// Notes that were indexed off a removable disk left the index the moment the
// disk was unmounted: the first search said the volume was gone, evicted the
// records, and every search after that reported "no matches" with no line at
// all — the notes were already indexed and had no business disappearing.
func TestUnmountedVolumeKeepsWhatItAlreadyIndexed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mount points are drive letters on windows")
	}
	tmp := hermeticIndexEnv(t)
	parent := filepath.Join(tmp, "Volumes")
	vol := filepath.Join(parent, "DejaVol")
	notes := filepath.Join(vol, "deja", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	old := mountParents
	mountParents = []string{parent}
	t.Cleanup(func() { mountParents = old })

	if err := os.MkdirAll(filepath.Dir(notes), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"2026-07-01T10:00:00Z","project":"proj","text":"the ledger checksum must be recomputed after compaction"}` + "\n"
	if err := os.WriteFile(notes, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	q := query.Options{Query: "checksum", Limit: 5}
	if ss, err := Search(dir, q); err != nil || len(ss) != 1 {
		t.Fatalf("mounted: %d sessions err=%v, want the note", len(ss), err)
	}

	// Unmount: the mount point stays, the volume under it does not.
	if err := os.RemoveAll(vol); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		var buf bytes.Buffer
		if err := Ensure(dir, "", false, &buf); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !strings.Contains(buf.String(), vol+" is not mounted") {
			t.Errorf("run %d said %q, want the volume named as not mounted", run, buf.String())
		}
		ss, err := Search(dir, q)
		if err != nil || len(ss) != 1 {
			t.Fatalf("run %d: %d sessions err=%v, want the note still searchable", run, len(ss), err)
		}
	}

	// A store the user deleted is still a deletion: those records go.
	store := filepath.Join(tmp, "claude", "projects", "-tmp-proj")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	sess := `{"type":"user","timestamp":"2026-07-02T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"backoff ladder in the uploader"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(sess), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, query.Options{Query: "backoff", Limit: 5}); err != nil || len(ss) != 1 {
		t.Fatalf("deleted-store setup: %d sessions err=%v", len(ss), err)
	}
	if err := os.RemoveAll(filepath.Join(tmp, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, query.Options{Query: "backoff", Limit: 5}); err != nil || len(ss) != 0 {
		t.Fatalf("deleted store: %d sessions err=%v, want none", len(ss), err)
	}
}

// macOS mounts a disk as "/Volumes/Name 1" when something already sits on
// "/Volumes/Name". deja evicted what it had indexed from that volume and told
// the user to reconnect a disk that was already connected.
func TestRenamedMountIsNamedInsteadOfEvicted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mount points are drive letters on windows")
	}
	tmp := hermeticIndexEnv(t)
	parent := filepath.Join(tmp, "Volumes")
	notes := filepath.Join(parent, "DejaIdx", "deja", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	old := mountParents
	mountParents = []string{parent}
	t.Cleanup(func() { mountParents = old })

	if err := os.MkdirAll(filepath.Dir(notes), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"2026-07-01T10:00:00Z","project":"proj","text":"the compaction window is 15 minutes"}` + "\n"
	if err := os.WriteFile(notes, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	q := query.Options{Query: "compaction", Limit: 5}
	if ss, err := Search(dir, q); err != nil || len(ss) != 1 {
		t.Fatalf("before the remount: %d sessions err=%v", len(ss), err)
	}

	// The volume comes back one name over; an empty one takes its old place.
	renamed := filepath.Join(parent, "DejaIdx 1")
	if err := os.Rename(filepath.Join(parent, "DejaIdx"), renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "DejaIdx"), 0o700); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		var buf bytes.Buffer
		if err := Ensure(dir, "", false, &buf); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !strings.Contains(buf.String(), "is mounted as "+filepath.Join(renamed, "deja")) {
			t.Errorf("run %d said %q, want the path the volume moved to", run, buf.String())
		}
		if ss, err := Search(dir, q); err != nil || len(ss) != 1 {
			t.Fatalf("run %d: %d sessions err=%v, want the note still searchable", run, len(ss), err)
		}
	}
	if got := renamedMount(filepath.Join(parent, "NoSuchVolume", "deja")); got != "" {
		t.Errorf("renamedMount invented %q", got)
	}
}

// A transcript the index has no state for at all. Audited on a real store: 1,533
// files tracked and ten never tracked, five of them written eight weeks
// earlier, with no surface saying so — a store's session count reads as
// "nothing written yet" rather than "files never opened" (#3747).
func TestUnreadCountsNameTheTranscriptsNeverRead(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")

	writeLines(t, filepath.Join(claude, "-p-app", "one.jsonl"),
		claudeLine("aaa", "2026-09-01T10:00:00Z", "the pool ran dry under load"))
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := HarnessUnreadCounts(dir); len(got) != 0 {
		t.Fatalf("a freshly built index has unread transcripts: %v", got)
	}

	// A second transcript arrives and nothing indexes it yet.
	writeLines(t, filepath.Join(claude, "-p-app", "two.jsonl"),
		claudeLine("bbb", "2026-09-02T10:00:00Z", "the cache eviction was wrong"))
	if got := HarnessUnreadCounts(dir)["claude"]; got != 1 {
		t.Fatalf("claude unread = %d, want 1", got)
	}

	// And the store this came from: one whose files all arrived after the
	// index was built, so it holds no session at all. Nothing in the manifest
	// speaks for it.
	other := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", other)
	writeLines(t, filepath.Join(other, "projects", "app", "chats", "session-q1.jsonl"),
		`{"sessionId":"q1","messages":[{"id":"m1","type":"user","content":"the retry budget was wrong","timestamp":"2026-09-03T10:00:00Z"}]}`)
	got := HarnessUnreadCounts(dir)
	if got["qwen"] == 0 {
		t.Fatalf("a store with nothing indexed reported no unread transcripts: %v", got)
	}

	// After a pass the count goes to nothing, which is what makes it a
	// prompt rather than a permanent complaint.
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := HarnessUnreadCounts(dir); len(got) != 0 {
		t.Fatalf("after an index pass, unread = %v", got)
	}
}

// A directory or a symlink in a store is not a transcript the index means to
// hold, and counting it would make the number unactionable.
func TestUnreadCountsIgnoreWhatTheWalkDeclines(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	writeLines(t, filepath.Join(claude, "-p-app", "one.jsonl"),
		claudeLine("aaa", "2026-09-01T10:00:00Z", "the pool ran dry under load"))
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	// A symlink to the transcript already indexed: the walk declines it, so it
	// is not unread work.
	link := filepath.Join(claude, "-p-app", "copy.jsonl")
	if err := os.Symlink(filepath.Join(claude, "-p-app", "one.jsonl"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := HarnessUnreadCounts(dir); len(got) != 0 {
		t.Fatalf("a symlink counted as an unread transcript: %v", got)
	}
}

// A store deja cannot read at all is a different report, and the count must not
// speak for it: an index that was never built has no manifest to compare
// against.
func TestUnreadCountsSayNothingWithoutAnIndex(t *testing.T) {
	tmp := t.TempDir()
	if got := HarnessUnreadCounts(filepath.Join(tmp, "index.db")); got != nil {
		t.Fatalf("unread counts without an index = %v, want nothing", got)
	}
}

// A store deja could not read must not be filed as read. opencode and Cursor
// hold their sqlite stores while they run, and a lock outlasting the timeout
// used to be recorded with the store's size and mtime — so the next pass
// found nothing changed, called the index up to date, and the history stayed
// missing until someone rebuilt by hand (#3176).
func TestAStoreThatCouldNotBeReadIsNotFiledAsRead(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(db, []byte("garbage, not a database, sixty-four bytes of nothing useful....."), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := range m.Files {
		if strings.Contains(p, "opencode.db") {
			t.Fatalf("a store that could not be read was filed as read: %s", p)
		}
	}
}

// "Ran here and passed" is true of most of what a session runs, and on a real
// store the top candidate for two files in this repository was a cleanup chain
// — `kill %1; pkill -f 'codex exec'; echo …` — run in two sessions, exit 0, and
// worth none of the bytes it costs at every action.
func TestOnlyACommandThatChecksTheWorkCountsAsVerifying(t *testing.T) {
	for _, cmd := range []string{
		"SVC_FIXTURES=$PWD/fixtures GOFLAGS_EXTRA=\"-tags golden\" make test",
		"go test ./internal/store -run TestGolden",
		"cd /w/app && npm run test",
		"golangci-lint run ./...",
		"python3 -m pytest tests/",
		"go build ./... && go vet ./...",
		"./scripts/verify-fast.sh",
		"scripts/verify.sh",
	} {
		if !VerifyCommand(cmd) {
			t.Errorf("a command that checks the work reads as one that does not: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"kill %1 2>/dev/null; pkill -f 'codex exec' 2>/dev/null; echo \"codex probe done\"",
		"git commit -am wip",
		"cp -R /tmp/a /tmp/b",
		"curl -s https://example.com",
		"npm install",
		"git status --short",
		"go run ./cmd/deja index --rebuild",
		"make test && rm -rf /tmp/scratch",
	} {
		if VerifyCommand(cmd) {
			t.Errorf("a command that checks nothing is offered as the way to check an edit: %q", cmd)
		}
	}
}

// The exact tier wins and stops: `retry` returns what wrote "retry" and never
// reaches the sessions that wrote "retries", because the stem tier below it
// only runs when exact came up empty. OtherWordForms is what lets the caller
// name the rung that was skipped.
func TestOtherWordFormsNamesFormsTheCorpusHolds(t *testing.T) {
	dir := nlIndex(t,
		"we decided the uploader will retry once on 5xx",
		"we decided to cap uploader retries at 3",
		"the uploader is retrying the same chunk forever",
	)
	got := OtherWordForms(dir, []string{"retry"})
	if !slices.Contains(got["retry"], "retries") || !slices.Contains(got["retry"], "retrying") {
		t.Fatalf("retry did not reach its other forms: %v", got)
	}
	if slices.Contains(got["retry"], "retry") {
		t.Errorf("the term itself is not another form: %v", got)
	}
	// A word whose forms the corpus does not hold gets no entry, so the
	// caller has nothing to print. A line on every search is noise.
	if forms := OtherWordForms(dir, []string{"uploader"}); len(forms) != 0 {
		t.Errorf("uploader = %v, want no other forms", forms)
	}
}
