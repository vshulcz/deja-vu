package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two doors, two answers to "another deja is building the index". `deja search`
// answers from the store as it stands and asks for a refresh in the background;
// the doors that must see a current store — `deja files` among them — take the
// blocking lock and say so, which is what #994 added the notice for. Both
// halves were only ever true by inspection: `mcp_no_block_test.go` pins the MCP
// server's side and `internal/index/lock_wait_test.go` pins that the wait is
// reported once, and nothing said what a person at a terminal gets — which of
// the two doors they are standing at decides whether they wait out a rebuild.
func TestASearchDoesNotWaitBehindAPassWhereTheOtherDoorsDo(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// Enough of a store that a rebuild is long against a search: the point is
	// the difference between the two, not either number.
	const sessions = 600
	for i := range sessions {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": fmt.Sprintf("s%03d", i), "cwd": "/tmp/app",
			"timestamp": stamp,
			"message": map[string]any{"role": "user", "content": fmt.Sprintf(
				"turn %d: the pool timed out during the migration and we raised max_conns", i)},
		})
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("s%03d.jsonl", i)), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	// behind runs one command while a rebuild holds the lock. It reports
	// whether the pass was still running when the command started — a door
	// that blocks returns after the pass is over by definition, so asking
	// afterwards would answer nothing — and whether it was still running when
	// the command came back, which is what "did not wait" looks like.
	behind := func(args ...string) (out, said string, startedDuring, returnedDuring bool) {
		t.Helper()
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(done)
			_ = index.Ensure(dir, "", true, nil)
		})
		time.Sleep(10 * time.Millisecond) // long enough for the pass to hold the lock
		select {
		case <-done:
		default:
			startedDuring = true
		}
		said = captureStderr(t, func() {
			var runErr error
			out, runErr = captureRun(t, args...)
			if runErr != nil {
				t.Fatalf("%v: %v", args, runErr)
			}
		})
		select {
		case <-done:
		default:
			returnedDuring = true
		}
		wg.Wait()
		return out, said, startedDuring, returnedDuring
	}

	// The blocking doors: they come back only once the pass is over.
	for _, args := range [][]string{
		{"files", "migration"},
		{"view", "--no-open", "--out", filepath.Join(t.TempDir(), "v.html")},
	} {
		_, _, started, returned := behind(args...)
		switch {
		case !started:
			t.Logf("the rebuild was over before %s started: nothing to say about waiting this time", args[0])
		case returned:
			t.Errorf("%s takes the blocking lock and came back while the pass was still running", args[0])
		}
	}

	// And the search, which must come back while the pass is still going: it
	// answers from the store as it stands and asks for the refresh in the
	// background rather than queueing behind a rebuild.
	out, said, searchStarted, searchReturned := behind("search", "migration")
	if !strings.Contains(out, "s001") {
		t.Fatalf("the search found nothing, so this measures nothing:\n%s", out)
	}
	// What it says is the part that does not depend on the clock: the search
	// took the store as it stands and asked for the refresh in the background.
	if !strings.Contains(said, "answering from the index of") {
		t.Errorf("the search did not answer from the store as it stands:\n%s", said)
	}
	if searchStarted && !searchReturned {
		t.Log("the search came back after the pass had finished: a slow box, or a rebuild that ended early")
	}
}

// The default result cap (15) was silent: 15 hits looked like the whole
// answer and nothing pointed at --all for the rest. deja narrates every other
// place the ladder hides a session, so a capped search says so too.
func TestSearchCapNamesTheHiddenRest(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two sessions match; a --limit of 1 caps it, which is the same shape the
	// default cap makes when more than 15 match.
	for _, id := range []string{"capone", "captwo"} {
		line := `{"type":"user","message":{"role":"user","content":"capneedle here in ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "--no-embed", "--limit", "1", "capneedle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "showing 1 of 2") || !strings.Contains(out, "--all") {
		t.Errorf("capped search hid the rest silently:\n%s", out)
	}

	// --all shows everything, so there is nothing to point at.
	out, err = captureRunStderr(t, "--no-embed", "--all", "capneedle")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "showing") && strings.Contains(out, "of") {
		t.Errorf("--all should not announce a cap:\n%s", out)
	}
}

// Search ranks on recency among other things, so a session stamped in the
// future is the one it places first — and it was the only date-ordered surface
// that printed "Jan 1 2099" beside an answer and said nothing. `last`, the
// brief, doctor and MCP recall have all named it since #696 and #2104 (#3595).
func TestSearchSaysWhenAHitIsStampedAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "work", "now.jsonl"), "today", []string{
		`{"type":"user","sessionId":"today","timestamp":"2026-09-13T09:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, checked the logs"}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "work", "ahead.jsonl"), "ahead", []string{
		`{"type":"user","sessionId":"ahead","timestamp":"2099-01-01T00:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, wrong clock"}}`,
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "search", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "stamped later than this machine's clock") {
		t.Errorf("search printed a date that has not happened and did not say so: %q", note)
	}

	// And it stays quiet on a store where every stamp is in the past, or the
	// sentence is noise on every search anyone ever runs.
	tmp2 := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "now.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"2026-09-13T09:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, checked the logs"}}`,
	})
	if err = index.Ensure(filepath.Join(tmp2, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	note, err = captureRunStderr(t, "search", "api")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "stamped later") {
		t.Errorf("search warned about a clock on a store with no future stamps: %q", note)
	}
}

// The counter `deja search` prints through must say what it wraps, or the
// printer cannot tell it is writing to a terminal (#4620).
func TestCountingWriterUnwrapsToTheTerminal(t *testing.T) {
	c := &countingWriter{w: os.Stdout}
	var u interface{ Unwrap() io.Writer } = c
	if u.Unwrap() != os.Stdout {
		t.Fatalf("countingWriter unwraps to %v, want os.Stdout", u.Unwrap())
	}
}

// A one-word query that names a subcommand used to run it. Two of those
// commands start an editor, so a search could launch a whole agent.
func TestBareWrapperNamesSearchInsteadOfLaunching(t *testing.T) {
	for _, name := range []string{"aider", "goose"} {
		t.Setenv("DEJA_INDEX_DIR", t.TempDir())
		// With no arguments the wrapper must not reach exec.LookPath at all.
		// Searching an empty index is quiet and harmless, which is the point.
		if err := run([]string{name}); err != nil && strings.Contains(err.Error(), "not on PATH") {
			t.Fatalf("bare `deja %s` tried to launch the harness: %v", name, err)
		}
	}
}

// The plugins shell out with whatever the user typed, so they need a form
// that can never be mistaken for a command.
func TestPluginsUseTheExplicitSearchForm(t *testing.T) {
	for name, js := range map[string]string{
		"pi":     piExtensionTS("/bin/deja"),
		"cline":  clinePluginJS("/bin/deja"),
		"hermes": hermesPluginPy("/bin/deja"),
	} {
		if !strings.Contains(js, `"search"`) {
			t.Fatalf("%s plugin passes the query as the first argument: a query of \"index\" would rebuild the index", name)
		}
	}
}

func TestSearchCommandNeedsAQuery(t *testing.T) {
	if err := cmdSearch(t.TempDir(), nil, ""); err == nil {
		t.Fatal("empty search accepted")
	}
}

// Indexing splits a compound — `identifierParts` is why `retry backoff` reaches
// a store that wrote `retry-backoff` — and querying did not, so the same
// difference the other way round found nothing at all (#2125).
func TestAHyphenatedQueryReachesTheWordsSpelledApart(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message": map[string]any{"role": "user", "content": "the retry-backoff helper keeps blowing up under load; " +
			"we raised the pool size"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise and the symmetry: the store's own spellings match, and so
	// does the compound spelled apart.
	for _, q := range []string{"retry-backoff", "retry backoff", "blowing up", "pool size"} {
		if out, _ := captureRun(t, "search", q); !strings.Contains(out, "s1") {
			t.Fatalf("%q finds nothing, so this measures nothing", q)
		}
	}
	// The direction that had nothing: a hyphen or an underscore where the
	// transcript had a space.
	for _, q := range []string{"blowing-up", "pool_size"} {
		out, _ := captureRun(t, "search", q)
		if !strings.Contains(out, "s1") {
			t.Errorf("%q finds nothing, though the session says those words apart:\n%s", q, out)
		}
	}
	// The words have to be together the way the compound says: a session that
	// holds both words far apart is not what "blowing-up" asked for, and one
	// that holds neither is not a match at all.
	for _, q := range []string{"vacuum-freeze", "pool-vacuum"} {
		if out, _ := captureRun(t, "search", q); strings.Contains(out, "s1") {
			t.Errorf("%q matched a session that does not say it:\n%s", q, out)
		}
	}
}

// "Try fewer words" is advice for a different problem when a filter the caller
// set is what emptied the result — `deja last` has named it all along (#715).
func TestEmptySearchNamesTheFilter(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the connection pool starves"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "connection", "--since", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "matched nothing under since") {
		t.Errorf("--since: %q", out)
	}
	if strings.Contains(out, "try fewer words") {
		t.Errorf("--since still gave the generic advice: %q", out)
	}

	out, err = captureRunStderr(t, "connection", "--project", "nosuch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `project "nosuch"`) {
		t.Errorf("--project: %q", out)
	}

	// An ordinary miss keeps the ordinary advice.
	out, err = captureRunStderr(t, "moria")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "try fewer words") {
		t.Errorf("plain miss: %q", out)
	}
}

// The hint from #674 was supposed to catch `stat` and `serch`, not every
// sentence starting with a short word (#715).
func TestCommandHintIgnoresShortFirstWords(t *testing.T) {
	for _, q := range []string{
		"a very long query that no session will ever contain",
		"a",
		"is the pool starving",
		"do we cap retries",
	} {
		if got := commandHint(q); got != "" {
			t.Errorf("%q produced %q", q, got)
		}
	}
	// The ones it exists for still work.
	for _, q := range []string{"stat", "serch pool", "shwo abc", "lasr"} {
		if got := commandHint(q); got == "" {
			t.Errorf("%q produced no hint", q)
		}
	}
}

// Folding a misspelled flag into the query turned a working search into "you
// have no such memory" (#755).
func TestNearestSearchFlag(t *testing.T) {
	typos := map[string]string{
		"--limti":   "--limit",
		"--projekt": "--project",
		"--harnes":  "--harness",
		"--jsonn":   "--json",
		"--rol":     "--role",
	}
	for typo, want := range typos {
		if got := nearestSearchFlag(typo); got != want {
			t.Errorf("nearestSearchFlag(%q) = %q, want %q", typo, got, want)
		}
	}
	// A query may legitimately contain a dash, and a real flag is not a typo.
	for _, ok := range []string{
		"--retry", "--json", "--limit", "--re", "--all",
		"pool", "-x", "--", "--zzzzzzzz", "--nosuchflag",
		"--soft", "--port", "--short", "--sorts", "--sort",
	} {
		if got := nearestSearchFlag(ok); got != "" {
			t.Errorf("nearestSearchFlag(%q) = %q, want silence", ok, got)
		}
	}
}

func TestParseSearchRejectsAMisspelledFlag(t *testing.T) {
	if _, err := parseSearch([]string{"pool", "--limti", "5"}); err == nil ||
		!strings.Contains(err.Error(), "did you mean --limit") {
		t.Errorf("err = %v", err)
	}
	// The query survives when nothing looks like a typo.
	o, err := parseSearch([]string{"--retry", "budget"})
	if err != nil || o.Query != "--retry budget" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
	o, err = parseSearch([]string{"pool", "--limit", "5"})
	if err != nil || o.Limit != 5 || o.Query != "pool" {
		t.Errorf("limit = %d query = %q err = %v", o.Limit, o.Query, err)
	}
}

func TestParseSearchTrimsSurroundingSpace(t *testing.T) {
	// A leading space left the exact-match tier hunting for " token", so an
	// exact-only term (an error code, a coined name) missed on a pasted query.
	for _, in := range []string{" WHITEWORD", "WHITEWORD ", "  WHITEWORD  "} {
		o, err := parseSearch([]string{in})
		if err != nil || o.Query != "WHITEWORD" {
			t.Errorf("parseSearch(%q): query = %q err = %v", in, o.Query, err)
		}
	}
	// Space-only is empty once trimmed, so it is rejected rather than searched.
	if _, err := parseSearch([]string{"   "}); err == nil {
		t.Errorf("space-only query should be rejected")
	}
}

func TestParseSearchDashDashEndsOptions(t *testing.T) {
	// `--` stops flag parsing so a query can be the literal text of a flag.
	// Before, --json after it still turned on JSON mode and "--" was left in
	// the query, so flag-colliding text was unsearchable.
	o, err := parseSearch([]string{"--", "--json"})
	if err != nil || o.Query != "--json" || o.JSON {
		t.Fatalf("parseSearch(-- --json): query=%q json=%v err=%v", o.Query, o.JSON, err)
	}
	o, err = parseSearch([]string{"pool", "--", "--limit", "5"})
	if err != nil || o.Query != "pool --limit 5" || o.Limit != 0 {
		t.Fatalf("parseSearch(pool -- --limit 5): query=%q limit=%d err=%v", o.Query, o.Limit, err)
	}
}

// A flag deja takes on another command is not a typo and is not near a search
// flag by edit distance, so it fell through into the query and a search that
// would have found everything reported nothing (#2249).
func TestParseSearchRejectsAFlagFromAnotherCommand(t *testing.T) {
	for flag, command := range map[string]string{
		"--offset":  "show",
		"--deep":    "doctor",
		"--dry-run": "forget",
		"--span":    "restore",
		"--tag":     "remember",
	} {
		_, err := parseSearch([]string{"retry", flag, "2"})
		if err == nil {
			t.Errorf("%s was taken as part of the query", flag)
			continue
		}
		if !strings.Contains(err.Error(), flag) || !strings.Contains(err.Error(), command) {
			t.Errorf("%s: %v — the message should name the flag and the command that takes it", flag, err)
		}
	}

	// The escape hatch stays open: after `--` the same text is a query.
	o, err := parseSearch([]string{"--", "retry", "--offset"})
	if err != nil || o.Query != "retry --offset" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
	// And a dash that is nobody's flag is still a query word.
	o, err = parseSearch([]string{"--retry", "budget"})
	if err != nil || o.Query != "--retry budget" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
}

// A word glued to a Latin prefix has no part of its own in the index — nothing
// splits "k8sнастройки" — so the substring rung is the only road to it, and
// that rung looked for the query token as typed. The store's own case reached
// it and no other did, which is the guess deja exists to spare the reader
// (#2145).
func TestAGluedWordIsReachableInAnotherCase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "смотрел k8sнастройки кластера",
		"s2": "чинил v2обработчик вебхука",
		"s3": "ничего общего: графики и дашборды",
		// The four-rune floor's case: "тест" sits at the end of "манифест",
		// and the two words have nothing to do with each other.
		"s4": "гонял манифест кластера через ci",
		"s5": "чинил автотест воркера ночью",
	} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise: the store's own spelling reaches it.
	for _, tc := range []struct{ query, want string }{
		{"настройки кластера", "s1"},
		{"обработчик вебхука", "s2"},
	} {
		if out, _ := captureRun(t, "search", tc.query); !strings.Contains(out, tc.want) {
			t.Fatalf("%q does not reach %s, so this measures nothing:\n%s", tc.query, tc.want, out)
		}
	}
	// The case nobody types a month later.
	for _, tc := range []struct{ query, want string }{
		{"настройка кластера", "s1"},
		{"настройками кластера", "s1"},
		{"обработчику вебхука", "s2"},
	} {
		out, _ := captureRun(t, "search", tc.query)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%q does not reach %s, which says the word in another case:\n%s", tc.query, tc.want, out)
		}
	}
	// And the rung has not become a wildcard. A word the store never says
	// finds nothing in any case, and neither does one whose short form sits
	// at the end of an unrelated token — "тест" inside "манифест", which is
	// what the four-rune floor is there to refuse.
	for _, q := range []string{"миграции кластера", "миграция кластера", "тесты кластера", "тест кластера"} {
		out, _ := captureRun(t, "search", q)
		for _, sid := range []string{"s1", "s2", "s3", "s4"} {
			if strings.Contains(out, sid) {
				t.Errorf("%q matched %s, which never says it:\n%s", q, sid, out)
			}
		}
	}
	// What it does admit, and on purpose: a word really inside a longer one.
	// "автотест" is an autotest, and a reader asking about tests wants it —
	// the same trade the substring rung makes when "code" reaches "opencode".
	if out, _ := captureRun(t, "search", "тесты воркера"); !strings.Contains(out, "s5") {
		t.Errorf("\"тесты воркера\" does not reach the autotest session:\n%s", out)
	}
	// The queries that do reach must not drag the unrelated sessions with them.
	for _, q := range []string{"настройка кластера", "обработчику вебхука"} {
		out, _ := captureRun(t, "search", q)
		for _, sid := range []string{"s3", "s4"} {
			if strings.Contains(out, sid) {
				t.Errorf("%q reached %s, which shares nothing with it:\n%s", q, sid, out)
			}
		}
	}
}

// #2554 gave the listing a line for what the ignore rule withheld. Search filters
// on the same rule inside the index and says nothing: the sessions that asked the
// question are covered by it, so the answer comes back short — or empty — with
// only the tier's own notes on screen (#2562).
func TestSearchSaysWhatTheIgnoreRuleWithheld(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	realDir := filepath.Join(claude, "projects", "-tmp-app")
	jobs := filepath.Join(claude, ".claude", "jobs", "abc", "projects", "-tmp-app")
	for _, d := range []string{realDir, jobs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	write := func(dir, sid, text string, ago time.Duration) {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-ago).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(realDir, "keeper", "the invoice renderer lost its footer", 2*time.Hour)
	for i, sid := range []string{"scratch1", "scratch2"} {
		write(jobs, sid, "why did the quokka telemetry sharding keep failing", time.Duration(i+1)*time.Hour)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "quokka telemetry sharding") })
	if strings.Contains(out, "scratch") {
		t.Fatalf("an ignored session was served:\n%s", out)
	}
	if !strings.Contains(stderr, "ignore rule") || !strings.Contains(stderr, "2") {
		t.Errorf("two matching sessions were withheld and search said %q", strings.TrimSpace(stderr))
	}

	// Silence when the rule took nothing from this answer.
	quiet := captureStderr(t, func() { _, _ = captureRun(t, "invoice renderer footer") })
	if strings.Contains(quiet, "ignore rule") {
		t.Errorf("the line printed for an answer the rule did not touch: %q", strings.TrimSpace(quiet))
	}
}

// deja log is the audit of what deja did, and three places say a search shows
// up in it: the docs ("`search` and `handoff` are the reader's own commands"),
// the comment over the kind constants, and the empty-log line itself. No path
// has ever written one (#2471).
func TestASearchLandsInTheLog(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := strings.Repeat("the pool checkout dominated the p99 of the orders pipeline ", 20)
	lines := []string{
		`{"type":"user","sessionId":"p1","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"user","content":"why is the p99 high? ` + body + `"}}`,
		`{"type":"assistant","sessionId":"p1","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"assistant","content":"the shared pool is the p99 ` + body + `"}}`,
	}
	if err := os.WriteFile(filepath.Join(store, "p1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if err := runSearch(dir, []string{"pool", "p99"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := runSearch(dir, []string{"zzzznothinghere"}, ""); err != nil {
		t.Fatal(err)
	}

	var found, empty int
	for _, e := range eventsOf(dir) {
		if e.Kind != usage.KindSearch {
			continue
		}
		if e.Empty {
			empty++
			continue
		}
		found++
		if e.Sessions == 0 || e.Bytes == 0 {
			t.Errorf("a search that matched recorded nothing about it: %#v", e)
		}
	}
	if found != 1 || empty != 1 {
		t.Errorf("two searches, one with hits and one without, left %d matched and %d empty events in the log", found, empty)
	}
}

func eventsOf(dir string) []usage.Event {
	events, _ := usage.EventsCounted(dir, 50)
	return events
}

// A question carrying one rare word the store holds is answered — and only
// when the words it stands among are words the corpus knows. That second half
// is what makes the first safe: among words nothing has ever written, a single
// surviving anchor is as likely to be a typo as a question, and the tier owes
// silence rather than a guess. Both halves are one condition in
// relevanceSearch (`len(terms) >= 3 && termsKnown >= 2`) and neither was
// visible from outside.
func TestALoneRareAnchorIsServedOnlyAmongWordsTheCorpusKnows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string, age time.Duration) {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, text := range []string{
		"deployed the service and watched the dashboards for a while",
		"the on-call rotation was quiet, wrote some docs instead",
		"refactored the client, moved the retries into the transport",
		"upgraded the database and ran the migrations in the morning",
		"cleaned up the logging, too many fields in every line",
		"the release to production went out on friday without a hitch",
		"took a break after the incident review, everyone was tired",
	} {
		write(fmt.Sprintf("f%d", i), text, time.Duration(i+3)*time.Hour)
	}
	// The one session that says the rare word, worded so that no question
	// below can reach it by an AND over its words.
	write("target", "the worker died because the retry_budget was exhausted", 2*time.Hour)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// Asked among words this corpus uses — "break" and "production" are in it
	// — the rare word carries the question on its own, and it is the relevance
	// tier that carries it. The tier matters as much as the hit: the rungs
	// above it would mean the answer came from a spelling guess.
	out, _ := captureRun(t, "search", "--json", "why did retry_budget break production")
	if !strings.Contains(out, "target") {
		t.Errorf("a question standing on one rare word the store holds found nothing:\n%s", out)
	}
	if !strings.Contains(out, `"tier":"relevance"`) {
		t.Errorf("the question was answered by another rung, so this measures something else:\n%s", out)
	}
	// Asked among words nothing has written, the same anchor is withheld: at
	// that point deja cannot tell a question from a typo.
	if out, _ = captureRun(t, "search", "why did retry_budget zzqx wwvv"); strings.Contains(out, "target") {
		t.Errorf("answered on one anchor among words the corpus never saw:\n%s", out)
	}
	// The other half of the same condition, and a separate rule: a short query
	// keeps the silence contract whatever its words are, because one lucky
	// word out of two is not a question.
	if out, _ = captureRun(t, "search", "retry_budget kkjh"); strings.Contains(out, "target") {
		t.Errorf("a two-word query was answered on one of them:\n%s", out)
	}
	// Both words known, still two of them: the length floor is what withholds
	// this one, and nothing else does. Without this case the floor can be
	// lowered to two and every assertion above still passes.
	if out, _ = captureRun(t, "search", "retry_budget production"); strings.Contains(out, "target") {
		t.Errorf("a two-word query was answered although the floor is three:\n%s", out)
	}
	// And the word on its own still answers exactly, which is the tier below.
	if out, _ = captureRun(t, "search", "retry_budget"); !strings.Contains(out, "target") {
		t.Fatalf("the rare word alone finds nothing, so this measures nothing:\n%s", out)
	}
}

// A token that mixes scripts — `api_настройки`, `webhook-обработчик` — takes
// the ASCII path through the form rules, because isCyrToken stops at the first
// Latin rune. Nothing pins what the reader gets: the reach comes from the
// parts the indexer splits out and from the close tier, not from the Russian
// endings, and both are a rung away from the token itself.
func TestAMixedScriptTokenIsStillReachable(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "поправил api_настройки воркера",
		"s2": "чинил webhook-обработчик в проде",
		"s3": "смотрел k8sнастройки кластера",
	} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, want string }{
		// The store's own spelling.
		{"api_настройки", "s1"},
		{"webhook-обработчик", "s2"},
		{"k8sнастройки", "s3"},
		// And another case of the Russian half, which is how someone would
		// actually type it a month later.
		{"api_настройка", "s1"},
		{"webhook-обработчику", "s2"},
		{"k8sнастройка", "s3"},
		// The Russian half on its own reaches the token it is part of.
		{"настройками воркера", "s1"},
	} {
		out, _ := captureRun(t, "search", tc.query)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%q does not reach %s:\n%s", tc.query, tc.want, out)
		}
	}
	// And it is not reaching everything: a Russian word the store does not
	// hold finds nothing, mixed token or not.
	if out, _ := captureRun(t, "search", "миграции кластера"); strings.Contains(out, "s1") {
		t.Errorf("a word no session says matched anyway:\n%s", out)
	}
}

// A correct plural is not a misspelling. The word-forms rung could not reach
// the singular of a noun ending in "e", so the fuzzy rung answered and the
// reader was told they had typed it wrong (#2137).
func TestAPluralIsAnsweredAsAWordFormNotASpelling(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message": map[string]any{"role": "user", "content": "the ingest pipeline stalled and the warm cache " +
			"was dropped on every release"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"pipelines", "caches", "releases"} {
		out, _ := captureRun(t, "search", q)
		if !strings.Contains(out, "s1") {
			t.Fatalf("%q finds nothing, so this measures nothing:\n%s", q, out)
		}
		errOut, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(errOut, "close spellings") {
			t.Errorf("%q is the plural, not a misspelling:\n%s", q, errOut)
		}
		if !strings.Contains(errOut, "word forms") {
			t.Errorf("%q lost the word-forms line:\n%s", q, errOut)
		}
	}
	// A word that really is mistyped keeps the sentence about spelling.
	errOut, err := captureRunStderr(t, "search", "pipelien")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "close spellings") {
		t.Errorf("a typo should still be answered as one:\n%s", errOut)
	}
}

// A rule that hides imported memory used to be applied after the result cap,
// so denied sessions still took the limited slots and the local sessions that
// matched never reached the page — an empty answer over a store that had ten
// of them (#1060).
func TestPolicyFilterRunsBeforeTheCap(t *testing.T) {
	tmp := hermeticEnv(t)

	// Imported sessions are newer, so they take the whole top of the ranking.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	for i := range 20 {
		rec := index.SyncRecord{
			Harness:   "claude",
			SessionID: fmt.Sprintf("peer-%02d", i),
			Project:   "work/api",
			Role:      "user",
			Text:      "the bearing seal on the peer machine",
			Time:      time.Date(2026, 6, i+1, 10, 0, 0, 0, time.UTC),
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-peer.jsonl"), []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 3 {
		id := fmt.Sprintf("local-%02d", i)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/local","timestamp":"2026-05-0%dT10:00:00Z","message":{"role":"user","content":"the bearing seal here at home"}}`, id, i+1)
		writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-local", id+".jsonl"), id, []string{line})
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "search", "--json", "--limit", "5", "bearing seal")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total    int  `json:"total"`
		Withheld int  `json:"policy_withheld"`
		Capped   bool `json:"capped"`
		Hits     []struct {
			Session struct {
				ID      string `json:"id"`
				Project string `json:"project"`
			} `json:"session"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(got.Hits) != 3 {
		t.Errorf("the rule ate the result slots: %d local sessions returned, want 3\n%s", len(got.Hits), out)
	}
	for _, h := range got.Hits {
		if strings.HasPrefix(h.Session.Project, "imported") {
			t.Errorf("a denied session was returned: %s (%s)", h.Session.ID, h.Session.Project)
		}
	}
	if got.Total != 3 {
		t.Errorf("total = %d, want 3 — it counts sessions the rule removed", got.Total)
	}
	if got.Withheld != 20 {
		t.Errorf("policy_withheld = %d, want 20 — it counts what fit in the cap, not what the rule denies", got.Withheld)
	}
}

// "no matches in 1 indexed session — try fewer words" is advice about wording
// for a session the rule never let search open: the count named the whole
// store rather than the part this path may read (#986).
func TestNoMatchesCountsWhatSearchMayRead(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer work on the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// Without a rule the miss is a wording problem and reads like one.
	var out bytes.Buffer
	printNoMatches(&out, dir, "ticker window", false)
	if !strings.Contains(out.String(), "no matches in 1 indexed session") {
		t.Errorf("an ordinary miss lost its count:\n%s", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	printNoMatches(&out, dir, "ticker window", false)
	got := out.String()
	if strings.Contains(got, "try fewer words") {
		t.Errorf("the reader was sent after their wording:\n%s", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("nothing named the rule that emptied the search path:\n%s", got)
	}
	if strings.Contains(got, "in 1 indexed session") {
		t.Errorf("the count still names sessions search cannot read:\n%s", got)
	}
}

// The ignore rule withholds sessions the same way and was not counted at all:
// every other surface said three while the empty answer said six, and told the
// reader to reword a query against history search never opened (#2707).
func TestNoMatchesCountsPastTheIgnoreRuleToo(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write := func(project, id, day string) {
		t.Helper()
		store := filepath.Join(root, "-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget in ` + project + `"},` +
			`"timestamp":"2026-08-0` + day + `T10:00:00Z","sessionId":"` + id + `","cwd":"/` + project + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A project name no test name can contain: Ignored substring-matches the
	// whole session path, and a t.TempDir() under a test called "hide" would
	// hide every session in the fixture.
	write("keep", "k1", "1")
	write("keep", "k2", "2")
	write("zonkohide", "h1", "3")
	write("zonkohide", "h2", "4")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printNoMatches(&out, dir, "quingleflarp", false)
	if !strings.Contains(out.String(), "no matches in 4 indexed sessions") {
		t.Fatalf("the fixture does not hold four sessions to begin with:\n%s", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	printNoMatches(&out, dir, "quingleflarp", false)
	got := out.String()
	if !strings.Contains(got, "no matches in 2 indexed sessions") {
		t.Errorf("the count names sessions the ignore rule keeps out of every answer:\n%s", got)
	}

	// And when the rule hides everything, the reader is told which rule did
	// it: counting the ignore rule into the same zero would otherwise put this
	// store under a sentence about the trust policy, quoting a trust rule that
	// allows everything.
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	printNoMatches(&out, dir, "quingleflarp", false)
	got = out.String()
	if !strings.Contains(got, "the ignore rule withholds every indexed session") {
		t.Errorf("a store emptied by the ignore rule was not told so:\n%s", got)
	}
	if strings.Contains(got, "trust policy") {
		t.Errorf("the wrong rule was named:\n%s", got)
	}
	if strings.Contains(got, "try fewer words") {
		t.Errorf("the reader was sent after their wording:\n%s", got)
	}
}

// A search for a word most sessions hold reads only the sessions that can
// reach its page (#5467). What it did not read still matched, and both
// surfaces that print a count have to say so: the JSON total and recall's
// "5 of N matched" read the pool's size otherwise.
func TestCommonWordCountsTheSessionsItDidNotRead(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(15) + 300
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := range n {
		id := fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i)
		line := `{"type":"user","message":{"role":"user","content":` +
			quoteJSON(fmt.Sprintf("the deploy of service %d is slow", i)) +
			`},"timestamp":"` + at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
			`","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runSearch(dir, []string{"--json", "deploy"}, ""); err != nil {
			t.Error(err)
		}
	})
	var env struct {
		Total  int               `json:"total"`
		Capped bool              `json:"capped"`
		Hits   []json.RawMessage `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%.400s", err, out)
	}
	if env.Total != n || !env.Capped || len(env.Hits) != 15 {
		t.Errorf("search --json: total %d capped %v hits %d, want %d capped 15", env.Total, env.Capped, len(env.Hits), n)
	}

	text, _, _, _, _, err := recallTextResultFrom(dir, "deploy", "", 5, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("(5 of %d matched)", n); !strings.Contains(text, want) {
		t.Errorf("recall does not say %q:\n%.600s", want, text)
	}
}

// A quoted phrase over two common words: the postings name every session
// holding both, and the paged total counted the unread ones among them, so
// search and recall said 106 matched where 5 say it (#5495).
func TestQuotedPhraseTotalCountsTheSessionsThatSayIt(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(15) + 600
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := range n {
		id := fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i)
		text := fmt.Sprintf("alpha went out first and beta followed in run %d", i)
		if i%(n/5) == n/10 {
			text = fmt.Sprintf("the alpha beta rollout of run %d", i)
		}
		line := `{"type":"user","message":{"role":"user","content":` + quoteJSON(text) +
			`},"timestamp":"` + at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
			`","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		q    string
		want int
	}{
		// The control: the words alone match every session.
		{"alpha beta", n},
		{`"alpha beta"`, 5},
	} {
		out := captureStdout(t, func() {
			if err := runSearch(dir, []string{"--json", "--limit", "3", c.q}, ""); err != nil {
				t.Error(err)
			}
		})
		var env struct {
			Total     int  `json:"total"`
			Estimated bool `json:"total_estimated"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v\n%.400s", err, out)
		}
		if env.Total != c.want || env.Estimated {
			t.Errorf("search --json %s: total %d estimated %v, want %d", c.q, env.Total, env.Estimated, c.want)
		}
	}

	text, _, _, _, _, err := recallTextResultFrom(dir, `"alpha beta"`, "", 3, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "(3 of 5 matched)") || !strings.Contains(text, "2 more matches") {
		t.Errorf("recall does not say 3 of 5:\n%.600s", text)
	}
}

// When retrieval only estimated the unread sessions, every line that counts
// them says so.
func TestEstimatedTotalSaysAbout(t *testing.T) {
	for _, c := range []struct {
		line, want string
	}{
		{recallCountLineAbout("alpha beta", search.TierExact, 0, 3, 40, 0, false, true), "(3 of about 40 matched)"},
		{recallCountLineAbout("alpha beta", search.TierExact, 3, 3, 40, 0, false, true), "(matches 4-6 of about 40)"},
		// An estimate of none left over still says it is one, and not by
		// editing the query it echoes.
		{recallCountLineAbout("1 of 3", search.TierExact, 0, 3, 3, 0, false, true), `"1 of 3" (3 of about 3 matched)`},
		{contextOthersNote(41, true), "about 40 other sessions"},
	} {
		if !strings.Contains(c.line, c.want) {
			t.Errorf("%q does not say %q", c.line, c.want)
		}
	}
	if exact := recallCountLineAbout("alpha beta", search.TierExact, 0, 3, 40, 0, false, false); strings.Contains(exact, "about") {
		t.Errorf("an exact count line says about: %q", exact)
	}
}

// A --re miss told the reader to try --re, and listed the pattern's words as
// if they were looked up together (#5457).
func TestRegexMissAdviceFitsAPattern(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, text := range []string{"the quokkabloom pool starves", "plinths are missing from the manifest", "the cache warms slowly"} {
		id := string(rune('a'+i)) + "1"
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q, want string
	}{
		{"^nosuchxyz$", "without ^ or $"},
		{"nosuch[^0-9]\\$", "try a looser pattern"},
		{"quokkabloom plinths", "try a looser pattern"},
	} {
		out, err := captureRunStderr(t, "--re", c.q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, c.want) || strings.Contains(out, "or --re") {
			t.Errorf("--re %q: %q, want %q", c.q, out, c.want)
		}
		if strings.Contains(out, "no session has them together") {
			t.Errorf("--re %q: per-word line on a pattern: %q", c.q, out)
		}
	}
	// Control: a plain miss keeps its advice and its per-word line.
	out, err := captureRunStderr(t, "quokkabloom plinths")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "try fewer words or --re") || !strings.Contains(out, "no session has them together") {
		t.Errorf("plain miss: %q", out)
	}
}

// The two-step loop #1321 asks for: find the session by what was said, then
// search inside it for what was done. Without --session the second step meant
// turning the id back into a file path and grepping it by hand.
func TestSearchInsideOneSession(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id, user, tool string) {
		lines := `{"type":"user","message":{"role":"user","content":"` + user + `"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n" +
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + tool + `"}]},"timestamp":"2026-08-02T10:01:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", "aaa11111", "why does the build fail", "go build ./... undefined parseThing")
	write("b.jsonl", "bbb22222", "why does the build fail here too", "go build ./... undefined otherThing")
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	all, err := captureRun(t, "search", "--role", "tool", "--all", "go build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "parseThing") || !strings.Contains(all, "otherThing") {
		t.Fatalf("the fixture does not put work in both sessions:\n%s", all)
	}

	one, err := captureRun(t, "search", "--session", "aaa1", "--role", "tool", "--all", "go build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, "parseThing") {
		t.Errorf("searching inside the session lost its own work:\n%s", one)
	}
	if strings.Contains(one, "otherThing") {
		t.Errorf("searching inside one session answered from another:\n%s", one)
	}
}

// A search that comes back empty because of a filter has to name the filter:
// "no matches" alone reads as "you have no such memory" (#686, #715, #727).
func TestEmptySearchNamesTheSessionFilter(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the build fails on staging"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa11111","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The advice goes to stderr, where every other "why is this empty" line goes.
	out, err := captureRunStderr(t, "search", "--session", "zzz99999", "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `session "zzz99999"`) {
		t.Errorf("an empty result does not say a session filter was applied:\n%s", out)
	}
}

// The flag has to reach the search options, carry its value, and behave like
// its neighbours when it is mistyped or left empty.
func TestSearchSessionFlagParses(t *testing.T) {
	o, err := parseSearch([]string{"--session", "01a00feb", "--role", "tool", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Session != "01a00feb" {
		t.Errorf("session = %q", o.Session)
	}
	if o.Role != "tool" || o.Query != "build" {
		t.Errorf("the rest of the line was lost: %#v", o)
	}
}

func TestSearchSessionFlagNeedsAValue(t *testing.T) {
	if _, err := parseSearch([]string{"--session"}); err == nil {
		t.Error("a flag with no value was accepted")
	}
}

// A near miss names the flag rather than being swallowed into the query, as it
// does for every other search flag (#755).
func TestSearchSessionFlagTypoIsNamed(t *testing.T) {
	_, err := parseSearch([]string{"--sesion", "abc", "build"})
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Errorf("typo error = %v", err)
	}
}

// yearOldStore writes one claude session dated a year back, which is what
// someone returning to deja after a year actually has.
func yearOldStore(t *testing.T) time.Time {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	when := time.Now().AddDate(-1, 0, 0).UTC()
	stamp := when.Format("2006-01-02T15:04:05Z")
	body := fmt.Sprintf(`{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"the kafka consumer rebalance loops"}}`, stamp) + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return when
}

// The reader typed `--since 30d`; "since 720h0m0s" is not the flag they passed,
// and activeFilters has taken a raw string for exactly this since #715 — the
// search path was the one that passed "" (#1059).
func TestSearchSinceEchoesWhatWasTyped(t *testing.T) {
	yearOldStore(t)

	out, err := captureRunStderr(t, "kafka", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "under since 30d") {
		t.Errorf("search did not echo the typed duration: %q", out)
	}
	if strings.Contains(out, "720h") {
		t.Errorf("search printed the parsed duration: %q", out)
	}

	// The --since=30d form goes through splitEqualsForms and must read the same.
	out, err = captureRunStderr(t, "kafka", "--since=30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "under since 30d") || strings.Contains(out, "720h") {
		t.Errorf("--since=30d form: %q", out)
	}
}

// A store whose every session predates the window cannot answer any --since
// query, so "matched nothing" is a claim about the query when the window is
// what emptied the result.
func TestSinceOlderThanWholeStoreSaysSo(t *testing.T) {
	when := yearOldStore(t)
	want := "every one of the 1 indexed sessions is older than that window — the newest is " +
		when.Local().Format("2006-01-02")

	out, err := captureRunStderr(t, "kafka", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Errorf("search: want %q in %q", want, out)
	}

	out, err = captureRunStderr(t, "last", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Errorf("last: want %q in %q", want, out)
	}
}

// The line is about the window, not about every empty result: a store the
// window does cover must not claim its sessions are older than it.
func TestSinceWithinStoreRangeStaysQuiet(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	stamp := time.Now().Add(-2 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	body := fmt.Sprintf(`{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"the kafka consumer rebalance loops"}}`, stamp) + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "nosuchterm", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "older than that window") {
		t.Errorf("store inside the window got the older-than-window line: %q", out)
	}
}

// sortedIDs runs a search and returns the session ids of its JSON hits.
func sortedIDs(t *testing.T, dir string, args ...string) (ids []string, total int) {
	t.Helper()
	out := captureStdout(t, func() {
		if err := runSearch(dir, append([]string{"--json"}, args...), ""); err != nil {
			t.Error(err)
		}
	})
	var env struct {
		Total int `json:"total"`
		Hits  []struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%.400s", err, out)
	}
	for _, h := range env.Hits {
		ids = append(ids, h.Session.ID)
	}
	return ids, env.Total
}

// sessionN is the id commonWordStore gives session i; session i is i minutes
// newer than session 0.
func sessionN(i int) string { return fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i) }

// Results came back by relevance only, and finding last week's session among
// them meant reading every page (#5577). --sort date lists the newest match
// first, in the text and in --json.
func TestSearchSortDateListsNewestFirst(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := commonWordStore(t, 12, func(int) string { return work })

	ranked, _ := sortedIDs(t, dir, "deploy")
	byDate, total := sortedIDs(t, dir, "--sort", "date", "deploy")
	if total != 12 || len(byDate) != 12 {
		t.Fatalf("--sort date: %d hits of %d, want 12", len(byDate), total)
	}
	for i, id := range byDate {
		if want := sessionN(11 - i); id != want {
			t.Fatalf("--sort date hit %d is %s, want %s:\n%v", i+1, id, want, byDate)
		}
	}
	if strings.Join(ranked, ",") == strings.Join(byDate, ",") {
		t.Fatal("the ranked order is already newest first, so this test proves nothing")
	}
	if rel, _ := sortedIDs(t, dir, "--sort", "relevance", "deploy"); strings.Join(rel, ",") != strings.Join(ranked, ",") {
		t.Errorf("--sort relevance changed the default order:\n%v\n%v", rel, ranked)
	}
}

// A word most sessions hold is ranked from a pool of the best thousand or so
// (#5467). Sorting that pool by date listed the newest of the best, which
// skipped every new session that scored low; the date order reads the newest
// matches instead, so the page is the newest of the whole match.
func TestSearchSortDateOverACappedPool(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(search.DefaultLimit) + 600
	dir := commonWordStore(t, n, func(int) string { return work })

	ids, total := sortedIDs(t, dir, "--sort", "date", "deploy")
	if total != n {
		t.Errorf("total %d, want %d", total, n)
	}
	if len(ids) != search.DefaultLimit {
		t.Fatalf("%d hits, want a page of %d", len(ids), search.DefaultLimit)
	}
	for i, id := range ids {
		if want := sessionN(n - 1 - i); id != want {
			t.Fatalf("hit %d is %s, want %s: the page skipped newer matches\n%v", i+1, id, want, ids)
		}
	}
}

// The text answer says the order it is in.
func TestSearchSortDateSaysSo(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := commonWordStore(t, 3, func(int) string { return work })
	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			if err := runSearch(dir, []string{"--sort", "date", "deploy"}, ""); err != nil {
				t.Error(err)
			}
		})
	})
	if !strings.Contains(errOut, "newest first") {
		t.Errorf("stderr does not name the order:\n%s", errOut)
	}
}

func TestSearchSortRejectsAnUnknownOrder(t *testing.T) {
	if _, err := parseSearch([]string{"--sort", "size", "deploy"}); err == nil || !strings.Contains(err.Error(), "date or relevance") {
		t.Errorf("--sort size: %v", err)
	}
	o, err := parseSearch([]string{"--sort=date", "deploy"})
	if err != nil || o.Sort != search.SortDate || o.Query != "deploy" {
		t.Errorf("--sort=date: %+v %v", o, err)
	}
}

// ^s turns the search list newest first and back. The newest are read from
// the whole match a page at a time, so the next page carries on in date order
// rather than sorting what one page of the ranking held (#5577).
func TestTUISortToggleReadsTheNewestMatches(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 600
	dir := commonWordStore(t, n, func(int) string { return work })
	a := newTestTUI(t, dir)
	typeQuery(t, a, "deploy")
	if label, note := a.sectionLabel(); !strings.HasSuffix(note, "best first") {
		t.Errorf("a ranked list is headed %q · %q", label, note)
	}
	if s := screen(a.frame(120, 36)); !strings.Contains(s, "^s  newest first") {
		t.Errorf("the footer does not offer the date order:\n%s", s)
	}
	// At 80 columns the footer keeps what it had; the label names the order.
	wantOnScreen(t, screen(a.frame(80, 30)), "continue in…", "best first")

	a.ctrlKey('s')
	drain(t, a)
	if !a.byDate {
		t.Fatal("^s did not turn the list to date order")
	}
	checkNewest := func(upTo int) {
		t.Helper()
		if len(a.rows) < upTo {
			t.Fatalf("listed %d cards, want %d", len(a.rows), upTo)
		}
		for i := range upTo {
			if want := sessionN(n - 1 - i); a.rows[i].s.ID != want {
				t.Fatalf("card %d is %s, want %s", i+1, a.rows[i].s.ID, want)
			}
		}
	}
	checkNewest(tuiPage)
	if a.rows[0].section == "" {
		t.Error("a date-ordered list has no day heading over its first card")
	}
	if _, note := a.sectionLabel(); !strings.Contains(note, "newest first") {
		t.Errorf("the header does not say the order: %q", note)
	}
	if label, _ := a.sectionLabel(); !strings.HasPrefix(label, grouped(n)+" sessions") {
		t.Errorf("the list is headed %q, want every one of the %d matches", label, n)
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "newest first", "^s  best first")

	a.move(len(a.rows))
	drain(t, a)
	checkNewest(2 * tuiPage)

	a.ctrlKey('s')
	drain(t, a)
	if a.byDate {
		t.Fatal("^s again left the list in date order")
	}
	// It reads as far as the reader scrolled, ranked over that deeper read.
	o := a.box().options(nil)
	o.Limit = a.asked
	want, _, err := tuiSearchPage(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range want {
		if a.rows[i].s.ID != h.Session.ID {
			t.Fatalf("back to best first, card %d is %s, want %s", i+1, a.rows[i].s.ID, h.Session.ID)
		}
	}
	if a.rows[0].section != "" {
		t.Errorf("a ranked list carries the day heading %q", a.rows[0].section)
	}
}

// The relevance and error tiers rank a window before anything is ordered, so
// a date order over them is the newest of that window, and says so when the
// match ran past it.
func TestNewestFirstNoteNamesARankedWindow(t *testing.T) {
	for _, c := range []struct {
		o          search.Options
		shown      int
		tierWindow bool
		window     int
		want       string
	}{
		{search.Options{Total: 3}, 3, false, 0, "all 3 matches, newest first"},
		{search.Options{Total: 1600}, 15, false, 0, "newest first"},
		{search.Options{Total: 1234}, 15, true, 50, "newest first among the 50 closest of 1,234"},
		{search.Options{Total: 40}, 15, false, 40, "newest first"},
	} {
		if got := newestFirstNote(c.o, c.shown, c.tierWindow, c.window); got != c.want {
			t.Errorf("newestFirstNote(%d, %d, %v, %d) = %q, want %q", c.o.Total, c.shown, c.tierWindow, c.window, got, c.want)
		}
	}
}

// A relevance answer read by date lists the sessions holding every word
// first. The rest restart the days, so they are headed apart rather than
// under a second "Yesterday".
func TestTUIDateHeadsPartialMatchesApart(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	hits := make([]search.Hit, 0, len(a.rows))
	for i, r := range a.rows {
		hits = append(hits, search.Hit{Session: r.s, Strict: i == len(a.rows)-1})
	}
	search.ByDate(hits)
	a.rowsByDate = true
	a.setRows(nil, hits, len(hits))
	var heads []string
	for _, r := range a.rows {
		if r.section != "" {
			heads = append(heads, r.section)
		}
	}
	want := []string{"This week", "Yesterday · some of the words", "This week · some of the words"}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Errorf("headings %q, want %q", heads, want)
	}
}

// A search must not pay for a rewrite of the whole index. When a store deja
// cannot append to has changed, the answer comes from the index as it stands
// and the refresh is detached — on a 1.7 GB store the old behaviour turned a
// query that matched nothing into a 194 s wait (#1521). A store deja *can*
// append to stays inline, because reading the new bytes is cheap.
func TestSearchDoesNotWaitForARewrite(t *testing.T) {
	tmp := hermeticEnv(t)

	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(claude, "s1.jsonl")
	line := func(text, stamp string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + stamp + `","sessionId":"s1","cwd":"/proj"}` + "\n"
	}
	first := line("staleneedle the first turn", "2026-07-02T10:00:00Z")
	if err := os.WriteFile(transcript, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	aiderDir := filepath.Join(tmp, "aider")
	if err := os.MkdirAll(aiderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(aiderDir, ".aider.chat.history.md")
	if err := os.WriteFile(history, []byte("# aider chat started at 2026-07-02 10:00:00\n\n#### rewriteneedle first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Claude appends: cheap, so it stays on the search thread and the new turn
	// is in this very answer.
	if err := os.WriteFile(transcript, []byte(first+line("appendedneedle the second turn", "2026-07-02T10:05:00Z")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRunStderr(t, "search", "appendedneedle")
	if err != nil {
		t.Fatalf("search after append: %v\n%s", err, out)
	}
	if strings.Contains(out, "refreshing in the background") {
		t.Errorf("an append was deferred to the warmup instead of run inline:\n%s", out)
	}

	// aider has no append path, so its growth is rewrite-grade work.
	body := "# aider chat started at 2026-07-02 10:00:00\n\n#### rewriteneedle first\n\n#### deferredneedle second\n"
	if err = os.WriteFile(history, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = captureRunStderr(t, "search", "deferredneedle")
	if err != nil && !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("search during a rewrite: %v\n%s", err, out)
	}
	if !strings.Contains(out, "refreshing in the background") {
		t.Errorf("the search waited for the rewrite:\n%s", out)
	}

	// --rebuild is the escape hatch: someone who asked for the rebuild waits
	// for it and sees the result.
	out, err = captureRunStderr(t, "search", "--rebuild", "deferredneedle")
	if err != nil {
		t.Fatalf("rebuild search: %v\n%s", err, out)
	}
	if strings.Contains(out, "refreshing in the background") {
		t.Errorf("--rebuild deferred the work it was asked to do:\n%s", out)
	}
}

// A query with no word in it is not a query with a short word in it. Emoji are
// four bytes, well over the cut #828 describes, and the tokenizer drops them
// because they are symbols — telling the reader to try longer words sends them
// after something that would never have worked (#2133).
func TestASymbolOnlyQuerySaysWhyItFoundNothing(t *testing.T) {
	symbolQueryStore(t)

	for _, q := range []string{"🔥", "🔥🔥", "✅", "!!!", "→", "\u2764\ufe0f", "\U0001F468\u200d\U0001F469\u200d\U0001F467"} {
		out, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "nothing to search for") || !strings.Contains(out, "are not words") {
			t.Errorf("%q: want the sentence about words, got:\n%s", q, out)
		}
		if strings.Contains(out, "too short") {
			t.Errorf("%q is not too short, it holds no word at all:\n%s", q, out)
		}
	}
	// The sentence that is about length keeps the queries it is about.
	for _, q := range []string{"p", "a b"} {
		out, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "too short") {
			t.Errorf("%q is the short-word case and should still say so:\n%s", q, out)
		}
	}
	// --re does not use the word index at all, so neither sentence is true
	// there: a regex that found nothing is an ordinary miss.
	out, err := captureRunStderr(t, "search", "--re", "🔥")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nothing to search for") {
		t.Errorf("--re does not go through the word index:\n%s", out)
	}
}

// An emoji written with a joiner or a variation selector used to leave that
// invisible character standing as the whole query, so a heart asked for its
// selector and answered with every session that spelled some other emoji the
// same way (#2133).
func TestAnEmojiQueryDoesNotMatchADifferentEmoji(t *testing.T) {
	symbolQueryStore(t)

	out, _ := captureRun(t, "search", "\u2764\ufe0f")
	for _, sid := range []string{"s1", "s2"} {
		if strings.Contains(out, sid) {
			t.Errorf("the heart query matched %s, which says a different emoji:\n%s", sid, out)
		}
	}
	// The words beside the emoji still find both sessions, which is the only
	// way either of them was ever reachable.
	for q, want := range map[string]string{"disk": "s2", "bearing": "s1"} {
		if out, _ := captureRun(t, "search", q); !strings.Contains(out, want) {
			t.Errorf("%q lost %s, so this measures nothing:\n%s", q, want, out)
		}
	}
}

// Two sessions whose only emoji are spelled with the invisible characters at
// issue: a variation selector in one, a zero-width joiner in the other.
func symbolQueryStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "the pump bearing failed ⚠️ again",
		"s2": "the disk is nearly full \U0001F468\u200d\U0001F469\u200d\U0001F467 said the family",
	} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The end of #2117: a query pasted with typographic punctuation reaches the
// word the index holds, rather than missing outright or being rescued by the
// close-spellings fallback — which tells the reader they misspelled something
// they did not.
func TestAPastedQueryFindsWhatItAsksFor(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the retry budget keeps blowing up under load"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise: the plain query works, so the rest is about the punctuation.
	if out, _ := captureRun(t, "search", "retry budget"); !strings.Contains(out, "retry budget") {
		t.Fatalf("the plain query found nothing, so this measures nothing: %q", out)
	}
	for _, q := range []string{
		"“retry budget”",
		"—retry—",
		"«retry»",
		"budget…",
		"retry’s budget",
	} {
		var out string
		stderr := captureStderr(t, func() { out, _ = captureRun(t, "search", q) })
		if !strings.Contains(out, "s1") {
			t.Errorf("%q found nothing: %q %q", q, out, strings.TrimSpace(stderr))
			continue
		}
		if strings.Contains(stderr, "close spellings") {
			t.Errorf("%q was treated as a misspelling: %q", q, strings.TrimSpace(stderr))
		}
	}
}

// A --word search does not have, after the query, was folded into it, so
// `deja goroutines --bogus` searched for both words and found nothing, and
// `deja goroutines --help` searched for "--help".
func TestSearchRefusesAFlagItDoesNotHave(t *testing.T) {
	for _, args := range [][]string{
		{"goroutines", "--bogus"},
		{"goroutines", "--max-hits", "3"},
	} {
		_, err := parseSearch(args)
		if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "after `--`") {
			t.Errorf("parseSearch(%q) = %v, want an unknown-flag refusal", args, err)
		}
	}
	// Still queries: the text after --, a query that starts with a dash, a
	// phrase with a dash inside, and a single dash.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pool", "--", "--bogus"}, "pool --bogus"},
		{[]string{"--retry", "budget"}, "--retry budget"},
		{[]string{"pool", "a --dash inside"}, "pool a --dash inside"},
		{[]string{"-v"}, "-v"},
		{[]string{"rollback", "-1"}, "rollback -1"},
	} {
		o, err := parseSearch(tc.args)
		if err != nil || o.Query != tc.want {
			t.Errorf("parseSearch(%q) = %q, %v; want the query %q", tc.args, o.Query, err, tc.want)
		}
	}
}

func TestBareSearchHelpPrintsSearchHelp(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "goroutines", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja search [flags] <query>") {
		t.Errorf("want the search help:\n%s", out)
	}
}

// Asking with the singular over transcripts that use the -ies plural returned
// "no matches in N indexed sessions" — advice about wording, for a word the
// store holds in another form (#1079).
func TestSearchFindsTheIesPluralFromTheSingular(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One topic per written form, so a hit names which session answered.
	for _, tc := range []struct{ id, text string }{
		{"s-winch", "the winch subsystem: retries handled during service"},
		{"s-macerator", "the macerator subsystem: queries handled during service"},
		{"s-alternator", "the alternator subsystem: proxies handled during service"},
	} {
		lines := make([]string, 0, 3)
		for i := range 3 {
			lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/stem","timestamp":"2026-06-0%dT10:00:00Z","message":{"role":"user","content":%q}}`, tc.id, i+1, tc.text+fmt.Sprintf(" line %d", i)))
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-stem", tc.id+".jsonl"), tc.id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for q, want := range map[string]string{
		"query": "s-macerator",
		"proxy": "s-alternator",
	} {
		out, err := captureRun(t, "search", "--json", q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var got struct {
			Tier string `json:"tier"`
			Hits []struct {
				Session struct {
					ID string `json:"id"`
				} `json:"session"`
			} `json:"hits"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q: json: %v\n%s", q, err, out)
		}
		if len(got.Hits) == 0 {
			t.Errorf("%q answered nothing over a store that holds it as a plural", q)
			continue
		}
		ids := make([]string, 0, len(got.Hits))
		for _, h := range got.Hits {
			ids = append(ids, h.Session.ID)
		}
		if ids[0] != want {
			t.Errorf("%q returned %v, want %s first", q, ids, want)
		}
	}

	// And the plural still finds the singular: the rule works both ways.
	out, err := captureRun(t, "search", "retries")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "s-winch") {
		t.Errorf("the control query lost its own session:\n%s", out)
	}
}
