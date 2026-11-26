package index

import (
	"crypto/sha1" //nolint:gosec // a short id, not a security hash
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// abortedPassIndex leaves an index behind whose last pass died between the
// parse and the manifest write, with exactly one unreadable line on disk.
func abortedPassIndex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"s1","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(turn("2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// One unreadable line and one good one: without the good one the pass has
	// no buckets to write and so never reaches the failure below.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(
		turn("2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out") +
			turn("2026-01-02T03:04:07Z", "assistant", "raised the pool to 40")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Kill the pass after the parse and before the manifest is written.
	buckets := filepath.Join(dir, "buckets")
	if err := os.Chmod(buckets, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err == nil {
		t.Fatal("the pass was supposed to fail before writing the manifest, so this measures nothing")
	}
	if err := os.Chmod(buckets, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func skipWhereADirectoryCannotBeMadeUnwritable(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a write on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
}

// The malformed-line counters used to be cleared in one place: the manifest
// fold, inside writeManifest. A pass that died before it left its count behind
// for whoever parsed next, so one bad line on disk was reported as two — with
// the manifest agreeing, which is the worst shape for a number a person is
// meant to act on (#2010).
func TestAnAbortedPassDoesNotLendItsCountToTheNextOne(t *testing.T) {
	skipWhereADirectoryCannotBeMadeUnwritable(t)
	dir := abortedPassIndex(t)

	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()
	if !strings.Contains(said, "— 1 line skipped") {
		t.Errorf("one unreadable line on disk, and the run does not report exactly one:\n%s", said)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("one unreadable line on disk, manifest says %d", got)
	}
}

// The same leak by the path a recall takes. An aborted pass leaves records.bin
// short, so the next search finds the index damaged and rebuilds — through
// rebuildForSearch, which never passes through updateIndex, which is where the
// first fix for this put the drain.
func TestASearchRebuildDoesNotInheritADeadPassCount(t *testing.T) {
	skipWhereADirectoryCannotBeMadeUnwritable(t)
	dir := abortedPassIndex(t)

	if err := EnsureForSearch(dir, query.Options{}, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("one unreadable line on disk, the search rebuild recorded %d", got)
	}
}

// A session forgotten under the id an older deja gave it stays forgotten. aider
// ids were the history's path and the session's place in it, and became the
// path and the start time (#4332): the tombstone names the old id, and the
// rebuild that follows the upgrade indexed the session again under the new one.
func TestAForgottenAiderSessionStaysForgottenUnderItsNewID(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	doc := "# aider chat started at 2026-09-30 10:00:00\n\n#### fix the retry loop in worker.go  \n\nChanged the loop to back off.\n\n" +
		"# aider chat started at 2026-09-30 11:00:00\n\n#### paste the staging token here  \n\nNoted the hunter2 token.\n"
	if err := os.WriteFile(hist, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", root)
	h := sha1.Sum([]byte(hist)) //nolint:gosec // a short id, not a security hash
	legacy := "aider:aider-" + hex.EncodeToString(h[:6]) + "-2"
	if err := writeTombstones(map[string]bool{legacy: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	for _, rebuild := range []bool{true, false} {
		if err := Ensure(dir, "aider", rebuild, nil); err != nil {
			t.Fatal(err)
		}
		if ss, _ := Search(dir, search.Options{Query: "hunter2"}); len(ss) != 0 {
			t.Fatalf("the session forgotten as %s is back (rebuild=%v): %#v", legacy, rebuild, ss)
		}
		if ss, _ := Search(dir, search.Options{Query: "retry"}); len(ss) != 1 {
			t.Fatalf("the session nobody forgot is gone (rebuild=%v): %#v", rebuild, ss)
		}
		// The next pass re-reads the file after an append.
		f, err := os.OpenFile(hist, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("\n#### and the hunter2 rotation  \n\nRotated.\n")
		_ = f.Close()
	}
}

// A session the re-read file still holds under another id has not left it. Two
// launches in one second are told apart by order, so taking the first out of
// the file hands the second the first's id: the second's old record was kept
// as one that had left the file, and the session was indexed twice.
func TestAnAiderSessionWithANewIDIsNotKeptTwice(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	first := "# aider chat started at 2026-09-30 10:00:00\n\n#### the first launch  \n\nAnswered the alpha question.\n\n"
	second := "# aider chat started at 2026-09-30 10:00:00\n\n#### the second launch  \n\nAnswered the bravo question.\n"
	if err := os.WriteFile(hist, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "bravo"}); len(ss) != 1 {
		t.Fatalf("the session the file still holds is indexed %d times: %#v", len(ss), ss)
	}
}

// aider has no session ids, and deja numbered sessions by their place in the
// history file. People delete that file because aider appends to it forever;
// deja keeps what it held, and the next aider run at the same path started a
// new file whose first session took id 1 again and overwrote the kept one
// (#4332).
func TestANewAiderHistoryKeepsTheDeletedOnesSessions(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(hist, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("# aider chat started at 2026-09-30 10:00:00\n\n#### fix the retry loop in worker.go  \n\nChanged the loop to back off.\n\n#### now add jitter to the backoff  \n\nAdded jitter with rand.Int63n.\n")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "home", ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hist); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "jitter"}); len(ss) != 1 {
		t.Fatalf("the deleted history's session was not kept: %#v", ss)
	}
	write("\n# aider chat started at 2026-10-01 09:00:00\n\n#### rename the config loader  \n\nRenamed it to loadSettings.\n")
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "jitter"}); len(ss) != 1 {
		t.Fatalf("the new history overwrote the kept session: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "loadSettings"}); len(ss) != 1 {
		t.Fatalf("the new history's session is not indexed: %#v", ss)
	}
	// aider appends to the new file: both are still there, once each.
	f, err := os.OpenFile(hist, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n#### and the env override  \n\nloadSettings reads DEJA_ENV first.\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"jitter", "loadSettings"} {
		if ss, _ := Search(dir, search.Options{Query: q}); len(ss) != 1 {
			t.Fatalf("%q after an append: %#v", q, ss)
		}
	}
	// A rebuild reads the file whole, and the kept session is in no file.
	if err := Ensure(dir, "aider", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"jitter", "loadSettings"} {
		if ss, _ := Search(dir, search.Options{Query: q}); len(ss) != 1 {
			t.Fatalf("%q after a rebuild: %#v", q, ss)
		}
	}
}

const aiderZoneHistory = "# aider chat started at 2026-07-20 23:45:00\n\n#### why does the pool run dry  \n\nthe cap is too low\n\n" +
	"# aider chat started at 2026-07-21 00:30:00\n\n#### raise the pool cap to 40  \n\nraised it\n"

func aiderZoneEnv(t *testing.T) (hist, dir string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_AIDER_ROOTS": "aider"})
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	hist = filepath.Join(tmp, "aider", "proj", ".aider.chat.history.md")
	parityWrite(t, hist, aiderZoneHistory, false)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	return hist, filepath.Join(tmp, "index.db")
}

// aider headers are read in the machine's zone, so an index built before a
// zone change held other instants than a rebuild after it, and an incremental
// pass kept them (#5053). The pass after the change rereads them.
func TestAiderZoneChangeMatchesRebuild(t *testing.T) {
	hist, dir := aiderZoneEnv(t)
	time.Local = time.FixedZone("pago", -11*3600)
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Fatal("an index read in this zone reports drift")
	}

	time.Local = time.FixedZone("kiritimati", 14*3600)
	if !AiderZoneDrift(dir) {
		t.Error("an index read 25 hours east reports no drift")
	}
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Error("drift is still reported after the pass")
	}
	sameAsRebuild(t, dir)

	// Growth after the change is read in the new zone and stays in step.
	parityWrite(t, hist, "# aider chat started at 2026-07-22 08:00:00\n\n#### add a pool metric  \n\nadded it\n", true)
	parityPass(t, dir, false)
	sameAsRebuild(t, dir)
}

// A kept row of a deleted history stays as it was read, by a rebuild too.
// Counting it as drift would rebuild the index on every pass.
func TestAiderKeptRowsAreNotZoneDrift(t *testing.T) {
	hist, dir := aiderZoneEnv(t)
	time.Local = time.FixedZone("pago", -11*3600)
	parityPass(t, dir, false)
	if err := os.Remove(hist); err != nil {
		t.Fatal(err)
	}
	parityPass(t, dir, false)
	// aider starts a new history at the same path (#4332).
	parityWrite(t, hist, "# aider chat started at 2026-08-01 09:00:00\n\n#### rename the loader  \n\nrenamed it\n", false)
	parityPass(t, dir, false)

	time.Local = time.FixedZone("kiritimati", 14*3600)
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Error("kept rows of the deleted history count as drift after the pass")
	}
	// A fresh build has no kept rows, so the rebuild is in place.
	before := paritySnapshot(t, dir)
	parityPass(t, dir, true)
	if after := paritySnapshot(t, dir); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("incremental index differs from a rebuild:\n  incremental: %v\n  rebuild:     %v", before, after)
	}
	if AiderZoneDrift(dir) {
		t.Error("kept rows count as drift after a rebuild")
	}
}

func TestAmpTrailingRootSeparatorMatchesDiscoveredFile(t *testing.T) {
	root := t.TempDir()
	ampRoot := filepath.Join(root, "amp", "threads")
	t.Setenv("DEJA_AMP_ROOT", ampRoot+string(filepath.Separator))

	path := filepath.Join(ampRoot, "thread-1.json")
	if err := os.MkdirAll(ampRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `{"id":"thread-1","title":"Trailing root","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	files := sources.AmpThreadFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("AmpThreadFiles = %v, want [%s]", files, path)
	}
	discovered := files[0]
	if got := harnessForPath(discovered); got != "amp" {
		t.Fatalf("harnessForPath(%q) = %q, want amp", discovered, got)
	}
	sessions, err := parseChangedFile("", discovered, FileState{})
	if err != nil {
		t.Fatalf("parseChangedFile(%q): %v", discovered, err)
	}
	if len(sessions) != 1 || sessions[0].ID != "thread-1" {
		t.Fatalf("parseChangedFile(%q) = %#v, want thread-1 session", discovered, sessions)
	}
}

func TestAmpDiscoveryAndIncrementalIndexParsing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	setHome(t, home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "amp", "threads"))
	path := filepath.Join(os.Getenv("DEJA_AMP_ROOT"), "thread-index.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"id":"thread-index","title":"Index Amp","created":1767337445000,"env":{"initial":{"trees":[{"uri":"file:///tmp/index-project"}]}},"messages":[{"role":"user","content":[{"type":"text","text":"index needle"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	files := currentFiles("amp")
	if _, ok := files[path]; !ok {
		t.Fatalf("currentFiles(amp) omitted %q: %v", path, files)
	}
	if got := harnessForPath(path); got != "amp" {
		t.Fatalf("harnessForPath = %q, want amp", got)
	}
	sessions, err := parseChangedFile("amp", path, FileState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Harness != "amp" || sessions[0].Project != "tmp/index-project" {
		t.Fatalf("parseChangedFile = %#v", sessions)
	}
}

func TestLooksLikeQuestion(t *testing.T) {
	for _, s := range []string{
		"did we fix the jwks cache stampede before?",
		"why does the pool exhaust under load",
		"сколько по времени займут такие прогоны",
		"репозиторий готов к публикации?",
	} {
		if !looksLikeQuestion(s) {
			t.Errorf("%q is a question", s)
		}
	}
	// Instructions to an agent repeat verbatim across sessions by construction,
	// which is exactly why they must not count as questions someone asked.
	for _, s := range []string{
		"Use the deja recall tool to search for: openclaw harness",
		"Call the build_check tool with scope all",
		"Reply with only the raw JSON returned by the tool",
		"Continue if you have next steps, or stop and ask for clarification",
		"",
		"   ",
	} {
		if looksLikeQuestion(s) {
			t.Errorf("%q is not a question someone asked", s)
		}
	}
}

func TestNotAskedRejectsHarnessText(t *testing.T) {
	for _, s := range []string{
		"<system-reminder>do the thing</system-reminder>",
		"[Request interrupted by user]",
		"The following tool was executed by the user",
		"This session is being continued from a previous conversation",
		"Continue from where you left off.",
		"<deja-recall>...</deja-recall>",
	} {
		if !notAsked(s) {
			t.Errorf("%q is written by a harness, not a person", s)
		}
	}
	if notAsked("why is the index rebuilding on every run?") {
		t.Error("a real question should survive")
	}
}

func TestQuestionStemNeedsSubstance(t *testing.T) {
	if got := questionStem("Why  does THE pool exhaust, under load?"); got != "why does the pool exhaust under load" {
		t.Fatalf("got %q", got)
	}
	if questionStem("ok thanks") != "" {
		t.Fatal("four words or fewer is not a question worth matching")
	}
}

func TestAskedHashesAreStableAndBounded(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	a := askedHashes([]model.Message{{Role: "user", Text: q}})
	b := askedHashes([]model.Message{{Role: "user", Text: "  WHY does the connection POOL exhaust, under load? "}})
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("the same question asked twice must hash alike: %v vs %v", a, b)
	}
	// Assistant turns are not questions the user asked.
	if got := askedHashes([]model.Message{{Role: "assistant", Text: q}}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	many := make([]model.Message, 0, 30)
	for i := range 30 {
		many = append(many, model.Message{Role: "user", Text: "why does thing number " + string(rune('a'+i)) + " fail here?"})
	}
	if got := askedHashes(many); len(got) != askedQuestionCap {
		t.Fatalf("stored %d hashes, want the cap of %d", len(got), askedQuestionCap)
	}
	// The same question twice in one session is one question.
	if got := askedHashes([]model.Message{{Role: "user", Text: q}, {Role: "user", Text: q}}); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

// askedFixture writes real transcripts and indexes them, the way the other
// tests in this package do: FindAskedTwice reads the manifest the ingest wrote,
// so a hand-built store would test something else.
func askedFixture(t *testing.T, sessions map[string][]string, when map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, msgs := range sessions {
		var b strings.Builder
		for _, m := range msgs {
			fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"cwd":"/tmp/app","timestamp":%q,"message":{"role":"user","content":%q}}`+"\n",
				id, when[id], m)
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
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
	return dir
}

func TestFindAskedTwicePicksTheWidestSpan(t *testing.T) {
	// The same question two months apart, and another asked twice in one
	// afternoon. The second is a person retrying; the first is the one worth a
	// line on a screen.
	q := "why does the connection pool exhaust under load?"
	flaky := "how do i make the flaky test stop failing?"
	dir := askedFixture(t,
		map[string][]string{
			"a": {q},
			"b": {q},
			"c": {flaky},
			"d": {flaky},
		},
		map[string]string{
			"a": "2026-03-01T10:00:00Z",
			"b": "2026-05-01T10:00:00Z",
			"c": "2026-05-02T10:00:00Z",
			"d": "2026-05-02T14:00:00Z",
		})
	got, ok := FindAskedTwice(dir, nil)
	if !ok {
		t.Fatal("a question asked two months apart should be found")
	}
	if got.Text != q {
		t.Fatalf("got %q", got.Text)
	}
	if len(got.Sessions) != 2 || !got.Sessions[0].Updated.After(got.Sessions[1].Updated) {
		t.Fatalf("sessions should come back newest first: %+v", got.Sessions)
	}
}

// A resume, a fork or a share writes one conversation under a second session
// id, and every question in it repeats verbatim. On a real 2,300-session store
// all ten repeats at least 48 hours apart were this, so the only thing the
// line ever had to show was the reader's own continued conversation (#3711).
func TestFindAskedTwiceIgnoresOneConversationUnderTwoIDs(t *testing.T) {
	turns := []string{
		"why does the connection pool exhaust under load?",
		"how do we roll the jwks cache without a stampede?",
		"what did we decide about the retry budget here?",
		"why is the nightly reindex slower than the full one?",
	}
	resumed := append(append([]string{}, turns...), "should the worker take its own pool?")
	dir := askedFixture(t,
		map[string][]string{"a": turns, "b": resumed},
		map[string]string{
			"a": "2026-03-01T10:00:00Z",
			"b": "2026-05-01T10:00:00Z",
		})
	if got, ok := FindAskedTwice(dir, nil); ok {
		t.Fatalf("one conversation under two ids is not a question asked twice: %q", got.Text)
	}

	// The case the surface exists for still fires: two sessions that share the
	// question and nothing else. This is why the copy test excludes the hash
	// they matched on — counting it would drop exactly this pair.
	q := "why does the connection pool exhaust under load?"
	dir = askedFixture(t,
		map[string][]string{
			"c": append([]string{q}, "what does the deploy script do about migrations?"),
			"d": append([]string{q}, "how do i make the flaky cache test stop failing?"),
		},
		map[string]string{
			"c": "2026-03-01T10:00:00Z",
			"d": "2026-05-01T10:00:00Z",
		})
	got, ok := FindAskedTwice(dir, nil)
	if !ok || got.Text != q {
		t.Fatalf("two sessions sharing one question is the repeat worth showing: ok=%v text=%q", ok, got.Text)
	}
}

func TestFindAskedTwiceStaysQuietWithoutARepeat(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{"a": {"why does the pool exhaust under load?"}},
		map[string]string{"a": "2026-03-01T10:00:00Z"})
	if _, ok := FindAskedTwice(dir, nil); ok {
		t.Fatal("one asking is not a repeat")
	}
	if _, ok := FindAskedTwice(t.TempDir(), nil); ok {
		t.Fatal("an empty store has nothing to say")
	}
}

func TestPrefixMatchesCounts(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{
			"aa1": {"why does the pool exhaust under load?"},
			"aa2": {"why does the cache stampede?"},
			"bb1": {"why is the build slow?"},
		},
		map[string]string{
			"aa1": "2026-03-01T10:00:00Z",
			"aa2": "2026-03-02T10:00:00Z",
			"bb1": "2026-03-03T10:00:00Z",
		})
	if got := PrefixMatches(dir, "aa"); got != 2 {
		t.Fatalf("PrefixMatches(aa) = %d, want 2", got)
	}
	if got := PrefixMatches(dir, "aa1"); got != 1 {
		t.Fatalf("an exact id matches once, got %d", got)
	}
	if got := PrefixMatches(dir, "zz"); got != 0 {
		t.Fatalf("got %d, want none", got)
	}
	if got := PrefixMatches(dir, ""); got != 0 {
		t.Fatal("an empty prefix is not a question worth answering")
	}
	// FindByPrefix keeps picking the newest of the matches.
	s, ok, err := FindByPrefix(dir, "aa")
	if err != nil || !ok || s.ID != "aa2" {
		t.Fatalf("got %q ok=%v err=%v, want the newest match", s.ID, ok, err)
	}
}

// A lost buckets/ directory used to read as a healthy index: the manifest is
// intact, so freshness passed and every search answered "no matches in 0
// indexed sessions". That is the one failure a memory tool must not present as
// an empty result — the reader concludes it is useless rather than broken.
func TestMissingBucketsForcesARebuild(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{"a": {"why does the pool exhaust under load?"}},
		map[string]string{"a": "2026-03-01T10:00:00Z"})
	if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("an index with no postings is not intact")
	}
	// HasManifest deliberately still says yes: it answers "is there a manifest
	// here", which doctor uses to tell a missing index from a stale one.
	// Wholeness is recordsIntact's question.
	// And a healthy index still passes, or every command would rebuild.
	fresh := askedFixture(t,
		map[string][]string{"b": {"why does the cache stampede?"}},
		map[string]string{"b": "2026-03-02T10:00:00Z"})
	fm, err := readManifest(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !recordsIntact(fresh, fm) || !HasManifest(fresh) {
		t.Fatal("a healthy index must not be reported as broken")
	}
}

// `forget --dry-run` exists to answer "how much am I about to lose". It used to
// return zero messages, because the count ran after the dry-run branch, and to
// report its result in the past tense as though the deletion had happened.
func TestForgetDryRunPredictsWhatTheRealRunDoes(t *testing.T) {
	mk := func() string {
		return askedFixture(t,
			map[string][]string{
				"keep": {"why does the cache stampede under load?"},
				"drop": {"why does the pool exhaust under load?"},
			},
			map[string]string{"keep": "2026-03-01T10:00:00Z", "drop": "2026-03-02T10:00:00Z"})
	}
	dir := mk()
	dry, err := Forget(dir, ForgetOptions{Session: "drop", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Sessions != 1 || dry.Messages != 1 || dry.Tombstones != 1 {
		t.Fatalf("dry run reported %+v, want one of each", dry)
	}
	// Nothing may have changed.
	if _, ok, _ := FindByID(dir, "drop"); !ok {
		t.Fatal("a dry run must leave the session in place")
	}
	dropped, err := Forget(dir, ForgetOptions{Session: "drop"})
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Sessions != dry.Sessions || dropped.Messages != dry.Messages {
		t.Fatalf("the dry run promised %+v and the real run did %+v", dry, dropped)
	}
}

// deepverify compares what a source parses to what the index holds. It has to
// count what ingestion would *keep*: a message that is empty, or that strips to
// empty once deja's own injected recall is removed, is never written — so
// counting it reported drift on a healthy index and told the reader to rebuild,
// which reproduces it. Seen on a real codex session: "source parses 6 messages,
// index holds 5", surviving every rebuild.
func TestDeepVerifyIgnoresMessagesIngestionWouldDrop(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Text: "a real question about the pool"},
		{Role: "user", Text: "   "},
		{Role: "user", Text: ""},
		{Role: "assistant", Text: "a real answer about pgx"},
	}
	seen := msgSeen{}
	want := 0
	for _, m := range msgs {
		if strings.TrimSpace(stripSelfRecall(m.Text)) == "" {
			continue
		}
		if !seen.dup("k", m.Role, m.Time, m.Text) {
			want++
		}
	}
	if want != 2 {
		t.Fatalf("counted %d messages, want the two with content", want)
	}
	// A genuinely missing message must still count, or the check stops working.
	seen2 := msgSeen{}
	n := 0
	for _, m := range append(msgs, model.Message{Role: "user", Text: "a third real one"}) {
		if strings.TrimSpace(stripSelfRecall(m.Text)) == "" {
			continue
		}
		if !seen2.dup("k", m.Role, m.Time, m.Text) {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("counted %d, want three — real content must never be skipped", n)
	}
}

// assistantTurnsStore has a session from this week, one from long ago, one
// the ignore rule keeps out and one in a project the caller does not allow.
func assistantTurnsStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id, early, late string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		line := func(typ, at, content string) string {
			return `{"type":"` + typ + `","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"` + typ + `","content":` + content + `}}` + "\n"
		}
		body := line("user", early, `"why does the lease expire early"`) +
			line("assistant", early, `[{"type":"text","text":"reply from `+id+` before the week"}]`) +
			line("assistant", late, `[{"type":"text","text":"reply from `+id+`"}]`)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep", "week1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	write("keep", "old1", "2026-07-01T10:00:00Z", "2026-07-01T10:05:00Z")
	write("scratch", "ign1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	write("other", "deny1", "2026-10-01T10:00:00Z", "2026-10-12T10:00:00Z")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEachAssistantTurnSinceReadsOnlyThisWeeksAllowedTurns(t *testing.T) {
	dir := assistantTurnsStore(t)
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	allow := func(project string) bool { return filepath.Base(project) != "other" }
	var got []string
	err := EachAssistantTurnSince(dir, since, allow, func(m SessionMeta, text string, at time.Time) {
		if !at.After(since) {
			t.Errorf("%q at %v is before the cut", text, at)
		}
		got = append(got, m.ID+": "+text)
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"week1: reply from week1"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Nothing after the cut reads nothing.
	n := 0
	if err := EachAssistantTurnSince(dir, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), nil, func(SessionMeta, string, time.Time) { n++ }); err != nil || n != 0 {
		t.Fatalf("a cut after every session gave %d turns, err %v", n, err)
	}
}

func TestEachAssistantTurnSinceReportsAMissingStore(t *testing.T) {
	setHome(t, t.TempDir())
	if err := EachAssistantTurnSince(filepath.Join(t.TempDir(), "none.db"), time.Time{}, nil, func(SessionMeta, string, time.Time) {}); err == nil {
		t.Fatal("a store that does not exist must be an error, not an empty week")
	}
}

func TestRecentSpokenAssistantTurnsForTermsSkipsToolsAndHonoursAllowedKeys(t *testing.T) {
	h := newTwoWayEnv(t)
	s1 := hPrompt("ledger", "s1", 2, 0, "update throttle.go") +
		hBash("ledger", "s1", 2, 1, "t1", "cat throttle.go", "package ledger // throttle.go in tool output only", false) +
		hSay("ledger", "s1", 2, 2, "We settled on token bucket in throttle.go because leaky bucket starved bursts.")
	s2 := hPrompt("ledger", "s2", 3, 0, "check throttle.go") +
		hSay("ledger", "s2", 3, 1, "In s2 we kept throttle.go burst capacity at 64.")
	s3 := hPrompt("ledger", "s3", 4, 0, "inspect throttle.go") +
		hBash("ledger", "s3", 4, 1, "t2", "grep bucket throttle.go", "throttle.go token bucket", false) +
		hSay("ledger", "s3", 4, 2, "All checks completed.")

	h.put("ledger", "s1", s1)
	h.put("ledger", "s2", s2)
	h.put("ledger", "s3", s3)

	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{
		"claude:s1": true,
		"claude:s3": true, // s3 only mentions throttle in tool output, not spoken assistant text
	}
	turns, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"throttle"}, allowed, 10)
	if err != nil {
		t.Fatalf("RecentSpokenAssistantTurnsForTerms: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 spoken assistant turn from allowed sessions, got %d: %+v", len(turns), turns)
	}
	if turns[0].Key != "claude:s1" || !strings.Contains(turns[0].Text, "starved bursts") {
		t.Errorf("unexpected turn returned: %+v", turns[0])
	}

	allowed["claude:s2"] = true
	turnsBoth, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"throttle"}, allowed, 10)
	if err != nil {
		t.Fatalf("RecentSpokenAssistantTurnsForTerms with s2: %v", err)
	}
	if len(turnsBoth) != 2 || turnsBoth[0].Key != "claude:s2" || turnsBoth[1].Key != "claude:s1" {
		t.Errorf("expected newest-first [claude:s2, claude:s1], got %+v", turnsBoth)
	}

	// Cap truncation (maxRecords = 1), unmatched term, and unknown session key branches.
	capped, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"ab", "missingtermxyz", "throttle"}, allowed, 1)
	if err != nil || len(capped) != 1 || capped[0].Key != "claude:s2" {
		t.Errorf("expected newest 1 capped turn claude:s2, got %+v (err=%v)", capped, err)
	}
	noneByTerm, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"missingtermxyz"}, allowed, 5)
	if err != nil || len(noneByTerm) != 0 {
		t.Errorf("expected 0 turns for unmatched term, got %+v (err=%v)", noneByTerm, err)
	}
	noneByKey, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"throttle"}, map[string]bool{"claude:unknown": true}, 5)
	if err != nil || len(noneByKey) != 0 {
		t.Errorf("expected 0 turns for unknown key, got %+v (err=%v)", noneByKey, err)
	}
	noGiveUps, err := GiveUpRecordsForKeys(dir, allowed)
	if err != nil || len(noGiveUps) != 0 {
		t.Errorf("expected 0 give-up records when store has no GaveUp sessions, got %+v (err=%v)", noGiveUps, err)
	}
}

func TestGiveUpRecordsForKeysIndexesUserAndAssistantReversalsOnly(t *testing.T) {
	h := newTwoWayEnv(t)
	s1 := hPrompt("ledger", "s1", 2, 0, "try batching outbox") +
		hBash("ledger", "s1", 2, 1, "t1", "git status", "reverted in tool output only", false) +
		hSay("ledger", "s1", 2, 2, "Routine progress note without any rollback.") +
		hSay("ledger", "s1", 2, 3, "We reverted batchFlushOutbox because holding the lock across network I/O caused deadlocks.")
	s2 := hPrompt("ledger", "s2", 3, 0, "Откатили локальный кеш в dispatcher.go, потому что он ломал порядок доставки.") +
		hSay("ledger", "s2", 3, 1, "Записал, оставляем прямую запись.")
	s3 := hPrompt("ledger", "s3", 4, 0, "checkテスト") +
		hSay("ledger", "s3", 4, 1, "Each new test failed with its fix reverted.")

	h.put("ledger", "s1", s1)
	h.put("ledger", "s2", s2)
	h.put("ledger", "s3", s3)

	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	recs, err := GiveUpRecordsForKeys(dir, map[string]bool{
		"claude:s1": true,
		"claude:s2": true,
		"claude:s3": true,
	})
	if err != nil {
		t.Fatalf("GiveUpRecordsForKeys: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 spoken reversal records (assistant s1 + user s2, excluding tool output and s3 test-soundness rollback), got %d: %+v", len(recs), recs)
	}
	seen := map[string]string{}
	for _, r := range recs {
		seen[r.Key] = r.Role + ":" + r.Text
	}
	if !strings.Contains(seen["claude:s1"], "assistant:") || !strings.Contains(seen["claude:s1"], "batchFlushOutbox") {
		t.Errorf("missing or wrong s1 assistant reversal: %q", seen["claude:s1"])
	}
	if !strings.Contains(seen["claude:s2"], "user:") || !strings.Contains(seen["claude:s2"], "dispatcher.go") {
		t.Errorf("missing or wrong s2 user reversal: %q", seen["claude:s2"])
	}
	if _, bad := seen["claude:s3"]; bad {
		t.Errorf("test-soundness rollback s3 must not be indexed as GiveUp: %q", seen["claude:s3"])
	}

	// Incremental per-key queries on a warm manifest view must isolate each
	// session key without missing subsequent keys or scanning records.bin for
	// non-GaveUp keys.
	onlyS1, err := GiveUpRecordsForKeys(dir, map[string]bool{"claude:s1": true})
	if err != nil || len(onlyS1) != 1 || onlyS1[0].Key != "claude:s1" {
		t.Fatalf("scoped s1 query = %+v (err=%v)", onlyS1, err)
	}
	onlyS2, err := GiveUpRecordsForKeys(dir, map[string]bool{"claude:s2": true})
	if err != nil || len(onlyS2) != 1 || onlyS2[0].Key != "claude:s2" {
		t.Fatalf("scoped s2 query = %+v (err=%v)", onlyS2, err)
	}
	if removeErr := os.Remove(filepath.Join(dir, "records.bin")); removeErr != nil {
		t.Fatal(removeErr)
	}
	noOp, err := GiveUpRecordsForKeys(dir, map[string]bool{"claude:s3": true})
	if err != nil || len(noOp) != 0 {
		t.Fatalf("non-GaveUp key must not touch records.bin: got %+v, err=%v", noOp, err)
	}
}

// A crash after records flushed but before the manifest committed leaves an
// orphan tail; re-appending it used to duplicate messages silently (#181).
func TestRecordsIntactRejectsOrphanTail(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{RecordsSize: 10}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if !recordsIntact(dir, m) {
		t.Fatal("exact size must be intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 14), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("orphan tail must not count as intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("truncated log must not count as intact")
	}
}

func TestSwapIndexDirAndRecovery(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "index.db")
	tmp := filepath.Join(base, "tmp-build")
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "marker"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("marker = %q", b)
	}
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old must be cleaned up after a completed swap")
	}
	// Interrupted swap: dir gone, .old parked — recovery restores it.
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	b, _ = os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("recovered marker = %q", b)
	}
	// With a healthy dir, recovery only clears leftovers.
	if err := os.MkdirAll(dir+".old", 0o700); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal("stale .old must be removed when dir is healthy")
	}
}

func TestWriteBucketLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ab.bin")
	if err := writeBucket(p, map[string][]posting{"tok": {{Off: 1, Sid: 2}}}); err != nil {
		t.Fatal(err)
	}
	got, err := readBucket(p)
	if err != nil || len(got["tok"]) != 1 {
		t.Fatalf("bucket roundtrip = %#v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteTombstonesAtomicNoTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := writeTombstones(map[string]bool{"claude:s1": true, "codex:s2": true}); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	if !got["claude:s1"] || !got["codex:s2"] {
		t.Fatalf("tombstones = %#v", got)
	}
	if _, err := os.Stat(tombstonePath() + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	// Overwrite shrinks the set — rename must fully replace, not append.
	if err := writeTombstones(map[string]bool{"claude:s1": true}); err != nil {
		t.Fatal(err)
	}
	if got := readTombstones(); got["codex:s2"] || !got["claude:s1"] {
		t.Fatalf("after shrink = %#v", got)
	}
}

func TestIngestHealthPersistedToManifest(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	dir := filepath.Join(tmp, "index.db")
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"good line"}}` + "\n" +
		`{"broken json` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h := IngestHealth(dir)
	if h["claude"].MalformedLines != 1 {
		t.Fatalf("ingest health = %#v, want claude malformed=1", h)
	}
}

func TestExportDeferredCommitsWatermarkOnlyOnAck(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"watermark ack test"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "out")
	n, commit, err := ExportDeferred(dir, out, "")
	if err != nil || n != 1 {
		t.Fatalf("deferred export = %d, %v", n, err)
	}
	// Not committed: a re-export must still see the record.
	n2, commit2, err := ExportDeferred(dir, filepath.Join(tmp, "out2"), "")
	if err != nil || n2 != 1 {
		t.Fatalf("pre-ack re-export = %d, %v — watermark advanced before ack", n2, err)
	}
	_ = commit2
	if err = commit(); err != nil {
		t.Fatal(err)
	}
	// Committed: the watermark holds, so strictly older work is not resent.
	// Records sitting exactly on the watermark are resent by design (import
	// dedupes them) — see the tie handling in exportRecordsDeferred.
	// Same source file: watermarks are per source, so an older record must
	// land in the file that already has one.
	older := line + `{"type":"user","sessionId":"s1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"older than the watermark"}}` + "\n"
	if err = os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n3, _, err := ExportDeferred(dir, filepath.Join(tmp, "out3"), "")
	if err != nil {
		t.Fatal(err)
	}
	if n3 > 1 {
		t.Fatalf("post-ack export = %d — watermark not persisted (older work resent)", n3)
	}
	// Sessions must survive the core-only manifest write.
	ss, err := Recent(dir, 5)
	if err != nil || len(ss) == 0 {
		t.Fatalf("sessions clobbered by watermark commit: %v, %v", ss, err)
	}
}

// A file indexed while its first line was still torn records SafeSize==0 with
// bytes on disk. The next pass must NOT resume an append from that ambiguous 0
// (which drops the first message or duplicates a lone line) — it must fall back
// to a full re-index (#appendloss).
func TestAppendSkipsIncrementalWhenNothingWasComplete(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	p := filepath.Join(claude, "-proj", "s1.jsonl") // harnessForPath -> "claude"
	changed := map[string]FileState{p: {Path: p, Size: 120}}
	old := map[string]FileState{p: {Path: p, Size: 40, SafeSize: 0}}
	if canAppendIncremental(changed, old) {
		t.Fatal("SafeSize==0 with bytes on disk must force full re-index, not append")
	}
	// A normal grown file with a real SafeSize still appends incrementally.
	old[p] = FileState{Path: p, Size: 40, SafeSize: 40}
	if !canAppendIncremental(changed, old) {
		t.Fatal("a file with a complete prior line should still append incrementally")
	}
}

func TestProjectRelevantRanksByIDFNotFiller(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// s1: rare topical term "quetzalcoatl". s2: only common filler "need the".
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "the quetzalcoatl migration and need the fix")
	mk("s2", "need the report and need the update")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A prompt full of filler plus the one rare term must rank s1 first.
	got, _, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, []string{"need", "the", "quetzalcoatl"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "s1" {
		t.Fatalf("ranking = %v, want s1 first (rare term dominates filler)", got)
	}
}

// Whether "write" also tries "writes" was decided by the whole store, so work
// in an unrelated project could switch the fold off and leave a question
// matching nothing in the project that answers it.
func TestStemFoldIsDecidedByTheProjectNotTheStore(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	mk := func(project, id, text string) {
		p := filepath.Join(claudeRoot, project)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(p, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The session that answers says "writes"; the exact form the question uses
	// exists only somewhere else entirely.
	mk("-tmp-app", "answer", "parquet writes are batched per hour, not per row")
	mk("-tmp-other", "elsewhere", "remember to write the changelog before tagging")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, matched, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, []string{"write", "parquet"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "answer" {
		t.Fatalf("ranking = %v, want the session that says \"writes\"", got)
	}
	// Both query terms have to count. Asserting only the order passes whether
	// or not the fold happened, because nothing else in this project holds
	// "parquet" — the count is the fact being fixed.
	if matched[0] != 2 {
		t.Fatalf("matched = %d, want both terms: the fold was decided by another project's session", matched[0])
	}
}

func TestProjectRelevantDottedTermNeedsAllSubTokens(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
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
	// s1 contains stray octets from unrelated numbers; s2 the full address.
	mk("s1", "screenshot batch 138 of run 79 finished")
	mk("s2", "deploy target 203.0.113.51 rotated keys")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, matched, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, []string{"203.0.113.51"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range got {
		if s.ID == "s1" && matched[i] > 0 {
			t.Fatalf("octet-only session counted as a match: %v", matched)
		}
	}
	found := false
	for i, s := range got {
		if s.ID == "s2" && matched[i] >= 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("full-address session missing: got=%v matched=%v", len(got), matched)
	}
}

// A subject word said once in each of four sessions is rarer than a working
// word said forty times in each of three. Counting whole sessions says the
// opposite — In real-world stores, "pgbouncer" scored 1.31 and "branch"
// 1.50, because eleven marathon sessions hold 99% of everything ever said and
// every one of them mentions the ordinary word.
func TestRarityCountsMessagesNotWholeSessions(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	write := func(id string, lines []string) {
		p := filepath.Join(claudeRoot, "-tmp-app")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, text := range lines {
			b.WriteString(`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n")
		}
		if err := os.WriteFile(filepath.Join(p, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		write(fmt.Sprintf("subj%d", i), []string{"we put prepared statements behind pgbouncer today"})
	}
	for i := range 3 {
		lines := make([]string, 0, 40)
		for k := range 40 {
			lines = append(lines, fmt.Sprintf("pushed the branch again after fix %d", k))
		}
		write(fmt.Sprintf("work%d", i), lines)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	subj := termRarity(t, dir, "pgbouncer")
	work := termRarity(t, dir, "branch")
	if subj <= work {
		t.Fatalf("pgbouncer scored %d, branch %d: the subject has to be the rarer of the two", subj, work)
	}
}

// termRarity reports what the ranking thought one term was worth, scaled to an
// integer so a comparison reads cleanly.
func termRarity(t *testing.T, dir, term string) int {
	t.Helper()
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	rank, err := relevantMetasCounts(dir, m, []string{"tmp/app"}, []string{term}, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	return int(rank.idf[term] * 1000)
}

// On a large store the relevance pool is fused with a per-message reading:
// a session whose single user turn carries the query's words moves up past
// one that scatters fewer of the same words over many assistant turns. It is
// a fusion, so the pool leader keeps its place — the gain measured on a
// 19k-session pile is hit@1 15 -> 18, not a new ranking (#3016).
func TestBestMessageFusionLiftsTheTurnThatHoldsTheFact(t *testing.T) {
	terms := []string{"degree", "graduated", "university"}
	idf := map[string]float64{"degree": 2.0, "graduated": 2.5, "university": 1.5}
	scattered := model.Session{ID: "scattered", Messages: []model.Message{
		{Role: "assistant", Text: "a degree in anything is useful"},
		{Role: "assistant", Text: "many people graduated last year"},
		{Role: "assistant", Text: "the university opens in autumn"},
	}}
	thin := model.Session{ID: "thin", Messages: []model.Message{
		{Role: "assistant", Text: "a degree is a degree"},
	}}
	focused := model.Session{ID: "focused", Messages: []model.Message{
		{Role: "user", Text: "I graduated with a degree in physics from the university of Michigan"},
	}}
	// Pool order as the session-level ranking hands it over: focused last.
	got := sessionIDs(rerankByBestMessage([]model.Session{scattered, thin, focused}, terms, idf))
	want := []string{"scattered", "focused", "thin"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Fusion, not replacement: two sessions the messages cannot tell apart keep
// the pool's order, so a ranking that was right stays right.
func TestBestMessageFusionKeepsThePoolOrderOnTies(t *testing.T) {
	terms := []string{"bike", "commute"}
	idf := map[string]float64{"bike": 2.0, "commute": 2.0}
	a := model.Session{ID: "a", Messages: []model.Message{{Role: "user", Text: "my bike commute"}}}
	b := model.Session{ID: "b", Messages: []model.Message{{Role: "user", Text: "the bike commute again"}}}
	got := rerankByBestMessage([]model.Session{a, b}, terms, idf)
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("order changed on a tie: %v", sessionIDs(got))
	}
}

func sessionIDs(ss []model.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

// A peer's file edits must be blamable after import. blame reads
// SessionMeta.Touched to find who changed a file; the import path built the
// imported meta without it, so `deja blame auth.go` showed only local edits
// even though the peer's `files` records were imported and searchable.
func TestImportPopulatesTouchedForBlame(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A foreign batch: a speech turn (so the session is real) and a files record
	// naming the edited path. Written by hand so it is unambiguously not this
	// machine's own export.
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lines := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"working on auth","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/auth.go","time":"` + when + `"}` + "\n"
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
	var found *SessionMeta
	for i := range metas {
		if metas[i].OrigID == "peer1" || metas[i].ID != "" && metas[i].Project == "imported:svc" {
			found = &metas[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("imported session not found in %d metas", len(metas))
	}
	touchesAuth := false
	for _, p := range found.Touched {
		if p == "/repo/auth.go" {
			touchesAuth = true
		}
	}
	if !touchesAuth {
		t.Fatalf("imported session Touched=%v does not include /repo/auth.go — blame cannot attribute the peer's edit", found.Touched)
	}
}

// A reversal reported in an earlier import batch must survive a later batch
// that does not repeat it: import is incremental, so GaveUp unions with the
// row like Touched/Asked/Hit do, rather than being cleared by a quiet batch.
func TestImportGaveUpSurvivesALaterBatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(shared, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gaveUpOf := func() bool {
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := range metas {
			if metas[i].OrigID == "peer2" || metas[i].Project == "imported:svc2" {
				return metas[i].GaveUp
			}
		}
		t.Fatal("imported session not found")
		return false
	}

	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	write("deja-sync-a.jsonl",
		`{"harness":"claude","session_id":"peer2","project":"svc2","role":"assistant","text":"reverted the retry cap, it made the queue worse","time":"`+t0+`"}`+"\n")
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	if !gaveUpOf() {
		t.Fatal("the reversal reported in the first batch was not marked")
	}

	// A later batch with more of the same session but no reversal line.
	t1 := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := os.Remove(filepath.Join(shared, "deja-sync-a.jsonl")); err != nil {
		t.Fatal(err)
	}
	write("deja-sync-b.jsonl",
		`{"harness":"claude","session_id":"peer2","project":"svc2","role":"assistant","text":"added a metric for the queue depth","time":"`+t1+`"}`+"\n")
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	if !gaveUpOf() {
		t.Error("a later quiet batch cleared the reversal mark from an earlier one")
	}
}

// bridgedStore holds the three things a rephrased question needs to be
// answerable: the session that settled it in the project's own word, a session
// that explains that word in ordinary ones, and enough other sessions that the
// ordinary words are not rare by accident.
func bridgedStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 84)
	for i := range 80 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("filler%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "notes on the weekly planning meeting and the invoice"}},
		})
	}
	// Said in three sessions, which is the evidence the co-occurrence map asks
	// for before it records a pairing.
	for i := range 3 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "the debounce is how long we wait before sending what someone typed"}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "we set the debounce on the search box to 250ms"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A question asked in words the project does not use reaches the session that
// settled it, through the sentence that ties the two vocabularies.
//
// The rescue rung in cooccur.go cannot: it intersects postings, so the best it
// can reach is the record holding both vocabularies — the sentence that
// explains the word, never the session that used it and settled something.
func TestARephrasedQuestionReachesTheAnswer(t *testing.T) {
	dir := bridgedStore(t)
	terms := []string{"long", "wait", "sending", "typed"}
	ranked, _, _, _, err := ProjectRelevant(dir, []string{"p"}, terms, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	got := make([]string, 0, len(ranked))
	for _, s := range ranked {
		got = append(got, s.ID)
		if s.ID == "answer" {
			found = true
		}
	}
	if !found {
		t.Errorf("the session that settled it was never reached: %v", got)
	}
}

// The same machinery must not answer a question whose subject the project never
// held. Its words are ordinary, and the corpus's neighbours for ordinary words
// are every subject it has — which is how a bridge becomes a machine for
// confident wrong answers.
func TestAnAbsentSubjectIsNotBridgedTo(t *testing.T) {
	dir := bridgedStore(t)
	ranked, _, strong, _, err := ProjectRelevant(dir, []string{"p"}, []string{"open", "file", "read"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range ranked {
		if s.ID == "answer" && strong[i] > 0 {
			t.Errorf("a question about something the project never held was "+
				"answered with %q", s.ID)
		}
	}
}

func TestUnseenStrongSubjectBlocksVerbNeighbourBridge(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 86)
	for i := range 80 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("bg%02d", i), Harness: "claude", Project: "other", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "weekly changelog cleanup and invoice check"}},
		})
	}
	// Three sessions tie "postpone" and "backlog" inside project "home".
	for i := range 3 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("pair%02d", i), Harness: "claude", Project: "home", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "we will postpone that item until the backlog review"}},
		})
	}
	// Rare subject "ambergris" lives only in "away", never in "home".
	sessions = append(sessions, model.Session{
		ID: "away01", Harness: "claude", Project: "away", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "ambergris shard rotation uses a 12h lease"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	ranked, matched, strong, naming, _, err := ProjectRelevantNaming(dir, []string{"home"}, []string{"postpone", "ambergris"}, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range ranked {
		if matched[i] >= 2 || strong[i] >= 2 || naming[i] > 0 {
			t.Fatalf("unexpected cross-project bridge on %q: matched=%d strong=%d naming=%d",
				s.ID, matched[i], strong[i], naming[i])
		}
	}
}

// A bucket file written by a different index layout — or simply truncated —
// used to reach make() with a length taken from the file itself, which panics
// instead of failing. Found in the field: an older deja binary reading a newer
// index crashed the harness hook it was running under.
func TestOpenBucketDirRejectsImpossibleCounts(t *testing.T) {
	cases := map[string]func() []byte{
		"entry count larger than the file": func() []byte {
			b := append([]byte{}, bucketMagic...)
			return binary.AppendUvarint(b, 1<<62)
		},
		"token longer than the file": func() []byte {
			b := append([]byte{}, bucketMagic...)
			b = binary.AppendUvarint(b, 1)
			return binary.AppendUvarint(b, 1<<62)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bucket")
			if err := os.WriteFile(path, build(), 0o644); err != nil {
				t.Fatal(err)
			}
			entries, f, err := openBucketDir(path)
			if f != nil {
				_ = f.Close()
			}
			if err == nil {
				t.Fatalf("accepted a corrupt bucket: %d entries", len(entries))
			}
			if !errors.Is(err, errCorruptIndex) {
				t.Fatalf("err = %v, want errCorruptIndex so callers can rebuild", err)
			}
		})
	}
}

// bucketDirStore is a Claude store under a temp dir and the index beside it.
// add writes one single-turn session and indexes it.
func bucketDirStore(t *testing.T) (dir string, add func(id, text string)) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(root, "-tmp-orchard")
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

func keyOf(t *testing.T, word string) string {
	t.Helper()
	keys := queryKeys(word)
	if len(keys) != 1 {
		t.Fatalf("%q has keys %q, want one", word, keys)
	}
	return keys[0]
}

func sessionsHolding(t *testing.T, dir, word string) int {
	t.Helper()
	posts, err := postingsFor(dir, keyOf(t, word))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]bool{}
	for _, p := range posts {
		seen[p.Sid] = true
	}
	return len(seen)
}

// cachedStale reports a cached directory of dir whose file on disk is no
// longer the one it was read from.
func cachedStale(dir string) string {
	for p, d := range cachedBucketDirs(dir) {
		fi, err := os.Stat(p)
		if err != nil || !sameFileVersion(d.stat, fi) {
			return p
		}
	}
	return ""
}

// cachedDirs is the directories cached for dir.
func cachedDirs(dir string) map[*cachedBucketDir]bool {
	out := map[*cachedBucketDir]bool{}
	for _, d := range cachedBucketDirs(dir) {
		out[d] = true
	}
	return out
}

// The bucket directories a query read are kept for the next one. A publish
// is another generation of the index: the next query reads the new
// postings, and no directory of the files it replaced is kept.
func TestBucketDirsFollowAPublish(t *testing.T) {
	dir, add := bucketDirStore(t)
	add("s1", "the pear tree needs grafting before the thaw")
	if n := sessionsHolding(t, dir, "grafting"); n != 1 {
		t.Fatalf("first build: grafting in %d sessions, want 1", n)
	}
	before := bucketDirReads.Load()
	if n := sessionsHolding(t, dir, "grafting"); n != 1 {
		t.Fatalf("second read: grafting in %d sessions, want 1", n)
	}
	if n := bucketDirReads.Load() - before; n != 0 {
		t.Fatalf("a second query read %d bucket directories off disk, want 0", n)
	}

	kept := cachedDirs(dir)
	if len(kept) == 0 {
		t.Fatal("control: nothing cached after two queries")
	}
	add("s2", "grafting wax and a pruning saw for the pear tree")
	for d := range cachedDirs(dir) {
		if kept[d] {
			t.Fatal("a directory read before the publish is still cached after it")
		}
	}
	add("s3", "the quince took its grafting scion in march")
	if n := sessionsHolding(t, dir, "grafting"); n != 3 {
		t.Fatalf("after two publishes grafting is in %d sessions, want 3", n)
	}
	if n := sessionsHolding(t, dir, "quince"); n != 1 {
		t.Fatalf("after a publish the new word quince is in %d sessions, want 1", n)
	}
	if p := cachedStale(dir); p != "" {
		t.Fatalf("after a publish the cache still holds the directory of a file it replaced: %s", p)
	}
	ss, err := Search(dir, query.Options{Query: "grafting", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 3 {
		t.Fatalf("search after a publish found %d sessions, want 3", len(ss))
	}
}

// A bucket file replaced under an unchanged manifest, the window between a
// writer renaming its buckets in and committing the manifest, or a file
// damaged in place, is read as it is now, not through the directory of the
// file it replaced.
func TestBucketDirsFollowAFileReplacedUnderTheManifest(t *testing.T) {
	dir, add := bucketDirStore(t)
	add("s1", "the medlar tree fruits late in november")
	key := keyOf(t, "medlar")
	if n := sessionsHolding(t, dir, "medlar"); n != 1 {
		t.Fatalf("first build: medlar in %d sessions, want 1", n)
	}
	p, ok := bucketPath(dir, key)
	if !ok {
		t.Fatal("no bucket for medlar")
	}
	// A word of the same bucket that no session holds.
	other := ""
	for _, w := range []string{"mealybug", "meadow", "medick", "mezereon", "melon", "medicinal"} {
		if k := keyOf(t, w); bucket(k) == bucket(key) {
			other = w
			break
		}
	}
	if other == "" {
		t.Fatal("no candidate word shares medlar's bucket")
	}
	if n := sessionsHolding(t, dir, other); n != 0 {
		t.Fatalf("control: %s in %d sessions before the rewrite", other, n)
	}
	data, err := readBucketFile(p)
	if err != nil {
		t.Fatal(err)
	}
	data[keyOf(t, other)] = data[key]
	if err := writeBucket(p, data); err != nil {
		t.Fatal(err)
	}
	if n := sessionsHolding(t, dir, other); n != 1 {
		t.Fatalf("a bucket rewritten under the same manifest still read through the old directory: %s in %d sessions, want 1", other, n)
	}

	// Damaged in place: the error that sends the reader to rebuild, not an
	// answer from memory.
	if err := os.WriteFile(p, []byte("not a bucket at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := postingsFor(dir, key); !errors.Is(err, errCorruptIndex) {
		t.Fatalf("a damaged bucket read as %v, want a corrupt-index error", err)
	}
}

// Concurrent recalls share the cached directories while the index is
// written under them. Run with -race.
func TestBucketDirsConcurrentReaders(t *testing.T) {
	dir, add := bucketDirStore(t)
	add("s1", "the walnut drops its leaves first")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	stopReaders := sync.OnceFunc(func() { close(stop); wg.Wait() })
	defer stopReaders()
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = postingsFor(dir, "twalnut")
				_, _ = SearchDetailed(dir, query.Options{Query: "walnut leaves", All: true})
				if runtime.GOOS == "windows" {
					time.Sleep(10 * time.Millisecond)
				}
			}
		})
	}
	add("s2", "a walnut sapling by the gate")
	add("s3", "walnut husks stain the hands")
	stopReaders()
	if n := sessionsHolding(t, dir, "walnut"); n != 3 {
		t.Fatalf("after concurrent reads walnut is in %d sessions, want 3", n)
	}
}

// A bucket file written by another version of deja is not a damaged one, and
// the line a reader gets is the only thing that tells them which it is: the
// first wording sent someone looking for a disk fault they did not have
// (#492).
func TestAnIndexFromAnotherVersionIsNotCalledDamaged(t *testing.T) {
	tmp := t.TempDir()
	buckets := filepath.Join(tmp, "buckets", "x00.bin")
	if err := os.MkdirAll(filepath.Dir(buckets), 0o755); err != nil {
		t.Fatal(err)
	}
	// A bucket in the shape the previous format wrote: right size, old magic.
	if err := os.WriteFile(buckets, append([]byte("DJB1"), 0x00), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := openBucketDir(buckets)
	if err == nil {
		t.Fatal("a bucket in the old format was accepted")
	}
	if !IsCorrupt(err) {
		t.Errorf("the old format is not reported as unreadable: %v", err)
	}
	if !strings.Contains(err.Error(), "older deja") || !strings.Contains(err.Error(), "DJB1") {
		t.Errorf("the error does not say which version wrote it: %v", err)
	}
	if got := damagedOrOutdated(err); got != "index written by another version of deja" {
		t.Errorf("the line a reader sees is %q", got)
	}

	// And a bucket that really is corrupt still reads as damage.
	if err = os.WriteFile(buckets, []byte{0x01, 0x02, 0x03, 0x04, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = openBucketDir(buckets)
	if err == nil {
		t.Fatal("a bucket with no magic at all was accepted")
	}
	if got := damagedOrOutdated(err); got != "index damaged" {
		t.Errorf("a corrupt bucket reads as %q", got)
	}
	if strings.ContainsAny(err.Error(), "\x00\x01\x02") {
		t.Errorf("raw bytes reached the message: %q", err.Error())
	}
}

// bucketPath refuses a name bucket did not make; if bucket ever made one the
// pattern misses, that token's postings would silently read as empty.
func TestEveryBucketNameIsAccepted(t *testing.T) {
	toks := []string{"a", "ab", "abc", "a1", "_x", "z9q", "Ab", "a-b", "a.b", "日本", "日", "привет", "ёж", "\xff\xfe", "a\xff", "ab\xff", "1", "x", "", "émoji😀", "😀"}
	for r := rune(0); r < 0x3000; r += 7 {
		toks = append(toks, string(r), string(r)+"a", "a"+string(r), string([]rune{r, r + 1, r + 2}))
	}
	for _, tok := range toks {
		if b := bucket(tok); !bucketName.MatchString(b) {
			t.Fatalf("bucket(%q) = %q, which bucketPath refuses", tok, b)
		}
	}
}

// The build counts what reached the index; a turn that is only deja's own
// recall reaches nothing, having been stripped before anything counts it. The
// difference between the two numbers an install prints is exactly that, and it
// is now carried rather than left to be worked out (#3386).
func TestTheBuildSaysHowManyOfItsOwnBlocksItDropped(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the quokkabloom retry"}}
{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"<deja-recall>\nRecalled history from prior sessions.\n</deja-recall>"}}
{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":"the retry is capped at three"}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if LastBuild.Messages != 2 {
		t.Errorf("indexed %d messages, want the two a person and the agent wrote", LastBuild.Messages)
	}
	if LastBuild.Dropped != 1 {
		t.Errorf("dropped = %d, want deja's own block counted", LastBuild.Dropped)
	}
}

// BuiltSummary reads the manifest the ingest wrote: sessions, the harnesses
// they came from, and the asked hashes that appear in more than one session.
func TestBuiltSummaryCountsFromTheManifest(t *testing.T) {
	q := "why does the retry loop drop the last attempt"
	dir := askedFixture(t, map[string][]string{
		"a": {q},
		"b": {q},
		"c": {"how do I run the integration tests here"},
	}, map[string]string{
		"a": "2026-03-01T10:00:00Z",
		"b": "2026-05-01T10:00:00Z",
		"c": "2026-06-01T10:00:00Z",
	})
	sessions, harnesses, repeated := BuiltSummary(dir, nil)
	if sessions != 3 || harnesses != 1 || repeated != 1 {
		t.Fatalf("summary = %d sessions, %d harnesses, %d repeated; want 3, 1, 1", sessions, harnesses, repeated)
	}
	// The trust gate: a rule that withholds the project withholds the count.
	if s, h, r := BuiltSummary(dir, func(string) bool { return false }); s+h+r != 0 {
		t.Fatalf("a gate that allows nothing still counted %d/%d/%d", s, h, r)
	}
	if s, _, r := BuiltSummary(dir, func(string) bool { return true }); s != 3 || r != 1 {
		t.Fatalf("a gate that allows everything counted %d sessions, %d repeated", s, r)
	}
}

func TestBuiltSummaryIsZeroWithoutAManifest(t *testing.T) {
	tmp := t.TempDir()
	if s, h, r := BuiltSummary(filepath.Join(tmp, "missing"), nil); s+h+r != 0 {
		t.Fatalf("no manifest, yet %d/%d/%d", s, h, r)
	}
	// "" means the default directory, which under a hermetic home is empty too.
	setHome(t, tmp)
	t.Setenv("DEJA_INDEX_DIR", "")
	if s, h, r := BuiltSummary("", nil); s+h+r != 0 {
		t.Fatalf("default dir under an empty home, yet %d/%d/%d", s, h, r)
	}
}

// A resumed conversation is written again under a new key and carries every
// question the original did. The built note's count is the brief's rule: one
// conversation written twice is not a question asked twice.
func TestBuiltSummarySkipsAResumedConversation(t *testing.T) {
	q := "why does the retry loop drop the last attempt"
	rest := []string{"where does the scheduler read its failover timeout", "how do I run the integration tests here"}
	dir := askedFixture(t, map[string][]string{
		"orig":   append([]string{q}, rest...),
		"resume": append([]string{q}, rest...),
		"other":  {"what is the deploy command for staging here"},
	}, map[string]string{
		"orig":   "2026-03-01T10:00:00Z",
		"resume": "2026-03-04T10:00:00Z",
		"other":  "2026-06-01T10:00:00Z",
	})
	if _, _, repeated := BuiltSummary(dir, nil); repeated != 0 {
		t.Fatalf("a resumed conversation counted as %d repeated question(s)", repeated)
	}
}

func postingsFrom(sid uint32, n int, base int64) []posting {
	out := make([]posting, 0, n)
	for i := range n {
		out = append(out, posting{Off: base + int64(i), Sid: sid})
	}
	return out
}

// TestCapPostingsKeepsEverySession is the property the cap must not break: it
// bounds how much of a session is read, never which sessions are candidates. A
// session dropped here would vanish from the results entirely.
func TestCapPostingsKeepsEverySession(t *testing.T) {
	posts := slices.Concat(postingsFrom(1, 500, 0), postingsFrom(2, 3, 1000), postingsFrom(3, 64, 2000), postingsFrom(4, 65, 3000))

	got := capPostingsPerSession(posts, 64)
	bySid := map[uint32]int{}
	for _, p := range got {
		bySid[p.Sid]++
	}
	for sid, want := range map[uint32]int{1: 64, 2: 3, 3: 64, 4: 64} {
		if bySid[sid] != want {
			t.Fatalf("sid %d kept %d, want %d", sid, bySid[sid], want)
		}
	}
	if len(bySid) != 4 {
		t.Fatalf("sessions kept %d, want 4", len(bySid))
	}
}

// TestCapPostingsSamplesAcrossTheSession checks the part that decides whether
// the cap loses conclusions: taking the first n would read only the opening of
// a long session, and what a session settled tends to be at its end.
func TestCapPostingsSamplesAcrossTheSession(t *testing.T) {
	posts := postingsFrom(7, 1000, 0)
	got := capPostingsPerSession(posts, 10)
	if len(got) != 10 {
		t.Fatalf("kept %d, want 10", len(got))
	}
	if got[len(got)-1].Off < 900 {
		t.Fatalf("last kept offset %d — the sample never reaches the end", got[len(got)-1].Off)
	}
	// Evenly spaced: no two kept offsets closer than a fraction of the stride.
	for i := 1; i < len(got); i++ {
		if d := got[i].Off - got[i-1].Off; d < 50 {
			t.Fatalf("kept offsets %d and %d are %d apart, not spread", got[i-1].Off, got[i].Off, d)
		}
	}
}

// TestCapPostingsUntouchedBelowTheBound guards the ordinary query: nothing is
// sampled, reordered, or copied when no session is over the bound.
func TestCapPostingsUntouchedBelowTheBound(t *testing.T) {
	posts := append(postingsFrom(1, 5, 0), postingsFrom(2, 9, 100)...)
	got := capPostingsPerSession(posts, 64)
	if len(got) != len(posts) {
		t.Fatalf("kept %d of %d", len(got), len(posts))
	}
	for i := range posts {
		if got[i] != posts[i] {
			t.Fatalf("posting %d changed", i)
		}
	}
	if n := capPostingsPerSession(posts, 0); len(n) != len(posts) {
		t.Fatalf("cap 0 kept %d, want all", len(n))
	}
}

// postingSnapshot is every posting of an index named by what it points at:
// the token, the session and the record. Offsets and session ids differ
// between an updated index and a rebuild, and what they resolve to must not.
func postingSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byOrd := map[uint32]string{}
	for key, meta := range m.Sessions {
		byOrd[meta.Ord] = key
	}
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tbl := tablesFromManifest(m)
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if isOverlay(e.Name()) {
			continue // read with its bucket
		}
		data, err := readBucket(filepath.Join(dir, "buckets", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for tok, posts := range data {
			if bucket(tok)+".bin" != e.Name() {
				t.Errorf("token %q filed in %s", tok, e.Name())
			}
			posts = liveOnly(posts, m.DeadRecords)
			if len(posts) == 0 {
				continue
			}
			for _, p := range posts {
				r, err := readRecordAt(f, p.Off, tbl)
				if err != nil {
					t.Fatalf("posting %q at %d: %v", tok, p.Off, err)
				}
				h := fnv.New64a()
				_, _ = h.Write([]byte(r.Text))
				out[tok] = append(out[tok], fmt.Sprintf("%s|%s|%s|%s|%x|tool=%v", byOrd[p.Sid], r.Key, r.Role, r.Time.UTC().Format(time.RFC3339Nano), h.Sum64(), p.Tool))
			}
		}
	}
	snap := map[string]string{}
	for tok, ps := range out {
		sort.Strings(ps)
		snap[tok] = strings.Join(ps, "\n")
	}
	return snap
}

func samePostingsAsRebuild(t *testing.T, inc string) {
	t.Helper()
	fresh := inc + "-postings"
	parityPass(t, fresh, true)
	samePostings(t, inc, fresh)
}

// samePostings fails the test where two indexes post differently.
func samePostings(t *testing.T, inc, fresh string) {
	t.Helper()
	a, b := postingSnapshot(t, inc), postingSnapshot(t, fresh)
	var diffs []string
	for tok := range a {
		if a[tok] != b[tok] {
			diffs = append(diffs, fmt.Sprintf("%q\n  incremental: %s\n  rebuild:     %s", tok, a[tok], b[tok]))
		}
	}
	for tok := range b {
		if _, ok := a[tok]; !ok {
			diffs = append(diffs, fmt.Sprintf("%q missing from the incremental index", tok))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		if len(diffs) > 10 {
			diffs = diffs[:10]
		}
		t.Errorf("postings differ from a rebuild:\n%s", strings.Join(diffs, "\n"))
	}
}

// A pass that replaces one session tokenizes and encodes that session's
// records, not the store's: every other record is copied as stored and keeps
// its postings, moved to where it now sits (#5383). The postings still answer
// what a rebuild's do.
func TestReplacementPassWorkIsBoundedByTheChange(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	const sessions, turns = 40, 6
	body := func(id string, n int, tail string) string {
		var b strings.Builder
		for i := range n {
			role, text := "user", fmt.Sprintf(`"why does the %s retry loop spin, take %d"`, id, i)
			if i%2 == 1 {
				role, text = "assistant", fmt.Sprintf(`"the backoff in %s never resets, ещё раз %d 索引"`, id, i)
			}
			b.WriteString(parityClaudeLine(id, fmt.Sprintf("%02d:00Z", i), role, text))
		}
		if tail != "" {
			b.WriteString(parityClaudeLine(id, "59:00Z", "user", tail))
		}
		return b.String()
	}
	for i := range sessions {
		id := fmt.Sprintf("s%02d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body(id, turns, ""), false)
	}
	// A tool output long enough to be stored deflated, so a carried record
	// that is compressed is carried too.
	long := strings.Repeat("compiling pkg/ledger.go\\n--- FAIL: TestLedgerCursor\\n", 400)
	parityWrite(t, filepath.Join(proj, "s00.jsonl"),
		parityClaudeLine("s00", "58:00Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./ledger"}}]`)+
			parityClaudeLine("s00", "58:01Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":"`+long+`"}]`), true)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)

	// Rewritten shorter, which no append can follow: the replacement pass.
	changed := filepath.Join(proj, "s07.jsonl")
	parityWrite(t, changed, body("s07", 2, `"quokka marmalade"`), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, later, later)
	passKeyed.Store(0)
	passEncoded.Store(0)
	parityPass(t, inc, false)
	keyed, encoded := passKeyed.Load(), passEncoded.Load()
	// The control: the pass did replace s07 rather than append to it.
	s, ok, err := FindByIdentity(inc, "claude", "s07")
	if err != nil || !ok || len(s.Messages) != 3 {
		t.Fatalf("s07 after the rewrite = %d messages ok=%v err=%v, want 3", len(s.Messages), ok, err)
	}
	if keyed != 3 || encoded != 3 {
		t.Errorf("the pass tokenized %d and encoded %d records, want the 3 of the session it replaced", keyed, encoded)
	}
	sameAsRebuild(t, inc)
	samePostingsAsRebuild(t, inc)
}

// An old index whose postings do not all read is no source for the carried
// records' postings: they are keyed from their text again, and the result is
// still a rebuild's.
func TestReplacementPassRekeysWhenPostingsAreShort(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	// The pass that writes the log anew: an appending one keeps the postings
	// files it has and reads none of them.
	appendReplacing = false
	defer func() { appendReplacing = true }()
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	for i := range 6 {
		id := fmt.Sprintf("s%d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"),
			parityClaudeLine(id, "00:00Z", "user", fmt.Sprintf(`"pgbouncer pool %s"`, id))+
				parityClaudeLine(id, "01:00Z", "assistant", `"the pool is per process"`), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	// Postings files that no longer read, each its own way: the rest cannot
	// stand for them, and two workers failing at once must not crash the pass.
	if err := os.WriteFile(filepath.Join(inc, "buckets", bucket("tpgbouncer")+".bin"), []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(inc, "buckets", bucket("tis")+".bin")
	if fi, err := os.Stat(short); err != nil {
		t.Fatal(err)
	} else if err := os.Truncate(short, fi.Size()-1); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(proj, "s3.jsonl")
	parityWrite(t, changed, parityClaudeLine("s3", "00:00Z", "user", `"narwhal"`), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, later, later)
	passKeyed.Store(0)
	parityPass(t, inc, false)
	if got := passKeyed.Load(); got != 11 {
		t.Errorf("keyed %d records, want all 11 of the store", got)
	}
	samePostingsAsRebuild(t, inc)
}

// A new session lands between `deja index`'s freshness check and its pass:
// its prompt in the Claude prompt log, its transcript beside it. The pass
// must not read the log against a walk that has no transcript, and once the
// transcript moves to another project the index must match a rebuild, which
// drops the log's copy of the prompt (#5503).
func TestANewSessionBetweenCheckAndPassMatchesARebuild(t *testing.T) {
	for _, how := range []string{"EnsureChecked", "Ensure", "walk-then-updateIndex"} {
		t.Run(how, func(t *testing.T) {
			h := newTwoWayEnv(t)
			r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
			r.openStores()
			s1 := &historySession{project: "svc", sid: "s1", day: 3}
			r.promptLogged(s1, "start: pool ledger")
			h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
			pass := func() {
				t.Helper()
				if err := Ensure(r.index, "", false, nil); err != nil {
					t.Fatal(err)
				}
			}
			pass()
			// Something else changes, so the check is stale.
			h.add("svc", "s1", r.turns(s1))
			fresh, _, c := CheckUpToDate(r.index, "")
			if fresh || c == nil {
				t.Fatal("the index is up to date after a transcript grew")
			}
			s10 := &historySession{project: "app", sid: "s10", day: 4}
			r.promptLogged(s10, "start: parser outbox index")
			h.put("app", "s10", hPrompt("app", "s10", 4, 0, "start: parser outbox index")+r.turns(s10))
			var err error
			switch how {
			case "EnsureChecked":
				err = EnsureChecked(c, false, io.Discard)
			case "walk-then-updateIndex":
				// The check's walk, read under the lock as it stands: the
				// transcript is missing from it, as one that lands between any
				// walk and its parse is.
				unlock, lerr := lockDir(r.index)
				if lerr != nil {
					t.Fatal(lerr)
				}
				err = updateIndex(r.index, "", "", c.files, false, io.Discard)
				unlock()
			default:
				err = Ensure(r.index, "", false, io.Discard)
			}
			if err != nil {
				t.Fatal(err)
			}
			// The session moves to another project.
			if err := os.MkdirAll(filepath.Dir(h.path("infra", "s10")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(h.path("app", "s10"), h.path("infra", "s10")); err != nil {
				t.Fatal(err)
			}
			s10.project = "infra"
			pass()
			r.compare(5, 1)
			h.add("infra", "s10", r.turns(s10))
			pass()
			r.compare(5, 2)
		})
	}
}

// A prompt-log line and its session's transcript read in one pass, the line
// naming the project the transcript has since left: the client's log parser
// looks for the transcript under the old project and keeps the line, and a
// rebuild drops it because the transcript holds the session. The append path
// read the log first and kept it; the replacing path, which another file's
// deletion sends the pass down, kept it whatever the order (#5503).
func TestAPromptLogLineIsDroppedForATranscriptReadInTheSamePass(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		name := "append"
		if replacing {
			name = "replacing"
		}
		t.Run(name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
			r.openStores()
			s1 := &historySession{project: "svc", sid: "s1", day: 3}
			r.promptLogged(s1, "start: pool ledger")
			h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
			s2 := &historySession{project: "svc", sid: "s2", day: 3}
			h.put("svc", "s2", hPrompt("svc", "s2", 3, 0, "why does the kafka outbox fail")+r.turns(s2))
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			s10 := &historySession{project: "app", sid: "s10", day: 4}
			r.promptLogged(s10, "start: parser outbox index")
			s10.project = "infra"
			h.put("infra", "s10", hPrompt("app", "s10", 4, 0, "start: parser outbox index")+r.turns(s10))
			if replacing {
				if err := os.Remove(h.path("svc", "s2")); err != nil {
					t.Fatal(err)
				}
			}
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			r.compare(5, 1)
		})
	}
}

// A prompt already in the log when the check walks, and its transcript
// landing after it: the log parser skips a session whose transcript is on
// disk, so a pass over the check's walk read neither, and once the client
// deleted the transcript nothing was left to read. A rebuild keeps the
// session from the log (#5046); the pass now walks again (#5503).
func TestATranscriptLandingAfterTheCheckIsNotLost(t *testing.T) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	s1 := &historySession{project: "svc", sid: "s1", day: 3}
	r.promptLogged(s1, "start: pool ledger")
	h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s18 := &historySession{project: "svc", sid: "s18", day: 4}
	r.promptLogged(s18, "fix it")
	fresh, _, c := CheckUpToDate(r.index, "")
	if fresh || c == nil {
		t.Fatal("the index is up to date after the prompt log grew")
	}
	h.put("svc", "s18", hPrompt("svc", "s18", 4, 0, "fix it")+r.turns(s18))
	w := storeWalks.Load()
	if err := EnsureChecked(c, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took the check's walk though a transcript arrived after it")
	}
	if err := os.Remove(h.path("svc", "s18")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	r.compare(5, 1)
}

// The same for a Codex rollout filed under a month the store has no
// directory for yet: Codex files them by year, month and day, so the
// directory that changes is two above the ones the walk listed.
func TestARolloutInANewMonthAfterTheCheckIsNotLost(t *testing.T) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	rollout := func(c *codexRollout, month int) string {
		p := filepath.Join(r.more.codexRoot, "sessions", "2026", fmt.Sprintf("%02d", month), fmt.Sprintf("%02d", c.day),
			fmt.Sprintf("rollout-2026-%02d-%02dT10-00-00-%s.jsonl", month, c.day, c.id))
		writeHistoryFile(r, p, codexLine(c.at(), "session_meta", map[string]any{"id": c.id, "cwd": "/w/app"})+r.codexTurns(c), false)
		return p
	}
	rollout(&codexRollout{id: "019a0000-0000-7000-8000-000000000001", day: 3}, 6)
	rollout(&codexRollout{id: "019a0000-0000-7000-8000-000000000002", day: 4}, 6)
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	b := &codexRollout{id: "019a0000-0000-7000-8000-000000000003", day: 5}
	hist := filepath.Join(r.more.codexRoot, "history.jsonl")
	writeHistoryFile(r, hist, hjson(map[string]any{"session_id": b.id, "ts": b.at().Unix(), "text": "why does the kafka outbox fail"}), false)
	fresh, _, c := CheckUpToDate(r.index, "")
	if fresh || c == nil {
		t.Fatal("the index is up to date after the prompt log grew")
	}
	p := rollout(b, 7)
	w := storeWalks.Load()
	if err := EnsureChecked(c, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took the check's walk though a rollout arrived after it")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	r.compare(5, 1)
}

func cherryChunk(text string) string {
	return `{"type":"assistant","uuid":"u-` + text[len(text)-3:] + `","requestId":"req-9","sessionId":"cs-9","timestamp":"2026-09-30T11:00:01.000Z","message":{"id":"msg-9","role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

// Cherry Studio appends a snapshot line per stream chunk. An index that ran
// between two chunks stored the half reply, and the next pass appended the
// full one beside it: show kept "the loop has no" next to the finished
// answer, the duplicate #3644 removed for a full read (#4346).
func TestCherryStudioIndexMidStreamKeepsOneReply(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "CherryStudio", "Data", "Agents", ".claude", "projects")
	proj := filepath.Join(root, "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "cs-9.jsonl")
	head := `{"type":"user","sessionId":"cs-9","timestamp":"2026-09-30T11:00:00.000Z","cwd":"/tmp/proj","message":{"role":"user","content":"why does the retry loop spin"}}` + "\n" +
		cherryChunk("the loop") + cherryChunk("the loop has no")
	if err := os.WriteFile(path, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cherrystudio", false, nil); err != nil {
		t.Fatal(err)
	}

	full := "the loop has no attempt cap, so it spins until the server answers"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(cherryChunk("the loop has no attempt cap") + cherryChunk(full))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "cherrystudio", false, nil); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var replies []string
	for _, r := range recs {
		if r.Record.SourcePath == path && r.Record.Role == "assistant" {
			replies = append(replies, r.Record.Text)
		}
	}
	if len(replies) != 1 || replies[0] != full {
		t.Errorf("assistant records = %q, want only the finished reply", replies)
	}
}

func legacyBucket(tok string) string {
	runes := []rune(tok)
	if len(runes) >= 2 && isShardASCII(runes[0]) && isShardASCII(runes[1]) {
		return safe(string(runes[:2]))
	}
	if len(runes) > 3 {
		runes = runes[:3]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(string(runes)))
	return fmt.Sprintf("x%02x", h.Sum32()%256)
}

func legacyCJKKeys(s string) []string {
	bgs := cjkfold.Bigrams(s)
	out := make([]string, 0, len(bgs))
	for _, bg := range bgs {
		out = append(out, "t"+cjkfold.String(bg))
	}
	return out
}

func collectCJKIndexKeys492(s string) []string {
	var out []string
	cjkIndexKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

func assertSameKeySet492(t *testing.T, input string, got, want []string) {
	t.Helper()

	gotSet := make(map[string]struct{}, len(got))
	for _, key := range got {
		gotSet[key] = struct{}{}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, key := range want {
		wantSet[key] = struct{}{}
	}

	if len(gotSet) != len(wantSet) {
		t.Fatalf("input %q: key-set size differs: got %d (%q), want %d (%q)",
			input, len(gotSet), got, len(wantSet), want)
	}
	for key := range wantSet {
		if _, ok := gotSet[key]; !ok {
			t.Fatalf("input %q: missing key %q: got %q, want %q",
				input, key, got, want)
		}
	}
}

func countKey492(keys []string, want string) int {
	n := 0
	for _, key := range keys {
		if key == want {
			n++
		}
	}
	return n
}

func TestBucketDifferential492(t *testing.T) {
	cases := []struct {
		name string
		tok  string
	}{
		{name: "empty", tok: ""},
		{name: "one ASCII rune", tok: "t"},
		{name: "two ASCII runes", tok: "ta"},
		{name: "three ASCII runes", tok: "tag"},
		{name: "second rune non-ASCII", tok: "t界"},
		{name: "CJK followed by Latin", tok: "界tag"},
		{name: "supplementary Han", tok: "t𠀾"},
		{name: "ASCII punctuation", tok: "t-"},
		{name: "ASCII uppercase and punctuation", tok: "T!"},
		{name: "invalid prefix", tok: "t\xff\xfe"},
		{name: "truncated multibyte prefix", tok: "t\xe8\xa8"},
		{name: "lone continuation byte", tok: "\x80"},
		{name: "lone continuation bytes", tok: "\x80\xbf"},
		{name: "valid RuneError query term", tok: "t\ufffdquery"},
		{name: "RuneError followed by invalid byte", tok: "t\ufffd\xffquery"},
		{name: "invalid third rune after ASCII shard", tok: "ta\xff"},
		{name: "invalid byte after hashed prefix", tok: "t界\xff"},
		{name: "invalid byte beyond hashed prefix", tok: "t界語\xff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bucket(tc.tok)
			want := legacyBucket(tc.tok)
			if got != want {
				t.Fatalf("bucket(%q) = %q, legacyBucket = %q; bytes: % x",
					tc.tok, got, want, []byte(tc.tok))
			}
		})
	}
}

func TestCJKIndexKeysDifferential492(t *testing.T) {
	longRun := strings.Repeat("漢字仮名", 24)
	if len(longRun) <= 64 {
		t.Fatalf("long-run fixture is only %d bytes", len(longRun))
	}

	cases := []struct {
		name string
		text string
	}{
		{name: "ASCII guard", text: "plain ASCII text only"},
		{name: "fold collision", text: "關係 关系"},
		{name: "single-rune runs", text: "茶 A 山"},
		{name: "two-rune run", text: "装订"},
		{name: "supplementary-plane Han", text: "𠀾界𠀀"},
		{name: "kana only", text: "ひらがなカタカナ"},
		{name: "mark run adjacent to CJK", text: "テストーー"},
		{name: "Hangul only", text: "한글만사용"},
		{name: "mixed CJK and Latin boundaries", text: "abc装订def 관계 xyz"},
		{name: "long CJK run across tokenizer byte boundary", text: longRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectCJKIndexKeys492(tc.text)
			want := legacyCJKKeys(tc.text)
			assertSameKeySet492(t, tc.text, got, want)
		})
	}
}

// TestCJKMarkOnlyRuns492 pins the intentional asymmetry between query and
// index emission. The Bigrams mark guard (#1392) has no index-side mirror. On
// one- and two-mark runs the token path emits the identical key, so the
// asymmetry never reaches the index. On runs of three or more marks cjkIndexKeys
// contributes interior bigrams the token path does not, so mirroring the guard
// would change the index. This table is the tripwire for both directions
// (#492).
func TestCJKMarkOnlyRuns492(t *testing.T) {
	cases := []struct {
		input     string
		wantKeys  []string
		covered   bool
		extraKeys []string
	}{
		{input: "ー", wantKeys: []string{"tー"}, covered: true},
		{input: "ーー", wantKeys: []string{"tーー"}, covered: true},
		{
			input:     "ーーーー",
			wantKeys:  []string{"tーーーー", "tーー"},
			extraKeys: []string{"tーー"},
		},
		{input: "ｰ", wantKeys: []string{"tｰ"}, covered: true},
		{input: "ﾞ", wantKeys: []string{"tﾞ"}, covered: true},
		{input: "ﾟ", wantKeys: []string{"tﾟ"}, covered: true},
		{input: "〆", wantKeys: []string{"t〆"}, covered: true},
		{
			input:     "ｰﾞﾟ",
			wantKeys:  []string{"tｰﾞﾟ", "tｰﾞ", "tﾞﾟ"},
			extraKeys: []string{"tｰﾞ", "tﾞﾟ"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := cjkfold.Bigrams(tc.input); len(got) != 0 {
				t.Fatalf("cjkfold.Bigrams(%q) = %q, want no keys", tc.input, got)
			}

			gotKeys := indexKeys(tc.input)
			assertSameKeySet492(t, tc.input, gotKeys, tc.wantKeys)

			tokenPath := make(map[string]struct{})
			for _, tok := range tokens(tc.input) {
				tokenPath["t"+tok] = struct{}{}
			}
			for _, part := range identifierParts(tc.input) {
				tokenPath["t"+part] = struct{}{}
			}

			cjkKeys := collectCJKIndexKeys492(tc.input)
			cjkSet := make(map[string]struct{}, len(cjkKeys))
			for _, key := range cjkKeys {
				cjkSet[key] = struct{}{}
			}

			if tc.covered {
				for _, key := range cjkKeys {
					if _, ok := tokenPath[key]; !ok {
						t.Fatalf("input %q: cjkIndexKeys key %q is not covered by token path", tc.input, key)
					}
				}
				return
			}

			if len(tc.extraKeys) == 0 {
				t.Fatalf("input %q: non-covered fixture names no extra cjkIndexKeys keys", tc.input)
			}
			for _, key := range tc.extraKeys {
				if _, ok := cjkSet[key]; !ok {
					t.Fatalf("input %q: cjkIndexKeys did not emit golden extra key %q; got %q",
						tc.input, key, cjkKeys)
				}
				if _, ok := tokenPath[key]; ok {
					t.Fatalf("input %q: golden extra key %q is unexpectedly covered by token path",
						tc.input, key)
				}
			}
		})
	}
}

func TestCJKMarkGuardSweep492(t *testing.T) {
	matched := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		if !cjkfold.IsCJK(r) {
			continue
		}

		input := string(r)
		if len(cjkfold.Bigrams(input)) != 0 {
			continue
		}
		matched++

		gotKeys := indexKeys(input)
		indexSet := make(map[string]struct{}, len(gotKeys))
		for _, key := range gotKeys {
			indexSet[key] = struct{}{}
		}

		wantKey := "t" + input
		if _, ok := indexSet[wantKey]; !ok {
			t.Fatalf("rune %U: indexKeys(%q) = %q, missing token-path key %q",
				r, input, gotKeys, wantKey)
		}

		tokenPath := make(map[string]struct{})
		for _, tok := range tokens(input) {
			tokenPath["t"+tok] = struct{}{}
		}
		for _, part := range identifierParts(input) {
			tokenPath["t"+part] = struct{}{}
		}

		cjkKeys := collectCJKIndexKeys492(input)
		for _, key := range cjkKeys {
			if _, ok := tokenPath[key]; !ok {
				t.Fatalf("rune %U: cjkIndexKeys key %q is not covered by token path; cjk keys %q",
					r, key, cjkKeys)
			}
		}
	}

	if matched < 5 {
		t.Fatalf("mark-guard sweep matched %d CJK runes, want at least 5", matched)
	}
}

func TestCJKIndexKeysFoldCollision492(t *testing.T) {
	traditional := "關係"
	simplified := "关系"
	if cjkfold.String(traditional) != cjkfold.String(simplified) {
		t.Fatalf("fixture does not fold to one key: %q -> %q, %q -> %q",
			traditional, cjkfold.String(traditional),
			simplified, cjkfold.String(simplified))
	}

	input := traditional + " " + simplified
	got := collectCJKIndexKeys492(input)
	want := legacyCJKKeys(input)
	assertSameKeySet492(t, input, got, want)

	collisionKey := "t" + cjkfold.String(traditional)
	if n := countKey492(want, collisionKey); n != 2 {
		t.Fatalf("legacy fixture should emit collision key twice, got %d in %q", n, want)
	}
	if n := countKey492(got, collisionKey); n != 1 {
		t.Fatalf("new emitter should deduplicate collision key, got %d in %q", n, got)
	}
}

// The byte-identical buckets/ check testifies for ingestion only. The query
// side still depends on cjkBigrams emitting unfolded bigrams — the
// function-word filter must see the runes the user typed — and that has no
// index artifact to diff, so this pins it directly: Traditional-script input
// through expandCJKTokens keeps its script, before and after the ingestion
// emitter split (#492).
func TestExpandCJKTokensKeepsScript492(t *testing.T) {
	got := query.ExpandCJKTokens([]string{"韓國簽證"})
	want := []string{"韓國", "國簽", "簽證"}
	if len(got) != len(want) {
		t.Fatalf("query.ExpandCJKTokens(韓國簽證) = %q, want %q", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("query.ExpandCJKTokens(韓國簽證)[%d] = %q, want %q (folded or reordered)", i, got[i], w)
		}
	}
	if f := cjkfold.String("韓國"); f == "韓國" {
		t.Fatalf("fixture lost its point: %q does not fold", "韓國")
	}
}

// The benchmarks pit the new emitter and shard function against the legacy
// copies above inside one binary, so the comparison is immune to the
// binary-layout variance that dominates whole-build wall clocks on small
// corpora (#492).

var benchJAText = strings.Repeat("昨日のビルドで漢字の索引が遅くなった原因を調べた。設定を確認し、キャッシュを削除した。", 4)

func BenchmarkCJKKeysLegacy(b *testing.B) {
	for range b.N {
		keys := legacyCJKKeys(benchJAText)
		if len(keys) == 0 {
			b.Fatal("no keys")
		}
	}
}

func BenchmarkCJKKeysNew(b *testing.B) {
	for range b.N {
		n := 0
		cjkIndexKeys(benchJAText, func(string) { n++ })
		if n == 0 {
			b.Fatal("no keys")
		}
	}
}

var benchBucketToks = []string{"tconfig", "t設定", "t漢字", "tjanuary", "tcache", "t索引", "t2026-01", "tビル"}

func BenchmarkBucketLegacy(b *testing.B) {
	for range b.N {
		for _, tok := range benchBucketToks {
			if legacyBucket(tok) == "" {
				b.Fatal("empty bucket")
			}
		}
	}
}

func BenchmarkBucketNew(b *testing.B) {
	for range b.N {
		for _, tok := range benchBucketToks {
			if bucket(tok) == "" {
				b.Fatal("empty bucket")
			}
		}
	}
}

func TestCJKIndexDifferentialGenerative492(t *testing.T) {
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // a seeded fixture, not crypto

	t.Run("bucket random bytes", func(t *testing.T) {
		for i := range 1000 {
			b := make([]byte, rng.Intn(32))
			for j := range b {
				b[j] = byte(rng.Intn(256)) //nolint:gosec // Intn(256) fits a byte
			}
			if len(b) > 0 && i%5 == 0 {
				b[0] = 0xff
			}

			tok := string(b)
			got := bucket(tok)
			want := legacyBucket(tok)
			if got != want {
				t.Fatalf("case %d: bucket bytes % x = %q, legacyBucket = %q",
					i, b, got, want)
			}
		}
	})

	t.Run("CJK and ASCII mixes", func(t *testing.T) {
		alphabet := []rune{
			'关', '關', '系', '係', '装', '訂', '計', '数',
			'漢', '字', '𠀾', '𠀀', '界',
			'あ', 'い', 'う', 'カ', 'ナ',
			'한', '글', '만',
			'a', 'Z', '0', '_', '-', ' ', '.', '/', '\n',
		}
		for range 300 {
			var text strings.Builder
			n := rng.Intn(80)
			for range n {
				text.WriteRune(alphabet[rng.Intn(len(alphabet))])
			}

			input := text.String()
			got := collectCJKIndexKeys492(input)
			want := legacyCJKKeys(input)
			assertSameKeySet492(t, input, got, want)
		}
	})
}

func claudeHistoryEntry(t *testing.T, id, display string, at time.Time) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"display": display, "pastedContents": map[string]any{}, "timestamp": at.UnixMilli(), "project": "/tmp/proj", "sessionId": id})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// A session known only from Claude Code's prompt log is one session however
// many prompts it has, and an append reads the same as a rebuild (#5046).
func TestClaudeHistoryAppendMatchesARebuild(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	at := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "gone-1", "why does the retry loop spin", at), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	parityWrite(t, h, claudeHistoryEntry(t, "gone-1", "and why does the backoff never reset", at.Add(time.Minute)), true)
	parityPass(t, inc, false)
	sameAsRebuild(t, inc)
	s, ok, err := FindByIdentity(inc, "claude", "gone-1")
	if err != nil || !ok || len(s.Messages) != 2 || s.Path != h {
		t.Fatalf("prompts-only session = %+v ok=%v err=%v", s, ok, err)
	}
}

// A transcript deja kept after Claude Code deleted it holds the prompt the log
// holds too. The log is read for sessions with no transcript on disk, which a
// kept one is: its prompt was merged in a second time, and on the append path
// the log took the row because the kept path was gone (#5046).
func TestAKeptTranscriptIsNotShadowedByItsPrompts(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "kept-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("kept-1", "00:00Z", "user", `"fix the flaky pool test"`)+
		parityClaudeLine("kept-1", "00:05Z", "assistant", `"the conn was never closed, closing it in the defer"`), false)
	asked := time.Date(2026, 7, 17, 9, 0, 0, 400_000_000, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "kept-1", "fix the flaky pool test", asked), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if err := os.Remove(tr); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	before := paritySnapshot(t, inc)["claude:kept-1"]
	if before == "" {
		t.Fatal("the deleted transcript was not kept")
	}
	check := func(when string) {
		t.Helper()
		s, ok, err := FindByIdentity(inc, "claude", "kept-1")
		if err != nil || !ok {
			t.Fatalf("%s: kept session gone: %v", when, err)
		}
		if s.Path != tr {
			t.Errorf("%s: the row is %s, want the kept transcript %s", when, s.Path, tr)
		}
		n := 0
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "fix the flaky pool test") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: the prompt is held %d times: %+v", when, n, s.Messages)
		}
	}
	// A line the log gains for the same id reaches the append path, where the
	// kept row's path no longer exists.
	parityWrite(t, h, claudeHistoryEntry(t, "kept-1", "fix the flaky pool test", asked.Add(time.Second))+
		claudeHistoryEntry(t, "gone-2", "what did the pool fix change", asked.Add(time.Hour)), true)
	parityPass(t, inc, false)
	check("after an append")
	// A rebuild reads every line of the log again, the kept one's included.
	parityPass(t, inc, true)
	check("after a rebuild")
	if got := paritySnapshot(t, inc)["claude:kept-1"]; got != before {
		t.Errorf("the kept session changed:\n  %s\nwant\n  %s", got, before)
	}
	if _, ok, _ := FindByIdentity(inc, "claude", "gone-2"); !ok {
		t.Error("a session only the log knows was not read")
	}
}

// Codex's history.jsonl had the same hole: a rollout deja kept after it was
// deleted got its prompt again from the log on the next rebuild.
func TestAKeptCodexRolloutIsNotShadowedByItsPrompt(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	const id = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0009"
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
	parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the retry loop spin"}]}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
	parityWrite(t, filepath.Join(tmp, "codex", "history.jsonl"), `{"session_id":"`+id+`","ts":1784278801,"text":"why does the retry loop spin"}`+"\n", false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if err := os.Remove(roll); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	parityPass(t, inc, true)
	s, ok, err := FindByIdentity(inc, "codex", id)
	if err != nil || !ok {
		t.Fatalf("kept rollout gone: %v", err)
	}
	n := 0
	for _, m := range s.Messages {
		if m.Text == "why does the retry loop spin" {
			n++
		}
	}
	if n != 1 || s.Path != roll || s.Project == "history" {
		t.Errorf("kept rollout after a rebuild: prompt held %d times, row %s, project %q", n, s.Path, s.Project)
	}
}

// countPrompt is how many messages of a session hold text, and the row's path.
func countPrompt(t *testing.T, dir, harness, id, text string) (int, string) {
	t.Helper()
	s, ok, err := FindByIdentity(dir, harness, id)
	if err != nil || !ok {
		t.Fatalf("%s:%s not indexed: %v", harness, id, err)
	}
	n := 0
	for _, m := range s.Messages {
		if strings.Contains(m.Text, text) {
			n++
		}
	}
	return n, s.Path
}

// Claude Code writes the prompt to history.jsonl before the transcript line,
// and deja's prompt hook runs a pass in between: a new session's first prompt
// is indexed from the log, and the transcript lands a pass later. The log's
// copy goes and the session is the transcript's, as a rebuild has it (#5046).
func TestATranscriptArrivingAfterItsPromptTakesTheSessionOver(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	asked := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "new-1", "fix the flaky pool test", asked), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if n, _ := countPrompt(t, inc, "claude", "new-1", "fix the flaky pool test"); n != 1 {
		t.Fatalf("the log's prompt was not indexed before the transcript: %d", n)
	}
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "new-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("new-1", "00:00.5Z", "user", `"fix the flaky pool test"`)+
		parityClaudeLine("new-1", "00:05Z", "assistant", `"the conn was never closed"`), false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "new-1", "fix the flaky pool test"); n != 1 || path != tr {
		t.Errorf("after the transcript landed the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// The same pair in Codex: a rollout written after its history line.
func TestACodexRolloutArrivingAfterItsHistoryLineTakesItOver(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	const id = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0010"
	parityWrite(t, filepath.Join(tmp, "codex", "history.jsonl"), `{"session_id":"`+id+`","ts":1784278801,"text":"why does the retry loop spin"}`+"\n", false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
	parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the retry loop spin"}]}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "codex", id, "why does the retry loop spin"); n != 1 || path != roll {
		t.Errorf("after the rollout landed the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// The other order: a transcript on disk first, its prompt logged later. The
// parser leaves the line to the transcript.
func TestAPromptLoggedAfterItsTranscriptIsLeftToIt(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "first-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("first-1", "00:00Z", "user", `"why does the cache miss"`)+
		parityClaudeLine("first-1", "00:05Z", "assistant", `"the key carries the port"`), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	parityWrite(t, filepath.Join(tmp, "history.jsonl"), claudeHistoryEntry(t, "first-1", "why does the cache miss", time.Date(2026, 7, 17, 9, 0, 0, 300_000_000, time.UTC)), false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "first-1", "why does the cache miss"); n != 1 || path != tr {
		t.Errorf("the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// A prompt the log holds a moment before its transcript waits a pass: the file
// is read again from the same offset, and by then the parser skips the line,
// so the common order never takes the full rewrite a takeover needs.
func TestAFreshPromptWaitsForItsTranscript(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	now := time.Now().UTC().Truncate(time.Second)
	inc := filepath.Join(tmp, "inc")
	// A store built first, so the passes below are appends.
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-proj", "old-1.jsonl"), parityClaudeLine("old-1", "00:00Z", "user", `"an older question"`), false)
	parityPass(t, inc, false)
	parityWrite(t, h, claudeHistoryEntry(t, "live-1", "why does the queue stall", now), false)
	parityPass(t, inc, false)
	if _, ok, _ := FindByIdentity(inc, "claude", "live-1"); ok {
		t.Fatal("a prompt seconds old was indexed from the log before its transcript could land")
	}
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "live-1.jsonl")
	line := fmt.Sprintf(`{"type":"user","sessionId":"live-1","timestamp":%q,"cwd":"/tmp/proj","message":{"role":"user","content":"why does the queue stall"}}`+"\n", now.Add(500*time.Millisecond).Format(time.RFC3339Nano))
	parityWrite(t, tr, line, false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "live-1", "why does the queue stall"); n != 1 || path != tr {
		t.Errorf("the prompt is held %d times, row %s", n, path)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := m.Files[h]; !ok || f.Size == 0 {
		t.Error("the log was not marked read once the transcript landed")
	}
	sameAsRebuild(t, inc)
}

func TestClaudeAutoMemoryFullLifecycleInIndex(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	claudeRoot := filepath.Join(home, ".claude", "projects")
	memDir := filepath.Join(claudeRoot, "-Users-alex-svc", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CLAUDE_MEM_DB", filepath.Join(tmp, "no-claude-mem.db"))
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(tmp, "no-xcode"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(tmp, "no-cc-mirror"))

	notePath := filepath.Join(memDir, "release_gate.md")
	writeNote := func(body string, stamp time.Time) {
		t.Helper()
		if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(notePath, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	writeNote("---\nname: release-gate\ndescription: Deployment freeze rules\ntype: project\n---\nFirst paragraph: GlacierTokenOne.\n\nSecond paragraph: GlacierTokenTwo deep in body.\n", t0)

	dir := filepath.Join(tmp, "index")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("initial Ensure: %v", err)
	}

	// Both paragraphs from full body must be searchable.
	for _, q := range []string{"GlacierTokenOne", "GlacierTokenTwo"} {
		ss, err := Search(dir, search.Options{Query: q})
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(ss) != 1 {
			t.Fatalf("Search(%q) returned %d sessions, want 1: %#v", q, len(ss), ss)
		}
	}

	// Update in place (longer text, newer mtime): ParseFrom is nil so replacePass
	// replaces prior content cleanly without duplicating old paragraphs.
	t1 := t0.Add(time.Minute)
	writeNote("---\nname: release-gate\ndescription: Updated deployment rules\ntype: project\n---\nReplaced paragraph: GlacierTokenThree only.\n\nExtra trailing paragraph for larger file size.\n", t1)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("update Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenTwo"}); err != nil || len(ss) != 0 {
		t.Fatalf("stale GlacierTokenTwo remained after edit: ss=%#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenThree"}); err != nil || len(ss) != 1 {
		t.Fatalf("updated GlacierTokenThree = %#v, err=%v", ss, err)
	}

	// Editing frontmatter to veto the note drops its indexed records.
	t2 := t1.Add(time.Minute)
	writeNote("---\nname: release-gate\nstate: rejected\n---\nGlacierTokenFour must be vetoed.\n", t2)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("veto Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenThree"}); err != nil || len(ss) != 0 {
		t.Fatalf("GlacierTokenThree remained after veto: ss=%#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFour"}); err != nil || len(ss) != 0 {
		t.Fatalf("vetoed GlacierTokenFour indexed: ss=%#v err=%v", ss, err)
	}

	// Re-enable, index, then delete the .md file from disk: deletion removes it
	// cleanly rather than keeping it like an expired transcript.
	t3 := t2.Add(time.Minute)
	writeNote("---\nname: release-gate\n---\nGlacierTokenFive active note.\n", t3)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("re-enable Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFive"}); err != nil || len(ss) != 1 {
		t.Fatalf("GlacierTokenFive before delete = %#v, err=%v", ss, err)
	}
	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("post-delete Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFive"}); err != nil || len(ss) != 0 {
		t.Fatalf("deleted note remained in index: ss=%#v err=%v", ss, err)
	}
}

func TestClaudeMemDBIndexingAndSearch(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)

	db := filepath.Join(tmp, "claude-mem.db")
	t.Setenv("DEJA_CLAUDE_MEM_DB", db)

	stmts := `
create table sdk_sessions (
  sdk_session_id text primary key,
  claude_session_id text,
  project text,
  created_at_epoch integer,
  completed_at_epoch integer,
  last_activity_epoch integer
);

create table user_prompts (
  id integer primary key autoincrement,
  sdk_session_id text,
  prompt_text text,
  created_at_epoch integer
);

create table observations (
  id integer primary key autoincrement,
  sdk_session_id text,
  title text,
  subtitle text,
  narrative text,
  text text,
  facts text,
  files_modified text,
  created_at_epoch integer
);

create table session_summaries (
  id integer primary key autoincrement,
  sdk_session_id text,
  request text,
  investigated text,
  learned text,
  completed text,
  next_steps text,
  notes text,
  created_at_epoch integer
);

insert into sdk_sessions values (
  's1', 'c1', '/Users/alice/projects/payment-service',
  1700000000, 1700000100, 1700000200
);

insert into user_prompts values (
  1, 's1', 'payment gateway timeout retry loop', 1700000010
);

insert into observations values (
  1, 's1', 'Payment Gateway Findings', 'Timeout in retry loop',
  'Exponential backoff prevents cascading failure in payment gateway',
  'Circuit breaker observed working', 'Fact: latency dropped',
  '["pkg/pay/gateway.go"]', 1700000050
);

insert into session_summaries values (
  1, 's1', 'Fix payment gateway timeout',
  'Investigated backoff logic',
  'Learned optimal retry window is 200ms',
  'Completed payment circuit breaker',
  'Next steps write integration tests',
  'Notes: production verified', 1700000100
);
`
	c := exec.CommandContext(t.Context(), "sqlite3", db)
	c.Stdin = strings.NewReader(stmts)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v %s", err, out)
	}

	indexDir := filepath.Join(tmp, "index")
	if err := Ensure(indexDir, "", false, io.Discard); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	results, err := Search(indexDir, search.Options{Query: "circuit breaker payment", All: true})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("Search returned 0 results for 'circuit breaker payment'")
	}

	s := results[0]
	if s.ID != "cm-s1" {
		t.Errorf("ID = %q, want cm-s1", s.ID)
	}
	if s.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", s.Harness)
	}
}

// A clean result that lands in the pass after its call gives the command the
// exit 0 a rebuild stamps on it, in every harness whose reader records an
// explicit clean exit (antigravity, claude, clawcode, codex, commandcode,
// copilot, goose, kiro-cli, pi, omp and qwen). kimi carries bare stdout and
// stays unstamped as a control.
func TestACleanResultInTheNextPassStampsItsExit(t *testing.T) {
	cases := []struct {
		name, env, file string
		head, call, res string
	}{
		{
			name: "codex", env: "DEJA_CODEX_ROOT",
			file: "sessions/2026/07/17/rollout-2026-07-17T09-00-00-0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001.jsonl",
			head: `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001","cwd":"/tmp/proj"}}` + "\n" +
				`{"timestamp":"2026-07-17T09:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"timestamp":"2026-07-17T09:02:00.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go vet ./retry\"}","call_id":"call_3"}}` + "\n",
			res:  `{"timestamp":"2026-07-17T09:02:01.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_3","output":"Process exited with code 0\nOutput:\nok"}}` + "\n",
		},
		{
			name: "copilot", env: "DEJA_COPILOT_ROOT",
			file: "session-state/0199cccc-0000-7000-8000-000000000001/events.jsonl",
			head: `{"type":"session.start","timestamp":"2026-07-17T09:00:00Z","data":{"sessionId":"0199cccc-0000-7000-8000-000000000001","startTime":"2026-07-17T09:00:00Z","context":{"cwd":"/tmp/proj"}}}` + "\n" +
				`{"type":"user.message","timestamp":"2026-07-17T09:00:01Z","data":{"content":"fix the retry loop"}}` + "\n",
			call: `{"type":"tool.execution_start","timestamp":"2026-07-17T09:02:00Z","data":{"toolCallId":"tc1","toolName":"bash","arguments":{"command":"go vet ./retry"}}}` + "\n",
			res:  `{"type":"tool.execution_complete","timestamp":"2026-07-17T09:02:01Z","data":{"toolCallId":"tc1","success":true,"result":{"content":"ok"},"toolTelemetry":{"metrics":{"exit_code":0}}}}` + "\n",
		},
		{
			name: "kimi", env: "DEJA_KIMI_ROOT",
			file: "sessions/wd_proj/session_t01/agents/main/wire.jsonl",
			head: `{"type":"metadata","protocol_version":"1.4","created_at":1790870000000}` + "\n" +
				`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},"time":1790870004000}` + "\n",
			call: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c1","name":"Bash","args":{"command":"go vet ./retry"}},"time":1790870004786}` + "\n",
			res:  `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"ok"}},"time":1790870004853}` + "\n",
		},
		{
			name: "claude", env: "DEJA_CLAUDE_ROOT",
			file: "-tmp-proj/c1.jsonl",
			head: `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}` + "\n",
			call: `{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}` + "\n",
		},
		{
			name: "clawcode", env: "DEJA_CLAWCODE_ROOT",
			file: "s1.jsonl",
			head: `{"type":"session_meta","session_id":"s1","created_at_ms":1759350554000,"workspace_root":"/tmp/proj"}` + "\n" +
				`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","message":{"role":"assistant","blocks":[{"type":"tool_use","id":"call_1","name":"bash","input":"{\"command\":\"go vet ./retry\"}"}]}}` + "\n",
			res:  `{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"call_1","tool_name":"bash","output":"{\"stdout\":\"ok\",\"stderr\":\"\",\"interrupted\":false}","is_error":false}]}}` + "\n",
		},
		{
			name: "commandcode", env: "DEJA_COMMANDCODE_ROOT",
			file: "-tmp-proj/s1.jsonl",
			head: `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-10-01T20:09:15.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-10-01T20:09:16.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"shell_command","input":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-10-01T20:09:17.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"Exit code: 0\nok"}]}]}}` + "\n",
		},
		{
			name: "goose", env: "DEJA_GOOSE_ROOT",
			file: "sessions/20261001_1.jsonl",
			head: `{"working_dir":"/tmp/proj","description":"fix the retry loop","message_count":0}` + "\n" +
				`{"role":"user","created":1790848800,"content":[{"type":"text","text":"fix the retry loop"}]}` + "\n",
			call: `{"role":"assistant","created":1790848801,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go vet ./retry"}}}}]}` + "\n",
			res:  `{"role":"user","created":1790848802,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"exit_code":0}}}}]}` + "\n",
		},
		{
			name: "kiro-cli", env: "DEJA_KIRO_ROOT",
			file: "cli/11111111-2222-4333-8444-555555555555.jsonl",
			head: `{"version":"v1","kind":"Prompt","data":{"message_id":"m0","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}` + "\n",
			call: `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go vet ./retry"}}}],"meta":{"timestamp":1790848801}}}` + "\n",
			res:  `{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"ok","stderr":""}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}` + "\n",
		},
		{
			name: "pi", env: "DEJA_PI_ROOT",
			file: "--tmp-proj--/s-retry.jsonl",
			head: `{"type":"session","version":3,"id":"s-retry","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-09-01T09:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-vet","name":"bash","arguments":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"c-vet","toolName":"bash","content":[{"type":"text","text":"ok"}],"details":{},"isError":false}}` + "\n",
		},
		{
			name: "omp", env: "DEJA_OMP_ROOT",
			file: "--tmp-proj--/2026-10-01T10-00-00-000Z_o1.jsonl",
			head: `{"type":"session","version":3,"id":"omp-s1","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-10-01T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"details":{"exitCode":0},"isError":false}}` + "\n",
		},
		{
			name: "qwen", env: "DEJA_QWEN_ROOT",
			file: "projects/-tmp-proj/chats/q1.jsonl",
			head: `{"type":"user","sessionId":"q1","timestamp":"2026-07-17T09:00:00Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"assistant","sessionId":"q1","timestamp":"2026-07-17T09:00:01Z","cwd":"/tmp/proj","message":{"role":"model","parts":[{"functionCall":{"id":"f1","name":"run_shell_command","args":{"command":"go vet ./retry"}}}]}}` + "\n",
			res:  `{"type":"tool_result","sessionId":"q1","timestamp":"2026-07-17T09:00:02Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"functionResponse":{"id":"f1","name":"run_shell_command","response":{"output":"Command: go vet ./retry\nDirectory: (root)\nOutput: ok\nError: (none)\nExit Code: 0\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 4242"}}}]}}` + "\n",
		},
		{
			name: "antigravity", env: "DEJA_ANTIGRAVITY_ROOT",
			file: "brain/conv-vet-1/.system_generated/logs/transcript.jsonl",
			head: `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-01T09:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}` + "\n",
			call: `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-01T09:02:00Z","content":"","tool_calls":[{"id":"tc1","name":"run_command","args":{"CommandLine":"\"go vet ./retry\"","Cwd":"\"/tmp/proj\""}}]}` + "\n",
			res:  `{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-10-01T09:02:01Z","content":"Command exited with code 0.\nCommand output:\nok"}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := parityEnv(t, map[string]string{tc.env: "store"})
			f := filepath.Join(tmp, "store", filepath.FromSlash(tc.file))
			parityWrite(t, f, tc.head, false)
			inc := filepath.Join(tmp, "inc")
			parityPass(t, inc, false)
			parityWrite(t, f, tc.call, true)
			parityPass(t, inc, false)
			parityWrite(t, f, tc.res, true)
			parityPass(t, inc, false)
			sameAsRebuild(t, inc)
			snap := paritySnapshot(t, inc)
			held, stamped := false, false
			for _, v := range snap {
				if strings.Contains(v, "$ go vet ./retry  → exit 0") {
					stamped = true
				}
				if strings.Contains(v, "$ go vet ./retry") {
					held = true
				}
			}
			if tc.name == "kimi" {
				if !held || stamped {
					t.Errorf("kimi should hold unstamped command cleanly, got stamped=%v held=%v (%q)", stamped, held, snap)
				}
			} else if !stamped {
				t.Errorf("%s did not stamp  → exit 0 across incremental pass: %q", tc.name, snap)
			}
		})
	}
}

func TestAntigravityAppendCrossRepoReadKeepsInitialProject(t *testing.T) {
	root := parityEnv(t, map[string]string{"DEJA_ANTIGRAVITY_ROOT": "ag"})
	inc := filepath.Join(root, "inc")
	repoMain := filepath.Join(root, "work", "org", "repo-main")
	repoOther := filepath.Join(root, "work", "org", "other-repo")
	for _, repo := range []string{repoMain, repoOther} {
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	viewCall := func(path string) map[string]any {
		return map[string]any{"name": "view_file", "args": map[string]any{"AbsolutePath": path}}
	}
	marshalRow := func(row map[string]any) string {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	headCalls := []map[string]any{
		viewCall(filepath.Join(repoMain, "a.go")),
		viewCall(filepath.Join(repoMain, "b.go")),
		viewCall(filepath.Join(repoMain, "c.go")),
		viewCall(filepath.Join(repoMain, "d.go")),
		viewCall(filepath.Join(repoMain, "e.go")),
	}
	tailCalls := []map[string]any{
		viewCall(filepath.Join(repoOther, "README.md")),
		viewCall(filepath.Join(repoOther, "go.mod")),
		viewCall(filepath.Join(repoOther, "doc.go")),
	}
	p := filepath.Join(root, "ag", "brain", "conv-proj-1", ".system_generated", "logs", "transcript.jsonl")
	head := marshalRow(map[string]any{
		"source":     "USER_EXPLICIT",
		"type":       "USER_INPUT",
		"created_at": "2026-07-01T10:00:00Z",
		"content":    "inspect repo-main",
	}) + "\n" + marshalRow(map[string]any{
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"created_at": "2026-07-01T10:00:01Z",
		"content":    "reading repo-main",
		"tool_calls": headCalls,
	}) + "\n"
	parityWrite(t, p, head, false)
	parityPass(t, inc, false)

	tail := marshalRow(map[string]any{
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"created_at": "2026-07-01T10:01:00Z",
		"content":    "reading other-repo",
		"tool_calls": tailCalls,
	}) + "\n"
	parityWrite(t, p, tail, true)
	parityPass(t, inc, false)

	sameAsRebuild(t, inc)
	metas, err := AllMeta(inc)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Project != "org/repo-main" {
		t.Fatalf("AllMeta = %+v, want 1 row with Project %q", metas, "org/repo-main")
	}
}

// `cline history update --title` rewrites the <id>.json manifest and leaves
// the transcript alone, and only a changed transcript marked the session for
// a re-read, so the old title stayed (#4319).
func TestClineManifestRenameIsIndexed(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "cline-sessions")
	t.Setenv("DEJA_CLINE_ROOT", root)

	id := "1790000000000_abcde"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, id+".json")
	write := func(title string) {
		t.Helper()
		body := `{"sessionId":"` + id + `","cwd":"/tmp/proj","metadata":{"title":"` + title + `"}}`
		if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("run the tests")
	transcript := `{"version":1,"messages":[{"role":"user","content":[{"type":"text","text":"clinetitleneedle run the tests"}],"ts":1767225600000}]}`
	if err := os.WriteFile(filepath.Join(dir, id+".messages.json"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(tmp, "index.db")
	opts := search.Options{Query: "clinetitleneedle", Harness: "cline"}
	if err := EnsureForSearch(indexDir, opts, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err := Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "run the tests" {
		t.Fatalf("bad indexed title: %#v err=%v", recent, err)
	}

	// Same size, so only the mtime says the manifest changed.
	write("renamed: pytest")
	tick := time.Now().Add(10 * time.Second)
	if err = os.Chtimes(manifest, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(indexDir, opts, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err = Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "renamed: pytest" {
		t.Fatalf("manifest-only rename was not indexed: %#v err=%v", recent, err)
	}
}

// The state has to follow the manifest the reader opens, which is named after
// the session directory whatever the transcript beside it is called.
func TestClineMetadataFileIsTheReadersManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_CLINE_ROOT", root)
	dir := filepath.Join(root, "1790000000000_abcde")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"sessionId":"1790000000000_abcde"}`
	if err := os.WriteFile(filepath.Join(dir, "1790000000000_abcde.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1790000000000_abcde.messages.json", "teammate.messages.json"} {
		k, ok := kindForPath(filepath.Join(dir, name))
		if !ok || k.Sidecar == nil {
			t.Fatalf("%s: no sidecar for the cline-sdk kind", name)
		}
		if size, _ := k.Sidecar(filepath.Join(dir, name)); size != int64(len(manifest)) {
			t.Errorf("%s: sidecar size %d, want the manifest's %d", name, size, len(manifest))
		}
	}
}

// The substring rung found its candidates in another project, so the project
// filter left none. The close-tier check then had no offsets to read, and no
// offsets meant the whole record log: a recall that found nothing read every
// record on the store first, 13 s on 4050 sessions (#5370).
func TestCloseTierWithNoCandidateLeftReadsNoRecordLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	ss := []model.Session{{ //nolint:prealloc // a fixture literal the loop below grows
		Harness: "claude", ID: "elsewhere", Project: "other",
		Messages: []model.Message{{Role: "user", Text: "the reverseproxy bridge between services kept retrying"}},
	}, {
		Harness: "claude", ID: "here", Project: "mine",
		Messages: []model.Message{{Role: "user", Text: "the bridge between services in our app keeps retrying"}},
	}}
	for i := range 40 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("filler-%d", i), Project: "mine",
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("rename the parser field %d", i)},
				{Role: "assistant", Text: fmt.Sprintf("renamed field %d in the token stream", i)},
			},
		})
	}
	writeTestStore(t, dir, ss...)

	read := 0
	wholeLogScanned = func() { read++ }
	t.Cleanup(func() { wholeLogScanned = nil })

	o := query.Options{Query: "proxy bridge services", All: true, Project: "mine"}
	res, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if read != 0 {
		t.Errorf("the recall read %d records of the whole log after the filter left no candidate", read)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "here" {
		ids := make([]string, 0, len(res.Sessions))
		for _, s := range res.Sessions {
			ids = append(ids, s.ID)
		}
		t.Errorf("want the session in this project first, got %v", ids)
	}
}

// A command record holds the invocation on its first line, and the listing is
// what keeps it to one row. The claude parser refuses multi-line commands
// outright, but it is not the only parser writing these records — codex,
// cline, cursor and kimi write them without that rule — so this guard is the
// only thing between a heredoc and the `deja how` listing.
//
// Measured by removing it: with firstTextLine returning its argument whole, no
// test in this package failed. This is that test.
func TestCommandListingKeepsOneCommandToOneRow(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	// Two sessions run the same invocation with different heredoc bodies —
	// what a reader means by "the same command", and two sessions is what the
	// listing needs before it remembers one at all.
	session := func(id, body string) model.Session {
		return model.Session{
			Harness: "codex", ID: id, Project: "work",
			Messages: []model.Message{{
				Role: roleCommand,
				Text: "$ psql -c \"select count(*)\n  from orders\n  where state = '" + body + "'\"",
				Time: at,
			}},
		}
	}
	buildCommands(dir, []model.Session{session("c1", "stuck"), session("c2", "late")})

	uses := ReadCommands(dir)
	if len(uses) != 1 {
		t.Fatalf("the listing holds %d rows, want 1: %+v", len(uses), uses)
	}
	u := uses[0]
	if strings.Contains(u.Command, "\n") {
		t.Errorf("a row spans lines: %q", u.Command)
	}
	if u.Command != `$ psql -c "select count(*)` {
		t.Errorf("row command = %q", u.Command)
	}
	if u.Sessions != 2 || u.Runs != 2 {
		t.Errorf("two runs of one invocation counted as %d runs in %d sessions", u.Runs, u.Sessions)
	}
}

// A single-line command is untouched, and two genuinely different invocations
// stay two rows — the guard must not merge what only shares a first word.
func TestCommandListingKeepsDifferentInvocationsApart(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	msg := func(text string) model.Message {
		return model.Message{Role: roleCommand, Text: text, Time: at}
	}
	ss := make([]model.Session, 0, 2)
	for _, id := range []string{"d1", "d2"} {
		ss = append(ss, model.Session{
			Harness: "codex", ID: id, Project: "work",
			Messages: []model.Message{msg("$ go test ./..."), msg("$ go build ./cmd/deja")},
		})
	}
	buildCommands(dir, ss)

	uses := ReadCommands(dir)
	if len(uses) != 2 {
		t.Fatalf("the listing holds %d rows, want 2: %+v", len(uses), uses)
	}
	for _, u := range uses {
		if strings.Contains(u.Command, "\n") {
			t.Errorf("a row spans lines: %q", u.Command)
		}
		if u.Sessions != 2 {
			t.Errorf("%q counted %d sessions, want 2", u.Command, u.Sessions)
		}
	}
}

// The indexing line is the first output anyone sees from deja (#737).
func TestPluralS(t *testing.T) {
	for n, want := range map[int]string{0: "s", 1: "", 2: "s", 42: "s"} {
		if got := pluralS(n); got != want {
			t.Errorf("pluralS(%d) = %q, want %q", n, got, want)
		}
	}
}
