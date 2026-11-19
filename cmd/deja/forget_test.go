package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// "older than 0 days" is the whole store, and the typo that produces it is 0
// for 30. A negative duration filters nothing in `last` while deleting
// everything here (#739).
func TestForgetBeforeRejectsNonPositiveAges(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"work about the pool"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, age := range []string{"0d", "0", "0h", "-5d", "-1h"} {
		_, err := captureRun(t, "forget", "--before", age, "--dry-run")
		if err == nil {
			t.Errorf("--before %s was accepted", age)
			continue
		}
		if !strings.Contains(err.Error(), "which is every session") {
			t.Errorf("--before %s: %v", age, err)
		}
	}
	// Real ages still work, including sub-day ones.
	for _, age := range []string{"30d", "1h", "90m", "1s"} {
		if _, err := captureRun(t, "forget", "--before", age, "--dry-run"); err != nil {
			t.Errorf("--before %s: %v", age, err)
		}
	}
	// A date is a different form and keeps its own error.
	if _, err := captureRun(t, "forget", "--before", "2026-01-01", "--dry-run"); err != nil {
		t.Errorf("date form: %v", err)
	}
	if _, err := captureRun(t, "forget", "--before", "yesterdayish", "--dry-run"); err == nil ||
		!strings.Contains(err.Error(), "neither a duration nor a date") {
		t.Errorf("garbage form: %v", err)
	}
}

// The line names the copy and the command that takes it, which is the whole
// session: the reader decides, since the fork's own work goes too.
func TestForgetCopiesLine(t *testing.T) {
	if got := forgetCopiesLine(nil); got != "" {
		t.Fatalf("no copies printed %q", got)
	}
	one := forgetCopiesLine([]string{"claude:s9"})
	for _, want := range []string{"1 other session still holds", "claude:s9", "`deja forget --session s9`", "with its own work"} {
		if !strings.Contains(one, want) {
			t.Errorf("one copy: %q lacks %q", one, want)
		}
	}
	two := forgetCopiesLine([]string{"claude:s1", "claude:s9"})
	if !strings.Contains(two, "2 other sessions still hold") || !strings.Contains(two, "--session <id>` drops each") {
		t.Errorf("two copies: %q", two)
	}
}

// The fallback for records written before projects were recorded matched the
// project name anywhere in the digest, so `forget --project api` deleted a
// digest about work/web that said "a late api call" (#2330).
func TestForgetMatchesTheProjectWhereADigestNamesIt(t *testing.T) {
	match := forgetDigestMatcher(index.ForgetOptions{Project: "api"}, nil)

	elsewhere := usage.Snapshot{Digest: "<deja-recall>\n1. [claude] work/web · claude:b0 · 2 matches\n   the layout shift came from a late api call\n"}
	if match(elsewhere) {
		t.Errorf("a digest about work/web was matched because its text says \"api\"")
	}
	listed := usage.Snapshot{Digest: "<deja-recall>\n1. [claude] api · claude:a0 · 2 matches\n   the gateway timeout was a read deadline\n"}
	if !match(listed) {
		t.Errorf("a digest whose listing names the project was not matched")
	}
	hookBlock := usage.Snapshot{Digest: "<deja-recall>\n- **api** `a0` · 2026-08-28\n  the gateway timeout was a read deadline\n"}
	if !match(hookBlock) {
		t.Errorf("a hook digest naming the project was not matched")
	}
	// The recorded field still wins, and it is exact.
	recorded := usage.Snapshot{Projects: []string{"work/web"}, Digest: "1. [claude] api · anything\n"}
	if match(recorded) {
		t.Errorf("a record that names its own projects was matched by its text")
	}
	if !match(usage.Snapshot{Projects: []string{"api"}, Digest: "no project in the text at all"}) {
		t.Errorf("a record that names the project in its field was not matched")
	}
}

// Session ids are matched the same way: where the digest names one, not
// wherever the string happens to appear.
func TestForgetMatchesSessionIDsWhereADigestNamesThem(t *testing.T) {
	match := forgetDigestMatcher(index.ForgetOptions{Session: "a0"}, []string{"claude:a0"})
	if !match(usage.Snapshot{Digest: "1. [claude] api · claude:a0 · 2 matches\n"}) {
		t.Errorf("a digest listing the forgotten session was not matched")
	}
	if match(usage.Snapshot{Digest: "1. [claude] work/web · claude:b0 · 2 matches\n   we set a0 to 3 in the config\n"}) {
		t.Errorf("a digest was matched because its prose contains the id")
	}
}

// forget drops the messages and adds tombstones; the injection log kept the
// digests served from them, so `deja view` republished forgotten text with no
// trust rule in play (#2325).
func TestForgetTakesTheDigestsOfWhatItForgot(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. the quaxbolt overflow was an int32 cast\n", 1, 400, nil,
		[]string{"imported:secretclient/api"}, "local+imported")
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. my own connection pool question\n", 1, 400, nil,
		[]string{"tmp/mine"}, "local+imported")

	if _, err = captureRun(t, "forget", "--project", "imported:secretclient/api"); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "int32 cast") {
		t.Errorf("the forgotten project's digest is still in the injection log")
	}
	if !strings.Contains(string(log), "my own connection pool question") {
		t.Errorf("forget took a digest that belongs to another project")
	}
	// The events stay: they record that something was served, which is what
	// the counters read.
	if events := usage.Events(dir, 0); len(events) != 2 {
		t.Errorf("events = %d, want both — an event is what happened, not the content", len(events))
	}
}

// --dry-run says what it would take and takes nothing.
func TestForgetDryRunLeavesTheDigestsAlone(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"a question about pools"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedFrom(dir, usage.KindRecall, "<deja-recall>\n1. a question about pools\n", 1, 400, nil,
		[]string{"tmp/mine"}, "")

	out, err := captureRun(t, "forget", "--project", "tmp/mine", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "digest") {
		t.Errorf("the dry run does not say the digests would go:\n%s", out)
	}
	log, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "a question about pools") {
		t.Errorf("a dry run removed a digest")
	}
}

// `deja forget` is the one command that destroys history on purpose, and a
// build can be running while it does — a hook, another terminal, the warmup
// after an upgrade. The index lock is what keeps the two apart; this holds the
// outcome that matters rather than the mechanism: exactly what was named goes,
// nothing else does, and the store is still an index afterwards (#2427).
func TestForgetAgainstARebuildDropsOnlyWhatItNamed(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	for _, project := range []string{"keep", "gone"} {
		store := filepath.Join(root, "-work-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 40 {
			at := time.Now().Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)
			line := fmt.Sprintf(`{"type":"user","sessionId":"%s%d","timestamp":%q,"cwd":"/work/%s",`+
				`"message":{"role":"user","content":"the retry budget in %s take %d"}}`,
				project, i, at, project, project, i)
			name := filepath.Join(store, fmt.Sprintf("%s%d.jsonl", project, i))
			if err := os.WriteFile(name, []byte(strings.Repeat(line+"\n", 8)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		// A rebuild, the heaviest thing that can be holding the index.
		_ = index.Ensure(dir, "", true, nil)
	})
	out, errOut := captureBoth(t, "forget", "--project", "work/gone", "--all-matches")
	wg.Wait()
	if !strings.Contains(out+errOut, "sessions dropped") {
		t.Fatalf("forget did not report what it did:\n%s\n%s", out, errOut)
	}

	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("the index is not usable after the two ran together: %v", err)
	}
	if index.Damaged(dir) {
		t.Fatalf("the store was left damaged")
	}
	metas, err := index.Recent(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	kept, gone := 0, 0
	for _, m := range metas {
		switch {
		case strings.HasSuffix(m.Project, "work/gone"):
			gone++
		case strings.HasSuffix(m.Project, "work/keep"):
			kept++
		}
	}
	if gone != 0 {
		t.Errorf("%d sessions from the forgotten project came back", gone)
	}
	if kept != 40 {
		t.Errorf("the other project holds %d of its 40 sessions", kept)
	}
}

// The advice line named permissions unconditionally, so on a full disk it sent
// the reader to chmod a file that was already writable (#808).
func TestForgetNamesTheCauseItCouldNotClearTheTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fs.ErrPermission, "fix that file's permissions"},
		{"no space", syscall.ENOSPC, "free some space on that filesystem"},
		{"anything else", errors.New("i/o error"), "clear the problem above"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := forgetTitleFix(tc.err); got != tc.want {
				t.Errorf("fix = %q, want %q", got, tc.want)
			}
		})
	}
}

// A failed rewrite must not damage the notes file. The leftover temp file the
// same failure used to leave behind needs a full filesystem to reproduce, so
// it is verified by hand rather than here (#808).
func TestForgetPromotedTitlesKeepsTheNotesFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line := `{"kind":"promoted","session":"claude:s1","project":"p","title":"acme yard job","text":"t","src":"claude","state":"accepted","ts":"2026-07-01T10:00:00Z"}`
	if err := os.WriteFile(notes, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A read-only directory: the rewrite cannot create its temp file at all.
	// This used to put a directory where the temp file would go, which worked
	// while the temp name was derived from the destination — it is unique per
	// writer now, so two `deja remember` calls cannot land on one name (#1319).
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := sources.ForgetPromotedTitles(func(src string) bool { return src == "claude:s1" }); err == nil {
		t.Fatal("the rewrite reported success")
	}
	after, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "acme yard job") {
		t.Error("the notes file lost content on a failed rewrite")
	}
}

// The session-start digest is cached as prose beside the index, one file per
// working directory, and nothing removed one — so after `deja forget` the
// block quoting the forgotten session was still on disk. Recall was not
// serving it (the entry expires in a minute), but the privacy command is about
// the content, not only the answer (#3411, the shape #2537 closed for a note).
func TestForgetTakesTheCachedDigestWithIt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-api", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/w/api","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer overflows under load"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := hookCacheEntry{At: time.Now(), CWD: cwd, Gate: hookGate(), Digest: "recalled: the zonkobuffer overflows under load", Sessions: 1, Raw: 7}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	cache := hookCachePath(dir, cwd)
	if err = os.WriteFile(cache, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err = captureRun(t, "forget", "--session", "s1"); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadFile(cache)
	if err == nil && strings.Contains(string(left), "zonkobuffer") {
		t.Errorf("the forgotten session is still quoted in %s", filepath.Base(cache))
	}
}

// A tombstone whose session exists nowhere suppresses nothing. This machine
// carried three left by a stand run months ago, and `forget --list` printed
// them exactly like a session someone forgot on purpose yesterday (#3753).
func TestForgetListMarksATombstoneWhoseSessionIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid string) string {
		t.Helper()
		p := filepath.Join(store, name)
		rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"` + sid + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(p, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("kept.jsonl", "kept-one")
	gone := write("gone.jsonl", "gone-one")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"kept-one", "gone-one"} {
		if _, err := captureRunStderr(t, "forget", "--session", sid); err != nil {
			t.Fatal(err)
		}
	}
	// One transcript is deleted on disk after it was forgotten; the other is
	// still there, and its tombstone is the only thing keeping it out of search.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		id, _, _ := strings.Cut(line, "\t")
		rows[id] = line
	}

	if got := rows["claude:gone-one"]; !strings.Contains(got, "suppresses nothing") {
		t.Errorf("the tombstone of a deleted session is not marked: %q", got)
	}
	// Still standing between a transcript and search: that row is unchanged.
	if got := rows["claude:kept-one"]; got != "claude:kept-one" {
		t.Errorf("a tombstone still hiding a transcript was marked: %q", got)
	}
	// Still one line per tombstone, each starting with its id — `wc -l` and
	// `cut -f1` read the list the way they did before.
	if len(rows) != 2 {
		t.Errorf("want one row per tombstone, got %d:\n%s", len(rows), out)
	}
}

// Nothing forgotten whose session is gone: the list is byte-for-byte what it
// was, so a machine with no leftovers sees no change at all.
func TestForgetListIsUnchangedWithNoOrphans(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"ab12-one","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab12"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "claude:ab12-one" {
		t.Errorf("list = %q, want only the id", out)
	}
}

// The list is where someone who dropped more than they meant to lands, and it
// named no way back: `--unforget` was in `deja help` only, and the hint for a
// guessed `deja unforget` pointed at this same list (#919).
func TestForgetListNamesTheWayBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"ab12-one","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab12"); err != nil {
		t.Fatal(err)
	}

	// The ids stay on stdout so the list is still pipeable; the way back is a
	// note beside it.
	ids, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ids, "--unforget") {
		t.Errorf("the id list is no longer just ids:\n%s", ids)
	}
	note, err := captureRunStderr(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "--unforget") {
		t.Errorf("the list does not name the way back: %q", note)
	}

	// Nothing forgotten, nothing to say.
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:ab12-one"); err != nil {
		t.Fatal(err)
	}
	if note, err := captureRunStderr(t, "forget", "--list"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "--unforget") {
		t.Errorf("an empty list still advises an undo: %q", note)
	}

	// And the wrong guess with a real answer behind it names that answer
	// rather than the list alone.
	if got := commandHint("unforget"); !strings.Contains(got, "forget --unforget") {
		t.Errorf("the guess hint does not name the command: %q", got)
	}
}

// An empty list printed nothing on either stream, which reads the same as a
// command that did not run — and this is the command the other refusals point
// at (#1040). The line goes to stderr so a pipe still counts only ids.
func TestAnEmptyForgetListSaysSo(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout is not empty, a pipe would count it: %q", out)
	}
	errOut, err := captureRunStderr(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "nothing is forgotten on this machine") {
		t.Errorf("an empty list says nothing at all: %q", errOut)
	}
}

// Everything deja files under its own harness counted as a "promoted note", so
// forgetting a day of `remember` notes named something the reader had never
// done (#957).
func TestForgetNamesTheKindOfNotesItIsDropping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result index.ForgetResult
		want   string
		avoid  string
	}{
		{"only day buckets", index.ForgetResult{Sessions: 1, Notes: 1}, "a day of your own notes", "promoted"},
		{"several day buckets", index.ForgetResult{Sessions: 2, Notes: 2}, "2 days of your own notes", "promoted"},
		{"only promoted", index.ForgetResult{Sessions: 1, Notes: 1, Promoted: 1}, "promoted note", "day of your own"},
		{"both kinds", index.ForgetResult{Sessions: 4, Notes: 3, Promoted: 1}, "1 promoted, 2 days of notes", ""},
		{"no notes at all", index.ForgetResult{Sessions: 2}, "", "note"},
	} {
		got := forgetNotesLine(tc.result)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: said %q about sessions that are not notes", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q does not say %q", tc.name, got, tc.want)
		}
		if tc.avoid != "" && strings.Contains(got, tc.avoid) {
			t.Errorf("%s: %q claims %q", tc.name, got, tc.avoid)
		}
		// No doubled articles from composing the sentence.
		if strings.Contains(got, "a a ") || strings.Contains(got, "is 1 ") {
			t.Errorf("%s: reads badly: %q", tc.name, got)
		}
	}
}

// A note the user asked to forget has to lose its text, not just its place in
// the index: a tombstone silences search while the line stays in notes.jsonl,
// which deja wrote (#841).
func TestForgettingANoteRemovesItsLine(t *testing.T) {
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, text string }{
		{"s1", "the acme corp renewal price"},
		{"s2", "the winch brake pads"},
	} {
		line := `{"type":"user","message":{"role":"user","content":"` + tc.text + `"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + tc.id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, tc.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "s1", "--state", "accepted", "--note", "acme renewal agreed at 240k"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "s2", "--state", "accepted", "--note", "brake pads replaced"); err != nil {
		t.Fatal(err)
	}

	// Forgetting the source keeps the note — the decision is often why the raw
	// session was safe to forget (#666) — but must say the text is still there.
	out, err := captureRun(t, "forget", "--session", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "still holds what you wrote there") {
		t.Errorf("forgetting the source does not say the note kept its content:\n%s", out)
	}
	body, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "240k") {
		t.Fatalf("the note lost its content when only the source was forgotten:\n%s", body)
	}

	// Forgetting the note itself removes the line, and leaves the other note.
	out, err = captureRun(t, "forget", "--session", "deja-note-claude-s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed 1 promoted note") {
		t.Errorf("the removal is not reported:\n%s", out)
	}
	body, err = os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "240k") {
		t.Errorf("the forgotten note still holds its text:\n%s", body)
	}
	if !strings.Contains(string(body), "brake pads replaced") {
		t.Errorf("an unrelated note was removed too:\n%s", body)
	}

	// A note swept up by --project is a decision the reader deliberately kept:
	// #690 keeps it, and naming a session is the only case that may destroy it.
	out, err = captureRun(t, "forget", "--project", "proj")
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "brake pads replaced") {
		t.Errorf("--project destroyed a decision it should keep:\n%s\n%s", out, body)
	}
}

// The borrowed title is the forgotten session's first turn — a customer name
// in the case that found this (#666, #690). Dropping the rewrite error left
// three success lines standing over a line still on disk (#804).
func TestForgetSaysWhenTheBorrowedTitleSurvived(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	// Its own directory: hermeticEnv puts notes.jsonl beside HOME, and locking
	// that would lock the whole fixture rather than the notes file.
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"gearbox oil change for acme yard"},"timestamp":"2026-07-03T10:00:00Z","sessionId":"p7c","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "p7c", "--state", "accepted", "--note", "80W90"); err != nil {
		t.Fatal(err)
	}
	notes := sources.NotesFile()
	before, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "acme yard") {
		t.Fatalf("the note did not borrow the title:\n%s", before)
	}

	locked := filepath.Dir(notes)
	if err = os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	out, err := captureRun(t, "forget", "--session", "p7c")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "could not clear the title") {
		t.Errorf("a failed rewrite was reported as success:\n%s", out)
	}
	// The error text already contains the temp path, so asserting on the file
	// name alone passes without the advice line.
	if !strings.Contains(out, "it is still in "+notes) {
		t.Errorf("the output does not say where the borrowed title still is:\n%s", out)
	}
	if !strings.Contains(out, "run this again") {
		t.Errorf("the output does not say what to do:\n%s", out)
	}
	if strings.Contains(out, "cleared the borrowed title") {
		t.Errorf("output claims the title was cleared:\n%s", out)
	}
	after, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "acme yard") {
		t.Fatal("fixture no longer reproduces the failure")
	}
	_ = dir
}

// The #666 cleanup keyed off --session, so --project and --before left the
// borrowed line — a customer name, in the case that found this — sitting in
// notes.jsonl after the project was forgotten (#690).
func TestForgetByProjectClearsBorrowedTitlesAndNamesTheNotes(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	other := filepath.Join(tmp, "claude", "proj-q")
	for _, d := range []string{root, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(dir, sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/` + filepath.Base(dir) + `","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "a1", "the payroll export for ACME-4471")
	write(root, "a2", "the payroll retry path")
	write(other, "b1", "unrelated caching work")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "a1", "--state", "accepted", "--note", "batch the export nightly"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The dry run has to warn about the notes too — it exists to answer "how
	// much am I about to lose", and the notes are the part worth keeping.
	// The project name is derived from the store's directory layout, so it
	// carries the platform's separator — hard-coding "proj/p" passes on unix
	// and matches nothing on Windows.
	project := projectOf(t, dir, "a1")
	dry, err := captureRun(t, "forget", "--project", project, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry, "1 of them is a promoted note") {
		t.Errorf("dry run does not mention the note: %q", dry)
	}

	out, err := captureRun(t, "forget", "--project", project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cleared the borrowed title") {
		t.Errorf("forget --project said %q", out)
	}
	// The line offered `--session deja-note-…`, an id that matches nothing,
	// while the command was holding the real one (#1030). Whatever it names
	// has to be a selector the reader can paste back.
	if strings.Contains(out, "deja-note-…") {
		t.Errorf("the advice still elides the id: %q", out)
	}
	noteID := "deja-note-claude-a1"
	if !strings.Contains(out, "`deja forget --session "+noteID+"`") {
		t.Errorf("the advice does not name the note it just cleared: %q", out)
	}
	// The count folds notes and transcripts together, so it has to say which
	// is which: the notes are what the reader deliberately kept.
	// Two raw sessions and one note: the exact split, not just the words
	// "promoted note" — the line above already contains those.
	if !strings.Contains(out, "1 of them is a promoted note") {
		t.Errorf("report does not distinguish notes: %q", out)
	}
	if !strings.Contains(out, "sessions dropped: 3") {
		t.Errorf("dropped count changed: %q", out)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "notes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ACME-4471") {
		t.Errorf("the forgotten project's first line survived on disk:\n%s", b)
	}
	if !strings.Contains(string(b), "batch the export nightly") {
		t.Errorf("the decision was destroyed:\n%s", b)
	}
	// The dropped set holds the notes' own rows too, and turning one of those
	// into an id produced `deja-note-deja-deja-note-…`, which matches nothing.
	if strings.Contains(out, "deja-note-deja-") {
		t.Errorf("the advice offers an id built from a note's own row: %q", out)
	}
	// Last, because it removes the note: the id the line offers has to be a
	// selector that works, and the note's own row is already out of the index.
	gone, err := captureRun(t, "forget", "--session", noteID)
	if err != nil {
		t.Fatalf("the id the line offered does not run: %v", err)
	}
	if !strings.Contains(gone, "removed 1 promoted note") {
		t.Errorf("running the advised command removed nothing: %q", gone)
	}
	b, err = os.ReadFile(filepath.Join(tmp, "notes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "batch the export nightly") {
		t.Errorf("the advised command left the note on disk:\n%s", b)
	}
}

// A project that was never promoted from must not have its notes touched.
func TestForgetLeavesOtherProjectsNotesAlone(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the payroll export"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	keep := map[string]any{"ts": "2026-07-21T10:00:00Z", "project": "elsewhere", "text": "someone else's decision",
		"kind": "promoted", "session": "claude:zz9", "state": "accepted", "title": "a title from another project"}
	line := mustMarshal(t, keep)
	if err := os.WriteFile(notes, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--project", projectOf(t, index.DefaultDir(), "a1")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a title from another project") {
		t.Errorf("cleared a title belonging to a project that was not forgotten:\n%s", b)
	}
}

// projectOf reads a session's project back out of the manifest, so a test can
// name it the way this platform recorded it.
func projectOf(t *testing.T, dir, id string) string {
	t.Helper()
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == id {
			return m.Project
		}
	}
	t.Fatalf("session %q not in the manifest", id)
	return ""
}

// The refusal has to happen before anything is written. An earlier version of
// this check ran after index.Forget and printed "matches 2 sessions" over
// tombstones it had already added — a refusal that refused nothing (#870).
func TestForgetRefusesAWideSelectorWithoutDroppingAnything(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s10", "s11"} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted ` + id + `"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")

	// A prefix with no exact session behind it: refused, and nothing written.
	if err := runForget(dir, []string{"--session", "s"}); err == nil {
		t.Fatal("a prefix of three sessions was not refused")
	}
	if got := index.Tombstones(); len(got) != 0 {
		t.Fatalf("the refusal still forgot: %v", got)
	}

	// An id that names a session exactly means that session, not the two ids
	// it happens to be a prefix of.
	if err := runForget(dir, []string{"--session", "s1"}); err != nil {
		t.Fatalf("exact id refused: %v", err)
	}
	got := index.Tombstones()
	if len(got) != 1 || !strings.HasSuffix(got[0], ":s1") {
		t.Fatalf("tombstones = %v, want only claude:s1", got)
	}
}

// The refusal sends the reader to `--dry-run`, and the dry run counted what
// `--all-matches` would drop — a number the command as typed never produces
// (#1032).
func TestAmbiguousDryRunPreviewsTheCommandAsTyped(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s10", "s11"} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted ` + id + `"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--session", "s", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "as it stands this run drops nothing") {
		t.Errorf("the dry run does not say the command as typed drops nothing:\n%s", out)
	}
	if !strings.Contains(out, "with --all-matches it would drop: 3 sessions") {
		t.Errorf("the dry run does not say whose 3 sessions those are:\n%s", out)
	}
	// The refusal promises a listing, so the dry run names the sessions, and
	// it does not point the reader back at the command they just ran.
	if !strings.Contains(out, "sessions: claude:s1, claude:s10, claude:s11") {
		t.Errorf("the dry run lists no ids:\n%s", out)
	}
	if strings.Contains(out, "--dry-run` lists") {
		t.Errorf("the dry run sends the reader to itself:\n%s", out)
	}
	// The unambiguous form keeps its plain wording — nothing to qualify there.
	one, err := captureRun(t, "forget", "--session", "s1", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, "would drop: 1 session,") || strings.Contains(one, "as it stands") {
		t.Errorf("an exact id got the ambiguous wording:\n%s", one)
	}
}

// A repeated selector used to take the last one and drop the first without a
// word, so a command meant to delete two sessions deleted one and exited 0 —
// leaving behind a session the reader believes is gone (#2271).
func TestForgetRefusesARepeatedSelector(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"forget", "--session", "a1b2", "--session", "c3d4", "--dry-run"},
		{"forget", "--project", "alpha", "--project", "beta", "--dry-run"},
		{"forget", "--before", "30d", "--before", "10d", "--dry-run"},
	} {
		out, err := captureRun(t, args...)
		if err == nil {
			t.Errorf("%v was accepted, printing %q", args[1:], out)
			continue
		}
		if !strings.Contains(err.Error(), "twice") {
			t.Errorf("%v said %v — the message should say the flag was given twice", args[1:], err)
		}
	}

	// One of each still works, including the combination that narrows.
	if _, err := captureRun(t, "forget", "--session", "a1b2", "--dry-run"); err != nil {
		t.Errorf("a single --session: %v", err)
	}
	if _, err := captureRun(t, "forget", "--project", "alpha", "--before", "30d", "--dry-run"); err != nil {
		t.Errorf("--project with --before: %v", err)
	}
}

// The dry run and the run that acts must answer the same question the same
// way. #957 taught the sentence to tell a `remember` note from a promoted one
// and wired it into the `--dry-run` branch alone, so the run that actually
// dropped a day of notes called them promoted — the wrong answer, on the path
// that changes something, checked against the right answer one command earlier
// (#3599).
func TestForgetSaysTheSameThingWhetherOrNotItActs(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "remember", "the queue drains only after the worker restarts"); err != nil {
		t.Fatal(err)
	}
	id := onlyNoteID(t)

	dry, err := captureRun(t, "forget", "--session", id, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	said, err := captureRun(t, "forget", "--session", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ label, out string }{{"dry run", dry}, {"the run that acts", said}} {
		if !strings.Contains(c.out, "your own notes") {
			t.Errorf("%s does not call a remember note what it is:\n%s", c.label, c.out)
		}
		if strings.Contains(c.out, "promoted note") {
			t.Errorf("%s calls a remember note promoted, which the reader never did:\n%s", c.label, c.out)
		}
	}
}

// onlyNoteID is the id of the single note the store holds, read the way a
// reader would: off the search result rather than out of the manifest.
func onlyNoteID(t *testing.T) string {
	t.Helper()
	out, err := captureRun(t, "search", "queue drains", "--json")
	if err != nil {
		t.Fatal(err)
	}
	const key = `"id":"`
	_, after, ok := strings.Cut(out, key)
	if !ok {
		t.Fatalf("the note is not in the index:\n%s", out)
	}
	rest := after
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unreadable hit:\n%s", out)
	}
	return rest[:j]
}

// `--session` takes a prefix, so an id that names one session exactly could
// still reach every id beside it — twelve sessions for a selector that named
// one, and the count arrived afterwards, in the past tense (#870).
func TestForgetScopeRefusal(t *testing.T) {
	if err := forgetScopeRefusal("s1", 1, false); err != nil {
		t.Errorf("a single match was refused: %v", err)
	}
	err := forgetScopeRefusal("s1", 12, false)
	if err == nil {
		t.Fatal("a prefix of twelve sessions was not refused")
	}
	got := err.Error()
	for _, want := range []string{"matches 12 sessions", "--dry-run", "--all-matches"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not mention %q: %q", want, got)
		}
	}
	// Asked for explicitly, it proceeds.
	if err = forgetScopeRefusal("s1", 12, true); err != nil {
		t.Errorf("--all-matches still refused: %v", err)
	}
	// An elided id has no longer prefix to offer, so the wording differs and
	// must not send the reader after one (#859).
	err = forgetScopeRefusal("deja-2026…er-service", 2, false)
	if err == nil {
		t.Fatal("an ambiguous elided id was not refused")
	}
	if got := err.Error(); !strings.Contains(got, "the line elides") || strings.Contains(got, "--all-matches") {
		t.Errorf("elided wording = %q", got)
	}
}

// Every other command that takes a session id takes it bare. forget wants
// --session, which is right for the destructive one; what it said when it did
// not get it was that the id is an unknown flag, naming nothing the reader
// could do about it (#2656).
func TestForgetNamesTheFlagAnIdBelongsTo(t *testing.T) {
	for _, id := range []string{
		"imported-1aa758f985c5",
		"imported-…a758f985c5",
		"deadbeefcafe",
	} {
		t.Run(id, func(t *testing.T) {
			hermeticEnv(t)
			_, err := captureRun(t, "forget", id)
			if err == nil {
				t.Fatalf("forget accepted a bare id, which is the selector it deliberately does not take")
			}
			if strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("an id is not a flag: %v", err)
			}
			if !strings.Contains(err.Error(), "--session") {
				t.Fatalf("the message does not name the flag that would have worked: %v", err)
			}
			if !strings.Contains(err.Error(), id) {
				t.Fatalf("the message does not echo what was typed: %v", err)
			}
		})
	}
}

// A word that really is a flag keeps the flat refusal: naming --session there
// would send a reader after the wrong thing.
func TestForgetStillRefusesAnUnknownFlag(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "forget", "--sessions", "abc")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("a mistyped flag should still be called one, got %v", err)
	}
}

// Forgetting 100 sessions one id at a time took 10.5s against 0.2s for one
// `--project` call, and the bare refusal named no selector at all — so the
// expensive way was the discoverable one (#1022).
func TestForgetRefusalNamesItsSelectors(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "forget")
	if err == nil {
		t.Fatal("forget with no selector was accepted")
	}
	for _, want := range []string{"--session", "--project", "--before", "--dry-run", "--list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
	// And it must not advertise a selector forget does not have.
	for _, absent := range []string{"--query", "--harness"} {
		if strings.Contains(err.Error(), absent) {
			t.Errorf("the refusal offers %s, which forget does not accept: %v", absent, err)
		}
	}
}

// Two transcripts can write the same harness:id, and then one manifest row
// holds both conversations. The build says so once (#698); forget said
// "1 session" and took two, from two projects (#970).
func TestForgetSaysWhenARowCoversTwoConversations(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, p := range []struct{ dir, cwd, text string }{
		{"-proj-a", "/proj-a", "FIRST conversation about the ticker"},
		{"-proj-b", "/proj-b", "SECOND conversation about the vault"},
	} {
		store := filepath.Join(root, p.dir)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := `{"type":"user","message":{"role":"user","content":"` + p.text + `"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"dup","cwd":"` + p.cwd + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, "dup.jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--session", "dup", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more than one conversation") {
		t.Errorf("the dry run does not say the row holds two conversations:\n%s", out)
	}
	out, err = captureRun(t, "forget", "--session", "dup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more than one conversation") {
		t.Errorf("forget does not say what it took:\n%s", out)
	}

	// A row that holds one conversation says nothing extra.
	store := filepath.Join(root, "-solo")
	if err = os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a solo conversation"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"solo","cwd":"/solo"}` + "\n"
	if err = os.WriteFile(filepath.Join(store, "solo.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--session", "solo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "more than one conversation") {
		t.Errorf("an ordinary session was called a shared row:\n%s", out)
	}
}

// The tombstone is written before the rebuild that takes the records out. When
// the index cannot be written, forget handed back `mkdir /…/idx.tmp: permission
// denied` and left the reader believing the session was gone while search kept
// returning it (#976).
func TestForgetSaysWhatIsStillVisibleWhenTheIndexCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision about the retry counter"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s19","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s19.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "ro")
	dir := filepath.Join(parent, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755)
		_ = os.Chmod(dir, 0o755)
	})

	_, err := captureRun(t, "forget", "--session", "s19")
	if err == nil {
		t.Fatal("forget reported success with an index it could not write")
	}
	msg := err.Error()
	if strings.Contains(msg, ".tmp") || strings.Contains(msg, "mkdir") {
		t.Errorf("the failure names an internal path: %q", msg)
	}
	if !strings.Contains(msg, "check the directory's permissions") {
		t.Errorf("the failure does not say what to change: %q", msg)
	}
	if !strings.Contains(msg, "still in the index") {
		t.Errorf("the failure does not say the session is still visible: %q", msg)
	}
}

// forgetKeyOf answers "which session did this selector reach", which is what
// lets the failure above say whether the tombstone landed (#976).
func TestForgetKeyOfNamesTheSessionASelectorReached(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"abc123","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "abc123.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Session: "abc"}); got != "claude:abc123" {
		t.Errorf("forgetKeyOf(prefix) = %q, want claude:abc123", got)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Session: "nothing-like-it"}); got != "" {
		t.Errorf("a selector matching nothing named %q", got)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Project: "proj"}); got != "" {
		t.Errorf("a project selector named a single session: %q", got)
	}
}

// The dry probe that decides the scope refusal fails the same ways the real
// pass does, and its error went out raw: on an ejected volume `deja forget`
// answered `mkdir /Volumes/dejaidx: permission denied` (#976). Measured on a
// real HFS+ image; here the same shape is exercised through ensureError, which
// is what the probe now uses.
func TestForgetProbeErrorIsWordedLikeEveryOther(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "gone-volume", "index.db")
	err := ensureError(gone, os.ErrPermission)
	if err == nil || !strings.Contains(err.Error(), "is not there") {
		t.Errorf("a vanished mount point is worded as: %v", err)
	}
	if strings.Contains(err.Error(), "mkdir") {
		t.Errorf("the wording is still a syscall: %v", err)
	}
}

// forget writes the tombstone file before anything else, and a read-only
// ~/.config/deja arrived as "check the index directory's permissions" — a
// directory that was writable, so the suggested fix changed nothing (#1031).
func TestForgetNamesTheTombstoneFileItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision about the retry counter"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s21","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s21.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	privacy := filepath.Join(os.Getenv("HOME"), ".config", "deja")
	if err := os.MkdirAll(privacy, 0o700); err != nil {
		t.Fatal(err)
	}
	tombstones := filepath.Join(privacy, "tombstones")
	if err := os.WriteFile(tombstones, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privacy, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(privacy, 0o700) })

	_, err := captureRun(t, "forget", "--session", "claude:s21")
	if err == nil {
		t.Fatal("forget did not refuse with an unwritable tombstone file")
	}
	if !strings.Contains(err.Error(), tombstones) {
		t.Errorf("the refusal does not name the file it could not write: %v", err)
	}
	if strings.Contains(err.Error(), "DEJA_INDEX_DIR") {
		t.Errorf("the refusal sends the reader at the index instead: %v", err)
	}
	// The refusal is only worth anything if nothing was dropped.
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "s21") {
		t.Errorf("the session went away under a refused forget: %q", out)
	}
}

// On a machine whose index was never built, `deja forget --session` blamed an
// unmounted disk, exited 1 and wrote no tombstone, so the first `deja index`
// took the session in (#5057).
func TestForgetWithoutIndexRecordsTheTombstone(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the zanzibar rollout plan"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	kept := strings.ReplaceAll(strings.ReplaceAll(body, "a1", "b2"), "zanzibar", "harmless")
	if err := os.WriteFile(filepath.Join(root, "b2.jsonl"), []byte(kept), 0o644); err != nil {
		t.Fatal(err)
	}
	// Neither the index nor the directory it goes in exists yet.
	dir := filepath.Join(tmp, "cache", "deja", "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	if _, err := captureRun(t, "forget", "--project", "nowhere"); err == nil || strings.Contains(err.Error(), "unmounted") {
		t.Errorf("a selector nothing matches on a fresh machine: %v", err)
	}
	out, err := captureRun(t, "forget", "--session", "a1")
	if err != nil {
		t.Fatalf("forget on a machine with no index: %v", err)
	}
	if !strings.Contains(out, "nothing is indexed yet") || !strings.Contains(out, "claude:a1") {
		t.Errorf("forget did not say there was nothing indexed yet: %q", out)
	}
	if !index.Tombstoned("claude:a1") {
		t.Fatal("no tombstone was written")
	}
	if _, err := os.Stat(filepath.Join(dir, "tombstones")); err == nil {
		t.Error("a mirror was written into an index that does not exist")
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	indexed := index.IndexedSessionKeys(dir)
	if !indexed["claude:b2"] {
		t.Fatalf("the build did not index the session left alone: %v", indexed)
	}
	if indexed["claude:a1"] {
		t.Error("the first `deja index` took in the session forgotten before it")
	}
}

// The unmount message is for a build the disk went away from, not for a read
// of an index that was never built.
func TestEnsureErrorNoIndexYetIsNotAnUnmountedDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never", "index.db")
	got := ensureError(dir, fmt.Errorf("read manifest: %w", &fs.PathError{Op: "open", Path: filepath.Join(dir, "manifest.gob"), Err: syscall.ENOENT})).Error()
	if strings.Contains(got, "unmounted") || !strings.Contains(got, "nothing is indexed yet") {
		t.Errorf("index never built: %q", got)
	}
}

// doctor read a DEJA_INDEX_DIR outside the home whose directory a build would
// create as an unmounted disk. An empty mount point still reads as one.
func TestFreshHomePathOutsideHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	busy := t.TempDir()
	if err := os.WriteFile(filepath.Join(busy, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !freshHomePath(filepath.Join(busy, "idx")) {
		t.Error("a missing directory under a writable, used one was read as unmounted")
	}
	if freshHomePath(filepath.Join(t.TempDir(), "idx")) {
		t.Error("an empty directory outside the home no longer reads as a mount point")
	}
}
