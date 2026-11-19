package main

import (
	"bytes"
	"errors"
	"github.com/vshulcz/deja-vu/internal/index"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// `deja index` on a machine with no agent history printed the "indexing ..."
// line and nothing else: the step whose job is filling memory ended with no
// result at all, and the state only surfaced on the next command.
func TestIndexOnAMachineWithNoHistorySaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")

	var err error
	got := captureStderr(t, func() { err = cmdIndex(dir, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "nothing to index yet") {
		t.Errorf("index on an empty machine said nothing about the empty store:\n%s", got)
	}
	if !strings.Contains(got, "no agent history was found on this machine") {
		t.Errorf("index did not name why the store is empty:\n%s", got)
	}
	if !strings.Contains(got, "deja sources") {
		t.Errorf("index gave no next step:\n%s", got)
	}
}

// The same line must not appear once there is history: a real build already
// prints its per-harness counts.
func TestIndexWithHistoryDoesNotClaimNothingToIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")

	var err error
	out := captureStderr(t, func() { err = cmdIndex(dir, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if got := out; strings.Contains(got, "nothing to index yet") {
		t.Errorf("index called a store with a session empty:\n%s", got)
	}
}

// A note bucket's id names the local day of whichever process indexed the
// line, so a `remember` under TZ=UTC and one under the machine's own zone put
// the same moment into two buckets. `show` tells the reader that `deja index`
// regroups them, and it answered that the index was up to date (#1058).
func TestIndexRegroupsNotesGroupedInAnotherZone(t *testing.T) {
	east, err := time.LoadLocation("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	dir := filepath.Join(tmp, "idx")

	addNote := func(line string) {
		t.Helper()
		f, err := os.OpenFile(notes, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	inZone := func(zone *time.Location, f func()) {
		saved := time.Local
		time.Local = zone
		defer func() { time.Local = saved }()
		f()
	}

	// Two days apart in UTC, one day in UTC+14: the index built here splits
	// them, and carrying the laptop east regroups them.
	addNote(`{"ts":"2026-07-20T23:45:00Z","project":"tz","text":"the anemometer drifted"}`)
	addNote(`{"ts":"2026-07-21T09:00:00Z","project":"tz","text":"the barometer drifted too"}`)
	inZone(time.UTC, func() {
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	})

	// The split is the premise, not the finding: without two buckets here the
	// rest of the case proves nothing.
	if _, n := index.UpToDate(dir, ""); n != 2 {
		t.Fatalf("two notes on two UTC days gave %d sessions, want the 2 this case is about", n)
	}

	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	var indexErr error
	out := captureStderr(t, func() { inZone(east, func() { indexErr = cmdIndex(dir, nil) }) })
	if indexErr != nil {
		t.Fatal(indexErr)
	}

	var n int
	inZone(east, func() { _, n = index.UpToDate(dir, "") })
	if n != 1 {
		t.Errorf("deja index left %d note buckets for one day, want 1 — it said:\n%s", n, out)
	}
	if !strings.Contains(out, "another time zone") {
		t.Errorf("deja index regrouped without saying why:\n%s", out)
	}
}

// A run that wrote the index but left part of its input unread exited 0, the
// same as a clean one, so cron could not tell them apart (#5396).
func TestIndexExitsThreeWhenItLeftInputUnread(t *testing.T) {
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	seedClaude(t, claude, "app", "s2", "the redis pool too", "same fix")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a clean build: %v", err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a clean run with nothing new: %v", err)
	}

	// A line it could not use, in what this run read.
	f, err := os.OpenFile(filepath.Join(claude, "-app", "s2.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	if _, err := captureRunStderr(t, "index"); !isExitStatus(err, exitPartial) {
		t.Fatalf("a run that skipped a line returned %v, want exit status %d", err, exitPartial)
	}
	// The next run read nothing new, so it skipped nothing.
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a run after the skipped line returned %v", err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	locked := filepath.Join(claude, "-app", "s1.jsonl")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	for _, run := range []string{"the run that finds it", "the run after"} {
		if _, err := captureRunStderr(t, "index"); !isExitStatus(err, exitPartial) {
			t.Fatalf("%s with an unreadable transcript returned %v, want exit status %d", run, err, exitPartial)
		}
	}
	// --quiet says nothing about success; the status still says partial.
	if _, err := captureRunStderr(t, "index", "--quiet"); !isExitStatus(err, exitPartial) {
		t.Fatalf("--quiet returned %v, want exit status %d", err, exitPartial)
	}
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("readable again, the run returned %v", err)
	}
}

func isExitStatus(err error, code int) bool { //nolint:unparam // the code is what it checks for
	var s exitStatus
	return errors.As(err, &s) && int(s) == code
}

// Piped or under cron there is no live display: one line per phase, with
// counts, so a long run can be told from a stuck one (#5396).
func TestIndexPrintsItsPhasesWhenNotOnATerminal(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv marks the run a warmup, which publishes its phases to the
	// status file instead.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	saved := logoWanted
	logoWanted = func(*os.File) bool { return false }
	t.Cleanup(func() { logoWanted = saved })
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	out, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"deja: finding transcripts",
		"deja: reading sessions — 1 file",
		"deja: indexing messages — 2 messages",
		"deja: writing index",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q line:\n%s", strings.TrimSpace(want), out)
		}
	}
	if strings.Count(out, "deja: finding transcripts") != 1 {
		t.Errorf("a phase printed more than once:\n%s", out)
	}
	quiet, _ := captureRunStderr(t, "index", "--rebuild", "--quiet")
	if strings.Contains(quiet, "reading sessions") {
		t.Errorf("--quiet printed phases:\n%s", quiet)
	}
}

// The command whose whole job is building the index used to pass the syscall
// through: `mkdir /…/index.db.tmp: permission denied` names an internal temp
// path and no fix, while every reading command has said what to change since
// ensureError was written (#798).
func TestIndexExplainsADeniedWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	store := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"gypsy wildcat pitch mismatch"},"timestamp":"2026-07-04T10:00:00Z","sessionId":"p4d","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := cmdIndex(filepath.Join(locked, "index.db"), nil)
	if err == nil {
		t.Fatal("a denied write reported success")
	}
	if !strings.Contains(err.Error(), "check the directory's permissions") {
		t.Errorf("error does not say what to change: %v", err)
	}
	if strings.Contains(err.Error(), ".tmp") || strings.Contains(err.Error(), "mkdir") {
		t.Errorf("error still leaks the syscall and an internal path: %v", err)
	}
}

// `deja index --quiet` for the runs nobody is watching (#1827).
//
// The flag suppresses the success reporting and nothing else: a scheduled run
// that goes quiet about failure is worse than one that prints a line, so the
// tests below pin both halves.

func indexEnv(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
}

func TestIndexQuietSaysNothingOnSuccess(t *testing.T) {
	indexEnv(t)
	// Build once so the second run is the ordinary "already fresh" path a
	// shell profile or hook actually hits.
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index", "--quiet")
	if err != nil {
		t.Fatalf("index --quiet: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("index --quiet printed %q", out)
	}
}

func TestIndexWithoutQuietStillReports(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(out, "up to date") {
		t.Fatalf("index without --quiet said %q, expected the up-to-date line", out)
	}
}

// The issue asks which of the two the flag covers. Both: they print through
// the same sink, and a reader would not expect one to stay noisy.
func TestIndexQuietCoversRebuild(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index", "--quiet", "--rebuild")
	if err != nil {
		t.Fatalf("index --quiet --rebuild: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("index --quiet --rebuild printed %q", out)
	}
}

// Quiet is about the success line. A run that cannot build still has to say
// why, or a scheduled job stops working and nothing says so.
func TestIndexQuietStillReportsFailure(t *testing.T) {
	indexEnv(t)
	// A regular file where a parent directory should be, rather than an
	// unwritable directory: a mode bit denies nothing on Windows or to root,
	// so the build succeeded there and the test read that as quiet swallowing
	// the error. Creating a directory underneath a file fails everywhere.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocker, "index.db"))

	out, err := captureRunStderr(t, "index", "--quiet")
	if err == nil {
		t.Fatal("index --quiet into an unwritable directory reported success")
	}
	// The error carries the explanation; whichever stream it lands on, the
	// command must not have swallowed it.
	if strings.TrimSpace(out) == "" && strings.TrimSpace(err.Error()) == "" {
		t.Fatal("index --quiet failed silently")
	}
}

func TestIndexStillRefusesUnknownFlags(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index", "--quiett"); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

// The live display erases itself when the build ends, so an interactive
// rebuild left an empty screen: an animation ran for seconds and nothing said
// what was indexed. Piped output has printed the counts all along (#867).
func TestRebuildOnATerminalSaysWhatItIndexed(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// hermeticEnv points the warmup sentinel at a guard path for every test in
	// this package; this case is the ordinary interactive run.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	// Never the real thing: on Windows a detached child holds deja.test.exe
	// open and `go test` fails to remove it after the run.
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })

	run := func(t *testing.T) string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := os.Stderr
		os.Stderr = w
		// Drained concurrently: a windows pipe buffers a few KB and a rebuild
		// reports per file and per harness.
		done := make(chan string, 1)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		err = cmdIndex(dir, []string{"--rebuild"})
		os.Stderr = stderr
		_ = w.Close()
		out := <-done
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	got := run(t)
	if !strings.Contains(got, "indexed 1 session, 1 message") {
		t.Errorf("rebuild said nothing about what it built:\n%s", got)
	}

	// Not on the warmup child: nobody is watching its screen, and its output
	// goes to /dev/null.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(tmp, "sentinel"))
	if got := run(t); strings.Contains(got, "indexed 1 session") {
		t.Errorf("the detached warmup narrated to nobody:\n%s", got)
	}
}

// seedFailThenFix writes two sessions where a test failed and the same command
// ran after it, and builds the index.
func seedFailThenFix(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"s1", "s2"} {
		line := `{"type":"assistant","timestamp":"2026-07-01T10:00:00Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n" +
			`{"type":"user","timestamp":"2026-07-01T10:00:05Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"panic: runtime error: index out of range [3] with length 3"}]}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-07-01T10:01:00Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go generate ./..."}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// On a search-only index `deja fix` says no fixes were mined, rather than that
// nothing on this machine ever followed the error (#5395).
func TestFixOnASearchOnlyIndexSaysWhy(t *testing.T) {
	const errLine = "panic: runtime error: index out of range [3] with length 3"
	// The control: the same store mined names the remedy.
	var out bytes.Buffer
	if err := runFix(seedFailThenFix(t), []string{errLine}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go generate") {
		t.Fatalf("the mined store has no fix for the error, so this proves nothing: %q", out.String())
	}
	out.Reset()

	t.Setenv(index.SearchOnlyEnv, "1")
	dir := seedFailThenFix(t)
	if err := runFix(dir, []string{errLine}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "search-only") || strings.Contains(out.String(), "no session on this machine") {
		t.Errorf("deja fix on a search-only index: %q", out.String())
	}
	text, err := callMCPTool(dir, "fix", []byte(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "search-only") || strings.Contains(text, "No session on this machine") {
		t.Errorf("MCP fix on a search-only index: %q", text)
	}
}

// With the parse-level switch off, `deja how` names the switch rather than
// saying no command mentions the words.
func TestHowNamesTheSwitchThatKeptCommandsOut(t *testing.T) {
	var out bytes.Buffer
	if err := runHow(seedFailThenFix(t), []string{"go", "test"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go test") || strings.Contains(out.String(), "DEJA_INDEX") {
		t.Fatalf("the control store does not list the command: %q", out.String())
	}
	out.Reset()

	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	dir := seedFailThenFix(t)
	if err := runHow(dir, []string{"go", "test"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DEJA_INDEX_COMMANDS=0") {
		t.Errorf("deja how with commands off: %q", out.String())
	}
	text, err := callMCPTool(dir, "how", []byte(`{"what":"go test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "DEJA_INDEX_COMMANDS=0") {
		t.Errorf("MCP how with commands off: %q", text)
	}
}

// And `deja restore` names DEJA_INDEX_EDITS=0.
func TestRestoreNamesTheSwitchThatKeptEditsOut(t *testing.T) {
	t.Setenv("DEJA_INDEX_EDITS", "0")
	dir := seedFailThenFix(t)
	var out bytes.Buffer
	if err := runRestore(dir, []string{"main.go"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DEJA_INDEX_EDITS=0") {
		t.Errorf("deja restore with edits off: %q", out.String())
	}
}

// The index summary says how long the build took (#4630), at a precision that
// reads naturally for the size of the number.
func TestTookSuffix(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{-time.Second, ""},
		{150 * time.Millisecond, " in 0.15s"},
		{3200 * time.Millisecond, " in 3.2s"},
		{42 * time.Second, " in 42s"},
		{125 * time.Second, " in 2m05s"},
	} {
		if got := tookSuffix(tc.d); got != tc.want {
			t.Errorf("tookSuffix(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// The first-index greeting states the time when the build was timed, and
// claims none when it was not.
func TestFirstIndexInfoSaysHowLongItTook(t *testing.T) {
	b := index.BuildSummary{Initial: true, Sessions: 3, Messages: 12, Harnesses: 1,
		PerHarness: []index.HarnessCount{{Name: "claude", Sessions: 3, Messages: 12}}}

	timed := b
	timed.Took = 150 * time.Millisecond
	if got := strings.Join(firstIndexInfo(timed, ""), "\n"); !strings.Contains(got, "agent in 0.15s") {
		t.Errorf("timed build's summary does not say how long it took:\n%s", got)
	}
	if got := strings.Join(firstIndexInfo(b, ""), "\n"); strings.Contains(got, " in 0") {
		t.Errorf("untimed build's summary claims a duration:\n%s", got)
	}
}

// Every build that runs through withBuildProgress is timed, including the
// non-terminal path it returns early on: the first `deja search` or
// `deja warmup` builds the index there, and its greeting needs the time too.
func TestWithBuildProgressTimesEveryBuild(t *testing.T) {
	saved := index.LastBuild
	t.Cleanup(func() { index.LastBuild = saved })
	for _, sentinel := range []string{"", "1"} {
		t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
		index.LastBuild = index.BuildSummary{}
		err := withBuildProgress(func() error {
			time.Sleep(5 * time.Millisecond)
			index.LastBuild = index.BuildSummary{Initial: true, Messages: 1}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if index.LastBuild.Took < 5*time.Millisecond {
			t.Errorf("sentinel %q: Took = %v, want the build's wall time", sentinel, index.LastBuild.Took)
		}
	}
}

// `deja index` from cron runs the incremental pass, and it said "index is up
// to date" about a store with a transcript it could not read; only --rebuild
// named it (#5393). Every run says so while the path stays locked.
func TestIncrementalIndexNamesAPathItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	seedClaude(t, claude, "app", "s2", "the redis pool too", "same fix")
	if out, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first build: %v\n%s", err, out)
	}
	locked := filepath.Join(claude, "-app", "s1.jsonl")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	// doctor names it before any pass has: it opened only the newest file.
	if out, errOut := captureBoth(t, "doctor"); !strings.Contains(out+errOut, "permission denied on "+locked) {
		t.Errorf("doctor does not name the locked transcript:\n%s%s", out, errOut)
	}
	for _, run := range []string{"the run that finds it", "the run after"} {
		out, _ := captureRunStderr(t, "index")
		if !strings.Contains(out, "claude: 1 path could not be read") {
			t.Errorf("%s does not name the unreadable path:\n%s", run, out)
		}
	}
}

// Silence reads as "it did not run". `update` on the newest release and
// `doctor` on a fresh index both say so; this one returned to the prompt with
// nothing (#824).
func TestIndexSaysWhenThereIsNothingToDo(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"rudder trunk seal"},"timestamp":"2026-07-04T10:00:00Z","sessionId":"u9","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "up to date") {
		t.Errorf("the first build claimed there was nothing to do:\n%s", first)
	}

	second, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "up to date") {
		t.Errorf("a no-op run said nothing:\n%s", second)
	}

	// The warmup child runs this command; returning before the sentinel is
	// cleared leaves a build that is not running (#839).
	sentinel := filepath.Join(t.TempDir(), "warmup.sentinel")
	if werr := os.WriteFile(sentinel, []byte("1\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	if _, cerr := captureRunStderr(t, "index"); cerr != nil {
		t.Fatal(cerr)
	}
	if _, serr := os.Stat(sentinel); serr == nil {
		t.Error("a run with nothing to do left its warmup sentinel behind")
	}

	// --rebuild is explicit work, never "nothing to do".
	forced, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(forced, "up to date") {
		t.Errorf("--rebuild reported nothing to do:\n%s", forced)
	}

	// And the line belongs to `index` alone: a search that finds the index
	// fresh must not narrate it.
	found, err := captureRunStderr(t, "--all", "rudder")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(found, "up to date") {
		t.Errorf("a search narrated the index state:\n%s", found)
	}

	if fresh, n := index.UpToDate(indexDirForTest(), ""); !fresh || n == 0 {
		t.Errorf("UpToDate = %v, %d", fresh, n)
	}
}

// A run that waits behind another build finishes with the index current and
// nothing printed by the build itself — so the command owes the closing line
// the same run would print when nothing was in flight (#1751).
func TestAnIndexRunThatBuiltNothingStillSaysWhereItStands(t *testing.T) {
	var out bytes.Buffer
	w := &countingWriter{w: &out}
	if _, err := w.Write([]byte("deja: incremental index changed_files=1\n")); err != nil {
		t.Fatal(err)
	}
	if w.n == 0 {
		t.Error("the writer did not count what the build printed")
	}

	quiet := &countingWriter{w: &bytes.Buffer{}}
	if quiet.n != 0 {
		t.Error("a build that printed nothing counted output")
	}
	if line := indexQuietOutcome(true, 760); !strings.Contains(line, "up to date") || !strings.Contains(line, "760") {
		t.Errorf("a silent build over a current index ends with %q", line)
	}
	// A store that changed again while the other build was finishing: the run
	// still says where things stand rather than claiming to be current.
	if line := indexQuietOutcome(false, 760); strings.Contains(line, "up to date") || !strings.Contains(line, "760") {
		t.Errorf("a silent build over a stale index ends with %q", line)
	}
}

// The freshness walk enumerates every registered store and stats every
// candidate, which on a slow volume is the longest part of `deja index`. It sat
// in an `if` initializer, and Go runs an initializer before it tests the
// condition — so a run that had already reported what it did walked the stores
// again and threw the answer away (#3500).
//
// Pinned on the source because the waste is invisible from outside: the command
// printed the same bytes either way.
func TestTheQuietOutcomeWalkRunsInsideItsBranch(t *testing.T) {
	src := string(repoFile(t, "cmd/deja/main.go"))
	if strings.Contains(src, "if fresh, n := index.UpToDate(dir, \"\"); progress.n == 0 {") {
		t.Error("the freshness walk is back in the if initializer, so it runs when its result is discarded")
	}
	if !strings.Contains(src, "if progress.n == 0 {\n\t\tfresh, n := index.UpToDate(dir, \"\")") {
		t.Error("cmdIndex no longer asks for the quiet outcome inside the branch that prints it")
	}
}
