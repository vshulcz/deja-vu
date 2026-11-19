package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/stats"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEmptyIndexHintPointsAtTheRightNextStep(t *testing.T) {
	// Hermetic env: no store has any files, so the honest answer is that there
	// is no history here — not that the index needs building, which every path
	// reaching this message has just done.
	hermeticEnv(t)
	got := emptyIndexHint("nothing indexed yet")
	if !strings.Contains(got, "no agent history was found") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "run `deja index`") {
		t.Fatalf("advising a rebuild after a rebuild sends the reader in a circle: %q", got)
	}
	if !strings.Contains(got, "deja sources") {
		t.Fatalf("the reader needs somewhere to go: %q", got)
	}
}

func TestParseDurAcceptsWhatDejaDocuments(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"12h": 12 * time.Hour,
		"90m": 90 * time.Minute,
		"45s": 45 * time.Second,
	} {
		got, err := parseDur(in)
		if err != nil || got != want {
			t.Errorf("parseDur(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestParseDurExplainsItselfOnJunk(t *testing.T) {
	for _, in := range []string{"abc", "7x", "", "d", "-", "30 d"} {
		_, err := parseDur(in)
		if err == nil {
			t.Errorf("parseDur(%q) should fail", in)
			continue
		}
		// time.ParseDuration's own wording names Go's syntax and never mentions
		// days, which is the unit this flag is usually given.
		if strings.Contains(err.Error(), "time: invalid duration") {
			t.Errorf("parseDur(%q) leaked the stdlib message: %v", in, err)
		}
		if !strings.Contains(err.Error(), "30d") {
			t.Errorf("parseDur(%q) should say what is accepted: %v", in, err)
		}
	}
}

func TestEnsureErrorSaysWhatToChange(t *testing.T) {
	// A denied write surfaced as `ensure: open /…/index.db.lock: permission
	// denied` — an internal lock path and a syscall error, neither of which
	// tells the reader what to do.
	// The directory has to be one that exists: a path whose parent is gone is
	// a disk that was unmounted, which says something else entirely (#931).
	dir := filepath.Join(t.TempDir(), "index.db")
	err := ensureError(dir, fs.ErrPermission)
	if err == nil {
		t.Fatal("want an error")
	}
	got := err.Error()
	for _, want := range []string{dir, "permissions", "DEJA_INDEX_DIR"} {
		if !strings.Contains(got, want) {
			t.Errorf("message should mention %q: %s", want, got)
		}
	}
	if strings.Contains(got, ".lock") {
		t.Errorf("the lock file is deja's business, not the reader's: %s", got)
	}
	// Anything else keeps its original wording rather than being guessed at.
	other := ensureError("/x", errors.New("disk on fire"))
	if !strings.Contains(other.Error(), "disk on fire") {
		t.Errorf("unrelated failures must not be rewritten: %v", other)
	}
}

// "nothing to index yet" is the sentence for a machine deja has never had
// history from. Printed after a store went away it contradicts the line above
// it, which has just said what was lost: the reader is told they are new here
// and that N indexed files are gone, in that order (#1762).
func TestTheEmptyIndexSentenceKnowsSomethingWasLost(t *testing.T) {
	fresh := emptyIndexReason(index.BuildSummary{Initial: true}, 0)
	if !strings.Contains(fresh, "nothing to index yet") {
		t.Errorf("a first run says %q", fresh)
	}

	lost := emptyIndexReason(index.BuildSummary{}, 3)
	if strings.Contains(lost, "yet") {
		t.Errorf("a run that evicted a store still reads as a first run: %q", lost)
	}
	if !strings.Contains(lost, "3") {
		t.Errorf("the sentence does not say how much went away: %q", lost)
	}
	if !strings.Contains(lost, "gone") && !strings.Contains(lost, "went away") {
		t.Errorf("the sentence does not say what happened: %q", lost)
	}

	// One file reads as one file.
	if one := emptyIndexReason(index.BuildSummary{}, 1); strings.Contains(one, "1 indexed files") {
		t.Errorf("plural for a single file: %q", one)
	}
}

// Records kept back because their volume is merely unmounted have not gone
// away, so the counter must not see them — otherwise a reconnectable disk reads
// as history lost.
func TestUnmountedRecordsAreNotCountedAsGone(t *testing.T) {
	if n := index.ReportEvictedFiles(); n != 0 {
		t.Fatalf("the counter starts at %d", n)
	}
	// Two reads in a row: the first clears it, so a later command in the same
	// process cannot inherit a count that was already spent.
	if n := index.ReportEvictedFiles(); n != 0 {
		t.Errorf("the counter did not clear: %d", n)
	}
}

// Empty is how "no filter" is spelled internally, so a flag given an empty
// value used to reach the search as no filter at all — `--project ""` from a
// script with an unset variable searched the whole store and read as a scoped
// answer (#1612).
func TestFilterFlagsRefuseAnEmptyValue(t *testing.T) {
	for _, flag := range []string{"--harness", "--project", "--role", "--session"} {
		o, err := parseSearch([]string{"retry", flag, ""})
		if err == nil {
			t.Errorf("%s \"\" was accepted: harness=%q project=%q role=%q session=%q",
				flag, o.Harness, o.Project, o.Role, o.Session)
			continue
		}
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: error does not name the flag: %v", flag, err)
		}
	}
	// The control: real values still parse, and a flag left out stays unset.
	o, err := parseSearch([]string{"retry", "--harness", "claude", "--role", "user"})
	if err != nil {
		t.Fatalf("parseSearch with real values: %v", err)
	}
	if o.Harness != "claude" || o.Role != "user" || o.Project != "" {
		t.Errorf("harness=%q role=%q project=%q", o.Harness, o.Role, o.Project)
	}
}

// `deja last` parses its own flags, and had the same hole.
func TestLastFlagsRefuseAnEmptyValue(t *testing.T) {
	for _, flag := range []string{"--harness", "--project", "--role", "--from"} {
		if _, _, _, err := parseLast([]string{flag, ""}); err == nil {
			t.Errorf("last %s \"\" was accepted", flag)
		}
	}
	if _, o, _, err := parseLast([]string{"--harness", "claude"}); err != nil || o.Harness != "claude" {
		t.Errorf("last --harness claude = %q, %v", o.Harness, err)
	}
}

// The other four parsers had the same block. Review found them after the first
// fix landed on `deja` and `deja last` only (#1612).
func TestEveryFilterParserRefusesAnEmptyValue(t *testing.T) {
	if _, err := parseShow([]string{"abc", "--harness", ""}); err == nil {
		t.Error("show --harness \"\" was accepted")
	}
	if _, err := parseShow([]string{"abc", "--harness", "claude"}); err != nil {
		t.Errorf("show --harness claude: %v", err)
	}
	for _, flag := range []string{"--harness", "--project"} {
		if _, _, _, err := parseBlame([]string{"main.go", flag, ""}); err == nil {
			t.Errorf("blame %s \"\" was accepted", flag)
		}
	}
	if _, _, _, err := parseBlame([]string{"main.go", "--harness", "claude"}); err != nil {
		t.Errorf("blame --harness claude: %v", err)
	}
}

// stats and forget parse their flags inside their run functions, so they need
// the store to reach the guard. Both were named as untested in review.
func TestStatsAndForgetRefuseAnEmptyValue(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := runStats(dir, []string{"--harness", ""}); err == nil {
		t.Error("stats --harness \"\" was accepted")
	}
	if err := runForget(dir, []string{"--project", "", "--dry-run"}); err == nil {
		t.Error("forget --project \"\" was accepted")
	}
	// The control: --unforget keeps its own refusal, which asks for an id
	// rather than offering the flags that forget instead of restoring.
	err := runForget(dir, []string{"--unforget", ""})
	if err == nil || !strings.Contains(err.Error(), "needs an id") {
		t.Errorf("--unforget \"\" lost its own refusal: %v", err)
	}
}

// The only transcript on disk was deleted: the index keeps the session
// searchable and says so, and must not add that no history was found (#4221).
// The shape the bug was seen in: a Gemini CLI store after --delete-session.
func TestIndexDoesNotCallKeptHistoryMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats := filepath.Join(root, "tmp", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chats, "session-2026-10-01T13-56-22931634.jsonl")
	body := `{"sessionId":"22931634-2b2c-4b24-b439-2a495200e262","projectHash":"abc","startTime":"2026-10-01T13:56:00.000Z","lastUpdated":"2026-10-01T13:56:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T13:56:01.000Z","type":"user","content":[{"text":"why does the quillfern cache miss"}]}
{"id":"g1","timestamp":"2026-10-01T13:56:02.000Z","type":"gemini","content":"it is cold after every deploy"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The next `deja index` is a new process: nothing carried from this one.
	index.LastBuild = index.BuildSummary{}
	stderr, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "no longer on disk") {
		t.Fatalf("control: the pass did not report the deleted transcript:\n%s", stderr)
	}
	if strings.Contains(stderr, "nothing to index yet") {
		t.Fatalf("the index holds the session and still says nothing was found:\n%s", stderr)
	}
}

// The empty-index gate must not take the permission-denied line with it: with
// one Gemini project indexed and another behind chmod 000, that line is the
// only pointer to the store the pass could not read. It drops the "nothing to
// index" half, since the index still holds sessions (#4221).
func TestDeniedStorePlusNonEmptyIndexStillNamesTheDeniedStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not deny a read on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	write := func(project, id, word string) string {
		chats := filepath.Join(root, "tmp", project, "chats")
		if err := os.MkdirAll(chats, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"sessionId":"` + id + `","projectHash":"` + project + `","startTime":"2026-10-01T13:56:00.000Z","lastUpdated":"2026-10-01T13:56:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T13:56:01.000Z","type":"user","content":[{"text":"why does the ` + word + ` cache miss"}]}
{"id":"g1","timestamp":"2026-10-01T13:56:02.000Z","type":"gemini","content":"it is cold after every deploy"}
`
		if err := os.WriteFile(filepath.Join(chats, "session-2026-10-01T13-56-"+id[:8]+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(root, "tmp", project)
	}
	write("alpha", "11111111-2b2c-4b24-b439-2a495200e262", "quillfern")
	locked := write("beta", "22222222-2b2c-4b24-b439-2a495200e262", "marrowlace")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if deniedStoreCount() == 0 {
		t.Skip("chmod 000 left the project readable here")
	}
	index.LastBuild = index.BuildSummary{}
	stderr, err := captureRunStderr(t, "index")
	// Written, with a store it could not read: exit status 3 (#5396).
	if !isExitStatus(err, exitPartial) {
		t.Fatalf("got %v, want exit status %d", err, exitPartial)
	}
	if !strings.Contains(stderr, "could not be read (permission denied)") {
		t.Fatalf("the denied store went unnamed:\n%s", stderr)
	}
	for _, wrong := range []string{"nothing to index", "nothing is left"} {
		if strings.Contains(stderr, wrong) {
			t.Fatalf("the index holds sessions and stderr says %q:\n%s", wrong, stderr)
		}
	}
}

// The notes file is a store file, and it was counted by existence: an empty one
// — a note written and later forgotten — made deja answer "run `deja index`"
// one line under the index run it had just narrated (#996).
func TestAnEmptyNotesFileIsNotAgentHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if err := os.WriteFile(notes, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !noAgentHistoryFound() {
		t.Error("an empty notes file was counted as history")
	}
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-08-04T10:00:00Z","project":"p","text":"the ticker window stays at 30s"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if noAgentHistoryFound() {
		t.Error("a note someone wrote stopped counting as history")
	}
	if hint := emptyIndexHint("no matches"); !strings.Contains(hint, "deja index") {
		t.Errorf("a machine with history lost the advice that fits it: %q", hint)
	}
}

// An output-path flag given an empty value — a scripted `--to "$OUT"` / `--out
// "$OUT"` / `-o "$OUT"` with the shell variable unset — must be refused like an
// absent one, not silently treated as "flag not passed" (#5891).
func TestOutputPathFlagsRejectEmptyValue(t *testing.T) {
	dir := t.TempDir()
	for _, val := range []string{"", "   "} {
		if err := runPromote(dir, []string{"sess1234", "--to", val}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "promote: --to needs a path") {
			t.Fatalf("runPromote([sess1234 --to %q]) = %v; want 'promote: --to needs a path'", val, err)
		}
		if err := runView(dir, []string{"--out", val}); err == nil || !strings.Contains(err.Error(), "view: --out needs a path") {
			t.Fatalf("runView([--out %q]) = %v; want 'view: --out needs a path'", val, err)
		}
		if err := runRestore(dir, []string{"sess1234", "-o", val}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "restore: -o needs value") {
			t.Fatalf("runRestore([sess1234 -o %q]) = %v; want 'restore: -o needs value'", val, err)
		}
	}
}

// An unset shell variable arrives as an empty argument. Every other selector
// command refuses one; share printed a shareable digest of whichever session
// deja reached first, and ctx printed its transcript (#2259).
func TestAnEmptySelectorIsNotASession(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for i := 1; i <= 2; i++ {
		line := fmt.Sprintf(`{"type":"user","sessionId":"sess%d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop number %d"}}`, i, at, i)
		name := fmt.Sprintf("s%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(claude, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	// The premise: both commands work when they are given a real selector.
	if err := runShare(dir, []string{"sess1"}, &out); err != nil || !strings.Contains(out.String(), "sess1") {
		t.Fatalf("share of a real id: %v %q", err, out.String())
	}
	out.Reset()
	if err := cmdCtx(dir, []string{"sess1"}); err != nil {
		t.Fatalf("ctx of a real id: %v", err)
	}

	out.Reset()
	err := runShare(dir, []string{""}, &out)
	if err == nil {
		t.Errorf("share of an empty selector printed: %q", out.String())
	} else if !strings.Contains(err.Error(), "id prefix") {
		t.Errorf("share of an empty selector said %v", err)
	}

	if err := cmdCtx(dir, []string{""}); err == nil {
		t.Error("ctx of an empty selector was answered")
	} else if !strings.Contains(err.Error(), "id-prefix") {
		t.Errorf("ctx of an empty selector said %v", err)
	}
	// Space is the same nothing.
	out.Reset()
	if err := runShare(dir, []string{"  "}, &out); err == nil {
		t.Errorf("share of a blank selector printed: %q", out.String())
	}
}

// And the lookup itself: every id has the empty string as a prefix, so the
// guard belongs where the class lives rather than only at the callers (#2259).
func TestFindByPrefixMatchesNothingForABlankPrefix(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sess1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: a real prefix resolves.
	if _, ok, err := index.FindByPrefix(dir, "sess1"); err != nil || !ok {
		t.Fatalf("a real id did not resolve: %v %v", ok, err)
	}
	for _, p := range []string{"", " ", "\t"} {
		if s, ok, err := index.FindByPrefix(dir, p); ok || err != nil {
			t.Errorf("prefix %q resolved to %q (%v)", p, s.ID, err)
		}
	}
}

// A fresh install has nothing indexed, and the two commands people try first
// answered with silence and with section headings over nothing. Both read as
// broken rather than empty.
func TestStatsSaysWhatIsMissingWhenNothingIsIndexed(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{})
	got := out.String()
	if !strings.Contains(got, "nothing indexed yet") {
		t.Fatalf("no explanation on an empty index:\n%s", got)
	}
	// The headings are the part that made it look like a broken report.
	for _, heading := range []string{"By harness", "Top projects", "Last 12 months"} {
		if strings.Contains(got, heading) {
			t.Fatalf("empty section %q printed over an empty index:\n%s", heading, got)
		}
	}
	// Which next step depends on why it is empty — a rebuild when stores exist,
	// `deja sources` when there is no agent history at all — so assert that one
	// is offered rather than pinning the wording.
	if !strings.Contains(got, "deja index") && !strings.Contains(got, "deja sources") {
		t.Fatalf("no next step offered:\n%s", got)
	}
}

// A report with data must still print everything, or this guard has cost the
// feature rather than fixed it.
func TestStatsStillPrintsSectionsWithData(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{TotalSessions: 3, TotalMessages: 40})
	got := out.String()
	for _, heading := range []string{"By harness", "Top projects"} {
		if !strings.Contains(got, heading) {
			t.Fatalf("section %q went missing with data present:\n%s", heading, got)
		}
	}
}

func TestEmptyIndexHintNamesTheNextCommand(t *testing.T) {
	// With a store present but nothing indexed from it, a rebuild is the right
	// advice. The other branch — no history anywhere — is covered in
	// empty_advice_test.go, where repeating the build would achieve nothing.
	dir := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", dir)
	writeClaudeFixture(t, filepath.Join(dir, "proj", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"anything at all"}}`,
	})
	got := emptyIndexHint("no sessions indexed yet")
	for _, want := range []string{"no sessions indexed yet", "deja index", "deja doctor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint missing %q: %q", want, got)
		}
	}
}

// An empty window names the newest session and the --since that reaches it:
// "nothing in the last 1d" alone read like a machine with no history.
func TestAnEmptyRecapWindowNamesTheNewestSession(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecap(dir, []string{"--since", "1d"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "the newest session is from") || !strings.Contains(got, "`deja recap --since ") {
		t.Fatalf("the empty window does not say where the history is:\n%s", got)
	}
}

// On a machine with no history, tests, secrets, recap and rules candidates
// answer in the words every other command uses, and point at `deja sources`.
// "no provider keys … in your agent history" read as an all-clear.
func TestEmptyStatesShareOnePhrasing(t *testing.T) {
	hermeticEnv(t)
	for _, args := range [][]string{{"tests"}, {"secrets"}, {"recap"}, {"rules", "candidates"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "`deja sources` shows where deja looked") {
			t.Errorf("%v on an empty machine: %q", args, out)
		}
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "wrapped for sharing") {
		t.Errorf("an empty stats screen still prints its title:\n%s", out)
	}
}

// remember over an existing index used to open with "deja: updated 1 file
// (1 new message)" — the note it had just written, reported as news.
func TestRememberDoesNotNarrateItsOwnNote(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "remember", "staging deploys need the VPN", "--project", "p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "updated ") {
		t.Fatalf("remember narrated the index:\n%s", said)
	}
}

// The word the reader typed is the part they got right.
func TestSyncNamesWhatIsMissing(t *testing.T) {
	hermeticEnv(t)
	for sub, want := range map[string]string{
		"export": "deja sync export <dir>",
		"import": "deja sync import <dir>",
		"ssh":    "deja sync ssh <host>",
	} {
		err := runSync(index.DefaultDir(), []string{sub})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("sync %s: %v", sub, err)
		}
	}
}

// On a machine with no history yet, recall answered "No prior deja sessions
// matched <query>" — which reads as "your query missed" and invites an agent to
// keep rephrasing. The same function already separates three other reasons an
// answer is empty, for the reason its own comment gives: an agent told nothing
// matched concludes the work was never done (#680). "Nothing is indexed" is the
// fourth, and it is the one a first run always hits.
func TestAnEmptyStoreSaysSoRatherThanBlamingTheQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("the fixture indexed %d sessions, so this is not the empty case", len(metas))
	}

	for _, tool := range []string{"recall", "recall_context"} {
		text, err := callMCPTool(dir, tool, json.RawMessage(`{"query":"how did we set up the deploy"}`))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "matched") && !strings.Contains(text, "no indexed history") {
			t.Errorf("%s: an empty store answers as though the query missed:\n%s", tool, firstLines(text, 3))
		}
		if !strings.Contains(text, "no indexed history yet") {
			t.Errorf("%s: does not say the store is empty:\n%s", tool, firstLines(text, 3))
		}
		// A store nobody has forgotten anything from is a first run, and must
		// not be described as a removal — the two sentences share a prefix, so
		// the assertion above cannot tell them apart on its own (#2862).
		if strings.Contains(text, "forgotten") {
			t.Errorf("%s: a first run was reported as a deliberate removal:\n%s", tool, firstLines(text, 3))
		}
	}
}

// And a store that holds something keeps the ordinary answer: "nothing is
// indexed" must not be said about a query that simply found nothing.
func TestAStoreWithHistoryStillBlamesNeitherSide(t *testing.T) {
	dir := manySessionStore(t, 3)
	text, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zzzqqq wwwxxx vvvbbb"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "no indexed history") {
		t.Errorf("a store with sessions in it was called empty:\n%s", firstLines(text, 3))
	}
}

// An empty store is not a miss on the argument: deja has nothing to look in,
// and "no sessions mention it" reads as "looked, not there" (#834).
func TestEmptyStoreAnswersPointAtTheStores(t *testing.T) {
	hermeticEnv(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "anything"}},
		{"restore", []string{"restore", "some/file.go"}},
		{"ctx", []string{"ctx", "anything"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureRun(t, tc.args...)
			msg := out
			if err != nil {
				msg += err.Error()
			}
			if !strings.Contains(msg, "no agent history was found on this machine") {
				t.Errorf("empty store answered as a miss on the argument:\n%s", msg)
			}
		})
	}

	// With history, the ordinary answers stay: the store was searched and the
	// thing genuinely is not in it.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the hydraulic pump bearing failed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "zzzqqq"}},
		{"restore", []string{"restore", "some/file.go"}},
		{"ctx", []string{"ctx", "zzzqqqwww"}},
	} {
		t.Run(tc.name+" with history", func(t *testing.T) {
			out, err := captureRun(t, tc.args...)
			msg := out
			if err != nil {
				msg += err.Error()
			}
			if strings.Contains(msg, "no agent history") {
				t.Errorf("a store with history was called empty:\n%s", msg)
			}
		})
	}
}

// "This machine has no indexed history yet" is the right thing to say on a
// first run (#2862, #2863, #2865) and the wrong thing to say after the user
// forgot everything: the history existed, and "yet" claims it never did.
//
// deja knows the difference — forgetting leaves tombstones — and the CLI
// already says so on its own screen: "this machine also has 2 sessions
// forgotten (`deja forget --list`)".
func TestAnEmptiedStoreIsNotCalledAFirstRun(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"the zonkomatic deploy"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s1"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("the fixture still holds %d sessions, so this is not the emptied case", len(metas))
	}

	for _, tc := range []struct{ tool, args string }{
		{"recall", `{"query":"zonkomatic"}`},
		{"blame", `{"path":"main.go"}`},
		{"how", `{"what":"zonkomatic"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "no indexed history yet") {
			t.Errorf("%s: an emptied store is reported as a first run:\n%s", tc.tool, text)
		}
		if !strings.Contains(text, "forgotten") {
			t.Errorf("%s: nothing says the history was removed rather than absent:\n%s", tc.tool, text)
		}
		// The count, not just the word: "some sessions have been forgotten"
		// leaves the reader where they started, and a sentence that cannot
		// say how many is a sentence that never read the tombstones.
		if !strings.Contains(text, "1 session has been forgotten") {
			t.Errorf("%s: the sentence does not say how much was removed:\n%s", tc.tool, text)
		}
	}
}

// An empty store is not a query problem: "fewer words" cannot help when
// nothing is indexed, and last, blame and the brief all say what to do
// instead. Search is the command a new machine reaches for first (#832).
func TestSearchOnAnEmptyStorePointsAtTheStores(t *testing.T) {
	hermeticEnv(t)

	out, err := captureRunStderr(t, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no agent history was found on this machine") {
		t.Errorf("an empty machine still gets query advice:\n%s", out)
	}
	if strings.Contains(out, "try fewer words") {
		t.Errorf("advice that cannot be followed is still printed:\n%s", out)
	}

	// Everything forgotten is not the same as nothing indexed: `deja index`
	// cannot bring back a tombstoned session, and the forgotten count is the
	// line that ends the search (#844).
	forgetStore := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-gone")
	if err = os.MkdirAll(forgetStore, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := `{"type":"user","message":{"role":"user","content":"the winch brake pads"},"timestamp":"2026-07-02T10:00:00Z","sessionId":"g1","cwd":"/gone"}`
	if err = os.WriteFile(filepath.Join(forgetStore, "g.jsonl"), []byte(gone+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "forget", "--session", "g1"); err != nil {
		t.Fatal(err)
	}
	emptied, err := captureRunStderr(t, "winch", "brake")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(emptied, "forgotten (`deja forget --list`)") {
		t.Errorf("a store emptied by forget does not name what was forgotten:\n%s", emptied)
	}
	if strings.Contains(emptied, "run `deja index`") {
		t.Errorf("advice that cannot bring a tombstoned session back:\n%s", emptied)
	}

	// With history indexed, an ordinary miss keeps the ordinary answer.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err = os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the hydraulic pump bearing failed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
	if err = os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	miss, err := captureRunStderr(t, "zzzqqq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(miss, "no matches in 1 indexed session") {
		t.Errorf("an ordinary miss lost its answer:\n%s", miss)
	}
	if strings.Contains(miss, "no agent history") {
		t.Errorf("a store with history was called empty:\n%s", miss)
	}
}
