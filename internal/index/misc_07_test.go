package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"maps"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Records intern their key and source path into a table stored in the
// manifest. The table is append-only, so a log that grows must keep the ids
// it already handed out: restarting the table would give id 0 to a new string
// and silently repoint every record already using it — a message would come
// back attributed to a different session.
func TestInternedIDsSurviveIncrementalAppend(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id, ts, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	// Two sessions up front, so the table already holds s1's strings at the
	// low ids.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(line("s1", "2026-01-02T03:04:05Z", "alpha first message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"),
		[]byte(line("s2", "2026-01-02T04:04:05Z", "bravo first message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Grow ONLY the second session. A restarted table would intern s2's key
	// as id 0 — the id s1 already owns — and s1's existing records would come
	// back attributed to s2. Adding a file instead would force a full rebuild
	// and never exercise the append path.
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"),
		[]byte(line("s2", "2026-01-02T04:04:05Z", "bravo first message")+
			line("s2", "2026-01-02T04:05:05Z", "bravo second message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Every record must still name the session it belongs to.
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byText := map[string]string{}
	if err := eachRecord(filepath.Join(dir, "records.bin"), tbl, func(r Record) {
		byText[r.Text] = r.Key
		if _, ok := m.Sessions[r.Key]; !ok {
			t.Errorf("record %q resolved to key %q, which is not a session", r.Text, r.Key)
		}
		if r.SourcePath == "" {
			t.Errorf("record %q lost its source path", r.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]string{
		"alpha first message":  "claude:s1",
		"bravo first message":  "claude:s2",
		"bravo second message": "claude:s2",
	} {
		if got := byText[text]; got != want {
			t.Errorf("record %q attributed to %q, want %q", text, got, want)
		}
	}
	// And the whole thing is still searchable per session.
	for _, c := range []struct{ q, id string }{{"alpha first message", "s1"}, {"bravo second message", "s2"}} {
		ss, err := Search(dir, query.Options{Query: c.q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != c.id {
			t.Errorf("search %q returned %d sessions, want just %s", c.q, len(ss), c.id)
		}
	}
}

// The table only ever grows, so ids already in use keep their meaning.
func TestInternedIDsAreStable(t *testing.T) {
	tbl := newRecordTables()
	a := tbl.intern("claude:one")
	b := tbl.intern("/path/one.jsonl")
	if a == b {
		t.Fatal("distinct strings shared an id")
	}
	if again := tbl.intern("claude:one"); again != a {
		t.Fatalf("id changed on re-intern: %d then %d", a, again)
	}
	// A reader rebuilt from the persisted slice resolves the same way.
	rt := tablesFromStrings(tbl.strs)
	if rt.lookup(a) != "claude:one" || rt.lookup(b) != "/path/one.jsonl" {
		t.Fatalf("round trip lost ids: %q %q", rt.lookup(a), rt.lookup(b))
	}
	// Continuing a persisted table must not reuse an id.
	grown := tablesFromStrings(tbl.strs)
	c := grown.intern("claude:two")
	if c == a || c == b {
		t.Fatalf("new string reused id %d", c)
	}
	if grown.lookup(a) != "claude:one" {
		t.Fatal("growing the table repointed an existing id")
	}
	// An unknown id resolves to empty rather than panicking or aliasing.
	if got := rt.lookup(uint64(len(tbl.strs)) + 5); got != "" {
		t.Fatalf("out-of-range id resolved to %q", got)
	}
}

// The catalog must be rebuilt when a bucket changes, or a corrupt bucket goes
// unreported and the ladder never triggers a rebuild.
func TestCatalogCacheNoticesBucketChanges(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"quetzalcoatl marker"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	first, err := tokenCatalogCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !first["quetzalcoatl"] {
		t.Fatalf("catalog missing the indexed token")
	}
	if again, err := tokenCatalogCached(dir); err != nil || len(again) != len(first) {
		t.Fatalf("second call = %d tokens err=%v", len(again), err)
	}
	// Corrupting a bucket must invalidate the cache and surface as an error,
	// which is what makes the search ladder rebuild a damaged index.
	if err := os.WriteFile(filepath.Join(dir, "buckets", "zz.bin"), []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenCatalogCached(dir); err == nil {
		t.Fatal("a corrupt bucket was hidden by the cached catalog")
	}
}

func lineageKeys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// lineageStore indexes an asker "a", a copy "c" of a's opening that a went on
// past, a fork "d" that went on past what it copied, a session "u" in the same
// project that opened on its own, and a sub-agent "s" spawned by "a".
func lineageStore(t *testing.T) string {
	t.Helper()
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	opening := []model.Message{
		turn("user", "the retry loop spins forever", 0),
		turn("assistant", "the backoff never resets", 1),
	}
	with := func(extra ...model.Message) []model.Message {
		return append(append([]model.Message{}, opening...), extra...)
	}
	ss := []model.Session{
		{Harness: "claude", ID: "a", Project: "p", Messages: with(turn("user", "reset it on success", 2))},
		{Harness: "claude", ID: "c", Project: "p", Messages: with()},
		{Harness: "claude", ID: "d", Project: "p", Messages: with(turn("user", "try a jittered cap instead", 3))},
		{Harness: "claude", ID: "u", Project: "p", Messages: []model.Message{turn("user", "the retry loop spins forever", 9)}},
		{Harness: "claude", ID: "s", Project: "p", Kind: "subagent", Parent: "a", Messages: []model.Message{turn("user", "read the backoff code", 5)}},
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The asker's lineage is itself, its sub-agent, and the copy it already holds
// the end of. A fork that went on past the copy and a session that only shares
// the project stay answerable (#4547, #4549).
func TestLineageTakesTheHeldCopyAndSubAgentOnly(t *testing.T) {
	dir := lineageStore(t)
	got := lineageKeys(Lineage(dir, map[string]bool{"a": true}))
	if want := []string{"a", "c", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a = %v, want %v", got, want)
	}
	// The sub-agent asking counts its parent as itself (#4548).
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"s": true})), []string{"a", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of s = %v, want %v", got, want)
	}
	// The diverged fork holds the copy's end too, but not its sibling a's,
	// which went on past the copy and so still has news for it.
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"d": true})), []string{"c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of d = %v, want %v", got, want)
	}
}

// An asker the index has not seen yet is the newer copy of everything it opens
// alike with, and a fork it names as its source is its own work.
func TestLineageHeadMatchesByOpeningAndNamedParent(t *testing.T) {
	dir := lineageStore(t)
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	head := model.Session{Harness: "claude", ID: "h", Kind: "fork", Parent: "u", Messages: []model.Message{
		{Role: "user", Text: "the retry loop spins forever", Time: at},
	}}
	got := lineageKeys(Lineage(dir, map[string]bool{"h": true}, head))
	if want := []string{"a", "c", "d", "h", "u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of an unindexed fork = %v, want %v", got, want)
	}
	// A head that is not the asker says nothing about the asker.
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"u": true}, head)), []string{"u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a head not asking widened the lineage: %v, want %v", got, want)
	}
}

// A missing or unreadable manifest leaves the asker and nothing it cannot
// prove, so recall never hides a session it could not read.
func TestLineageWithoutAManifestIsTheAskerOnly(t *testing.T) {
	if got := Lineage(t.TempDir(), map[string]bool{"": true}); got != nil {
		t.Errorf("an empty id produced a lineage: %v", got)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	head := model.Session{Harness: "claude", ID: "h", Kind: "subagent", Parent: "p"}
	if got, want := lineageKeys(Lineage(missing, map[string]bool{"h": true}, head)), []string{"h", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing store: %v, want %v", got, want)
	}
	corrupt := t.TempDir()
	if err := os.WriteFile(filepath.Join(corrupt, "manifest.gob"), []byte("not a gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := lineageKeys(Lineage(corrupt, map[string]bool{"a": true})), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("corrupt manifest: %v, want %v", got, want)
	}
	if HasSession(missing, "a") || HasSession(corrupt, "a") {
		t.Error("HasSession answered yes without a readable manifest")
	}
}

// When the record log cannot be read, whether the asker holds a copy cannot be
// told, so the copy stays on the page; the sub-agent edge, read off the
// manifest alone, still holds.
func TestLineageKeepsTheCopyWhenRecordsAreUnreadable(t *testing.T) {
	dir := lineageStore(t)
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"a": true})), []string{"a", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage without records = %v, want %v", got, want)
	}
}

func TestHasSession(t *testing.T) {
	dir := lineageStore(t)
	if !HasSession(dir, "a") {
		t.Error("an indexed session is not found")
	}
	if HasSession(dir, "zzz") {
		t.Error("an unknown id is found")
	}
	if HasSession(dir, "") {
		t.Error("the empty id is found")
	}
}

// An asker read off its store in full leaves out only the indexed sessions
// whose last turn it holds: the copy it was forked from, not the sibling or
// the source that went on past it (#5129).
func TestLineageWholeLeavesOutOnlyWhatTheAskerHolds(t *testing.T) {
	dir := lineageStore(t)
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	opening := []model.Message{
		turn("user", "the retry loop spins forever", 0),
		turn("assistant", "the backoff never resets", 1),
	}
	// A fork of c: holds c's end, not a's or d's.
	w := model.Session{Harness: "claude", ID: "w", Kind: "fork", Messages: append(append([]model.Message{}, opening...),
		model.Message{Role: "user", Text: "untimed, not a turn"},
		turn("user", "what about a circuit breaker", 7))}
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"w": true}, w)), []string{"c", "w"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a whole fork of c = %v, want %v", got, want)
	}
	// A fork of a holds a's end, and with it c's.
	wa := model.Session{Harness: "claude", ID: "wa", Messages: append(append([]model.Message{}, opening...),
		turn("user", "reset it on success", 2), turn("user", "now add a test", 8))}
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"wa": true}, wa)), []string{"a", "c", "wa"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a whole fork of a = %v, want %v", got, want)
	}
	// A whole session that is not the asker says nothing about the asker.
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"u": true}, wa)), []string{"u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of u with an unrelated whole = %v, want %v", got, want)
	}
}

// deja narrates its own builds line by line and said nothing while waiting for
// someone else's, so a command that landed mid-rebuild sat silent for the
// length of it and read as hung (#994).
func TestWaitingForAnotherBuildIsReportedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Byte-range locks are per handle there and the second lockDir in one
		// process is a different case than two processes; the notice itself is
		// platform-independent.
		t.Skip("two locks in one process do not model contention on windows")
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	notices, ends := 0, 0
	old, oldDone := LockWaitNotice, LockWaitDone
	var onNotice func()
	LockWaitNotice = func() {
		notices++
		if onNotice != nil {
			onNotice()
		}
	}
	LockWaitDone = func() { ends++ }
	t.Cleanup(func() { LockWaitNotice, LockWaitDone = old, oldDone; lockWaitNoted = false })

	// A free lock is the ordinary case and must stay quiet.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if notices != 0 {
		t.Fatalf("an uncontended lock reported a wait: %d", notices)
	}

	// Held by someone else: the notice fires before the wait, and releasing
	// inside it proves the blocking LOCK_EX acquires immediately after.
	onNotice = unlock
	second, err := lockDir(dir)
	onNotice = nil
	if err != nil {
		t.Fatal(err)
	}
	second()
	if notices != 1 {
		t.Errorf("the wait was not reported exactly once: %d", notices)
	}
	// Its end is reported too, so a moving notice can stop.
	if ends != 1 {
		t.Errorf("the end of the wait was reported %d times", ends)
	}

	// A command takes several locks; the reader is told once, not per lock.
	lockWaitNoted = true
	noteLockWait()()
	if notices != 1 || ends != 1 {
		t.Errorf("the notice repeated itself: %d %d", notices, ends)
	}
}

// A rebuild holds the index lock, and rebuilds happen on their own schedule —
// after an upgrade, in the background, on the first run of a new format. The
// read paths a hook uses take that lock without blocking on purpose, because
// waiting would stall the agent for the whole rebuild. Nothing checked either
// half of that: not that the read still answers, and not that it returns at
// all rather than waiting.
func TestReadsAnswerWhileTheIndexLockIsHeld(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z",` +
		`"message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Hold it the way a rebuild does.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("could not take the lock this test is about")
	}
	defer unlock()

	done := make(chan struct{})
	var got int
	go func() {
		defer close(done)
		sessions, _, _, _, rerr := ProjectRelevant(dir, []string{"tmp/app"},
			[]string{"quetzalcoatl"}, 2)
		if rerr != nil {
			t.Errorf("read under the lock: %v", rerr)
		}
		got = len(sessions)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the read waited on the lock; a hook doing this stalls the agent " +
			"for the length of a rebuild")
	}
	if got == 0 {
		t.Error("the read returned nothing while a rebuild held the lock, so " +
			"recall goes silent for the length of every rebuild")
	}
}

// A sqlite store that cannot be read while a rebuild runs — locked by the
// agent using it, or unreadable for the moment — must not be recorded as
// read: the rebuild says so, doctor counts it, and the next pass reads it
// again once it opens. It used to commit an empty index for the store in
// silence and call it up to date until the next --rebuild.
func TestRebuildRetriesAStoreItCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the owner cannot read")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := os.Getenv("DEJA_OPENCODE_DB")
	runStoreSQL(t, db, `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"why does the lockedneedle pager stall"}');`)
	if err := os.Chmod(db, 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if h := IngestHealth(dir)["opencode"]; h.FailedFiles != 1 {
		t.Errorf("the unreadable store was not counted: %+v\n%s", h, out.String())
	}
	if !strings.Contains(out.String(), "could not be read") {
		t.Errorf("the rebuild said nothing about the store it could not read:\n%s", out.String())
	}
	// The file itself is untouched — same size, same mtime — only readable
	// again, which is what the end of a lock looks like.
	if err := os.Chmod(db, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	hits, err := Search(dir, query.Options{Query: "lockedneedle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("the store was not read again once it opened: %d hits\n%s", len(hits), out.String())
	}
}

// A pasted issue is a query of fifty-odd words. Scored like a question, the
// session that brushes most of them across a long day won over the short one
// that is the same bug: every extra word it happened to hold was paid in full,
// and nothing charged it for its length.
func TestPastedIssueFindsTheSameBugOverALongSession(t *testing.T) {
	// Past bestMessageStore sessions the result is re-read by its best
	// message, which must not undo what the long query ranked.
	for _, filler := range []int{80, bestMessageStore} {
		t.Run(strconv.Itoa(filler), func(t *testing.T) { pastedIssue(t, filler) })
	}
}

func pastedIssue(t *testing.T, filler int) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(root, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	issue := strings.Fields(`csv exporter drops trailing delimiter when quoting multiline cells,
		utf8 bom header duplicated after gzip stream flush, spreadsheet importer rejects file,
		reproduced with pandas reader and libreoffice calc, regression since writer buffering refactor,
		affects nightly billing ledger exports invoice reconciliation finance dashboard
		scheduler cron worker retries timeout s3 upload multipart checksum mismatch
		encoding latin1 fallback newline crlf normalization column ordering schema`)
	q := strings.Join(issue, " ")
	if n := len(RelevanceTerms(q)); n < longQueryTerms {
		t.Fatalf("the issue has %d terms, below the %d this test is about", n, longQueryTerms)
	}
	// The same bug, said once and fixed.
	writeMessages(t, proj, "same-bug", `csv exporter drops trailing delimiter when quoting multiline cells and the utf8 bom header is duplicated after gzip stream flush
fixed in the writer buffering refactor: flush the quote state before the gzip stream closes, spreadsheet importer accepts the file again`)
	// A long day that touched half the issue's words one or two at a time.
	var day strings.Builder
	for i := range 120 {
		a, b := issue[(i*7)%len(issue)], issue[(i*11+3)%len(issue)]
		fmt.Fprintf(&day, "step %d checked %s then %s while tidying unrelated module %d notes\n", i, a, b, i)
	}
	writeMessages(t, proj, "long-day", day.String())
	seedFiller(t, proj, filler)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "same-bug" {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("pasted issue ranked %v, want same-bug first", ids)
	}
}

func TestShortCJKQuestionIsNotADocument(t *testing.T) {
	q := "我们上周在支付服务里把对账任务的超时时间改成了多少秒以及为什么要这样改动配置文件还有重试次数和告警阈值"
	if n := len(RelevanceTerms(q)); n < longQueryTerms {
		t.Fatalf("only %d terms, the question no longer shows the inflation", n)
	}
	if isLongQuery(RelevanceTerms(q)) {
		t.Fatal("a one-sentence Chinese question was taken for a pasted document")
	}
	if !isLongQuery(strings.Fields(strings.Repeat("word ", longQueryTerms))) {
		t.Fatal("forty plain words are a document")
	}
}

// A line deja cannot parse is dropped and counted, and until #1993 the index
// run reported only what survived: ten turns on disk, "6 messages" on screen,
// and the four that vanished visible only through `deja doctor`. The narration
// already names a store it could not open; a line it could not parse is the
// same loss.
func TestAnIndexRunSaysWhatItCouldNotRead(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))

	turn := func(role, text string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "s1", "cwd": "/tmp/app",
			"timestamp": "2026-08-20T10:00:00Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Ten turns; four carry a raw escape inside the JSON string, which is what
	// a harness writes when it captures terminal output into a field without
	// escaping it. A raw control byte inside a JSON string is invalid JSON, so
	// claude_decode.go drops the whole line.
	var body strings.Builder
	for i := range 10 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		line := turn(role, "turn about pgbouncer number "+string(rune('0'+i)))
		if i >= 3 && i <= 6 {
			line = strings.Replace(line, "turn about", "turn \x1b[31mabout", 1)
		}
		body.WriteString(line + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second store, all of it readable. The count belongs to the store that
	// lost the lines: a fold that hands every store the run-wide total reads
	// the same on a one-store fixture, which is what this store is here to
	// stop.
	rollout := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(rollout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rollout, "rollout-2026-01-01T12-00-00-c1.jsonl"),
		[]byte(`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"c1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"a clean codex turn"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: four turns really are gone. Without this the case would pass
	// on a fixture whose lines all parsed, saying nothing about the silence.
	got, ok, err := FindByIdentity(dir, "claude", "s1")
	if err != nil || !ok {
		t.Fatalf("the session did not come back from the index: %v %v", ok, err)
	}
	if len(got.Messages) != 6 {
		t.Fatalf("fixture indexed %d of 10 turns, so it does not measure a drop", len(got.Messages))
	}
	if !strings.Contains(said, "6 message") {
		t.Fatalf("the run did not narrate the claude store at all: %q", said)
	}

	// The count of what was lost, in the line that reports what was kept. A
	// mention that omits the number ("some lines were skipped") leaves a person
	// unable to tell one bad write from half the store.
	if !strings.Contains(said, "4 line") {
		t.Errorf("an index run that dropped 4 unreadable lines never said so:\n%s", said)
	}
	if !strings.Contains(said, "deja: codex:") {
		t.Fatalf("the clean store never narrated, so nothing here checks attribution:\n%s", said)
	}
	for line := range strings.SplitSeq(said, "\n") {
		if strings.HasPrefix(line, "deja: codex:") && strings.Contains(line, "could not be read") {
			t.Errorf("the claude store's loss was charged to codex too: %q", line)
		}
	}
}

func TestManifestCacheInvalidatesOnReindex(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"c1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first session"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m1, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(m1.Sessions))
	}
	// Cached call returns the same view.
	if m2, _ := readManifestCached(dir); len(m2.Sessions) != 1 {
		t.Fatal("cache hit lost sessions")
	}
	// Reindex swaps manifest.gob; the cache must notice the new mtime/size.
	line2 := `{"type":"user","sessionId":"c2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"second session"}}` + "\n"
	if err = os.WriteFile(filepath.Join(proj, "c2.jsonl"), []byte(line2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m3, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m3.Sessions) != 2 {
		t.Fatalf("cache served stale manifest: %d sessions", len(m3.Sessions))
	}
}

// The manifest cache is keyed on manifest.gob's mtime and size, which its own
// comment called a pair the atomic swap always changes. It is not: a rewrite
// that keeps the size and lands inside one tick of the filesystem's timestamp
// resolution leaves both unchanged, and every read-only surface in the process
// — doctor's read state, the session count, friction, the brief — then answers
// from the manifest before it. This is the shape that made
// TestReadStateOf fail on CI and pass everywhere else: six manifests written
// in a loop, two of them the same size.
//
// Reproduced deterministically by putting the second write back on the first
// one's timestamp, which is what a coarse clock does on its own.
func TestAManifestRewriteIsVisibleEvenWhenMtimeAndSizeRepeat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.gob")

	first := Manifest{Version: version, Format: onDiskFormat, Sessions: map[string]SessionMeta{}}
	if err := writeManifest(dir, first); err != nil {
		t.Fatal(err)
	}
	if got := ReadStateOf(dir); got != ReadStateCurrent {
		t.Fatalf("the first manifest reads as %v, want ReadStateCurrent", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Same encoded size (one integer differs, same number of digits), then the
	// clock put back where it was.
	second := Manifest{Version: version, Format: onDiskFormat + 1, Sessions: map[string]SessionMeta{}}
	if err = writeManifest(dir, second); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if again, statErr := os.Stat(path); statErr != nil {
		t.Fatal(statErr)
	} else if again.Size() != fi.Size() || !again.ModTime().Equal(fi.ModTime()) {
		t.Skipf("the two manifests differ in size (%d against %d) — the collision this test needs is gone",
			again.Size(), fi.Size())
	}

	if got := ReadStateOf(dir); got != ReadStateUnreadable {
		t.Errorf("after a rewrite the store reads as %v, want ReadStateUnreadable — the cache answered from the manifest before it", got)
	}
	// And the cached manifest itself, not only the state derived from it.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != onDiskFormat+1 {
		t.Errorf("the cached manifest says format %d, want %d", m.Format, onDiskFormat+1)
	}
}

// The cache still has to be a cache: a read that follows a read with no write
// between them must not decode the manifest again.
func TestTheManifestCacheStillServesRepeatedReads(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifest(dir, Manifest{Version: version, Format: onDiskFormat, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifestCached(dir); err != nil {
		t.Fatal(err)
	}
	before := manifestDecodes.Load()
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := manifestDecodes.Load() - before; n != 0 {
		t.Fatalf("the second read decoded the manifest %d times, want it from the cache", n)
	}
	if m.Version != version {
		t.Errorf("cached manifest version = %d, want %d", m.Version, version)
	}
}

// The sync write path (writeSessionsWithSync) derives the session Title and
// Touched list from the raw ss it also redacts message-by-message for the
// record log. The Title is the one plaintext field it computes from user prose,
// so a secret in the first message must be scrubbed before it reaches
// sessions.gob — and nothing else in the index dir may hold it either.
func TestNoSecretSurvivesTheSyncBuild(t *testing.T) {
	const secret = "ghp_abcdefghijklmnop0123456789QRSTUV"
	dir := nlIndex(t, "how do I rotate "+secret+" in my deploy pipeline before the audit")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	var walk func(string)
	walk = func(p string) {
		info, err := os.Stat(p)
		if err != nil {
			return
		}
		if info.IsDir() {
			subs, _ := os.ReadDir(p)
			for _, s := range subs {
				walk(filepath.Join(p, s.Name()))
			}
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		checked++
		if strings.Contains(string(b), secret) {
			t.Errorf("raw secret survived into %s", p)
		}
	}
	for _, e := range entries {
		walk(filepath.Join(dir, e.Name()))
	}
	if checked == 0 {
		t.Fatal("no index files were read")
	}
}

// manualStore indexes one Claude session and returns the index and the
// transcript, so a test can change the transcript after the build.
func manualStore(t *testing.T) (dir, transcript string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	t.Setenv(ManualEnv, "")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript = filepath.Join(proj, "man1.jsonl")
	line := `{"type":"user","sessionId":"man1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, transcript
}

func growTranscript(t *testing.T, transcript string) {
	t.Helper()
	growTranscriptWith(t, transcript, "zanzibarquux")
}

func growTranscriptWith(t *testing.T, transcript, word string) {
	t.Helper()
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"type":"user","sessionId":"man1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"` + word + ` arrived later"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

type indexStamp struct {
	manifest []byte
	mtime    int64
}

func stampOf(t *testing.T, dir string) indexStamp {
	t.Helper()
	p := filepath.Join(dir, "manifest.gob")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return indexStamp{b, fi.ModTime().UnixNano()}
}

// With automatic updates off every Ensure answers from the index on disk: a
// transcript that changed after the build stays out of it, and the manifest
// is not rewritten (#5385).
func TestManualModeEnsureLeavesTheIndexAlone(t *testing.T) {
	for _, viaFile := range []bool{false, true} {
		dir, transcript := manualStore(t)
		if viaFile {
			if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"auto_index": false}`), 0o600); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Setenv(ManualEnv, "1")
		}
		if !Manual() {
			t.Fatalf("switch not seen (policy file: %v)", viaFile)
		}
		growTranscript(t, transcript)
		o := query.Options{Query: "zanzibarquux", All: true}
		calls := map[string]func() error{
			"Ensure":          func() error { return Ensure(dir, "", false, io.Discard) },
			"Ensure force":    func() error { return Ensure(dir, "", true, io.Discard) },
			"EnsureForSearch": func() error { return EnsureForSearch(dir, o, false, io.Discard) },
			"EnsureUnlessBusy": func() error {
				_, err := EnsureUnlessBusy(dir, io.Discard)
				return err
			},
			"EnsureForSearchNoWait": func() error {
				_, err := EnsureForSearchNoWait(dir, o, io.Discard)
				return err
			},
			"EnsureForSearchStale": func() error {
				stale, err := EnsureForSearchStale(dir, o, io.Discard)
				if stale {
					t.Errorf("EnsureForSearchStale asked for a background refresh")
				}
				return err
			},
		}
		// Each call gets a change of its own, so one that refreshed cannot
		// hide the next.
		for name, call := range calls {
			growTranscriptWith(t, transcript, "later"+strings.ReplaceAll(name, " ", ""))
			before := stampOf(t, dir)
			if err := call(); err != nil {
				t.Errorf("%s with an index on disk: %v", name, err)
			}
			if after := stampOf(t, dir); !bytes.Equal(after.manifest, before.manifest) || after.mtime != before.mtime {
				t.Errorf("%s rewrote the manifest with automatic updates off (policy file: %v)", name, viaFile)
			}
		}
		if r, err := SearchDetailed(dir, o); err != nil || len(r.Sessions) != 0 {
			t.Errorf("the change reached the index: %d sessions (%v)", len(r.Sessions), err)
		}

		// The explicit build still writes, and is what picks the change up.
		undo := AllowBuild()
		if err := Ensure(dir, "", false, io.Discard); err != nil {
			t.Fatal(err)
		}
		undo()
		if r, err := SearchDetailed(dir, o); err != nil || len(r.Sessions) != 1 {
			t.Errorf("deja index did not pick the change up: %d sessions (%v)", len(r.Sessions), err)
		}
	}
}

// The control: with the switch off the same change is picked up by the next
// Ensure, so the test above is not passing on a store that never refreshes.
func TestDefaultModeEnsureStillRefreshes(t *testing.T) {
	dir, transcript := manualStore(t)
	t.Setenv(ManualEnv, "0")
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"auto_index": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if Manual() {
		t.Fatal("DEJA_NO_AUTO_INDEX=0 did not win over the policy file")
	}
	before := stampOf(t, dir)
	growTranscript(t, transcript)
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if after := stampOf(t, dir); bytes.Equal(after.manifest, before.manifest) {
		t.Error("the default mode did not refresh the index")
	}
}

// With no index at all nothing is built; the error names the command.
func TestManualModeWithNoIndexSaysHowToBuildIt(t *testing.T) {
	manualStore(t)
	t.Setenv(ManualEnv, "1")
	dir := filepath.Join(t.TempDir(), "none")
	err := Ensure(dir, "", false, io.Discard)
	if !IsManualStop(err) || !strings.Contains(err.Error(), "deja index") {
		t.Fatalf("Ensure over no index = %v, want the command that builds it", err)
	}
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); !IsManualStop(err) {
		t.Errorf("EnsureForSearchStale over no index = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the index directory was created: %v", err)
	}
}

// A corrupt index is not rebuilt behind the reader's back either: the search
// says so and names the rebuild.
func TestManualModeCorruptIndexIsNotRebuilt(t *testing.T) {
	dir, _ := manualStore(t)
	t.Setenv(ManualEnv, "1")
	o := query.Options{Query: "quetzalcoatl"}
	if r, err := SearchWithRecoveryDetailed(dir, o, io.Discard); err != nil || len(r.Sessions) != 1 {
		t.Fatalf("clean search: %d sessions (%v)", len(r.Sessions), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "buckets", bucket("tquetzalcoatl")+".bin"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := stampOf(t, dir)
	_, err := SearchWithRecoveryDetailed(dir, o, io.Discard)
	if !IsManualStop(err) || !strings.Contains(err.Error(), "deja index --rebuild") {
		t.Fatalf("search over a corrupt bucket = %v, want the rebuild named", err)
	}
	if after := stampOf(t, dir); !bytes.Equal(after.manifest, before.manifest) {
		t.Error("the corrupt index was rebuilt with automatic updates off")
	}
}

// A session can surface through the relevance tier's fold — "camped" ranking a
// session that only ever says "camping". Callers count matches and cut
// snippets by looking for the query's words in the text, so with the raw terms
// alone such a result rendered as "0 matches" with no snippet.
func TestRelevanceMatchTermsCarryFoldForms(t *testing.T) {
	got := RelevanceMatchTerms("camped beside the lake")
	first := map[string]int{}
	for i, term := range got {
		if _, seen := first[term]; !seen {
			first[term] = i
		}
	}
	if _, ok := first["camped"]; !ok {
		t.Fatalf("raw query term missing: %v", got)
	}
	if _, ok := first["camping"]; !ok {
		t.Fatalf("fold form %q missing, so a session saying it renders 0 matches: %v", "camping", got)
	}
	// Raw terms come first: callers that truncate keep the words the user typed.
	if first["camping"] < first["camped"] {
		t.Fatalf("fold forms must follow the raw terms: %v", got)
	}
	// No duplicates — the count is user-visible.
	seen := map[string]bool{}
	for _, term := range got {
		if seen[term] {
			t.Fatalf("duplicate term %q in %v", term, got)
		}
		seen[term] = true
	}
}

// The same corpus, reached two ways: built in one go, or grown a session at a
// time. Two bugs came from these paths disagreeing about what they derive, and
// one of them (friction signatures hashed from unredacted text) sat in a field
// of this struct. So compare the whole row rather than one field at a time —
// whatever diverges next will be a field nobody thought to check.
func TestSessionMetadataIsTheSameWhicheverWayTheIndexGotThere(t *testing.T) {
	type sess struct{ name, ts, body, tail string }
	corpus := []sess{
		{name: "alpha", ts: "2026-01-02T03:04:05Z", body: `{"type":"user","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","isSidechain":true,"parentUuid":"root","agentId":"worker-1","message":{"role":"user","content":"the quokkasprocket deadlock in scheduler.go hangs every deploy"}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":120,"output_tokens":45},"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/app/scheduler.go","old_string":"mu.Lock()"}}]}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":80,"output_tokens":30},"role":"assistant","content":"took the queue lock before the worker lock"}}
`},
		{name: "beta", ts: "2026-01-03T03:04:05Z", body: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"build broken again"}}
{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"undefined: renderInvoice in vendor/billing/api.go"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":200,"output_tokens":60,"cache_read_input_tokens":500},"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":90,"output_tokens":25},"role":"assistant","content":"vendoring fixed it"}}
`, tail: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"connection refused: cache-a1:6379 for sk-live-ZZTOPSECRET1234567890abcd"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-opus-4-1-20250805","usage":{"input_tokens":150,"output_tokens":40},"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"systemctl restart redis"}}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-opus-4-1-20250805","usage":{"input_tokens":60,"output_tokens":20},"role":"assistant","content":"restarted the cache too"}}
`},
		// One whose text redaction rewrites. Without it the two paths agree even
		// when one of them skips redaction entirely, because raw and scrubbed are
		// the same string — and the fields derived from that text (the friction
		// signatures in Asked/Hit) would compare equal for the wrong reason. This
		// is what makes the comparison able to see #1300 at all.
		{name: "delta", ts: "2026-01-05T03:04:05Z", body: `{"type":"user","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"why does it say connection refused: db.internal:5432 for sk-live-ZZTOPSECRET1234567890abcd?"}}
{"type":"user","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: db.internal:5432 for sk-live-ZZTOPSECRET1234567890abcd"}]}]}}
{"type":"assistant","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"rotated the credential and it came back"}}
`},
		// One that gives up: GaveUp is derived, and derived is where paths drift.
		{name: "gamma", ts: "2026-01-04T03:04:05Z", body: `{"type":"user","sessionId":"gamma","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"why does the token refresh loop spin"}}
{"type":"assistant","sessionId":"gamma","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"reverted the token refresh change; it was not the loop"}}
`},
	}

	build := func(t *testing.T, grow bool) map[string]SessionMeta {
		t.Helper()
		tmp := t.TempDir()
		setHome(t, filepath.Join(tmp, "home"))
		claude := filepath.Join(tmp, "claude")
		proj := filepath.Join(claude, "-w-app")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_CLAUDE_ROOT", claude)
		dir := filepath.Join(tmp, "index.db")
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		appendTo := func(name, body string) {
			f, err := os.OpenFile(filepath.Join(proj, name+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(body); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if grow {
			for _, s := range corpus {
				body := strings.ReplaceAll(s.body, "TS", s.ts)
				// A transcript that grows rather than appears takes a third path:
				// canAppendIncremental only says yes for a file already indexed,
				// and that path writes records in place and touches no sidecar.
				// Splitting one session across two passes is what reaches it.
				if s.tail != "" {
					write(s.name, body)
					if err := Ensure(dir, "", false, nil); err != nil {
						t.Fatal(err)
					}
					appendTo(s.name, strings.ReplaceAll(s.tail, "TS", s.ts))
				} else {
					write(s.name, body)
				}
				if err := Ensure(dir, "", false, nil); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			for _, s := range corpus {
				write(s.name, strings.ReplaceAll(s.body+s.tail, "TS", s.ts))
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions
	}

	whole, grown := build(t, false), build(t, true)
	if len(whole) != len(corpus) {
		t.Fatalf("built whole indexed %d sessions, want %d", len(whole), len(corpus))
	}
	if len(whole) != len(grown) {
		t.Fatalf("built whole has %d sessions, grown has %d", len(whole), len(grown))
	}

	keys := make([]string, 0, len(whole))
	for k := range whole {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a, b := whole[k], grown[k]
		// Ord is the position a session got during its build and is expected to
		// differ; Path holds a temp directory unique to each build.
		a.Ord, b.Ord = 0, 0
		a.Path, b.Path = "", ""
		// Words, Asked, Hit, Touched and GaveUp were excluded here while a
		// session that grew never updated them (#1304). They are compared now:
		// the append path carries enough state to fold only what it has not
		// seen, so growing a session one message at a time reaches the same row
		// as building the whole thing at once. LastMsg fingerprints the newest
		// message either way and is part of that guarantee, so it is compared
		// too.
		if reflect.DeepEqual(a, b) {
			continue
		}
		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		var diffs []string
		for i := range va.NumField() {
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				diffs = append(diffs, fmt.Sprintf("%s: whole=%v grown=%v",
					va.Type().Field(i).Name, va.Field(i).Interface(), vb.Field(i).Interface()))
			}
		}
		t.Errorf("%s differs by build path — %s", k, strings.Join(diffs, "; "))
	}
}

// Every field added to SessionMeta has to say which of the three row-transfer
// paths (deleted-copy keep across versions, deleted-copy keep within the same
// version, incremental sync batch carry/reset) owns it. Leaving a new derived
// field out of withKeptRow or carryAccumulatedFrom silently drifts incremental
// passes and sync imports away from a rebuild.
func TestEverySessionMetaFieldIsClassifiedForCarryAndKeep(t *testing.T) {
	watermarkFields := map[string]bool{
		"Counted": true, "Words": true, "LastMsg": true, "Opening": true,
		"Kind": true, "Parent": true, "Agent": true,
		"Model": true, "Tokens": true, "ToolCalls": true, "ToolErrors": true,
	}
	keptSameVersionOnly := map[string]bool{
		"Settled": true, "RanCommand": true, "Asked": true, "Hit": true, "GaveUp": true,
		"Touched": true, "TouchHits": true, "Rules": true, "Revokes": true, "Planted": true,
	}
	syncCarryExtra := map[string]bool{
		"Asked": true, "Hit": true, "GaveUp": true, "NoText": true,
		"Touched": true, "TouchHits": true, "Settled": true, "RanCommand": true,
	}
	resetZeroed := map[string]bool{
		"Counted": true, "Words": true, "LastMsg": true, "Opening": true,
		"Asked": true, "Hit": true, "GaveUp": true, "NoText": true,
		"Touched": true, "TouchHits": true, "Settled": true, "RanCommand": true,
	}
	identityOrSpan := map[string]bool{
		"ID": true, "Harness": true, "Project": true, "Path": true,
		"Title": true, "AgentTitle": true, "Started": true, "Updated": true,
		"Shared": true, "SharedStarted": true, "SharedUpdated": true, "Copies": true,
		"OrigID": true, "RemoteID": true,
		"From": true, "Lifecycle": true, "LifecycleNote": true, "LifecycleAt": true,
		"Ord": true, "NoText": true, "Rules": true, "Revokes": true, "Planted": true,
	}

	rt := reflect.TypeFor[SessionMeta]()
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if !watermarkFields[name] && !keptSameVersionOnly[name] && !syncCarryExtra[name] && !identityOrSpan[name] {
			t.Errorf("SessionMeta.%s is unclassified: update withKeptRow / carryAccumulatedFrom / resetAccumulated in ingest_derived.go and classify it here", name)
		}
	}

	full := SessionMeta{
		Counted: 7, Words: 42, LastMsg: 99, Opening: 123,
		Kind: "subagent", Parent: "root-1", Agent: "worker",
		Model:      &model.Model{Provider: "openai", ID: "gpt-5.4"},
		Tokens:     &model.Tokens{Input: 100, Output: 50},
		ToolCalls:  map[string]int{"Bash": 2},
		ToolErrors: 1,
		Settled:    "fixed", RanCommand: true, Asked: []uint64{1}, Hit: []uint64{2}, GaveUp: true,
		Touched: []string{"a.go"}, TouchHits: []int{3}, NoText: true,
		Rules: []string{"make test"}, Revokes: []string{"go test"}, Planted: []string{"plan"},
	}

	keptSame := withKeptRow(SessionMeta{}, full, true)
	vFull := reflect.ValueOf(full)
	vKeptSame := reflect.ValueOf(keptSame)
	vKeptDiff := reflect.ValueOf(withKeptRow(SessionMeta{}, full, false))
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if watermarkFields[name] || keptSameVersionOnly[name] {
			if !reflect.DeepEqual(vKeptSame.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("withKeptRow(sameVersion=true) did not copy SessionMeta.%s", name)
			}
		}
		if watermarkFields[name] {
			if !reflect.DeepEqual(vKeptDiff.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("withKeptRow(sameVersion=false) did not copy watermark field SessionMeta.%s", name)
			}
		} else if keptSameVersionOnly[name] {
			if !vKeptDiff.Field(i).IsZero() {
				t.Errorf("withKeptRow(sameVersion=false) leaked version-bound field SessionMeta.%s", name)
			}
		}
	}

	var carried SessionMeta
	carryAccumulatedFrom(&carried, full)
	vCarried := reflect.ValueOf(carried)
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if watermarkFields[name] || syncCarryExtra[name] {
			if !reflect.DeepEqual(vCarried.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("carryAccumulatedFrom did not copy SessionMeta.%s", name)
			}
		}
	}

	resetAccumulated(&carried, false)
	vReset := reflect.ValueOf(carried)
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if resetZeroed[name] {
			if !vReset.Field(i).IsZero() {
				t.Errorf("resetAccumulated did not zero SessionMeta.%s", name)
			}
		}
	}
}

// metaViewsStore is a Claude store under a temp dir and the index beside it.
// add writes one single-turn session and rebuilds the index.
func metaViewsStore(t *testing.T) (dir string, add func(id, text string)) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(root, "-tmp-garden")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	add = func(id, text string) {
		t.Helper()
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	return dir, add
}

func sharedIDs(t *testing.T, dir string) string {
	t.Helper()
	metas, err := AllMetaShared(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// The views recall derives from the manifest are kept between queries. A
// publish has to replace them: a session indexed after the first query must
// be in the next one, by id, by ordinal and in every meta.
func TestMetaViewsFollowAPublish(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the compost heap needs turning before the frost")
	if got := sharedIDs(t, dir); got != "s1" {
		t.Fatalf("first build: %q", got)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first := viewsFor(m); viewsFor(m) != first {
		t.Fatal("the cached manifest's views are rebuilt on every call")
	}
	if n := len(viewsFor(m).metaByOrd()); n != 1 {
		t.Fatalf("%d sessions by ordinal, want 1", n)
	}

	add("s2", "the compost bin lid cracked in the frost")
	if got := sharedIDs(t, dir); got != "s1,s2" {
		t.Fatalf("after a publish the metas are %q, want s1,s2", got)
	}
	m2, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := viewsFor(m2)
	if n := len(v.metaByOrd()); n != 2 {
		t.Fatalf("after a publish %d sessions by ordinal, want 2", n)
	}
	if len(v.metasByID()["s2"]) != 1 {
		t.Fatal("after a publish the new session is missing by id")
	}
	ss, err := Search(dir, query.Options{Query: "compost", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("search after a publish found %d sessions, want 2", len(ss))
	}
	// The old manifest still answers for itself, not with the new table.
	if n := len(viewsFor(m).metaByOrd()); n != 1 {
		t.Fatalf("an older manifest got the newer views: %d sessions", n)
	}
}

// Another process publishes by swapping sessions.gob under this one; the
// next read sees a new file and must not pair it with the old views.
func TestMetaViewsFollowAnotherProcess(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the rain gauge overflowed on tuesday")
	if got := sharedIDs(t, dir); got != "s1" {
		t.Fatalf("first build: %q", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessions := maps.Clone(m.Sessions)
	sessions["claude:s9"] = SessionMeta{ID: "s9", Harness: "claude", Ord: 99}
	if _, err := writeSessionsBinAtomicStat(filepath.Join(dir, "sessions.bin"), sessions); err != nil {
		t.Fatal(err)
	}
	if got := sharedIDs(t, dir); got != "s1,s9" {
		t.Fatalf("after another process wrote the index the metas are %q, want s1,s9", got)
	}
}

// Concurrent MCP calls share one set of views while the index is rewritten
// under them. Run with -race.
func TestMetaViewsConcurrentReaders(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the hedge trimmer blade went dull")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := AllMetaShared(dir); err != nil {
					continue
				}
				if m, err := readManifestCached(dir); err == nil {
					v := viewsFor(m)
					_ = v.metaByOrd()
					_ = v.metasByID()
				}
			}
		})
	}
	add("s2", "the hedge along the lane needs a second cut")
	add("s3", "a hedge sparrow nests in the trimmer box")
	close(stop)
	wg.Wait()
	if got := sharedIDs(t, dir); got != "s1,s2,s3" {
		t.Fatalf("after concurrent reads the metas are %q", got)
	}
}

// A store on a disk that is not mounted looks exactly like a store whose files
// were deleted: the sessions leave the index and every surface then reads "no
// agent history was found on this machine" (#900).
func TestMissingTreesNamesADirectoryThatWentAwayWhole(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live", "proj")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(live, "gone.jsonl")
	if err := os.WriteFile(deleted, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	// One file gone from a directory that is still there: an ordinary
	// removal, and nothing to say about it.
	if got := missingTrees(map[string]bool{deleted: true}); len(got) != 0 {
		t.Errorf("an ordinary deletion was reported as a missing tree: %+v", got)
	}

	// A whole tree gone: reported once, at the outermost directory that is no
	// longer there, with the count under it.
	vanished := filepath.Join(root, "volume", "claude", "proj")
	files := map[string]bool{
		filepath.Join(vanished, "a.jsonl"): true,
		filepath.Join(vanished, "b.jsonl"): true,
		filepath.Join(vanished, "c.jsonl"): true,
	}
	got := missingTrees(files)
	if len(got) != 1 {
		t.Fatalf("missing trees = %+v, want one", got)
	}
	if got[0].dir != filepath.Join(root, "volume") {
		t.Errorf("reported %q, want the outermost directory that is gone", got[0].dir)
	}
	if got[0].files != 3 {
		t.Errorf("counted %d files, want 3", got[0].files)
	}
	if s := pluralFiles(1); s != "" {
		t.Errorf("one file pluralised: %q", s)
	}
	if s := pluralFiles(3); !strings.HasPrefix(s, "s") {
		t.Errorf("three files not pluralised: %q", s)
	}
}

// A transcript rewritten and moved to another project before the next pass is
// no rename: the old file is kept as a deleted transcript and the new one is
// read as a copy of the same session. The pass leaves the kept records where
// they were and appends the new ones; a rebuild wrote the kept file after the
// live one, so the session's tool output came back in another order and its
// friction texts, facts and fixes differed from the pass's.
func TestRewrittenAndMovedKeepsTheOrderARebuildWrites(t *testing.T) {
	h := newTwoWayEnv(t)
	wall := func(port string) string { return "dial tcp 127.0.0.1:" + port + ": connect: connection refused\n" }
	sess := func(p, sid string, day int, q, port string) string {
		return hPrompt(p, sid, day, 0, q) + hBash(p, sid, day, 1, sid+"a", "make migrate", wall(port), true)
	}
	h.twoWays("rewrite then move", func() {
		h.put("svc", "s1", sess("svc", "s1", 2, "why does the pool fail", "5432"))
		h.put("app", "s2", sess("app", "s2", 3, "why does the queue fail", "5432"))
		h.put("app", "s3", sess("app", "s3", 4, "why does the cache fail", "5432"))
	}, func() {
		h.put("svc", "s1", sess("svc", "s1", 5, "again: why does the ledger fail", "6432"))
		if err := os.MkdirAll(filepath.Dir(h.path("infra", "s1")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path("svc", "s1"), h.path("infra", "s1")); err != nil {
			t.Fatal(err)
		}
	})
}

// A session's commands do not always come in time order: a kept file can hold
// turns older than the copy on disk, and a transcript can hold them out of
// order itself. A pass takes its facts from them sorted by time; the rebuild
// took them as they came, and named another command the most recent.
func TestSessionFactsTakeCommandsInTimeOrder(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("commands out of time order", func() {
		h.put("app", "s1", hPrompt("app", "s1", 2, 0, "why does the pool fail"))
	}, func() {
		h.put("svc", "s2", hPrompt("svc", "s2", 3, 0, "why does the queue fail")+
			hBash("svc", "s2", 3, 5, "a", "make migrate", "ok", false)+
			hBash("svc", "s2", 3, 1, "b", "go test ./queue/...", "ok", false))
	})
}

// The rebuild's dedupe set holds an entry per message of the corpus, so its
// key has to stay a fixed size: a joined string with the time formatted and
// the hash in hex was a fresh allocation per message, 316 MB live at the peak
// of a 3.7M-message build.
func TestMsgSeenEntryIsFixedSize(t *testing.T) {
	const msgs = 2000
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	texts := make([]string, msgs)
	for i := range texts {
		texts[i] = strings.Repeat("x", 4096) + string(rune('a'+i%26)) + strings.Repeat("y", i)
	}
	key := "claude:" + strings.Repeat("0", 36)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	seen := msgSeen{}
	for i, text := range texts {
		if seen.dup(key, "user", at.Add(time.Duration(i)*time.Millisecond), text) {
			t.Fatalf("message %d reported as a repeat", i)
		}
	}
	runtime.ReadMemStats(&after)
	if !seen.dup(key, "user", at.Add(7*time.Millisecond), texts[7]) {
		t.Fatal("a repeated message was not caught")
	}
	if seen.dup(key, "user", at.Add(7*time.Millisecond+time.Nanosecond), texts[7]) {
		t.Fatal("the same text at another time was taken for a repeat")
	}
	if seen.dup(key, "user", at.Add(7*time.Millisecond).In(time.FixedZone("x", 3600)), texts[7]) == false {
		t.Fatal("the same instant in another zone was not taken for a repeat")
	}
	// The map itself, and nothing per entry beside it.
	perEntry := float64(after.Mallocs-before.Mallocs) / msgs
	if perEntry > 0.5 {
		t.Fatalf("%.2f allocations per message in the dedupe set; want the key stored inline", perEntry)
	}
}

// The bounded pick keeps exactly what sorting everything and cutting would,
// ties on the same moment included.
func TestNewestNMatchesAFullSort(t *testing.T) {
	r := rand.New(rand.NewSource(1)) //nolint:gosec // a seeded fixture, not crypto
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, size := range []int{0, 1, 59, 60, 61, 240, 241, 5000} {
		ss := make([]model.Session, size)
		for i := range ss {
			// Few distinct moments, so ties fall to harness and id.
			ss[i] = model.Session{ID: strconv.Itoa(r.Intn(size + 1)), Harness: []string{"claude", "codex"}[r.Intn(2)],
				Updated: base.Add(time.Duration(r.Intn(50)) * time.Hour)}
		}
		want := slices.Clone(ss)
		slices.SortStableFunc(want, func(a, b model.Session) int {
			if newestFirstSession(a, b) {
				return -1
			}
			if newestFirstSession(b, a) {
				return 1
			}
			return 0
		})
		if len(want) > 60 {
			want = want[:60]
		}
		got := newestN(slices.Clone(ss), 60)
		if len(got) != len(want) {
			t.Fatalf("size %d: kept %d, want %d", size, len(got), len(want))
		}
		for i := range got {
			if got[i].ID != want[i].ID || got[i].Harness != want[i].Harness || !got[i].Updated.Equal(want[i].Updated) {
				t.Fatalf("size %d: row %d is %+v, want %+v", size, i, got[i], want[i])
			}
		}
		if all := newestN(slices.Clone(ss), 0); len(all) != size {
			t.Fatalf("size %d: n=0 kept %d", size, len(all))
		}
	}
}

// An accented word can be typed and stored in either Unicode normalisation:
// precomposed (NFC) or decomposed (NFD, base + combining mark). Some editors
// and the macOS filesystem emit NFD, so a store and a query can carry the same
// word in different forms. The fold table covers Latin, Cyrillic and Greek, so
// all four combinations must find the record for each (#1098).
func TestNFCNFDSearchAllCombos(t *testing.T) {
	scripts := []struct{ name, nfc, nfd string }{
		{"latin", "café", "café"},  // café: é vs e+U+0301
		{"cyrillic", "мой", "мой"}, // мой: й vs и+U+0306
		{"greek", "ά", "ά"},        // ά: U+03AC vs α+U+0301
	}
	for _, sc := range scripts {
		stored := map[string]string{"nfc": sc.nfc, "nfd": sc.nfd}
		queries := map[string]string{"nfc": sc.nfc, "nfd": sc.nfd}
		for sName, sForm := range stored {
			for qName, qForm := range queries {
				t.Run(sc.name+"_"+sName+"_stored_"+qName+"_query", func(t *testing.T) {
					tmp := t.TempDir()
					claudeRoot := filepath.Join(tmp, "claude")
					proj := filepath.Join(claudeRoot, "-tmp-app")
					if err := os.MkdirAll(proj, 0o755); err != nil {
						t.Fatal(err)
					}
					line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"deployed to the ` + sForm + ` server"}}` + "\n"
					if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
						t.Fatal(err)
					}
					t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
					dir := filepath.Join(tmp, "index.db")
					t.Setenv("DEJA_INDEX_DIR", dir)
					if err := Ensure(dir, "", true, nil); err != nil {
						t.Fatal(err)
					}
					got, err := Search(dir, query.Options{Query: qForm, All: true})
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, s := range got {
						if s.ID == "s1" {
							found = true
						}
					}
					if !found {
						t.Fatalf("%s stored %s query %s: want s1, got %d sessions", sc.name, sName, qName, len(got))
					}
				})
			}
		}
	}
}

// A day bucket's id is the sending machine's rendering of a date, not the
// identity of what it holds. After that machine changed zone the same note
// arrived under a new bucket and every peer grew a second copy (#977).
func TestANoteDoesNotArriveTwiceUnderTwoBucketIds(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, recs ...SyncRecord) {
		t.Helper()
		var batch []byte
		for _, r := range recs {
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			batch = append(batch, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(exp, name), batch, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const text = "note written next morning"
	// The same note, exported before and after the sender changed zone: same
	// project, same timestamp, different bucket.
	write("deja-sync-a.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-13-p", Project: "p", Role: "user", Text: text})
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	write("deja-sync-b.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-12-p", Project: "p", Role: "user", Text: text})
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}

	ss, err := Search(dir, query.Options{Query: "next morning", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("the same note landed in %d sessions, want 1", len(ss))
	}

	// A different note in the same bucket is not a duplicate.
	write("deja-sync-c.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-12-p", Project: "p", Role: "user", Text: "a different note entirely"})
	if _, err = Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, query.Options{Query: "different note entirely", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("a genuinely new note was dropped as a duplicate")
	}
	// And an ordinary session still dedupes on its own id.
	write("deja-sync-d.jsonl", SyncRecord{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "an ordinary transcript line"})
	if _, err = Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	if _, err = Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, query.Options{Query: "ordinary transcript line", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("an ordinary session landed in %d sessions, want 1", len(ss))
	}
}

// A promoted note's title carries its state, and the loader rewrites it on
// every correction. The incremental path only ever derived a title for a
// session it had not seen, so the state froze at whatever the note held when
// it was first indexed: after `promote --state rejected` the store still said
// "[accepted]" on `deja last`, in the digest, and in the citation line the
// hook pre-writes for the agent to say aloud. Only a full rebuild caught up
// (#R11).
func TestPromotedNoteTitleTracksTheCorrection(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	when := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	const src = "claude:sess1"

	if err := sources.AppendPromotedSourced("work/api", "what pool size should the pool use",
		"pool size 200", src, "accepted", nil, when, when); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromotedSourced("work/api", "what pool size should the pool use",
		"rolled back, the pool stays at 20", src, "rejected", nil, when, when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Incremental, not a rebuild: the rebuild was never the broken path.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := model.PromotedNoteID(src)
	for _, m := range metas {
		if m.ID != id {
			continue
		}
		if strings.Contains(m.Title, "[accepted]") {
			t.Fatalf("the note still names the state the correction reversed: %q", m.Title)
		}
		if !strings.Contains(m.Title, "[rejected]") {
			t.Fatalf("the note's title lost its state: %q", m.Title)
		}
		return
	}
	t.Fatalf("no note %q in %d sessions", id, len(metas))
}

// The suite writes and reads promoted notes, and sources.NotesFile() resolves
// through XDG_DATA_HOME and APPDATA before it ever reaches HOME. When TestMain
// left those pointing at the developer's real profile, the suite appended
// fixtures to their store and then failed reading them back as leaked `deja`
// sessions (#1141). This pins the store under the temp root so that drift
// fails here, naming the real path, instead of eleven arithmetic tests.
func TestNotesFileStaysUnderTheTestRoot(t *testing.T) {
	nf := sources.NotesFile()
	home := os.Getenv("HOME")
	appdata := os.Getenv("APPDATA")
	// Anchor to HOME alone. TestMain points HOME at the temp root on every
	// platform and every notes path lands under it — root\AppData\Roaming on
	// Windows, root/.local/share elsewhere. Accepting an APPDATA prefix would
	// read the drift back from the very environment the guard exists to catch:
	// drop the APPDATA redirect (the exact shape of #1141) and NotesFile falls
	// back to the real profile, yet an APPDATA-prefix check would still pass.
	if home != "" && strings.HasPrefix(nf, home) {
		return
	}
	t.Fatalf("notes store resolved outside the test root: %q — TestMain is leaking to the developer's real store (HOME=%q APPDATA=%q); blank XDG_DATA_HOME/DEJA_NOTES_FILE and point APPDATA at the temp root (#1141)", nf, home, appdata)
}

// Only `deja index` regrouped drifted note buckets (#1058), so a search, `deja
// last` or the hook served the same local day as two rows for as long as the
// user never typed that command.
func TestSearchRegroupsNoteBucketsAfterAZoneChange(t *testing.T) {
	tmp := t.TempDir()
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	body := `{"ts":"2026-08-01T23:15:00Z","project":"proj","text":"the pool cap is 20"}` + "\n" +
		`{"ts":"2026-08-01T23:30:00Z","project":"proj","text":"the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.UTC

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := noteBuckets(t, dir); len(got) != 1 {
		t.Fatalf("one zone, one day: buckets = %v", got)
	}

	// Nine hours east: both notes belong to the next day here, and a note
	// written since lands in the bucket this machine would build.
	time.Local = time.FixedZone("east", 9*60*60)
	if err := os.WriteFile(notes, []byte(body+`{"ts":"2026-08-02T00:10:00Z","project":"proj","text":"rotate the certificate"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, nil); err != nil {
		t.Fatal(err)
	}
	got := noteBuckets(t, dir)
	if len(got) != 1 || got[0] != "deja-2026-08-02-proj" {
		t.Fatalf("one local day served as %v, want one bucket deja-2026-08-02-proj", got)
	}
}

func noteBuckets(t *testing.T, dir string) []string {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range metas {
		if m.Harness == "deja" {
			out = append(out, m.ID)
		}
	}
	return out
}

// A bucket id names a local day, so notes written at one moment under two
// zones sit in two buckets and only a rebuild regroups them (#1058). What a
// rebuild cannot regroup is a bucket that arrived over sync: it was grouped on
// the peer, and counting it would rebuild the index on every `deja index`.
func TestNotesZoneDrift(t *testing.T) {
	moment := time.Date(2026, 7, 20, 23, 45, 0, 0, time.UTC)
	notes := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if sources.NotesFile() != notes {
		t.Fatalf("notes file = %q, want %q", sources.NotesFile(), notes)
	}

	cases := []struct {
		name string
		meta SessionMeta
		want bool
	}{
		{"bucket grouped in this zone", SessionMeta{ID: "deja-2026-07-20-tz", Harness: "deja", Path: notes, Started: moment, Updated: moment}, false},
		{"bucket grouped east of here", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "deja", Path: notes, Started: moment, Updated: moment}, true},
		{"same day, another machine's notes", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "deja", Path: "/elsewhere/notes.jsonl", Started: moment, Updated: moment}, false},
		{"promoted note carries no day", SessionMeta{ID: model.PromotedNoteID("claude:abc"), Harness: "deja", Path: notes, Started: moment, Updated: moment}, false},
		{"another harness that starts with deja-", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "claude", Path: notes, Started: moment, Updated: moment}, false},
	}
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			m := Manifest{Version: version, Sessions: map[string]SessionMeta{c.meta.Harness + ":" + c.meta.ID: c.meta}}
			if err := writeManifest(dir, m); err != nil {
				t.Fatal(err)
			}
			if got := NotesZoneDrift(dir); got != c.want {
				t.Errorf("NotesZoneDrift = %v, want %v", got, c.want)
			}
		})
	}

	// No store yet is not drift: the first `deja index` has nothing to regroup.
	if NotesZoneDrift(t.TempDir()) {
		t.Error("NotesZoneDrift on a store with no manifest = true, want false")
	}
}

// The relevance tier used to load every record of every candidate session and
// fold all of it to find the few messages that matched: on a real store one
// query folded 120.7 MB across 140,355 messages, 10.3% of which held any query
// term. It reads the records the ranking already identified now, so a hit
// carries the messages that matched — and the pool, the order and the totals
// are what they were (#3491).
func TestTheRelevanceTierReadsTheRecordsThatMatched(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// One session with the answer buried in the middle of a long transcript,
	// and a pile of others so the query has a corpus to be ranked against.
	var b strings.Builder
	for i := range 300 {
		text := fmt.Sprintf("ordinary line %d about the sidebar layout", i)
		switch i {
		case 150:
			text = "the billing exporter drops every third retry"
		case 151:
			text = "the backoff counted from zero in the scheduler"
		}
		fmt.Fprintf(&b, `{"type":"user","sessionId":"long","cwd":"/work/app","timestamp":"2026-09-0%dT10:%02d:00Z","message":{"role":"user","content":%q}}`+"\n",
			1+i%9, i%60, text)
	}
	if err := os.WriteFile(filepath.Join(proj, "long.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for k := range 12 {
		id := fmt.Sprintf("other%d", k)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":"2026-09-02T11:%02d:00Z","message":{"role":"user","content":"work on the invoice renderer and the webpack config"}}`+"\n", id, k)
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Relevance alone: one token the store has never seen empties the
	// substring intersection the close tier would otherwise answer with, and
	// the rest are informative enough to rank.
	res, err := SearchWithRecoveryDetailed(dir, query.Options{
		Query: "billing exporter scheduler quibnotpresentword", Limit: 10,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance", res.Tier)
	}
	var found bool
	for _, s := range res.Sessions {
		if s.ID != "long" {
			continue
		}
		found = true
		if len(s.Messages) == 0 {
			t.Fatal("the hit carries no messages")
		}
		// The matched message is there, and the 299 that matched nothing are
		// not: that is the whole of the change.
		joined := ""
		var joinedSb83 strings.Builder
		for _, m := range s.Messages {
			joinedSb83.WriteString(m.Text + "\n")
		}
		joined += joinedSb83.String()
		for _, want := range []string{"billing exporter drops every third retry", "backoff counted from zero in the scheduler"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the matched message %q is missing:\n%s", want, joined)
			}
		}
		if len(s.Messages) > 20 {
			t.Errorf("the hit carries %d messages of a 300-message session", len(s.Messages))
		}
		if strings.Contains(joined, "ordinary line 10 ") {
			t.Errorf("a message that matched nothing was read:\n%s", joined)
		}
	}
	if !found {
		t.Fatalf("the session that answers the question is not in the answer (%d hits)", len(res.Sessions))
	}
}

// Ord is the posting's session id. A new session takes max(Ord)+1 over the
// manifest being built, but a re-parsed session takes its Ord from the OLD
// manifest — which the new map has not seen yet. When every existing file
// changes at once, the new session is handed an Ord an existing session is
// about to reclaim, and the two sessions' postings merge: one of them stops
// being findable by its own words.
func TestOrdsStayUniqueWhenEveryFileChangesAtOnce(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(file, id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, file), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("s1.jsonl", "s1", "alpha original content")
	write("s2.jsonl", "s2", "bravo original content")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Every existing file grows, and a new session appears whose file sorts
	// ahead of them — so it is assigned an Ord before the old ones reclaim
	// theirs.
	write("s1.jsonl", "s1", "alpha original content plus quetzalcoatl marker")
	write("s2.jsonl", "s2", "bravo original content plus xochipilli marker")
	write("aaa.jsonl", "s3", "charlie brandnew tlaloc marker")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byOrd := map[uint32]string{}
	for key, meta := range m.Sessions {
		if other, dup := byOrd[meta.Ord]; dup {
			t.Fatalf("sessions %q and %q share Ord %d — their postings are merged", other, key, meta.Ord)
		}
		byOrd[meta.Ord] = key
	}
	// And the behavior that Ord uniqueness protects: each session is findable
	// by the marker only it contains.
	for _, c := range []struct{ marker, id string }{
		{"quetzalcoatl", "s1"},
		{"xochipilli", "s2"},
		{"tlaloc", "s3"},
	} {
		got, err := Search(dir, query.Options{Query: c.marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != c.id {
			ids := []string{}
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			t.Errorf("search %q returned %v, want exactly [%s]", c.marker, ids, c.id)
		}
	}
}

// justAfterMidnight returns a moment that is today for every hour the suite
// might run at: local midnight plus a minute, or now if the run is inside that
// first minute.
func justAfterMidnight(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), 0, 1, 0, 0, now.Location())
	if t.After(now) {
		return now
	}
	return t
}

// The counters tested Updated.After(midnight) with no upper bound, so anything
// ahead of the clock was today's work, this week's, and the newest thing in the
// store — permanently. On a 10k-session store that read "today 2222" (#696).
func TestOverviewExcludesSessionsFromTheFuture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Anchored inside today rather than measured backwards from now: two hours
	// ago is yesterday when the suite runs shortly after midnight, and
	// SessionsToday counts from local midnight (#837).
	today := justAfterMidnight(now)
	sessions := map[string]SessionMeta{
		"claude:today":     {ID: "today", Harness: "claude", Updated: today},
		"claude:yesterday": {ID: "yesterday", Harness: "claude", Updated: now.AddDate(0, 0, -1)},
		"claude:lastmonth": {ID: "lastmonth", Harness: "claude", Updated: now.AddDate(0, -1, 0)},
		// Just inside the week, so narrowing the window shows up too.
		"claude:sixdays": {ID: "sixdays", Harness: "claude", Updated: now.Add(-6*24*time.Hour - 12*time.Hour)},
		// Just outside the week, so widening the window shows up.
		"claude:eightdays": {ID: "eightdays", Harness: "claude", Updated: now.Add(-7*24*time.Hour - 12*time.Hour)},
		"claude:soon":      {ID: "soon", Harness: "claude", Updated: now.Add(3 * time.Hour)},
		"claude:nextweek":  {ID: "nextweek", Harness: "claude", Updated: now.AddDate(0, 0, 6)},
		"claude:nextyear":  {ID: "nextyear", Harness: "claude", Updated: now.AddDate(1, 0, 0)},
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: sessions}); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Sessions != 8 {
		t.Errorf("sessions = %d, want 8 — the future ones still exist", ov.Sessions)
	}
	if ov.SessionsToday != 1 {
		t.Errorf("today = %d, want 1", ov.SessionsToday)
	}
	if ov.SessionsWeek != 3 {
		t.Errorf("this week = %d, want 3", ov.SessionsWeek)
	}
	// A session three hours from now is still ahead of the clock, even though
	// it falls on today's date.
	if ov.Future != 3 {
		t.Errorf("future = %d, want 3", ov.Future)
	}
}

func TestOverviewWithNoFutureSessionsSaysNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sessions := map[string]SessionMeta{
		"claude:a": {ID: "a", Harness: "claude", Updated: justAfterMidnight(now)},
		"claude:b": {ID: "b", Harness: "claude", Updated: now.AddDate(0, 0, -3)},
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: sessions}); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Future != 0 || ov.SessionsToday != 1 || ov.SessionsWeek != 2 {
		t.Errorf("ov = %+v", ov)
	}
}

// The "covering" line answers how far back a memory goes, and long-running
// sessions are the ones carrying the old history. Deriving the floor from the
// last activity hid everything before it (#786).
func TestOverviewCoversWhenTheOldestWorkBegan(t *testing.T) {
	_, dir := allHarnessEnv(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	m := Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
		"claude:long":  {ID: "long", Harness: "claude", Started: started, Updated: updated},
		"claude:short": {ID: "short", Harness: "claude", Started: updated, Updated: updated},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Oldest.Equal(started) {
		t.Errorf("oldest = %s, want the earliest start %s", ov.Oldest, started)
	}
	if !ov.Newest.Equal(updated) {
		t.Errorf("newest = %s, want %s", ov.Newest, updated)
	}

	// A session whose start never made it into the manifest still has to bound
	// the range, or an older store reports no coverage at all.
	m.Sessions = map[string]SessionMeta{"claude:nostart": {ID: "nostart", Harness: "claude", Updated: updated}}
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	ov, err = Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Oldest.Equal(updated) {
		t.Errorf("oldest = %s, want the fallback %s", ov.Oldest, updated)
	}
}

// halvedStore indexes sessions in two projects, one of which the reader's
// ignore rule keeps out of recall.
func halvedStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	// DEJA_POLICY_FILE, not a file under the home: this package shares one
	// XDG_CONFIG_HOME across its tests, so a policy written through
	// policy.Path() outlives the test that wrote it and every later test reads
	// it — which is how a rule of `*scratch*` silenced the default rule three
	// tests were pinning.
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id, day string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-` + day + `T10:00:00Z","message":{"role":"user","content":"the widget pipeline"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-` + day + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/x/` + id + `.go","old_string":"a","new_string":"b"}}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		write("keep", fmt.Sprintf("k%d", i), "01")
	}
	for i := range 6 {
		write("scratch", fmt.Sprintf("s%d", i), "02")
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Overview reports what the index holds, which is what `deja doctor` asks for.
// OverviewServable reports what recall can serve, which is what the brief and
// the "older than that window" line describe — and counting the first on those
// screens put three times the sessions on the brief that its own rows showed,
// dated to a day none of them happened on (#2650).
func TestOverviewServableLeavesOutTheIgnoredTree(t *testing.T) {
	dir := halvedStore(t)

	whole, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Sessions != 9 {
		t.Fatalf("the whole store holds 9 sessions, Overview says %d", whole.Sessions)
	}

	servable, err := OverviewServable(dir)
	if err != nil {
		t.Fatal(err)
	}
	if servable.Sessions != 3 {
		t.Fatalf("recall can serve 3 sessions, OverviewServable says %d", servable.Sessions)
	}
	// The range too: the ignored six are the only work on the second of July.
	if got := servable.Newest.UTC().Format("2006-01-02"); got != "2026-07-01" {
		t.Fatalf("the servable range ends %s, on a day only ignored sessions were worked on", got)
	}
	if !whole.Newest.After(servable.Newest) {
		t.Fatalf("the two ranges should differ on this store: whole %s, servable %s", whole.Newest, servable.Newest)
	}
}

// The span count is what `restore` would hand back, and restore refuses a tree
// the ignore rule covers.
func TestSpanInventoryCountsOnlyWhatRestoreWouldGiveBack(t *testing.T) {
	dir := halvedStore(t)
	spans, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if spans != 3 {
		t.Fatalf("3 sessions are servable and each replaced one span, got %d", spans)
	}
	if files != 3 {
		t.Fatalf("those three spans are in three files, got %d", files)
	}
}

// Losing the whole postings directory was caught in #946. Losing one file out
// of it was not: every token that lived there answered "no matches in N indexed
// sessions" while its text sat in the record log, `deja index` said the index
// was up to date and `doctor` called it healthy (#1088). A partial copy or an
// interrupted sync leaves exactly this — some files, not none.
func TestOneMissingBucketFileCountsAsDamage(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	// Two words far enough apart to land in different buckets, so removing one
	// file leaves the other's postings in place.
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-01-01T00:01:00Z", "pool exhausted"),
		claudeLine("s1", "2026-01-01T00:02:00Z", "vacuum deadlock"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index was reported as damaged")
	}

	buckets := filepath.Join(dir, "buckets")
	before := countBucketFiles(buckets)
	if before < 2 {
		t.Fatalf("this case needs more than one bucket file, got %d", before)
	}
	entries, err := os.ReadDir(buckets)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(buckets, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	// The premise: the directory is still there and still holds files, which is
	// everything the older check looked at.
	if !hasBucketFile(buckets) {
		t.Fatal("the probe emptied the directory; it is meant to leave files behind")
	}
	if !Damaged(dir) {
		t.Errorf("an index missing %d of %d bucket files was reported as intact", 1, before)
	}
}

// A manifest written before the count existed decodes with it zero, and must
// not send every store built by an older deja into a rebuild loop.
func TestAnUncountedManifestIsNotDamaged(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-01-01T00:01:00Z", "pool exhausted"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.BucketFiles = 0
	if err = writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	// writeManifestOnly restamps the count, so drop it the way an old build
	// left it: no stamp on disk at all.
	raw, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw.BucketFiles = 0
	if !recordsIntact(dir, raw) {
		t.Error("an index whose manifest carries no bucket count was called damaged")
	}
}

// The early index is a stopgap, so it has to be cheap whatever the newest
// sessions weigh. Counting sessions alone let 200 long recent ones hold more
// text than the rest of the store, and the stopgap answered later than a build
// without it finished (#4768).
func TestNewestSliceStopsAtTheMessageBudget(t *testing.T) {
	sess := func(msgs int) model.Session { return model.Session{Messages: make([]model.Message, msgs)} }
	long := make([]model.Session, 300)
	for i := range long {
		long[i] = sess(5000)
	}
	if n := newestSlice(long); n*5000 > partialPublishMessages || n < 1 {
		t.Errorf("200 long sessions: took %d (%d messages), budget %d", n, n*5000, partialPublishMessages)
	}
	short := make([]model.Session, 300)
	for i := range short {
		short[i] = sess(10)
	}
	if n := newestSlice(short); n != partialPublishSessions {
		t.Errorf("short sessions: took %d, want the %d-session cap", n, partialPublishSessions)
	}
	// The newest session goes in even when it alone is over the budget.
	if n := newestSlice([]model.Session{sess(partialPublishMessages * 3), sess(10)}); n != 1 {
		t.Errorf("oversized newest session: took %d, want 1", n)
	}
}

// A store can be half-readable: cursor keeps CLI transcripts as JSONL and its
// IDE sessions in SQLite, so without the sqlite3 CLI one half arrives and the
// other does not. The run narrated what it got and said nothing about the rest,
// while the same run names a store it could not read at all (#1758).
func TestNarrationNamesAHalfReadStore(t *testing.T) {
	got := harnessNarration("cursor", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, "sqlite3 CLI not found", 0, 0)
	if !strings.Contains(got, "1 session") {
		t.Errorf("the line lost its count: %q", got)
	}
	if !strings.Contains(got, "sqlite3 CLI not found") {
		t.Errorf("a half-read store says nothing about the half it could not read: %q", got)
	}

	// Nothing skipped: the line is what it always was.
	plain := harnessNarration("claude", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, "", 0, 0)
	if strings.Contains(plain, "—") {
		t.Errorf("a fully read store gained a caveat: %q", plain)
	}

	// The notes pseudo-source keeps its label.
	if n := harnessNarration("deja", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, "", 0, 0); !strings.HasPrefix(n, "deja: notes:") {
		t.Errorf("notes narrate as notes: %q", n)
	}
}

const wrappedPaste = "<pasted_content id=\"dd73\">\nSet up the zorblax reminder hook\n</pasted_content id=\"dd73\">"

func pasteTestStore(t *testing.T) (tmp, dir string) {
	t.Helper()
	tmp = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return tmp, dir
}

// claudeSession returns the stored session and its manifest row.
func claudeSession(t *testing.T, dir, id string) (string, SessionMeta) {
	t.Helper()
	ss, err := FindManyByIdentity(dir, []Identity{{Harness: "claude", ID: id}})
	if err != nil || len(ss) != 1 {
		t.Fatalf("session %s: %v, %d found", id, err, len(ss))
	}
	text := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		text = append(text, m.Text)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "claude" && m.ID == id {
			return strings.Join(text, "\n"), m
		}
	}
	t.Fatalf("no manifest row for %s", id)
	return "", SessionMeta{}
}

// A peer on an older build sends Claude Code prompts inside their
// <pasted_content> wrapper; they land as the pasted text, the way this
// machine's own reader stores them.
func TestASyncedPasteArrivesUnwrapped(t *testing.T) {
	tmp, dir := pasteTestStore(t)
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lines := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"<pasted_content id=\"dd73\">\nSet up the zorblax reminder hook\n</pasted_content id=\"dd73\">","time":"` + when + `"}` + "\n"
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-peer.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, m := range metas {
		if m.OrigID == "peer1" {
			id = m.ID
		}
	}
	if id == "" {
		t.Fatal("the synced session was not imported")
	}
	text, meta := claudeSession(t, dir, id)
	if text != "Set up the zorblax reminder hook" {
		t.Errorf("stored text = %q, want the pasted text alone", text)
	}
	if meta.Title != "Set up the zorblax reminder hook" {
		t.Errorf("title = %q", meta.Title)
	}
}

// Sessions a rebuild cannot re-read — a transcript the client deleted, one
// compacted away, one imported — are rebuilt from records.bin, which holds
// what the older reader stored. The version bump that takes the wrapper out
// has to take it out of those too, title included.
func TestARebuildUnwrapsAPasteAnOlderBuildStored(t *testing.T) {
	_, dir := pasteTestStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	const key = "claude:old1"
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	recs := map[string][]Record{key: {
		{Key: key, Role: "user", Text: wrappedPaste, Time: when, SourcePath: syncImportPath},
		{Key: key, Role: "assistant", Text: "<pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays", Time: when.Add(time.Minute), SourcePath: syncImportPath},
	}}
	metas := map[string]SessionMeta{key: {
		ID: "old1", Harness: "claude", Project: importedProjectPrefix + "svc", Path: syncImportPath,
		Title: truncateTitle(wrappedPaste, 60), Started: when, Updated: when.Add(time.Minute),
	}}
	if err := appendImportedRecords(dir, &m, recs, metas); err != nil {
		t.Fatal(err)
	}
	if _, meta := claudeSession(t, dir, "old1"); !strings.Contains(meta.Title, "pasted_content") {
		t.Fatalf("premise: the planted title is %q", meta.Title)
	}

	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	text, meta := claudeSession(t, dir, "old1")
	if want := "Set up the zorblax reminder hook\n<pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays"; text != want {
		t.Errorf("stored text = %q, want %q", text, want)
	}
	if meta.Title != "Set up the zorblax reminder hook" {
		t.Errorf("title = %q, want it derived again from the unwrapped prompt", meta.Title)
	}
}

// What a paste carries around an id: a chat wraps it in quotes or backticks,
// and deja's own screens print `harness:id` (#921).
func TestPastedSelectorStripsWhatAPasteCarries(t *testing.T) {
	const id = "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1"
	for _, tc := range []struct{ in, want string }{
		{id, id},
		{"  " + id + "  ", id},
		{`"` + id + `"`, id},
		{"'" + id + "'", id},
		{"`" + id + "`", id},
		{"“" + id + "”", id},
		{"‘" + id + "’", id},
		{"`\"" + id + "\"`", id},
		// Not a wrapping pair: a quote on one side is part of what was typed.
		{`"` + id, `"` + id},
		{"", ""},
		{`"`, `"`},
	} {
		if got := PastedSelector(tc.in); got != tc.want {
			t.Errorf("PastedSelector(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitSelectorTakesOnlyAHarnessPrefix(t *testing.T) {
	for _, tc := range []struct{ in, harness, id string }{
		{"claude:abc", "claude", "abc"},
		{"cursor-cli:abc", "cursor-cli", "abc"},
		{"abc", "", "abc"},
		// A colon at either end names no harness and no id.
		{":abc", "", ":abc"},
		{"claude:", "", "claude:"},
		// A left side that merely looks harness-shaped is split too — the
		// callers try the whole string first and then require the harness to
		// equal the session's own, so a split like this simply matches
		// nothing.
		{"2026-08-03T10:00:00Z", "2026-08-03T10", "00:00Z"},
		// A space is not part of any harness name.
		{"a b:c", "", "a b:c"},
	} {
		h, id := splitSelector(tc.in)
		if h != tc.harness || id != tc.id {
			t.Errorf("splitSelector(%q) = (%q, %q), want (%q, %q)", tc.in, h, id, tc.harness, tc.id)
		}
	}
}

// A session answers to its id, to the elided form, and to the harness:id shape
// — but only under its own harness.
func TestSelectorMatchesHonoursTheHarness(t *testing.T) {
	meta := SessionMeta{ID: "a1b2c3d4-1111", Harness: "claude"}
	for _, sel := range []string{"a1b2c3d4-1111", "a1b2", "claude:a1b2", "CLAUDE:a1b2c3d4-1111"} {
		if !selectorMatches(meta, sel) {
			t.Errorf("%q did not match", sel)
		}
	}
	for _, sel := range []string{"codex:a1b2", "b1b2", "claude:zzz"} {
		if selectorMatches(meta, sel) {
			t.Errorf("%q matched and should not have", sel)
		}
	}
}

// A rule written for a package has to reach an agent that asks by file: the
// agent about to edit cmd/reconcile/match.go recalls with that path, and the
// session that settled how cmd/reconcile changes are checked never names the
// file (#4762).
func TestFilePathRecallFindsTheRuleForItsDirectory(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("replay", "Rule: every change in cmd/reconcile is checked with the dry-run replay of the August bank file; expect 4117 matched.")
	write("cron", "The reconcile cron ran at 00:00 UTC but the bank file lands at 01:30 UTC.")
	write("pgx", "pgx v5.7.0 broke COPY of the NUMERIC amount columns in the bulk import.")
	write("readme", "Added a two-line README intro for ledgerd.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"cmd/reconcile/match.go",
		"cmd/reconcile/match.go tolerance",
		"/home/dev/ledger/cmd/reconcile/match.go",
		`cmd\reconcile\match.go`, // already split into words, ranks on them
		"cmd/reconcile/match.go amount tolerance 0.005 0.01",
	} {
		r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Sessions) == 0 || r.Sessions[0].ID != "replay" {
			var ids []string
			for _, s := range r.Sessions {
				ids = append(ids, s.ID)
			}
			t.Errorf("%q: got %v (tier %q), want the cmd/reconcile rule first", q, ids, r.Tier)
		}
	}
	// The file itself named in history still answers on the exact tier.
	write("file", "Changed the tolerance in cmd/reconcile/match.go to 0.005.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "cmd/reconcile/match.go", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 || r.Sessions[0].ID != "file" || r.Tier == query.TierRelevance {
		t.Errorf("exact file hit: tier %q, %d sessions", r.Tier, len(r.Sessions))
	}
}

func TestParentDirs(t *testing.T) {
	for q, want := range map[string][]string{
		"cmd/reconcile/match.go":                  {"cmd/reconcile"},
		"/home/dev/ledger/cmd/reconcile/match.go": {"home/dev/ledger/cmd/reconcile", "cmd/reconcile"},
		"README.md":           nil,
		"cmd/main.go":         nil, // "cmd" alone names half the corpus
		"cmd/reconcile":       nil, // a directory, not a file
		"pkg/index tolerance": nil,
	} {
		if got := parentDirs(RelevanceTerms(q)); !reflect.DeepEqual(got, want) {
			t.Errorf("parentDirs(%q) = %q, want %q", q, got, want)
		}
	}
}

// A long question that names a file is about more than the file. When a session
// already answers several of its other words, a session that merely mentions
// the file's directory must not be put ahead of it: on a real store a marathon
// that listed docs/registry once went to the top of a thirteen-word query whose
// answer had been second.
func TestDirectorySessionsDoNotJumpAnAnswerToTheRestOfTheQuery(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-site")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("review", "Review of the registry footer: the mobile table overflows and the grpfold toggle hides the CTA.")
	write("listing", "Listed the tree: docs/registry, docs/guide, cmd/deja, internal/index.")
	write("other", "Bumped the Go toolchain in CI.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "docs/registry/deepseek.html footer mobile table grpfold CTA", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "review" {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("got %v (directory %q), want the review that answers the rest of the query first", ids, r.Directory)
	}
	if r.Directory != "" {
		t.Errorf("answer labelled as about directory %q although the lead session is not", r.Directory)
	}
}

// Several sessions touched the directory, and the one holding the package rule
// also says the question's words. It has to lead, and the order has to be the
// same on every call: the directory's sessions came back in map order, so the
// rule was on a five-session page in some calls and off it in others.
func TestDirectoryLeadIsRankedAndStable(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-quill")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, sid := range []string{"port", "drafts", "outdir", "feed", "layout", "thumbs"} {
		write(sid, "Updated cmd/quill/"+sid+".go.")
	}
	// One of the question's words, not two: two would make it an answer to the
	// rest of the query and the directory lead would not run at all.
	write("check", "go test is not enough: before you call a change to cmd/quill done, run scripts/check.sh for verification.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var first []string
	for i := range 8 {
		r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "cmd/quill/main.go final verification quiet banner", All: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Directory == "" {
			t.Fatalf("call %d: the directory lead did not run, tier %q", i, r.Tier)
		}
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		if len(ids) == 0 || ids[0] != "check" {
			t.Fatalf("call %d: got %v (directory %q), want the rule first", i, ids, r.Directory)
		}
		if first == nil {
			first = ids
		} else if !reflect.DeepEqual(ids, first) {
			t.Fatalf("call %d: order %v, first call %v", i, ids, first)
		}
	}
}

func TestAnswersTheRestCountsWordsNotSubstrings(t *testing.T) {
	ss := []model.Session{{Messages: []model.Message{{Text: "Forget the setting, the numerical drift is fine."}}}}
	if answersTheRest(ss, RelevanceTerms("cmd/x/y.go set numeric")) {
		t.Error(`"set" in "setting" and "numeric" in "numerical" counted as the query's words`)
	}
	ss[0].Messages[0].Text = "Set the numeric column default."
	if !answersTheRest(ss, RelevanceTerms("cmd/x/y.go set numeric")) {
		t.Error("two of the query's words, whole, were not counted")
	}
}

// Off a terminal, a build says each phase on a line of its own, with the
// count it works through (#5396).
func TestABuildWritesOneLinePerPhase(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "phaseneedle"))
	var out bytes.Buffer
	SetPhaseLines(&out)
	err := Ensure(dir, "", true, nil)
	SetPhaseLines(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"deja: finding transcripts", "deja: reading sessions — 1 file", "deja: indexing messages — 1 message", "deja: writing index"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q line:\n%s", want, out.String())
		}
	}
	n := out.Len()
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != n {
		t.Errorf("a build after SetPhaseLines(nil) still wrote:\n%s", out.String()[n:])
	}
}

// The lines a pass skipped are counted for the run that skipped them, and a
// pass that reads nothing new adds none (#5396).
func TestSkippedThisRunCountsOnlyWhatThisPassRead(t *testing.T) {
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, p, claudeLine("a1", "2026-01-02T03:04:05Z", "skipneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	before := SkippedThisRun()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := SkippedThisRun() - before; got != 1 {
		t.Fatalf("the pass that read the bad line counted %d, want 1", got)
	}
	before = SkippedThisRun()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := SkippedThisRun() - before; got != 0 {
		t.Fatalf("a pass that read nothing new counted %d", got)
	}
}

// Past the first second each line says how far into the build it is.
func TestAPhaseLineSaysHowFarIn(t *testing.T) {
	var out bytes.Buffer
	p := &phaseWriter{w: &out, started: time.Now().Add(-3 * time.Second)}
	p.phase("writing index", 12)
	if got := out.String(); got != "deja: writing index (3s in)\n" {
		t.Fatalf("got %q", got)
	}
}

// omp, OpenClaw and prime write pi-format transcripts and have offset parsers
// in the registry, but the append gate never listed them, so every pass that
// saw one new turn re-read the whole file. pi, the fourth reader of the same
// shape, has been on the list all along.
//
// What has to hold before they join it: an appended turn lands in the session
// it continues, with the identity the whole read gives — which is what #2870's
// header fix made true for this reader.
func TestThePiShapedStoresAppendInsteadOfRereading(t *testing.T) {
	head := func(id, cwd string) string {
		return `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-01-02T03:04:05Z","cwd":"` + cwd + `"}` + "\n"
	}
	turn := func(role, ts, text string) string {
		return `{"type":"message","id":"m-` + ts + `","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}

	cases := []struct {
		harness string
		env     string
		// rel is the transcript path under the store root.
		rel string
	}{
		{"omp", "DEJA_OMP_ROOT", filepath.Join("-tmp-demo", "2026-01-02T03-04-05_ses-a.jsonl")},
		{"openclaw", "DEJA_OPENCLAW_ROOT", filepath.Join("agent-1", "sessions", "ses-b.jsonl")},
		{"prime", "DEJA_PRIME_ROOT", filepath.Join("-tmp-demo", "2026-01-02T03-04-05_ses-c.jsonl")},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			dir := t.TempDir()
			setHome(t, filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", os.Getenv("HOME"))
			store := filepath.Join(root, tc.harness)
			t.Setenv(tc.env, store)
			path := filepath.Join(store, tc.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			id := "ses-" + tc.harness
			body := head(id, "/tmp/demo") + turn("user", "2026-01-02T03:04:10Z", "why does pgbouncer time out")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			before := currentFileStateFor(t, path)

			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.WriteString(turn("assistant", "2026-01-02T03:05:00Z", "raised the pool to 40")); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			after := currentFileStateFor(t, path)
			if !canAppendIncremental(map[string]FileState{path: after}, map[string]FileState{path: before}) {
				t.Fatalf("%s still re-reads the whole file for one appended turn", tc.harness)
			}
			if err = Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}

			// The appended turn has to be findable, in the session it continues
			// and not beside it — the failure the header fix removed.
			all, err := Recent(dir, 50)
			if err != nil {
				t.Fatal(err)
			}
			var mine []string
			for _, s := range all {
				if s.Harness == tc.harness {
					mine = append(mine, s.ID)
				}
			}
			if len(mine) != 1 {
				t.Fatalf("%s: %d sessions after the append (%v), want the one it continued",
					tc.harness, len(mine), mine)
			}
			if mine[0] != id {
				t.Errorf("%s: session id = %q, want the header's %q", tc.harness, mine[0], id)
			}

			// Searched for, not listed: Recent carries metadata and no message
			// bodies, and asserting on those found nothing even after a full
			// rebuild — the instrument, not the index.
			hits, err := Search(dir, query.Options{Query: "raised the pool to 40", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, s := range hits {
				if s.Harness != tc.harness {
					continue
				}
				for _, m := range s.Messages {
					if strings.Contains(m.Text, "raised the pool to 40") {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("%s: the appended turn is not in the index", tc.harness)
			}
		})
	}
}

// planWall is a friction line by isFriction's rules: it names something
// specific, carries no quote or comment marker, and sits inside the length
// window. Its identifier is the only term the plan and the wall share.
const planWall = "ModuleNotFoundError: No module named pytest_asyncio"

// planCorpus builds a store where term frequency is set deliberately, because
// the two-tier floor is a claim about frequency and shape together:
//
//	pytest_asyncio  identifier, 20 of 59 sessions -> idf 1.05
//	cache, queue    plain,      20 of 59 sessions -> idf 1.05
//	stale, flush    plain,       4 of 59 sessions -> idf 2.48
//
// So the identifier and the plain pair at 20 differ only in shape, and the
// plain pair at 4 shows a plain term still has a route through.
//
// The wall is in the first four sessions, which is FrictionMinSessions plus
// one, and commands is how many of those also log a command record.
func planCorpus(t *testing.T, commands int) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "plan-index")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	sessions := make([]model.Session, 0, 59)
	for i := range 59 {
		s := model.Session{
			ID:      "s" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Harness: "claude",
			Project: "p",
			Updated: base.Add(time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "session about unrelated frontend styling work"},
			},
		}
		if i < 20 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "pytest_asyncio cache queue setup for the suite",
			})
		}
		if i >= 20 {
			// hydration never shares a session with stale, and purge_cache is
			// an identifier that only these four hold — both are here so a
			// term's reach can be checked against sessions that carry no wall.
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "hydration pass on the client",
			})
		}
		if i >= 20 && i < 24 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "purge_cache wipe between the runs",
			})
		}
		if i < 4 {
			s.Messages = append(s.Messages,
				model.Message{Role: "user", Text: "stale flush before the run"},
				model.Message{Role: "tool-output", Text: planWall},
			)
		}
		if i < commands {
			s.Messages = append(s.Messages, model.Message{
				Role: "command", Text: "pip install pytest_asyncio",
			})
		}
		sessions = append(sessions, s)
	}

	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func planSteps(terms ...string) [][]string { return [][]string{terms} }

func TestPlanFrictionMatchesReportsAWallSharingAPlanTerm(t *testing.T) {
	dir := planCorpus(t, 0)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(out), out)
	}
	if out[0].Wall.Text != planWall {
		t.Fatalf("wall text = %q", out[0].Wall.Text)
	}
	if len(out[0].Wall.Sessions) != 4 {
		t.Fatalf("carrier sessions = %d, want 4", len(out[0].Wall.Sessions))
	}
	// No command record exists here, and the finding still stands. The command
	// clause is a strengthening, not a precondition — 84 of 110 carrier
	// sessions in the census that motivated this hold no command at all.
	if out[0].Command != "" {
		t.Fatalf("command invented from a store with none: %q", out[0].Command)
	}
}

// The wall naming a plan term is the whole claim. A plan term that is in the
// index but absent from the wall text has nothing to say about that wall.
func TestPlanFrictionMatchesStaysSilentWhenTheWallNamesNothingInThePlan(t *testing.T) {
	dir := planCorpus(t, 0)
	// "stale flush" is in all four carrier sessions and clears the plain
	// floor, so the step survives selection and the sessions are candidates.
	// Only the wall text check stands between it and a finding.
	if steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("stale", "flush")); len(steps) != 1 {
		t.Fatalf("premise broken: step did not survive selection (%d steps)", len(steps))
	}
	if out := PlanFrictionMatches(dir, planSteps("stale", "flush"), nil, 0); out != nil {
		t.Fatalf("reported a wall that names no plan term: %+v", out)
	}
}

// keep runs before any session is materialized, so a denied project's text
// must not reach the caller.
func TestPlanFrictionMatchesRespectsTheActivationKeep(t *testing.T) {
	dir := planCorpus(t, 0)
	seen := 0
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), func(SessionMeta) bool {
		seen++
		return false
	}, 0)
	if out != nil {
		t.Fatalf("denied project crossed the boundary: %+v", out)
	}
	if seen == 0 {
		t.Fatal("keep was never consulted")
	}
}

// A plan is submitted at a moment when nothing may block on indexing, so a
// missing index is a silent miss and never a build.
func TestPlanFrictionMatchesNeverBuildsAnIndex(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "empty-index")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0); out != nil {
		t.Fatalf("findings from an empty index: %+v", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("index directory was written to: %v", entries)
	}
}

func TestPlanFrictionMatchesReportsACommandWhenOneWasLogged(t *testing.T) {
	dir := planCorpus(t, 2)
	// Two steps, and only the first one reaches the wall's carriers or is
	// named by the wall text. The second must contribute nothing rather than
	// borrow the first step's command.
	steps := [][]string{{"pytest_asyncio"}, {"purge_cache"}}
	out := PlanFrictionMatches(dir, steps, nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1", len(out))
	}
	if out[0].Command != "pip install pytest_asyncio" {
		t.Fatalf("command = %q", out[0].Command)
	}
	if len(out[0].CommandSessions) != 2 {
		t.Fatalf("command sessions = %d, want the 2 that logged it", len(out[0].CommandSessions))
	}
}

// A plan term that reaches only sessions carrying no wall is not a finding:
// the join is wall-to-plan, and half of it missing is silence.
func TestPlanFrictionMatchesStaysSilentWhenNoWallCarrierMatches(t *testing.T) {
	dir := planCorpus(t, 0)
	// purge_cache is indexed and clears the floor, but the four sessions that
	// hold it are not the four that hit the wall.
	if steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("purge_cache")); len(steps) != 1 {
		t.Fatalf("premise broken: purge_cache did not survive selection")
	}
	if out := PlanFrictionMatches(dir, planSteps("purge_cache"), nil, 0); out != nil {
		t.Fatalf("wall reported for a term no carrier holds: %+v", out)
	}
}

// A step's terms have to land on one session together, not merely exist.
func TestPlanIndexedStepsNeedsTermsInTheSameSession(t *testing.T) {
	dir := planCorpus(t, 0)
	// Both clear the plain floor at 4 sessions each, and neither session set
	// overlaps the other, so no session holds the two the step needs.
	steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("stale", "wipe"))
	if len(steps) != 0 {
		t.Fatalf("step survived with its terms in disjoint sessions: %+v", steps)
	}
}

func TestPlanFrictionMatchesIgnoresEmptyInput(t *testing.T) {
	dir := planCorpus(t, 0)
	if out := PlanFrictionMatches(dir, nil, nil, 0); out != nil {
		t.Fatalf("no steps produced %+v", out)
	}
	if out := PlanFrictionMatches(dir, planSteps("", "   "), nil, 0); out != nil {
		t.Fatalf("blank terms produced %+v", out)
	}
}

// Wall texts for the ordering corpus. All five name the same identifier, so
// which ones come back — and in what order — is decided by the clustering
// alone.
const (
	wallFive     = planWall
	wallLater    = "connection refused fetching pytest_asyncio from the mirror"
	wallTieOne   = "ImportError: cannot find module pytest_asyncio in the venv"
	wallTieTwo   = "fatal: pytest_asyncio plugin refused to load under the runner"
	wallTooRare  = "permission denied while installing pytest_asyncio locally"
	planWallSeen = 5
)

// planOrderCorpus spreads five walls over overlapping session sets so the
// ordering rule is observable: more carriers first, then the newest carrier,
// then the hash. wallTooRare is under FrictionMinSessions and must not appear
// at all.
//
//	wallFive     sessions 0-4   5 carriers
//	wallLater    sessions 3-5   3 carriers, newest is session 5
//	wallTieOne   sessions 0-2   3 carriers, newest is session 2
//	wallTieTwo   sessions 0-2   3 carriers, the same sessions as wallTieOne
//	wallTooRare  sessions 0-1   2 carriers
func planOrderCorpus(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "plan-order-index")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	walls := map[string][2]int{
		wallFive: {0, 5}, wallLater: {3, 6},
		wallTieOne: {0, 3}, wallTieTwo: {0, 3}, wallTooRare: {0, 2},
	}

	sessions := make([]model.Session, 0, 59)
	for i := range 59 {
		s := model.Session{
			ID:      "o" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Harness: "claude",
			Project: "p",
			Updated: base.Add(time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "session about unrelated frontend styling work"},
			},
		}
		if i < 20 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "pytest_asyncio setup for the suite",
			})
		}
		// Sorted so the records land in a stable order run to run.
		for _, text := range []string{wallFive, wallLater, wallTieOne, wallTieTwo, wallTooRare} {
			span := walls[text]
			if i >= span[0] && i < span[1] {
				s.Messages = append(s.Messages, model.Message{Role: "tool-output", Text: text})
			}
		}
		sessions = append(sessions, s)
	}

	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanFrictionMatchesOrdersWallsAndDropsTheRareOne(t *testing.T) {
	dir := planOrderCorpus(t)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 4 {
		t.Fatalf("findings = %d, want 4 (wallTooRare excluded): %+v", len(out), out)
	}
	for _, hit := range out {
		if hit.Wall.Text == wallTooRare {
			t.Fatal("a wall in 2 sessions was reported as recurring")
		}
	}
	if out[0].Wall.Text != wallFive || len(out[0].Wall.Sessions) != planWallSeen {
		t.Fatalf("most-carried wall not first: %q with %d sessions",
			out[0].Wall.Text, len(out[0].Wall.Sessions))
	}
	// Equal carrier counts, so the newer cluster wins.
	if out[1].Wall.Text != wallLater {
		t.Fatalf("newest of the three-carrier walls not second: %q", out[1].Wall.Text)
	}
	// wallTieOne and wallTieTwo share their carriers exactly, so only the hash
	// separates them — the order is fixed, which one wins is not asserted.
	rest := map[string]bool{out[2].Wall.Text: true, out[3].Wall.Text: true}
	if !rest[wallTieOne] || !rest[wallTieTwo] {
		t.Fatalf("tied walls missing: %q, %q", out[2].Wall.Text, out[3].Wall.Text)
	}
	again := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if again[2].Wall.Text != out[2].Wall.Text {
		t.Fatal("tie between identical clusters is not stable")
	}
}

func TestPlanFrictionMatchesStopsAtTheLimit(t *testing.T) {
	dir := planOrderCorpus(t)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 2)
	if len(out) != 2 {
		t.Fatalf("limit 2 returned %d", len(out))
	}
	// A limit keeps the front of the order, so it drops the weakest walls.
	if out[0].Wall.Text != wallFive || out[1].Wall.Text != wallLater {
		t.Fatalf("limit changed the order: %q, %q", out[0].Wall.Text, out[1].Wall.Text)
	}
}

// An empty dir means the default index, which the hermetic env points at a
// path that was never built.
func TestPlanFrictionMatchesFallsBackToTheDefaultDir(t *testing.T) {
	_ = hermeticIndexEnv(t)
	if out := PlanFrictionMatches("", planSteps("pytest_asyncio"), nil, 0); out != nil {
		t.Fatalf("findings from an unbuilt default index: %+v", out)
	}
}

func TestPlanTermSessionsIntersectsEveryKey(t *testing.T) {
	dir := planCorpus(t, 0)
	queue := planTermSessions(dir, "queue")
	if len(queue) == 0 {
		t.Fatal("premise broken: queue is not in the index")
	}
	// styling is in every session, so the pair is bounded by the narrower key
	// rather than the union of the two.
	if both := planTermSessions(dir, "styling queue"); len(both) != len(queue) {
		t.Fatalf("pair reached %d sessions, queue alone reaches %d", len(both), len(queue))
	}
	// hydration and stale are each indexed, and never in one session.
	if got := planTermSessions(dir, "hydration stale"); len(got) != 0 {
		t.Fatalf("terms that never co-occur intersected to %d sessions", len(got))
	}
	if got := planTermSessions(dir, "-"); got != nil {
		t.Fatalf("a term with no searchable key reached %d sessions", len(got))
	}
}

func mustPlanManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	manifest, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// The central claim of the two-tier floor: at one frequency, shape decides.
// Wall vocabulary is repeated-work vocabulary and so is common by
// construction, which is why the prompt floor at 2.0 excluded every one of
// these terms.
func TestPlanIndexedStepsGivesIdentifiersALowerFloor(t *testing.T) {
	dir := planCorpus(t, 0)
	manifest := mustPlanManifest(t, dir)

	// Same 20 sessions, so the same idf. Only the shape differs.
	identifier, _, needs := planIndexedSteps(dir, manifest, planSteps("pytest_asyncio"))
	if len(identifier) != 1 || len(identifier[0]) != 1 || identifier[0][0].text != "pytest_asyncio" {
		t.Fatalf("identifier term dropped: %+v", identifier)
	}
	if needs[0] != 1 {
		t.Fatalf("identifier step need = %d, want 1", needs[0])
	}
	if plain, _, _ := planIndexedSteps(dir, manifest, planSteps("cache", "queue")); len(plain) != 0 {
		t.Fatalf("plain terms at the same frequency survived: %+v", plain)
	}

	// And the plain floor is a floor, not a ban: rarer plain terms clear it.
	rare, _, rareNeeds := planIndexedSteps(dir, manifest, planSteps("stale", "flush"))
	if len(rare) != 1 || len(rare[0]) != 2 {
		t.Fatalf("rare plain terms dropped: %+v", rare)
	}
	if rareNeeds[0] != 2 {
		t.Fatalf("plain step need = %d, want 2", rareNeeds[0])
	}
}

func TestPlanIndexedStepsSkipsTermsTheIndexNeverSaw(t *testing.T) {
	dir := planCorpus(t, 0)
	steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("zzz_unseen_identifier"))
	if len(steps) != 0 {
		t.Fatalf("term absent from the index survived: %+v", steps)
	}
}

func TestPlanIsIdentifierTerm(t *testing.T) {
	for _, term := range []string{"pytest", "hermes-agent", "v1.2", "a_b", "go/mod", "x9"} {
		if !planIsIdentifierTerm(term) {
			t.Errorf("%q not identifier-shaped", term)
		}
	}
	for _, term := range []string{"cache", "queue", "stale", "the", ""} {
		if planIsIdentifierTerm(term) {
			t.Errorf("%q read as identifier-shaped", term)
		}
	}
	if !planHasIdentifierTerm([]string{"the", "cache", "pytest_asyncio"}) {
		t.Error("step with one identifier read as plain")
	}
	if planHasIdentifierTerm([]string{"the", "cache", "queue"}) {
		t.Error("plain step read as carrying an identifier")
	}
}

func TestPlanTextHasTerm(t *testing.T) {
	if !planTextHasTerm(planWall, "pytest_asyncio") {
		t.Error("wall does not match its own identifier")
	}
	if planTextHasTerm(planWall, "cache") {
		t.Error("wall matched a term it does not contain")
	}
	if planTextHasTerm(planWall, "   ") {
		t.Error("blank term matched")
	}
}

func TestPlanCommandForStepPrefersTheStrongestThenShortest(t *testing.T) {
	terms := []planIndexedTerm{{text: "pytest"}, {text: "asyncio"}}
	session := model.Session{Messages: []model.Message{
		{Role: "user", Text: "pytest asyncio talk, not a command"},
		{Role: "command", Text: "   \t  "},
		{Role: "command", Text: "pytest -q"},
		{Role: "command", Text: "  pip   install   pytest   asyncio  "},
		{Role: "command", Text: "pip install pytest asyncio --upgrade"},
	}}
	// Two terms beat one, and among equals the shorter command wins. Fields
	// collapsing is what makes the padded line the shorter of the two.
	if got := planCommandForStep(session, terms, 2); got != "pip install pytest asyncio" {
		t.Fatalf("command = %q", got)
	}
	// need is a floor: nothing clears 3 matches here.
	if got := planCommandForStep(session, terms, 3); got != "" {
		t.Fatalf("command %q cleared an impossible need", got)
	}
}

// The "es" case of the suffix switch returned before the "s" case could run,
// so the plural of every noun ending in "e" reached only a stub — "pipelin",
// never "pipeline" — and the fuzzy rung answered instead, telling the reader
// their correct plural was a misspelling (#2137).
func TestAPluralReachesTheSingularOfAnEEndingNoun(t *testing.T) {
	for _, tc := range []struct{ plural, singular string }{
		{"pipelines", "pipeline"},
		{"caches", "cache"},
		{"releases", "release"},
		{"queues", "queue"},
		// The "es" strip is right for these, and stays right.
		{"boxes", "box"},
		{"matches", "match"},
		{"classes", "class"},
		// #1079's case keeps working: consonant+y goes through "ies".
		{"retries", "retry"},
		// The "ies" case had the same shape as the "es" one: a word that is
		// already "ie" plus "s" needs only the "s" gone.
		{"movies", "movie"},
		{"cookies", "cookie"},
	} {
		forms := suffixForms(tc.plural)
		if !slices.Contains(forms, tc.singular) {
			t.Errorf("suffixForms(%q) has no %q: %q", tc.plural, tc.singular, forms)
		}
		// And the catalog decides, so a store that holds the singular finds it.
		got := stemMatches(tc.plural, map[string]bool{tc.singular: true})
		if !slices.Contains(got, tc.singular) {
			t.Errorf("stemMatches(%q) = %q, want it to reach %q", tc.plural, got, tc.singular)
		}
	}
}

// The session id is written as a delta now, so the property that matters is
// that any block of postings survives the round trip — not a list of cases
// someone thought of. The extremes are seeded deliberately because a zigzag
// over a uint32 subtraction is where an off-by-one lives: 0, 1<<31, MaxUint32,
// and the steps between them (#492).
func TestAnyBlockOfPostingsSurvivesTheRoundTrip(t *testing.T) {
	extremes := []uint32{0, 1, 2, math.MaxUint32, math.MaxUint32 - 1, 1 << 31, (1 << 31) - 1, 1<<31 + 1}
	rng := rand.New(rand.NewSource(11)) //nolint:gosec // a seeded fixture, not crypto
	for round := range 300 {
		n := 1 + rng.Intn(12)
		posts := make([]posting, 0, n)
		off := int64(rng.Intn(1000))
		for range n {
			// Offsets must ascend: a block is what writeBucket produced, and
			// it sorts by offset. The session ids are deliberately not sorted.
			off += int64(1 + rng.Intn(5000))
			var sid uint32
			switch rng.Intn(3) {
			case 0:
				sid = extremes[rng.Intn(len(extremes))]
			case 1:
				sid = uint32(rng.Intn(50)) //nolint:gosec // Intn(50) fits
			default:
				sid = rng.Uint32()
			}
			posts = append(posts, posting{Off: off, Sid: sid, Tool: rng.Intn(2) == 1})
		}
		got := decodePostings(encodePostings(posts))
		want := sortedUniquePostings(posts)
		if len(got) != len(want) {
			t.Fatalf("round %d: %d postings came back from %d", round, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round %d posting %d: got %+v, want %+v (block %+v)", round, i, got[i], want[i], want)
			}
		}
	}
}

// And the delta is scoped to its own block: two blocks encoded one after the
// other must not read each other's last session id.
func TestOneBlocksSessionIdsDoNotLeakIntoTheNext(t *testing.T) {
	first := []posting{{Off: 1, Sid: math.MaxUint32}, {Off: 2, Sid: 5}}
	second := []posting{{Off: 1, Sid: 7}}
	if got := decodePostings(encodePostings(second)); len(got) != 1 || got[0].Sid != 7 {
		t.Fatalf("a block read on its own gave %+v", got)
	}
	// Encoding the first block before the second must not change what the
	// second one decodes to.
	_ = encodePostings(first)
	if got := decodePostings(encodePostings(second)); len(got) != 1 || got[0].Sid != 7 {
		t.Fatalf("the previous block's session id leaked: %+v", got)
	}
}

// Two harnesses can carry the same id, and there the advice "use a longer
// prefix" cannot be followed — the ids are the same string (#719).
func TestPrefixHarnesses(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	claude := filepath.Join(tmp, "claude", "proj-p")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj-z", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"claude side"}}`)
	write(filepath.Join(claude, "abc99999.jsonl"),
		`{"type":"user","sessionId":"abc99999","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"other claude session"}}`)
	write(filepath.Join(qwen, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","parts":[{"text":"qwen side"}]}}`)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if got := PrefixHarnesses(dir, "abc12345"); strings.Join(got, "|") != "claude|qwen" {
		t.Errorf("shared id → %v", got)
	}
	// A prefix is not an id: "abc" matches three sessions but names none.
	if got := PrefixHarnesses(dir, "abc"); len(got) != 0 {
		t.Errorf("prefix → %v", got)
	}
	if got := PrefixHarnesses(dir, "abc99999"); strings.Join(got, "|") != "claude" {
		t.Errorf("unique id → %v", got)
	}
	if got := PrefixHarnesses(dir, ""); got != nil {
		t.Errorf("empty → %v", got)
	}
	if got := PrefixHarnesses(dir, "nothing-like-this"); len(got) != 0 {
		t.Errorf("missing → %v", got)
	}
}

// forget only grows the tombstone set, and rewriting the whole file was the
// cost of the command on a machine with a large history (#1029). Appending
// must leave the set readable and complete.
func TestAppendTombstonesKeepsEarlierKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := appendTombstones([]string{"claude:b", "claude:a"}); err != nil {
		t.Fatal(err)
	}
	if err := appendTombstones([]string{"claude:c"}); err != nil {
		t.Fatal(err)
	}
	// A no-op call must not create noise in the file.
	if err := appendTombstones(nil); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	for _, want := range []string{"claude:a", "claude:b", "claude:c"} {
		if !got[want] {
			t.Errorf("%q missing from the set after append: %v", want, got)
		}
	}
	raw, err := os.ReadFile(filepath.Join(privacyDir(), "tombstones"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Errorf("file holds %d lines, want 3:\n%s", lines, raw)
	}
}

// skipWindowsPortability is kept as the single place a windows-only skip would
// go, and is deliberately unused: the skips it held are removed, so windows CI
// reports what actually fails rather than reporting nothing. See #1119.
//
//nolint:unused // kept for the unix-path fixtures #1119 tracks
func skipWindowsPortability(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-path fixture; windows portability tracked in #1119")
	}
}
