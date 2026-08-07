package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPathTrimsTrailingSeparator(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db") + string(filepath.Separator)
	if got, want := Path(dir), strings.TrimSuffix(dir, string(filepath.Separator))+".usage.jsonl"; got != want {
		t.Fatalf("Path(%q) = %q, want %q", dir, got, want)
	}
	if got := read(filepath.Join(t.TempDir(), "missing.usage.jsonl")); got != nil {
		t.Fatalf("read missing = %#v", got)
	}
}

func TestRecordAndToday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	Record(dir, KindRecall, 1000)
	Record(dir, KindHook, 500)
	Record(dir, KindSearch, 9999) // human search, not counted
	recalls, bytes := Today(dir)
	if recalls != 2 || bytes != 1500 {
		t.Fatalf("today = %d recalls %d bytes, want 2/1500", recalls, bytes)
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("usage log missing: %v", err)
	}
}

func TestTodayIgnoresYesterday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	old := Event{Time: time.Now().UTC().Add(-48 * time.Hour), Kind: KindRecall, Bytes: 700}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(Path(dir), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindRecall, 300)
	recalls, bytes := Today(dir)
	if recalls != 1 || bytes != 300 {
		t.Fatalf("today = %d/%d, want 1/300", recalls, bytes)
	}
}

func TestCorruptLinesIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	zero, _ := json.Marshal(Event{Kind: KindRecall, Bytes: 99})
	if err := os.WriteFile(Path(dir), []byte("{broken\n\n"+string(zero)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindContext, 42)
	recalls, bytes := Today(dir)
	if recalls != 1 || bytes != 42 {
		t.Fatalf("today = %d/%d, want 1/42", recalls, bytes)
	}
}

func TestBackwardCompatibleEventsAndTotals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	old := `{"t":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","kind":"recall","bytes":10}` + "\n"
	if err := os.WriteFile(Path(dir), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	RecordResult(dir, KindContext, 20, 0, true)
	RecordResult(dir, KindHook, 30, 2, false)
	Record(dir, KindSearch, 100)
	got := Totals(dir)
	// Two agent recalls and one injection. Recalls used to be 3 because the
	// hook was counted on both lines, and `deja stats` printed that total
	// under "Recalls served" with "Injections 1" right below it.
	if got.Recalls != 2 || got.Injections != 1 || got.InjectedSessions != 2 || got.Bytes != 60 || got.InjectedBytes != 30 || got.EmptyResultRate != 0.5 {
		t.Fatalf("Totals = %#v", got)
	}
	if injected := InjectedToday(dir); injected != 30 {
		t.Fatalf("InjectedToday = %d, want 30", injected)
	}
}

func TestRotateMissingAndSmallNoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	rotate(p)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("rotate missing stat err = %v", err)
	}
	if err := os.WriteFile(p, []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotate(p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "small\n" {
		t.Fatalf("small log changed: %q", b)
	}
}

func TestRecordSwallowsDirectoryErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not reliable on windows")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(filepath.Join(blocked, "index.db"), KindRecall, 1)

	idx := filepath.Join(dir, "asdir")
	if err := os.Mkdir(Path(idx), 0o755); err != nil {
		t.Fatal(err)
	}
	Record(idx, KindRecall, 1) // OpenFile on a directory fails and is swallowed.
}

func TestRotateCreateTempFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "usage.jsonl")
	if err := os.WriteFile(p, []byte(strings.Repeat("x", rotateAt)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	rotate(p)
}

func TestRotateKeepsRecentWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	var sb strings.Builder
	oldEvent, _ := json.Marshal(Event{Time: time.Now().UTC().Add(-30 * 24 * time.Hour), Kind: KindRecall, Bytes: 1})
	newEvent, _ := json.Marshal(Event{Time: time.Now().UTC(), Kind: KindRecall, Bytes: 2})
	for sb.Len() < rotateAt {
		sb.Write(oldEvent)
		sb.WriteByte('\n')
	}
	sb.Write(newEvent)
	sb.WriteByte('\n')
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindHook, 3) // triggers rotation
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= rotateAt {
		t.Fatalf("log not rotated, size %d", fi.Size())
	}
	recalls, bytes := Today(dir)
	if recalls != 2 || bytes != 5 {
		t.Fatalf("today after rotate = %d/%d, want 2/5", recalls, bytes)
	}
}

// Record must stay silent when the log directory cannot be created.
func TestRecordSilentOnMkdirFailure(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// index dir nested under a regular file: MkdirAll must fail on every OS
	Record(filepath.Join(parent, "deep", "index.db"), KindRecall, 1)
	r, b := Today(filepath.Join(parent, "deep", "index.db"))
	if r != 0 || b != 0 {
		t.Fatalf("unexpected events recorded: %d/%d", r, b)
	}
}

// A déjà vu moment — the user asking something their own history already
// answered — is the only signal deja has that the *user* came back, rather than
// an agent pulling a session. It was recorded without session ids, so it could
// not reach ranking at all.
func TestDejaVuMomentsCountTowardsWornSessions(t *testing.T) {
	dir := t.TempDir()
	RecordDigestTerms(dir, KindDejaVu, "digest", 2, 100, []string{"pgbouncer"}, "s1", "s2")
	RecordServedSessions(dir, KindRecall, 10, 1, false, 50, []string{"s1"})
	// Kinds that say nothing about a session mattering must not count.
	RecordServedSessions(dir, KindSearch, 10, 1, false, 50, []string{"s3"})
	RecordServedSessions(dir, KindHandoff, 10, 1, false, 50, []string{"s4"})

	worn := WornSessions(dir)
	if worn["s1"] != 2 {
		t.Fatalf("s1 = %d, want the déjà vu moment and the recall", worn["s1"])
	}
	if worn["s2"] != 1 {
		t.Fatalf("s2 = %d, want the déjà vu moment", worn["s2"])
	}
	for _, id := range []string{"s3", "s4"} {
		if worn[id] != 0 {
			t.Fatalf("%s = %d, want nothing: a search or a handoff is not evidence the session mattered", id, worn[id])
		}
	}
}

// A log is over the size trigger because it is busy, not because it is old.
// Rewriting it then drops nothing and costs a full read-and-write on the
// recall path: measured on a 10k-session store, per-recall time went from
// 123ms at an empty log to 342ms at 1.2MB and 870ms at 5.8MB, with every one
// of those rewrites keeping every event.
func TestRecordSkipsRotationWhenNothingWouldBeDropped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	// An extra field no Event carries: a rewrite re-marshals and drops it, so
	// its survival says the file was appended to rather than rebuilt.
	var sb strings.Builder
	for sb.Len() < rotateAt {
		e, _ := json.Marshal(Event{Time: time.Now().UTC().Add(-time.Hour), Kind: KindRecall, Bytes: 1})
		sb.Write(append(e[:len(e)-1], []byte(`,"probe":1}`)...))
		sb.WriteByte('\n')
	}
	before := sb.String()
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindHook, 3)
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), before) {
		t.Fatalf("log was rewritten: %d bytes before, %d after, prefix intact = %v",
			len(before), len(after), strings.HasPrefix(string(after), before))
	}
	if n := strings.Count(string(after), `"probe":1`); n != strings.Count(before, `"probe":1`) {
		t.Fatalf("kept %d probe fields, want %d", n, strings.Count(before, `"probe":1`))
	}
}
