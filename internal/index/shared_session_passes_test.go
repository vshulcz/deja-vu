package index

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Two transcripts that hold one session id, changed over separate passes: an
// append to one and a later replacement of the other. The replacement dropped
// and re-read only its own file, so a turn both files hold was written twice,
// or the copy the append had deduplicated against the other file was lost
// with it (#5358). Every case ends where a rebuild would.
func TestPassesOverFilesSharingASessionMatchRebuild(t *testing.T) {
	const t0, t1, t2, t3 = "2026-01-02T03:04:05Z", "2026-01-02T03:04:06Z", "2026-01-02T04:00:00Z", "2026-01-02T05:00:00Z"
	type step func(t *testing.T, a, b, c string)
	cases := []struct {
		name, id string
		// also names another id the passes must leave as a rebuild does.
		also   string
		passes []step
	}{
		{
			name: "grown, then the other rewritten",
			id:   "s-x",
			passes: []step{
				func(t *testing.T, a, b, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-x", t0, "alpha one"))
					write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				},
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta queue"))
					bumpMTime(t, b)
				},
			},
		},
		{
			name: "grown, then a torn line finished",
			id:   "s-x",
			passes: []step{
				func(t *testing.T, a, b, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-x", t0, "alpha one"))
					line := claudeLine("s-x", t1, "beta cache")
					write(t, b, claudeLine("s-x", t0, "alpha one")+line[:len(line)-10])
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				},
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
					bumpMTime(t, b)
				},
			},
		},
		{
			name: "new copy, then the original rewritten",
			id:   "s-b",
			passes: []step{
				func(t *testing.T, a, b, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
					write(t, b, claudeLine("s-b", t0, "bravo two"))
				},
				func(t *testing.T, _, _, c string) {
					t.Helper()
					write(t, c, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t2, "charlie four"))
				},
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-b", t0, "bravo too"))
					bumpMTime(t, b)
				},
			},
		},
		{
			// The turn both files hold was filed under b alone; b dropping it
			// leaves a's copy as the only one, which no record of a names.
			name: "the file a shared turn was filed under drops it",
			id:   "s-x",
			passes: []step{
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-x", t0, "alpha one"))
				},
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-x", t1, "beta queue"))
					bumpMTime(t, b)
				},
			},
		},
		{
			// An unchanged third file owns the row; two others holding the
			// session change in one pass, one grown and one rewritten.
			name: "owner unchanged, two others change",
			id:   "s-x",
			passes: []step{
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-x", t0, "alpha one"))
				},
				func(t *testing.T, _, b, c string) {
					t.Helper()
					write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
					write(t, c, claudeLine("s-x", t1, "beta cache")+claudeLine("s-x", t2, "charlie four"))
				},
				func(t *testing.T, _, b, c string) {
					t.Helper()
					// beta cache is filed under b, which sorts first.
					appendTo(t, b, claudeLine("s-x", t3, "delta five"))
					write(t, c, claudeLine("s-x", t1, "beta cache")+claudeLine("s-x", t3, "charlie queue"))
					bumpMTime(t, c)
				},
			},
		},
		{
			// A copy grows while the file that owns the row is rewritten, in
			// the same pass: c holds s-b beside b, which sorts first.
			name: "a copy grows as the owner is rewritten",
			id:   "s-b",
			passes: []step{
				func(t *testing.T, a, b, c string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
					write(t, b, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t1, "bravo cache"))
					write(t, c, claudeLine("s-b", t0, "bravo two"))
				},
				func(t *testing.T, _, b, c string) {
					t.Helper()
					appendTo(t, c, claudeLine("s-b", t2, "charlie three"))
					write(t, b, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t1, "bravo queue"))
					bumpMTime(t, b)
				},
			},
		},
		{
			// The same, over two passes.
			name: "a copy grows, then the owner is rewritten",
			id:   "s-b",
			passes: []step{
				func(t *testing.T, a, b, c string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
					write(t, b, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t1, "bravo cache"))
					write(t, c, claudeLine("s-b", t0, "bravo two"))
				},
				func(t *testing.T, _, _, c string) {
					t.Helper()
					appendTo(t, c, claudeLine("s-b", t1, "bravo cache")+claudeLine("s-b", t2, "charlie three"))
				},
				func(t *testing.T, _, b, _ string) {
					t.Helper()
					write(t, b, claudeLine("s-b", t2, "bravo queue"))
					bumpMTime(t, b)
				},
			},
		},
		{
			// A transcript is named by its last sessionId, so a file whose id
			// changes is filed whole under the new one. The append filed the
			// turns it had read under the old id and the tail under the new
			// (#5379).
			name: "the id changes partway",
			id:   "s-b",
			also: "s-a",
			passes: []step{
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-b", t2, "bravo two"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-b", t3, "charlie three"))
				},
			},
		},
		{
			// And back: the file is the old session again, as a rebuild names it.
			name: "the id changes and changes back",
			id:   "s-a",
			also: "s-b",
			passes: []step{
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-b", t2, "bravo two"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-a", t3, "charlie three"))
				},
			},
		},
		{
			// The new id is one another file already holds.
			name: "the id changes to another file's",
			id:   "s-b",
			also: "s-a",
			passes: []step{
				func(t *testing.T, a, b, _ string) {
					t.Helper()
					write(t, a, claudeLine("s-a", t0, "alpha one"))
					write(t, b, claudeLine("s-b", t1, "bravo two"))
				},
				func(t *testing.T, a, _, _ string) {
					t.Helper()
					appendTo(t, a, claudeLine("s-b", t2, "charlie three"))
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			a := filepath.Join(claude, "p", "a.jsonl")
			b := filepath.Join(claude, "p", "b.jsonl")
			c := filepath.Join(claude, "p", "c.jsonl")
			dir := filepath.Join(tmp, "idx")
			for i, pass := range tc.passes {
				pass(t, a, b, c)
				indexPass(t, dir)
				if i > 0 {
					sessionMatchesRebuild(t, dir, "claude", tc.id)
					if tc.also != "" {
						sessionMatchesRebuild(t, dir, "claude", tc.also)
					}
				}
			}
		})
	}
}

// A transcript `deja resume --write-back` put back holds the conversation
// only, and is recorded as read so it never replaces the tool output the index
// holds (#4617). A rewrite of another copy of the session must not read it.
func TestACopyRewriteLeavesAWrittenBackTranscriptUnread(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	dir := filepath.Join(tmp, "idx")
	const t0, t1 = "2026-01-02T03:04:05Z", "2026-01-02T03:04:06Z"
	tool := `{"type":"user","sessionId":"s-x","timestamp":"` + t1 + `","message":{"role":"user","content":[{"type":"tool_result","content":"toolneedle output of the build"}]}}` + "\n"
	write(t, a, claudeLine("s-x", t0, "alpha question")+tool)
	write(t, b, claudeLine("s-x", t0, "alpha question"))
	indexPass(t, dir)
	if v := sessionView(t, dir, "claude", "s-x"); !strings.Contains(v, "toolneedle") {
		t.Fatalf("control: the tool output was not indexed:\n%s", v)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	write(t, a, claudeLine("s-x", t0, "alpha question"))
	if ok, err := AdoptWriteBack(dir, a, a); !ok || err != nil {
		t.Fatalf("adopt: %v %v", ok, err)
	}
	indexPass(t, dir)
	write(t, b, claudeLine("s-x", t0, "alpha questions")+claudeLine("s-x", "2026-01-02T03:05:00Z", "bravo follow up"))
	bumpMTime(t, b)
	var progress strings.Builder
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if v := sessionView(t, dir, "claude", "s-x"); !strings.Contains(v, "toolneedle") || !strings.Contains(v, "bravo follow up") {
		t.Errorf("the rewrite read the written-back copy and dropped its tool output:\n%s\nprogress: %s", v, progress.String())
	}
}

// The control: the same passes over distinct sessions match too, and the
// grown file is still appended from its offset rather than read again.
func TestPassesOverDistinctSessionsMatchRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	dir := filepath.Join(tmp, "idx")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "alpha one")+claudeLine("s-b", "2026-01-02T03:04:06Z", "beta cache"))
	indexPass(t, dir)
	appendTo(t, a, claudeLine("s-a", "2026-01-02T04:00:00Z", "alpha three"))
	indexPass(t, dir)
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "alpha one")+claudeLine("s-b", "2026-01-02T03:04:06Z", "beta queue"))
	bumpMTime(t, b)
	var progress strings.Builder
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, replacementPassMarker+" 1 changed transcript") {
		t.Errorf("a rewrite of a session no other file holds read more than its own file: %q", got)
	}
	sessionMatchesRebuild(t, dir, "claude", "s-a")
	sessionMatchesRebuild(t, dir, "claude", "s-b")
}

// Random passes over a few transcripts that share ids: each pass grows,
// rewrites, adds or deletes files, and the index after it holds what a
// rebuild of the same files holds.
func TestRandomPassesOverSharedSessionsMatchRebuild(t *testing.T) {
	seeds := 6
	if testing.Short() {
		seeds = 2
	}
	for seed := range seeds {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed))) //nolint:gosec // a seeded fixture, not crypto
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			dir := filepath.Join(tmp, "idx")
			ids := []string{"s-a", "s-b", "s-c"}
			words := []string{"alpha", "bravo", "cache", "delta", "queue", "retry", "webhook", "lockfile"}
			base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
			// A Claude transcript is one session, named by its last line;
			// an append can change the id it carries from then on (#5379).
			// Its turns run forward in time.
			// They come from a small pool so files repeat each other's turns,
			// times included, the way a fork or a resume copies them.
			files := []string{"f0.jsonl", "f1.jsonl", "f2.jsonl", "f3.jsonl"}
			ofFile := make([]string, len(files))
			last := make([]int, len(files))
			line := func(i int) string {
				n := min(last[i]+rng.Intn(3), 7)
				last[i] = n
				return claudeLine(ofFile[i], base.Add(time.Duration(n)*time.Minute).Format(time.RFC3339),
					words[n]+" "+words[(n+3)%len(words)])
			}
			path := func(i int) string { return filepath.Join(claude, "p", files[i]) }
			rewrite := func(i int) {
				ofFile[i], last[i] = ids[rng.Intn(len(ids))], rng.Intn(3)
				var s strings.Builder
				for range 1 + rng.Intn(3) {
					s.WriteString(line(i))
				}
				write(t, path(i), s.String())
			}
			for i := range 2 {
				rewrite(i)
			}
			indexPass(t, dir)
			for pass := range 6 {
				for range 1 + rng.Intn(2) {
					i := rng.Intn(len(files))
					p := path(i)
					_, err := os.Stat(p)
					op := rng.Intn(5)
					t.Logf("pass %d: %s op %d (exists %v)", pass, files[i], op, err == nil)
					switch {
					case err != nil:
						rewrite(i)
					case op == 0 || op == 1:
						appendTo(t, p, line(i))
					case op == 2:
						rewrite(i)
						bumpMTime(t, p)
					case op == 3:
						ofFile[i] = ids[rng.Intn(len(ids))]
						appendTo(t, p, line(i))
					default:
						// Deleted: the index keeps what it read, as a
						// rebuild does from the kept records.
						_ = os.Remove(p)
					}
				}
				indexPass(t, dir)
				// Rebuilt from a copy, so the deleted files' kept records are
				// what that rebuild carries too.
				fresh := filepath.Join(t.TempDir(), "rebuilt")
				if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
					t.Fatal(err)
				}
				if err := Ensure(fresh, "", true, nil); err != nil {
					t.Fatal(err)
				}
				for _, id := range ids {
					got, want := sortedLines(sessionView(t, dir, "claude", id)+rowView(t, dir, "claude", id)),
						sortedLines(sessionView(t, fresh, "claude", id)+rowView(t, fresh, "claude", id))
					if got != want {
						t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
					}
				}
				if t.Failed() {
					for i := range files {
						b, _ := os.ReadFile(path(i))
						t.Logf("%s:\n%s", files[i], b)
					}
					t.Fatalf("diverged after pass %d", pass)
				}
			}
		})
	}
}

func sortedLines(v string) string {
	lines := strings.Split(v, "\n")
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// rowView is the rest of a session's row that sessionView leaves out: the
// file that owns it and the span of the files that share it (#5380).
func rowView(t *testing.T, dir, harness, id string) string {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, m := range metas {
		if m.Harness == harness && m.ID == id {
			fmt.Fprintf(&b, "path=%s shared=%v shared_started=%s shared_updated=%s\n", m.Path, m.Shared,
				m.SharedStarted.UTC().Format(time.RFC3339), m.SharedUpdated.UTC().Format(time.RFC3339))
		}
	}
	return b.String()
}

// The control for the id-change cases: a tail that keeps the file's id is
// still appended from its offset, and only one that changes it re-reads the
// file (#5379).
func TestOnlyATailThatChangesTheIdRereadsItsFile(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	a := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "a.jsonl")
	dir := filepath.Join(tmp, "idx")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	indexPass(t, dir)
	for _, step := range []struct {
		sid     string
		rereads bool
	}{{"s-a", false}, {"s-b", true}, {"s-b", false}} {
		appendTo(t, a, claudeLine(step.sid, "2026-01-02T04:00:00Z", "more from "+step.sid))
		var progress strings.Builder
		if err := Ensure(dir, "", false, &progress); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(progress.String(), replacementPassMarker); got != step.rereads {
			t.Errorf("a %s tail re-read the file: %v, want %v (%q)", step.sid, got, step.rereads, progress.String())
		}
	}
	sessionMatchesRebuild(t, dir, "claude", "s-b")
	sessionMatchesRebuild(t, dir, "claude", "s-a")
}

// A transcript moved in one pass, its new copy holding an earlier turn too so
// the walk cannot pair the two files by their bytes, is the move the rename
// rule pairs by content: the row names the new file and is not shared with
// the one that went (#1086). A deleted transcript sends the pass to that rule
// (#5380).
func TestATranscriptMovedWithAnEarlierTurnIsNotSharedWithItsOldPath(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	dir := filepath.Join(tmp, "idx")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	write(t, b, claudeLine("s-a", "2026-01-02T03:00:00Z", "zero first")+claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	indexPass(t, dir)
	if got := rowView(t, dir, "claude", "s-a"); !strings.Contains(got, "path="+b+" shared=false") {
		t.Errorf("the moved transcript's row: %s", got)
	}
	sessionMatchesRebuild(t, dir, "claude", "s-a")
}

// A deleted owner whose copy beside it holds its last turn is kept, not taken
// for a move to the copy: the copy was there before. Its records stay under
// it (#5380).
func TestADeletedOwnerIsNotTakenForAMoveToItsCopy(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	dir := filepath.Join(tmp, "idx")
	write(t, a, claudeLine("s-x", "2026-01-02T03:04:05Z", "zebra unique turn"))
	indexPass(t, dir)
	write(t, b, claudeLine("s-x", "2026-01-02T03:04:05Z", "zebra unique turn")+claudeLine("s-x", "2026-01-02T03:05:00Z", "bravo more"))
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := m.Files[a]; !ok || !f.Kept {
		t.Errorf("the deleted owner is not kept: %+v %v", f, ok)
	}
	write(t, b, claudeLine("s-x", "2026-01-02T03:05:00Z", "bravo rewritten"))
	bumpMTime(t, b)
	indexPass(t, dir)
	if v := sessionView(t, dir, "claude", "s-x"); !strings.Contains(v, "zebra unique turn") {
		t.Errorf("the deleted transcript's turn went with its copy's rewrite:\n%s", v)
	}
	fresh := filepath.Join(t.TempDir(), "rebuilt")
	if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := sortedLines(sessionView(t, dir, "claude", "s-x") + rowView(t, dir, "claude", "s-x"))
	if want := sortedLines(sessionView(t, fresh, "claude", "s-x") + rowView(t, fresh, "claude", "s-x")); got != want {
		t.Errorf("after the passes:\n%s\nafter a rebuild:\n%s", got, want)
	}
}
