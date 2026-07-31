package index

import (
	"github.com/vshulcz/deja-vu/internal/query"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEachToolOutputStreamsOnlyToolRecords(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-stream")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var got []Record
	var harness string
	if err := EachToolOutput(dir, func(m SessionMeta, r Record) {
		got = append(got, r)
		harness = m.Harness
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one tool-output record, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "shellcheck") {
		t.Fatalf("wrong record: %q", got[0].Text)
	}
	// The session is what makes the record countable — a caller clustering
	// errors needs to know which session and which harness hit it.
	if harness != "claude" {
		t.Fatalf("harness = %q", harness)
	}
	if got[0].Time.IsZero() {
		t.Fatal("a record with no time cannot be dated on a report")
	}
}

func TestEachToolOutputOnMissingStore(t *testing.T) {
	if err := EachToolOutput(filepath.Join(t.TempDir(), "nope"), func(SessionMeta, Record) {}); err == nil {
		t.Fatal("a missing store should report an error")
	}
}

// recordsForKey skips the body of records belonging to other sessions. It must
// still return exactly what a full decode of the log would have returned for
// this one — the shortcut is a decode it avoids, not a record it drops.
func TestRecordsForKeyMatchesAFullScan(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-keys")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"k1", "k2"} {
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first turn in ` + sid + `"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer for ` + sid + `"}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records.bin")
	tbl := tablesFromManifest(m)
	for key := range m.Sessions {
		var want []Record
		if err := eachRecord(path, tbl, func(r Record) {
			if r.Key == key {
				want = append(want, r)
			}
		}); err != nil {
			t.Fatal(err)
		}
		got, err := recordsForKey(path, tbl, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || len(want) == 0 {
			t.Fatalf("%s: got %d records, full scan found %d", key, len(got), len(want))
		}
		for i := range want {
			if got[i].Text != want[i].Text || got[i].Role != want[i].Role || !got[i].Time.Equal(want[i].Time) {
				t.Fatalf("%s record %d differs: %#v vs %#v", key, i, got[i], want[i])
			}
		}
	}
}

// A replaced span is served only when asked for by role, so it never reaches
// a caller loading sessions the ordinary way — the inventory needs its own
// pass, and that is the whole reason this function exists.
func TestSpanInventoryCountsWhatRestoreCanHandBack(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-spans")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	edit := func(sid, path, old string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-07-20T10:00:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + path +
			`","old_string":"` + old + `","new_string":"replacement"}}]}}` + "\n"
	}
	// Two sessions, three spans, two distinct files — the same file edited in
	// both sessions must count once.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"fix it"}}`+"\n"+
			edit("s1", "/w/pool.go", "old pool code")+
			edit("s1", "/w/retry.go", "old retry code")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"), []byte(
		`{"type":"user","sessionId":"s2","cwd":"/w","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"again"}}`+"\n"+
			edit("s2", "/w/pool.go", "another old pool")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	spans, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if spans != 3 {
		t.Fatalf("spans = %d, want 3", spans)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2 — the same file in two sessions is one file", files)
	}
	// The ordinary load path cannot see them, which is why this exists.
	ss, err := SearchWithRecovery(dir, query.Options{All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == roleEdit {
				t.Fatal("a span reached ordinary retrieval; SpanInventory would be unnecessary")
			}
		}
	}
}

func TestSpanInventoryOnAStoreWithNoSpans(t *testing.T) {
	if _, _, err := SpanInventory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("no index should report an error rather than zero")
	}
}

// SpanInventory guards against a span whose first line is empty, and that
// guard cannot be exercised through a parser: both extractors drop an edit
// with no path, so no such record can be written. Measured on a real store:
// 0 edit records with an empty first line, 0 with no newline at all.
//
// So this pins the invariant the guard rests on rather than the guard itself.
// If a future parser starts recording pathless spans, the file count here
// changes and this fails — which is the moment the guard starts mattering.
func TestSpanInventorySkipsSpansWithNoPath(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-blank")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// old_string carrying a leading newline makes the stored span begin with
	// its own newline once the path line is written above it.
	body := `{"type":"user","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"edit it"}}` + "\n" +
		`{"type":"assistant","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"","old_string":"orphan span with no path","new_string":"x"}}]}}` + "\n" +
		`{"type":"assistant","sessionId":"b1","cwd":"/w","timestamp":"2026-07-20T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/real.go","old_string":"real old bytes","new_string":"y"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "b1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("files = %d, want the one span that names a file", files)
	}
}

// A manifest entry becomes a session in one place. Two constructors existed
// and only one carried Touched, so a caller reading it from Recent got an
// empty slice on every session and no error — silence, which is how it cost a
// wrong measurement before it was noticed (#633).
func TestRecentCarriesTouched(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-touch")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"t1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"look at the pool"}}` + "\n" +
		`{"type":"assistant","sessionId":"t1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/pool.go"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "t1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The manifest holds it...
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || len(metas[0].Touched) == 0 {
		t.Fatalf("manifest carries no Touched: %#v", metas)
	}
	// ...and every path that hands back a session must too.
	recent, err := Recent(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 {
		t.Fatalf("sessions = %d", len(recent))
	}
	if len(recent[0].Touched) == 0 {
		t.Fatal("Recent dropped Touched, so a caller sees an empty slice and no error")
	}
	if recent[0].Touched[0] != "/w/pool.go" {
		t.Fatalf("Touched = %v", recent[0].Touched)
	}
}

// Notes are stored under the harness "deja" and narrated during indexing as
// "notes", so `deja index` printed "notes: 5 sessions" and `--harness notes`
// answered "no sessions match" and exited 0. A filter that rejects the name
// the tool just printed is a silent miss.
func TestHarnessFilterAcceptsTheNameItPrints(t *testing.T) {
	for _, c := range []struct {
		stored, want string
		ok           bool
	}{
		{"deja", "notes", true},
		{"deja", "deja", true},
		{"claude", "claude", true},
		{"claude", "notes", false},
		{"notes", "deja", false},
		{"codex", "claude", false},
	} {
		if got := harnessMatches(c.stored, c.want); got != c.ok {
			t.Errorf("harnessMatches(%q, %q) = %v, want %v", c.stored, c.want, got, c.ok)
		}
	}
}
