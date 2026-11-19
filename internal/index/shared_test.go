package index

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// HarnessSharedCounts is what lets doctor tell a parse failure from an id
// collision (#1101); it had no test of its own, and the package floor noticed.
func TestHarnessSharedCounts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	store := t.TempDir()
	// Two transcripts writing one id: the manifest keeps a single row for them
	// and marks it shared.
	for i, name := range []string{"a.jsonl", "b.jsonl"} {
		rec := `{"type":"user","message":{"role":"user","content":"conversation ` + name + `"},"timestamp":"2026-08-0` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"dup1","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", store)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	shared := HarnessSharedCounts(dir)
	if shared["claude"] != 1 {
		t.Errorf("shared rows for claude = %d, want 1 (two transcripts, one id)", shared["claude"])
	}
	if n, err := SessionCount(dir); err != nil || n != 1 {
		t.Errorf("session count = %d (err %v), want 1", n, err)
	}
	if counts := HarnessSessionCounts(dir); counts["claude"] != 1 {
		t.Errorf("per-harness count = %d, want 1", counts["claude"])
	}
	if imported := ImportedSessionCounts(dir); imported["claude"] != 0 {
		t.Errorf("nothing was imported, got %d", imported["claude"])
	}
	// A directory with no manifest answers empty rather than panicking.
	if got := HarnessSharedCounts(filepath.Join(t.TempDir(), "none")); len(got) != 0 {
		t.Errorf("a missing manifest returned %v", got)
	}
}

// Two transcripts with one id in two projects (#699): a turn both hold is
// written once, under whichever copy was read first. Removing that copy's
// project took every record from its path, the surviving copy's commands and
// outputs with them, and left the row on the deleted file until a rebuild
// (#4310).
func TestRemovingOneCopyOfASharedIDKeepsWhatTheOtherHolds(t *testing.T) {
	c, tmp := newCFStore(t)
	for i := range 6 {
		c.write("mmm", i, fmt.Sprintf("fix the store test %d", i))
	}
	c.write("aaa", 0, "the same id in another project")
	c.appendTurn("aaa", 0, 30, "cargo build", "error[E0425]: cannot find value `x` in this scope")
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(c.path("aaa", 0))); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	key := "claude:" + c.sid(0)
	held := func(dir string) (string, map[string]int) {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		recs, err := ReadRecords(dir)
		if err != nil {
			t.Fatal(err)
		}
		by := map[string]int{}
		for _, r := range recs {
			if r.Record.Key == key {
				by[filepath.Base(filepath.Dir(r.Record.SourcePath))+" "+r.Record.Role]++
			}
		}
		return m.Sessions[key].Path, by
	}
	fresh := filepath.Join(t.TempDir(), "index.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	gotPath, got := held(dir)
	wantPath, want := held(fresh)
	if gotPath != wantPath || !reflect.DeepEqual(got, want) {
		t.Errorf("after the update: path %s, records %v\na full build:      path %s, records %v", gotPath, got, wantPath, want)
	}
	if a, b := ReadCommandFails(dir), ReadCommandFails(fresh); !reflect.DeepEqual(a, b) {
		t.Errorf("failure table after the update\n%+v\nfull build\n%+v", a, b)
	}
}

// A renamed copy that still sorts after its owner is followed by name: a
// rebuild reads the session's files in the same order, so nothing is read
// again. Reading it took the replacing path, 20 s for 20 renames at 40,000
// sessions (#5466).
func TestARenameThatKeepsTheOrderIsNotReadAgain(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)

	c := filepath.Join(filepath.Dir(b), "c.jsonl")
	if err := os.Rename(b, c); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if lastIngestFiles.Load() != 0 {
		t.Errorf("a copy renamed in place behind its owner read %d files again, want none", lastIngestFiles.Load())
	}
	keptMatchesRebuild(t, dir, "s-x")

	// The control: the same copy renamed ahead of its owner takes the row,
	// and is read again for it.
	if err := os.Rename(c, filepath.Join(filepath.Dir(c), "0.jsonl")); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if lastIngestFiles.Load() == 0 {
		t.Errorf("a copy renamed ahead of its owner was followed by name alone")
	}
	keptMatchesRebuild(t, dir, "s-x")
}

func TestRenameReorders(t *testing.T) {
	for _, tc := range []struct {
		files   []string
		renamed map[string]string
		want    bool
	}{
		{[]string{"a", "c"}, map[string]string{"c": "b"}, false},
		{[]string{"0", "a"}, map[string]string{"0": "b"}, true},
		{[]string{"c", "d"}, map[string]string{"c": "a", "d": "b"}, false},
		{[]string{"c", "d"}, map[string]string{"c": "b", "d": "a"}, true},
		{[]string{"a"}, map[string]string{"a": "z"}, false},
	} {
		if got := renameReorders(tc.files, tc.renamed); got != tc.want {
			t.Errorf("renameReorders(%v, %v) = %v, want %v", tc.files, tc.renamed, got, tc.want)
		}
	}
}

// Random passes over copies of one session spread over several project
// directories, with deletes, renames and rewrites, each compared to a rebuild:
// the records, the row, the command tables and the session facts. A deleted
// copy keeps every turn it held. CROSS_DIR_PARITY_SEEDS=from:to sweeps more
// seeds. The fixes are compared too once a pass mines them as a rebuild does
// (#5504): add "fixes" to the keys below.
func TestRandomPassesOverCopiesInSeveralProjectsMatchRebuild(t *testing.T) {
	from, to := 0, 3
	if testing.Short() {
		to = 2
	}
	if v := os.Getenv("CROSS_DIR_PARITY_SEEDS"); v != "" {
		a, b, _ := strings.Cut(v, ":")
		from, _ = strconv.Atoi(a)
		to, _ = strconv.Atoi(b)
	}
	for seed := from; seed < to; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) { crossDirRun(t, int64(seed)) })
	}
}

func crossDirRun(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // a seeded fixture, not crypto
	tmp := hermeticIndexEnv(t)
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(tmp, "idx")
	ids := []string{"s-a", "s-b"}
	words := []string{"alpha", "bravo", "cache", "delta", "queue", "retry", "webhook", "lockfile"}
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	// A Claude transcript is named for its session, so two projects share a
	// session by holding a file of the same name (#699).
	files := []string{"-tmp-app/s-a.jsonl", "-tmp-app/s-b.jsonl", "-tmp-infra/s-a.jsonl", "-tmp-infra/s-b.jsonl", "-tmp-ops/s-a.jsonl"}
	ofFile := make([]string, len(files))
	for i, f := range files {
		ofFile[i] = strings.TrimSuffix(filepath.Base(f), ".jsonl")
	}
	last := make([]int, len(files))
	// One turn: a prompt, a command and its output, the same in every copy
	// that holds turn n, so copies share it.
	turn := func(i int) string {
		n := min(last[i]+rng.Intn(3), 7)
		last[i] = n
		id, at := ofFile[i], base.Add(time.Duration(n)*time.Minute)
		cmd := "make zz" + words[n]
		out := "ok"
		if n%2 == 0 {
			out = "make: *** [zz" + words[n] + "] Error 2"
		}
		line := func(off time.Duration, role, content string) string {
			return fmt.Sprintf(`{"type":%q,"sessionId":%q,"timestamp":%q,"cwd":"/tmp/%s","message":{"role":%q,"content":%s}}`,
				role, id, at.Add(off).Format(time.RFC3339), words[n], role, content) + "\n"
		}
		tid := fmt.Sprintf("t%d", n)
		return line(0, "user", strconv.Quote(words[n]+" "+words[(n+3)%len(words)])) +
			line(time.Second, "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"`+cmd+`"}}]`) +
			line(2*time.Second, "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"`+out+`"}]`)
	}
	path := func(i int) string { return filepath.Join(claude, filepath.FromSlash(files[i])) }
	rewrite := func(i int) {
		last[i] = rng.Intn(3)
		var s strings.Builder
		for range 1 + rng.Intn(3) {
			s.WriteString(turn(i))
		}
		write(t, path(i), s.String())
	}
	view := func(dir, id string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id))
	}
	tables := func(dir string) map[string]string {
		return map[string]string{
			"commands": fmt.Sprint(ReadCommands(dir)),
			"fails":    fmt.Sprint(ReadCommandFails(dir)),
			"facts":    fmt.Sprint(ReadSessionFacts(dir)),
			"fixes":    fmt.Sprint(ReadFixes(dir)),
		}
	}
	// The prompts each deleted file held, which its session keeps: by path,
	// until a file is written there again.
	gone, seen := map[string][]string{}, map[string][]string{}
	snapshot := func() {
		clear(seen)
		for i, f := range files {
			if b, err := os.ReadFile(path(i)); err == nil {
				seen[f] = crossDirPrompts(t, string(b))
			}
		}
	}
	rewrite(0)
	rewrite(2)
	indexPass(t, dir)
	snapshot()
	for pass := range 8 {
		for range 1 + rng.Intn(3) {
			i := rng.Intn(len(files))
			p := path(i)
			_, err := os.Stat(p)
			op := rng.Intn(6)
			t.Logf("pass %d: %s op %d (exists %v)", pass, files[i], op, err == nil)
			switch {
			case err != nil:
				delete(gone, files[i])
				rewrite(i)
			case op <= 1 || op == 3:
				appendTo(t, p, turn(i))
			case op == 2:
				rewrite(i)
				bumpMTime(t, p)
			case op == 4:
				// To another project, under its name, the way a client moves
				// a transcript when its project moves.
				j := rng.Intn(len(files))
				if _, err := os.Stat(path(j)); err == nil || ofFile[j] != ofFile[i] {
					break
				}
				t.Logf("pass %d: %s renamed to %s", pass, files[i], files[j])
				delete(gone, files[j])
				if err := os.MkdirAll(filepath.Dir(path(j)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(p, path(j)); err != nil {
					t.Fatal(err)
				}
				last[j] = last[i]
			default:
				if prompts := seen[files[i]]; len(prompts) > 0 {
					gone[files[i]] = prompts
				}
				_ = os.Remove(p)
			}
		}
		// A new file holding every prompt of a deleted one under its id is
		// that file moved, and the index follows it.
		for f, held := range gone {
			for k, g := range files {
				b, err := os.ReadFile(path(k))
				if _, before := seen[g]; err != nil || before {
					continue
				}
				now := crossDirPrompts(t, string(b))
				if crossDirID(held) == crossDirID(now) && crossDirHolds(now, held) {
					delete(gone, f)
				}
			}
		}
		indexPass(t, dir)
		fresh := filepath.Join(t.TempDir(), "rebuilt")
		if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(fresh, "", true, nil); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if got, want := view(dir, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
			}
		}
		got, want := tables(dir), tables(fresh)
		for _, k := range []string{"commands", "fails", "facts"} {
			if got[k] != want[k] {
				t.Errorf("%s after incremental passes:\n%s\nafter a rebuild:\n%s", k, got[k], want[k])
			}
		}
		snapshot()
		for f, held := range gone {
			id := crossDirID(held)
			v := sessionView(t, dir, "claude", id)
			for _, h := range held {
				if _, turn, _ := strings.Cut(h, " "); !strings.Contains(v, turn+"\n") {
					t.Errorf("claude:%s lost %q of deleted %s", id, turn, f)
				}
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
}

// crossDirPrompts is each prompt line of a transcript as "id turn", the turn
// as sessionView prints it.
func crossDirPrompts(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for l := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		var w struct {
			SessionID string `json:"sessionId"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(l), &w); err != nil {
			t.Fatal(err)
		}
		var text string
		if json.Unmarshal(w.Message.Content, &text) == nil {
			out = append(out, w.SessionID+" user|"+w.Timestamp+"|"+text)
		}
	}
	return out
}

// crossDirID is the session a file is: its last line's.
func crossDirID(prompts []string) string {
	if len(prompts) == 0 {
		return ""
	}
	id, _, _ := strings.Cut(prompts[len(prompts)-1], " ")
	return id
}

func crossDirHolds(have, want []string) bool {
	in := map[string]bool{}
	for _, h := range have {
		_, turn, _ := strings.Cut(h, " ")
		in[turn] = true
	}
	for _, w := range want {
		if _, turn, _ := strings.Cut(w, " "); !in[turn] {
			return false
		}
	}
	return true
}

// sharedPair writes a and b under one project and returns their paths.
func sharedPair(t *testing.T, bodyA, bodyB string) (string, string) {
	t.Helper()
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a, b := filepath.Join(c, "p", "a.jsonl"), filepath.Join(c, "p", "b.jsonl")
	write(t, a, bodyA)
	write(t, b, bodyB)
	return a, b
}

// keptMatchesRebuild compares the row against a rebuild of a copy of dir, which
// carries the deleted files' records as a rebuild of dir would.
func keptMatchesRebuild(t *testing.T, dir, id string) { //nolint:unparam // the call names the session it compares
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuilt")
	if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	view := func(dir string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id) + copiesView(t, dir, "claude", id))
	}
	if got, want := view(dir), view(fresh); got != want {
		t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
	}
}

func holdsTurn(t *testing.T, dir, id, text string) bool { //nolint:unparam // the call names the session it reads
	t.Helper()
	return strings.Contains(sessionView(t, dir, "claude", id), "|"+text+"\n")
}

// A deleted transcript that shares its session with another, and does not own
// the row, went with its records: the turns only it held left the index.
func TestADeletedCopyKeepsTheTurnsOnlyItHeld(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if !holdsTurn(t, dir, "s-x", "bravo webhook") {
		t.Errorf("the deleted copy's own turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	keptMatchesRebuild(t, dir, "s-x")
}

// A deleted transcript's turns stay with the session when the copy on disk
// that shared them is rewritten without them: under that copy, through a
// rebuild or from the start, they went with the rewrite.
func TestARewrittenCopyLeavesADeletedTranscriptsTurns(t *testing.T) {
	body := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile") + claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	for _, tc := range []struct {
		name    string
		deleted int // 0 deletes a, the owner; 1 deletes b
		rebuild bool
	}{
		{"owner deleted, then a rebuild", 0, true},
		{"copy deleted", 1, false},
		{"copy deleted, then a rebuild", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			a, b := sharedPair(t, body, body)
			gone, live := a, b
			if tc.deleted == 1 {
				gone, live = b, a
			}
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			if err := os.Remove(gone); err != nil {
				t.Fatal(err)
			}
			indexPass(t, dir)
			if tc.rebuild {
				if err := Ensure(dir, "", true, nil); err != nil {
					t.Fatal(err)
				}
			}
			write(t, live, claudeLine("s-y", "2026-01-02T05:00:00Z", "cache queue"))
			bumpMTime(t, live)
			indexPass(t, dir)
			for _, turn := range []string{"alpha lockfile", "bravo webhook"} {
				if !holdsTurn(t, dir, "s-x", turn) {
					t.Errorf("%q of the deleted transcript left the index:\n%s", turn, sessionView(t, dir, "claude", "s-x"))
				}
			}
			keptMatchesRebuild(t, dir, "s-x")
		})
	}
}

// A transcript that now holds another session gives up the row of the one it
// held, as a rebuild does. Beside a deleted copy's records it kept the row and
// the title of a turn no longer in the session.
func TestAFileThatLeftTheSessionGivesUpItsRow(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a, b, cc := filepath.Join(c, "p", "a.jsonl"), filepath.Join(c, "p", "b.jsonl"), filepath.Join(c, "p", "c.jsonl")
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	write(t, b, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	write(t, a, claudeLine("s-x", "2026-01-02T02:59:00Z", "queue first")+t0)
	write(t, cc, t0+claudeLine("s-x", "2026-01-02T03:02:00Z", "cache retry"))
	indexPass(t, dir)
	write(t, a, claudeLine("s-y", "2026-01-02T05:00:00Z", "delta other"))
	bumpMTime(t, a)
	appendTo(t, cc, claudeLine("s-x", "2026-01-02T03:03:00Z", "retry again"))
	indexPass(t, dir)
	keptMatchesRebuild(t, dir, "s-x")
}

// A renamed transcript that shares its session is not followed by name alone:
// the row goes to the smallest path, as a rebuild gives it.
func TestARenamedCopyOwnsTheRowARebuildGivesIt(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	a, _ := sharedPair(t, claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile"),
		claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Rename(a, filepath.Join(filepath.Dir(a), "c.jsonl")); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	keptMatchesRebuild(t, dir, "s-x")
}

// A new file under a deleted transcript's id that holds only its last turn is
// a copy, not the transcript moved: the turns only the deleted one held stay.
func TestADeletedTranscriptIsNotMovedToACopyOfItsLastTurn(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(c, "p", "a.jsonl")
	t1 := claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	write(t, a, claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")+t1)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c, "p", "b.jsonl"), t1)
	indexPass(t, dir)
	if !holdsTurn(t, dir, "s-x", "alpha lockfile") {
		t.Errorf("the deleted transcript's first turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}

	// The control: a new file holding every turn, grown on the way, is the
	// transcript moved, and the old path goes.
	q := filepath.Join(c, "q", "a.jsonl")
	body := claudeLine("s-z", "2026-01-02T04:00:00Z", "cache queue") + claudeLine("s-z", "2026-01-02T04:01:00Z", "delta retry")
	write(t, q, body)
	indexPass(t, dir)
	if err := os.Remove(q); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c, "q", "moved.jsonl"), body+claudeLine("s-z", "2026-01-02T04:02:00Z", "queue webhook"))
	indexPass(t, dir)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[q]; ok {
		t.Errorf("a moved transcript kept its old path in the index")
	}
}

// Shared turns a rebuild files under the deleted transcript keep their place
// in the session: skipped in the live copy and written after it, a session
// read back its first turns last.
func TestARebuildKeepsTheOrderOfTurnsADeletedCopyShared(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile") + claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	a, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:02:00Z", "cache queue"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	appendTo(t, b, claudeLine("s-x", "2026-01-02T03:03:00Z", "delta retry"))
	indexPass(t, dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := FindByIdentity(dir, "claude", "s-x")
	if err != nil || !ok {
		t.Fatalf("s-x not found: %v", err)
	}
	got := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		got = append(got, m.Text)
	}
	if want := "alpha lockfile|bravo webhook|cache queue|delta retry"; strings.Join(got, "|") != want {
		t.Errorf("turns after a rebuild: %v, want %s", got, want)
	}
}

// A turn written after the deleted copy was last written was never in it, so
// a rewind of the live copy takes it out of the index, deleted copy or not.
func TestARewoundTurnIsNotKeptForADeletedCopy(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	a, b := sharedPair(t, t0, t0)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	write(t, a, t0+claudeLine("s-x", later, "wrong secretword"))
	bumpMTime(t, a)
	indexPass(t, dir)
	write(t, a, t0+claudeLine("s-x", later, "right turn"))
	bumpMTime(t, a)
	indexPass(t, dir)
	if holdsTurn(t, dir, "s-x", "wrong secretword") {
		t.Errorf("a rewound turn stayed under the deleted copy:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	if !holdsTurn(t, dir, "s-x", "alpha lockfile") {
		t.Errorf("the shared turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	keptMatchesRebuild(t, dir, "s-x")
}

// A forgotten session's deleted copies go with it, from the index forget
// writes and from a rebuild: kept, they counted as still searchable.
func TestAForgottenSessionTakesItsDeletedCopiesWithIt(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	// The tombstone forget writes stays with this test.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if _, err := Forget(dir, ForgetOptions{Session: "s-x"}); err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		if err := Ensure(dir, "", rebuild, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := m.Files[b]; ok && f.Kept {
			t.Errorf("rebuild=%v: the forgotten session's deleted copy is still kept", rebuild)
		}
	}
}

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

// The wider run of TestRandomPassesOverSharedSessionsMatchRebuild: more
// files, more passes, renames, and a rebuild of the rebuild, which has to
// hold what the first rebuild held. SHARED_PARITY_SEEDS=from:to sweeps more seeds.
func TestWideRandomPassesOverSharedSessionsMatchRebuild(t *testing.T) {
	from, to := 0, 8
	if testing.Short() {
		to = 4
	}
	if v := os.Getenv("SHARED_PARITY_SEEDS"); v != "" {
		a, b, _ := strings.Cut(v, ":")
		from, _ = strconv.Atoi(a)
		to, _ = strconv.Atoi(b)
	}
	for seed := from; seed < to; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) { wideSharedRun(t, int64(seed)) })
	}
}

func wideSharedRun(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // a seeded fixture, not crypto
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(tmp, "idx")
	ids := []string{"s-a", "s-b", "s-c"}
	words := []string{"alpha", "bravo", "cache", "delta", "queue", "retry", "webhook", "lockfile"}
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	files := []string{"f0.jsonl", "f1.jsonl", "f2.jsonl", "f3.jsonl", "f4.jsonl", "f5.jsonl"}
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
	view := func(dir, id string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id) + copiesView(t, dir, "claude", id))
	}
	// What each deleted file held, which its session keeps whatever happens
	// to the other files: by path, until a file is written there again.
	gone, seen := map[string][]string{}, map[string][]string{}
	snapshot := func() {
		clear(seen)
		for i, f := range files {
			if b, err := os.ReadFile(path(i)); err == nil {
				seen[f] = strings.Split(strings.TrimSpace(string(b)), "\n")
			}
		}
	}
	forget := func(i int) { delete(gone, files[i]) }
	rebuildOf := func(src string) string {
		fresh := filepath.Join(t.TempDir(), "rebuilt")
		if err := os.CopyFS(fresh, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(fresh, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return fresh
	}
	for i := range 2 {
		rewrite(i)
	}
	indexPass(t, dir)
	snapshot()
	for pass := range 8 {
		for range 1 + rng.Intn(3) {
			i := rng.Intn(len(files))
			p := path(i)
			_, err := os.Stat(p)
			op := rng.Intn(6)
			t.Logf("pass %d: %s op %d (exists %v)", pass, files[i], op, err == nil)
			switch {
			case err != nil:
				forget(i)
				rewrite(i)
			case op == 0 || op == 1:
				appendTo(t, p, line(i))
			case op == 2:
				rewrite(i)
				bumpMTime(t, p)
			case op == 3:
				ofFile[i] = ids[rng.Intn(len(ids))]
				appendTo(t, p, line(i))
			case op == 4:
				// Renamed to a free name, the way a client moves a transcript.
				j := rng.Intn(len(files))
				if _, err := os.Stat(path(j)); err == nil {
					break
				}
				t.Logf("pass %d: %s renamed to %s", pass, files[i], files[j])
				forget(j)
				if err := os.Rename(p, path(j)); err != nil {
					t.Fatal(err)
				}
				ofFile[j], last[j] = ofFile[i], last[i]
			default:
				// What the last pass read of it, if it read it at all.
				if lines := seen[files[i]]; len(lines) > 0 {
					gone[files[i]] = lines
				}
				_ = os.Remove(p)
			}
		}
		// A new file holding every turn of a deleted one under its id is that
		// file moved, whenever it lands, and the index follows it.
		for f := range gone {
			for k, g := range files {
				b, err := os.ReadFile(path(k))
				if _, before := seen[g]; err != nil || before || len(gone[f]) == 0 {
					continue
				}
				now := strings.Split(strings.TrimSpace(string(b)), "\n")
				if lineID(t, now[len(now)-1]) == lineID(t, gone[f][len(gone[f])-1]) && holdsTurns(t, now, gone[f]) {
					delete(gone, f)
				}
			}
		}
		indexPass(t, dir)
		fresh := rebuildOf(dir)
		again := rebuildOf(fresh)
		for _, id := range ids {
			if got, want := view(dir, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
			}
			if got, want := view(again, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after a rebuild of the rebuild:\n%s\nafter one rebuild:\n%s", id, got, want)
			}
		}
		snapshot()
		for f, held := range gone {
			// The file's session is its last line's.
			id := lineID(t, held[len(held)-1])
			v := sessionView(t, dir, "claude", id)
			for _, h := range held {
				if turn := lineTurn(t, h); !strings.Contains(v, turn+"\n") {
					t.Errorf("claude:%s lost %q of deleted %s", id, turn, f)
				}
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
}

// copiesView is the files a session's row names beside its owner, and which
// of them are kept: a deleted copy that drops out of the row can no longer
// keep its turns.
func copiesView(t *testing.T, dir, harness, id string) string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions[harness+":"+id]
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range row.Copies {
		fmt.Fprintf(&b, "copy=%s kept=%v\n", filepath.Base(p), m.Files[p].Kept)
	}
	return b.String()
}

type wideLine struct {
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content string `json:"content"`
	} `json:"message"`
}

func parseWideLine(t *testing.T, l string) wideLine {
	t.Helper()
	var w wideLine
	if err := json.Unmarshal([]byte(l), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func lineID(t *testing.T, l string) string {
	t.Helper()
	return parseWideLine(t, l).SessionID
}

// lineTurn is the line as sessionView prints it.
func lineTurn(t *testing.T, l string) string {
	t.Helper()
	w := parseWideLine(t, l)
	return "user|" + w.Timestamp + "|" + w.Message.Content
}

func holdsTurns(t *testing.T, have, want []string) bool {
	t.Helper()
	in := map[string]bool{}
	for _, l := range have {
		in[lineTurn(t, l)] = true
	}
	for _, l := range want {
		if !in[lineTurn(t, l)] {
			return false
		}
	}
	return true
}
