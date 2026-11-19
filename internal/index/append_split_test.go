package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// One changed file that could not be appended sent every changed file in the
// pass down the replacement path, which parses each from byte zero: a 1.27 GB
// Claude transcript that grew 281 KB was read whole because two other
// transcripts kept their size (#5324). Decided per file now: the grown one is
// appended from its offset, and only the rewritten one is re-read.
//
// The progress lines name the path each file took: the append path says what
// it updated, the replacement path how many transcripts it re-read.
func TestAMixedBatchAppendsWhatItCanAndReplacesTheRest(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	// Same length, different bytes: a real rewrite, which must still be read.
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)

	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("the grown transcript was not appended from its offset: %q", got)
	}
	if !strings.Contains(got, replacementPassMarker+" 1 changed transcript\n") &&
		!strings.Contains(got, replacementPassMarker+" 1 changed transcript,") {
		t.Errorf("the replacement path read more than the rewritten file: %q", got)
	}

	for query, want := range map[string]string{
		"billing webhook retries": "s-grown",
		"exporter retries":        "s-grown",
		"queue warms":             "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	if ss, _ := Search(dir, search.Options{Query: "cache warms", All: true}); len(ss) != 0 {
		t.Errorf("the rewritten file kept its old text: %#v", ss)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the index is not current after the pass")
	}
}

// A file whose mtime moved and whose bytes did not has nothing to read. It is
// recorded with its new state, so it neither forces a re-read nor keeps the
// index stale for the next pass (#5324).
func TestATouchedTranscriptIsRecordedNotReread(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot")+
		claudeLine("s-same", "2026-01-02T03:04:06Z", "token ghp_abcdefghijklmnop0123456789QRSTUV"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := Redactions(dir)
	if err != nil || before.Files[same] == 0 {
		t.Fatalf("control: the planted secret was not counted: %#v %v", before, err)
	}

	bumpMTime(t, same)
	if ok, _ := UpToDate(dir, ""); ok {
		t.Fatal("control: a new mtime should make the index look stale before the pass")
	}
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || strings.Contains(got, "updated") {
		t.Errorf("a touched transcript was read again: %q", got)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the new mtime was not recorded, so every pass sees the file as changed")
	}
	// What the read derived is still the index's: recording the mtime is not
	// a reason to forget the file's redactions.
	if after, redactErr := Redactions(dir); redactErr != nil || after.Files[same] != before.Files[same] {
		t.Errorf("redactions for the touched file: %d, was %d (%v)", after.Files[same], before.Files[same], redactErr)
	}

	// Touched beside a grown file: the pass is a plain append.
	bumpMTime(t, same)
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	progress.Reset()
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || !strings.Contains(got, "updated 1 file") {
		t.Errorf("a touched file sent the batch to replacement: %q", got)
	}
	ss, err := Search(dir, search.Options{Query: "cache warms", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s-same" {
		t.Fatalf("the touched file's session went missing: %#v", ss)
	}
}

// A transcript deleted in the same pass is kept, and the kept mark the first
// half writes into the walk must not reach the second half as a file put back.
func TestAMixedBatchKeepsADeletedTranscript(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	gone := filepath.Join(claude, "p", "gone.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	write(t, gone, claudeLine("s-gone", "2026-01-02T03:04:05Z", "the lockfile drifts nightly"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file") || !strings.Contains(got, "no longer on disk") {
		t.Errorf("progress: %q", got)
	}
	if strings.Contains(got, "skipping") {
		t.Errorf("the second half read the deleted file: %q", got)
	}
	for query, want := range map[string]string{
		"lockfile drifts": "s-gone",
		"billing webhook": "s-grown",
		"queue warms":     "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Files[gone].Kept {
		t.Errorf("the deleted transcript lost its kept mark: %#v", m.Files[gone])
	}
}

func bumpMTime(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	next := fi.ModTime().Add(3 * time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

// Two transcripts that hold one session are reconciled only when they are
// read together. Split, the grown one was appended and the other replaced on
// its own, and the replacement wrote the grown file's turn a second time
// (#5358 is the multi-pass form). Each case ends where a rebuild would.
func TestAMixedBatchSharingASessionMatchesRebuild(t *testing.T) {
	const t0, t1, t2 = "2026-01-02T03:04:05Z", "2026-01-02T03:04:06Z", "2026-01-02T04:00:00Z"
	cases := []struct {
		name, id string
		setup    func(t *testing.T, a, b, c string)
		change   func(t *testing.T, a, b, c string)
	}{
		{
			name: "grown and rewritten",
			id:   "s-x",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
			},
			change: func(t *testing.T, a, b, _ string) {
				t.Helper()
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta queue"))
				bumpMTime(t, b)
			},
		},
		{
			name: "grown and a torn line finished",
			id:   "s-x",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				line := claudeLine("s-x", t1, "beta cache")
				write(t, b, claudeLine("s-x", t0, "alpha one")+line[:len(line)-10])
			},
			change: func(t *testing.T, a, b, _ string) {
				t.Helper()
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
				bumpMTime(t, b)
			},
		},
		{
			name: "new copy beside a rewrite",
			id:   "s-b",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-a", t0, "alpha one"))
				write(t, b, claudeLine("s-b", t0, "bravo two"))
			},
			change: func(t *testing.T, _, b, c string) {
				t.Helper()
				write(t, c, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t2, "charlie four"))
				write(t, b, claudeLine("s-b", t0, "bravo too"))
				bumpMTime(t, b)
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
			tc.setup(t, a, b, c)
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			tc.change(t, a, b, c)
			indexPass(t, dir)
			sessionMatchesRebuild(t, dir, "claude", tc.id)
		})
	}
}

// sessionMatchesRebuild is matchesRebuild with the messages compared as a set: the
// order two files' turns interleave in follows the order they are read, which
// neither path fixes.
func sessionMatchesRebuild(t *testing.T, dir, harness, id string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuild.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	sorted := func(v string) string {
		lines := strings.Split(v, "\n")
		slices.Sort(lines)
		return strings.Join(lines, "\n")
	}
	got, want := sessionView(t, dir, harness, id)+rowView(t, dir, harness, id), sessionView(t, fresh, harness, id)+rowView(t, fresh, harness, id)
	if sorted(got) != sorted(want) {
		t.Errorf("%s:%s after incremental passes:\n%s\nafter a rebuild:\n%s", harness, id, got, want)
	}
}

// The control: distinct sessions still split, and still match a rebuild.
func TestAMixedBatchOfDistinctSessionsSplitsAndMatchesRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta cache"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	appendTo(t, a, claudeLine("s-a", "2026-01-02T04:00:00Z", "alpha three"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta queue"))
	bumpMTime(t, b)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("distinct sessions no longer split: %q", got)
	}
	sessionMatchesRebuild(t, dir, "claude", "s-a")
	sessionMatchesRebuild(t, dir, "claude", "s-b")
}

// A new transcript beside a torn tail must not make a large, grown transcript
// dead weight in records.bin (#6077).
func TestATornTailAndNewFileStillAppendTheGrownTranscript(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	big := filepath.Join(claude, "p", "big.jsonl")
	torn := filepath.Join(claude, "p", "torn.jsonl")
	var body strings.Builder
	for i := range 200 {
		body.WriteString(claudeLine("s-big", fmt.Sprintf("2026-01-02T03:%02d:%02dZ", i/60, i%60), fmt.Sprintf("turn %d about the exporter retry loop", i)))
	}
	write(t, big, body.String())
	full := claudeLine("s-torn", "2026-01-02T03:04:05Z", "the cache warms on boot")
	write(t, torn, claudeLine("s-torn", "2026-01-02T03:04:00Z", "first turn")+full[:len(full)/2])
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := before.Files[torn]; f.SafeSize >= f.Size || before.DeadBytes != 0 {
		t.Fatalf("control: torn file = %+v, dead bytes = %d", f, before.DeadBytes)
	}
	var bigOffsets []int64
	tbl := tablesFromManifest(before)
	if err = eachStoredRecord(filepath.Join(dir, "records.bin"), tbl, func(off int64, payload []byte) error {
		r, decErr := decodeRecord(payload, tbl)
		if decErr == nil && r.SourcePath == big {
			bigOffsets = append(bigOffsets, off)
		}
		return decErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(bigOffsets) != 200 {
		t.Fatalf("control: big transcript has %d records, want 200", len(bigOffsets))
	}

	appendTo(t, torn, full[len(full)/2:])
	appendTo(t, big, claudeLine("s-big", "2026-01-02T05:00:00Z", "one new turn on the big session"))
	write(t, filepath.Join(claude, "p", "new.jsonl"), claudeLine("s-new", "2026-01-02T05:00:01Z", "a new session starts"))
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("the 200-turn transcript was not appended: %q", got)
	}
	if !strings.Contains(got, replacementPassMarker+" 2 changed transcripts") {
		t.Errorf("replacement should read only the torn and new files: %q", got)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Only the torn file's first turn may be dead; none of big's 200 turns.
	if len(after.DeadRecords) != 1 {
		t.Errorf("dead records = %d, want only the torn file's old turn (dead bytes %d)", len(after.DeadRecords), after.DeadBytes)
	}
	for _, off := range bigOffsets {
		if slices.Contains(after.DeadRecords, off) {
			t.Fatalf("the 200-turn transcript has a dead record at %d", off)
		}
	}
	t.Logf("progress: %q; dead bytes %d -> %d, none from the 200-turn transcript", got, before.DeadBytes, after.DeadBytes)
	sessionMatchesRebuild(t, dir, "claude", "s-big")
	sessionMatchesRebuild(t, dir, "claude", "s-torn")
	sessionMatchesRebuild(t, dir, "claude", "s-new")
}

func TestANewFileSharingAnAppendedSessionKeepsTheBatchWhole(t *testing.T) {
	for _, tc := range []struct {
		name, initial, id string
	}{
		{"existing ID", claudeLine("s-grown", "2026-01-02T03:04:05Z", "alpha one"), "s-grown"},
		// No earlier user/assistant line names this file's session, so the
		// first such line can introduce an ID without preventing an append.
		{"introduced tail ID", "{\"type\":\"summary\",\"summary\":\"waiting for the first prompt\"}\n", "s-tail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			grown := filepath.Join(claude, "p", "grown.jsonl")
			rewritten := filepath.Join(claude, "p", "rewritten.jsonl")
			write(t, grown, tc.initial)
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta cache"))
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			before, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == "s-tail" {
				if _, ok := before.Sessions["claude:"+tc.id]; ok {
					t.Fatal("control: tail ID already has a session row")
				}
				if _, ok := before.Empty["claude:"+tc.id]; ok {
					t.Fatal("control: tail ID already has an empty row")
				}
			}
			line := claudeLine(tc.id, "2026-01-02T04:00:00Z", "alpha three")
			appendTo(t, grown, line)
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta queue"))
			bumpMTime(t, rewritten)
			write(t, filepath.Join(claude, "p", "new.jsonl"), line+claudeLine(tc.id, "2026-01-02T05:00:00Z", "charlie four"))
			changed, _, _ := diffWalk(currentFiles(""), before.Files)
			appendable, rest := splitAppendable(changed, before.Files, before.Sessions)
			if _, ok := appendable[grown]; !ok || len(appendable) != 1 || len(rest) != 2 {
				t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
			}
			var progress bytes.Buffer
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			got := progress.String()
			if strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
				t.Errorf("overlapping sessions must be replaced together: %q", got)
			}
			t.Logf("overlap guard: 1 append candidate, 2 rest files; progress: %q", got)
			sessionMatchesRebuild(t, dir, "claude", tc.id)
			sessionMatchesRebuild(t, dir, "claude", "s-other")
		})
	}
}

// A known file's torn first line has no row or Copies entry yet. Completing
// it can reveal the same session as an append, just as a new file can.
func TestAKnownFileWithoutARowSharingAnAppendKeepsTheBatchWhole(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "z", "s.jsonl")
	torn := filepath.Join(claude, "a", "s.jsonl")
	const command = "go test ./zzsplit"
	full := `{"type":"assistant","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"Bash","input":{"command":"` + command + `"}}]}}` + "\n"
	write(t, grown, full)
	write(t, torn, full[:len(full)/2])
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f, known := before.Files[torn]; !known || f.SafeSize >= f.Size {
		t.Fatalf("control: torn file must be known and incomplete: %+v, known=%t", f, known)
	}
	for _, rows := range []map[string]SessionMeta{before.Sessions, before.Empty} {
		for _, row := range rows {
			if slices.Contains(sessionFiles(row), torn) {
				t.Fatalf("control: a session row already names the torn file: %+v", row)
			}
		}
	}
	if row := before.Sessions["claude:s"]; row.Path != grown || len(row.Copies) != 0 || !row.RanCommand {
		t.Fatalf("control: command must belong to the grown file alone: %+v", row)
	}

	appendTo(t, grown, claudeLine("s", "2026-01-02T04:00:00Z", "alpha three"))
	appendTo(t, torn, full[len(full)/2:])
	// A second session running the command makes it visible in ReadCommands,
	// which lists only commands run in at least two distinct sessions.
	write(t, filepath.Join(claude, "n", "new.jsonl"), strings.Replace(full, `"sessionId":"s"`, `"sessionId":"s-new"`, 1))
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("overlapping sessions must be replaced together: %q", got)
	}
	if !commandsHold(dir, command) {
		t.Error("the command disappeared after completing the known torn file")
	}
	sessionMatchesRebuild(t, dir, "claude", "s")
	sessionMatchesRebuild(t, dir, "claude", "s-new")
	sameCommandTables(t, dir)
}

// A row names the known rest file's old session, but its appended lines can
// move the whole file to a session the append half touches.
func TestAKnownFileChangingSessionToAnAppendKeepsTheBatchWhole(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "z", "s.jsonl")
	changed := filepath.Join(claude, "a", "old.jsonl")
	const command = "go test ./zzsplit"
	full := `{"type":"assistant","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"Bash","input":{"command":"` + command + `"}}]}}` + "\n"
	write(t, grown, full)
	write(t, changed, claudeLine("old", "2026-01-02T03:04:00Z", "first turn"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if row := before.Sessions["claude:old"]; row.Path != changed || len(row.Copies) != 0 {
		t.Fatalf("control: a row must name the known rest file: %+v", row)
	}
	if row := before.Sessions["claude:s"]; row.Path != grown || len(row.Copies) != 0 || !row.RanCommand {
		t.Fatalf("control: command must belong to the grown file alone: %+v", row)
	}

	appendTo(t, grown, claudeLine("s", "2026-01-02T04:00:00Z", "alpha three"))
	appendTo(t, changed, full)
	// ReadCommands lists commands run in at least two distinct sessions.
	write(t, filepath.Join(claude, "n", "new.jsonl"), strings.Replace(full, `"sessionId":"s"`, `"sessionId":"new"`, 1))
	files, _, _ := diffWalk(currentFiles(""), before.Files)
	appendable, rest := splitAppendable(files, before.Files, before.Sessions)
	if _, ok := appendable[grown]; !ok || len(appendable) != 1 || len(rest) != 2 {
		t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
	}
	if _, ok := rest[changed]; !ok {
		t.Fatal("control: the changed session ID must send the known file to replacement")
	}
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("overlapping sessions must be replaced together: %q", got)
	}
	if !commandsHold(dir, command) {
		t.Error("the command disappeared after the known file changed session ID")
	}
	for _, u := range ReadCommands(dir) {
		if strings.TrimPrefix(u.Command, "$ ") == command && u.Sessions != 2 {
			t.Errorf("command has %d sessions, want 2", u.Sessions)
		}
	}
	for _, id := range []string{"old", "s", "new"} {
		sessionMatchesRebuild(t, dir, "claude", id)
	}
	sameCommandTables(t, dir)
}

func TestSplitSessionKeysMustBeDisjoint(t *testing.T) {
	s := model.Session{Harness: "codex", ID: "s"}
	for _, tc := range []struct {
		name               string
		rows, empty        map[string]SessionMeta
		appended, replaced []model.Session
		overlap            bool
	}{
		{
			name:     "disjoint old and parsed keys",
			rows:     map[string]SessionMeta{"codex:a": {Path: "a"}, "codex:b": {Path: "b"}},
			appended: []model.Session{s}, replaced: []model.Session{{Harness: "codex", ID: "t"}},
		},
		{
			name:    "old row names both halves",
			rows:    map[string]SessionMeta{"codex:s": {Path: "a", Copies: []string{"b"}}},
			overlap: true,
		},
		{
			name:     "old append row and parsed rest key",
			rows:     map[string]SessionMeta{"codex:s": {Path: "a"}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed append key and old rest row",
			rows:     map[string]SessionMeta{"codex:s": {Path: "b"}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed keys in both halves",
			appended: []model.Session{s}, replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "empty append row and parsed rest key",
			empty:    map[string]SessionMeta{"codex:s": {Path: "a"}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed append key and empty rest row",
			empty:    map[string]SessionMeta{"codex:s": {Path: "b"}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "append copy beside a kept owner",
			rows:     map[string]SessionMeta{"codex:s": {Path: "kept", Copies: []string{"a"}}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "rest copy beside a kept owner",
			empty:    map[string]SessionMeta{"codex:s": {Path: "kept", Copies: []string{"b"}}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "same ID in different harnesses",
			rows:     map[string]SessionMeta{"claude:s": {Path: "b"}},
			appended: []model.Session{s},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := Manifest{Sessions: tc.rows, Empty: tc.empty}
			ready := &splitReads{
				appendFiles: map[string]splitRead{"a": {sessions: tc.appended}},
				restFiles:   map[string]splitRead{"b": {sessions: tc.replaced}},
			}
			if got := splitOverlaps(old, map[string]FileState{"a": {}}, map[string]FileState{"b": {}}, ready); got != tc.overlap {
				t.Errorf("overlap = %t, want %t", got, tc.overlap)
			}
		})
	}
}

// A rewritten history can lose a session that an appended rollout takes over.
// Its old row must keep the two files in the same replacement pass.
func TestACodexHistoryLosingAnAppendedSessionKeepsTheBatchWhole(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex", "DEJA_XCODE_CODEX_ROOT": "xcode-codex"})
	t.Setenv("CODEX_HOME", "")
	const s = "019a0000-0000-7000-8000-000000000001"
	const other = "019a0000-0000-7000-8000-000000000002"
	const fresh = "019a0000-0000-7000-8000-000000000003"
	const prompt = "why does the exporter retry loop stall"
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	history := filepath.Join(tmp, "codex", "history.jsonl")
	rollout := filepath.Join(tmp, "xcode-codex", "sessions", "rollout-"+s+".jsonl")
	meta := func(id string) string {
		return codexLine(at, "session_meta", map[string]any{"id": id, "cwd": "/w/app"})
	}
	message := codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": prompt}}})
	otherLine := codexHistoryLine(other, at, "why does the cache warm on boot")
	write(t, history, codexHistoryLine(s, at, prompt)+otherLine)
	write(t, rollout, meta(s))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if row := before.Sessions["codex:"+s]; row.Path != history || len(row.Copies) != 0 {
		t.Fatalf("control: S must belong to history alone: %+v", row)
	}
	if _, known := before.Files[rollout]; !known {
		t.Fatal("control: the metadata-only rollout must already be known")
	}
	write(t, history, otherLine)
	appendTo(t, rollout, message)
	write(t, filepath.Join(tmp, "xcode-codex", "sessions", "rollout-"+fresh+".jsonl"), meta(fresh)+message)
	changed, _, _ := diffWalk(currentFiles(""), before.Files)
	appendable, rest := splitAppendable(changed, before.Files, before.Sessions)
	if _, ok := appendable[rollout]; !ok || len(appendable) != 1 || len(rest) != 2 {
		t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
	}
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("history and its rollout must be replaced together: %q", got)
	}
	for _, id := range []string{s, other, fresh} {
		sessionMatchesRebuild(t, dir, "codex", id)
	}
}

// Early reads must carry their diagnostics to the half that records the file,
// without losing or doubling counts when append writes its manifest first.
func TestASplitWithANewFileKeepsItsReadAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		whole    bool
	}{
		{"distinct session", "s-new", false},
		{"shares appended session", "s-grown", true},
		{"shares replaced session", "s-other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			grown := filepath.Join(claude, "p", "grown.jsonl")
			rewritten := filepath.Join(claude, "p", "rewritten.jsonl")
			fresh := filepath.Join(claude, "p", "new.jsonl")
			write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "alpha one"))
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta cache"))
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			appendTo(t, grown, "{bad tail}\n"+claudeLine("s-grown", "2026-01-02T04:00:00Z", "alpha three"))
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta queue"))
			bumpMTime(t, rewritten)
			write(t, fresh, "{bad new}\n"+claudeLine(tc.id, "2026-01-02T05:00:00Z", "token ghp_abcdefghijklmnop0123456789QRSTUV"))
			var progress bytes.Buffer
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if appended := strings.Contains(progress.String(), "updated 1 file"); appended == tc.whole {
				t.Errorf("whole replacement = %t, progress: %q", tc.whole, progress.String())
			}
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{fresh, grown} {
				if got := m.IngestFiles[p].Malformed; got != 1 {
					t.Errorf("%s malformed lines = %d, want 1", filepath.Base(p), got)
				}
			}
			if got := m.Files[fresh].Redactions; got != 1 {
				t.Errorf("new file redactions = %d, want 1", got)
			}
			if ok, _ := UpToDate(dir, ""); !ok {
				t.Error("early read's file state was not recorded")
			}
			sessionMatchesRebuild(t, dir, "claude", tc.id)
		})
	}
}
