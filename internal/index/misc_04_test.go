package index

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Large payloads are deflated; everything else is stored as it is. Whatever
// the encoding, the text must come back byte-identical and stay findable.
func TestCompressedRecordsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	tbl := newRecordTables()
	rw, err := newRecordWriter(f, tbl)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("func encodeRecord(r Record) []byte { return nil } // padding line\n", 400)
	random := make([]byte, compressFloor*2)
	for i := range random {
		random[i] = byte(i*7 + i/251) // poorly compressible
	}
	want := []Record{
		{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "short one", Time: time.Unix(10, 20)},
		{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "assistant", Text: big, Time: time.Unix(30, 40)},
		{Key: "claude:s2", SourcePath: "/tmp/s2.jsonl", Role: "user", Text: string(random), Time: time.Unix(50, 60)},
		{Key: "claude:s2", SourcePath: "/tmp/s2.jsonl", Role: "user", Text: "", Time: time.Unix(70, 80)},
	}
	for _, r := range want {
		if _, err := rw.write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Record
	if err := eachRecord(p, tbl, func(r Record) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d records, wrote %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Text != w.Text || g.Key != w.Key || g.SourcePath != w.SourcePath || g.Role != w.Role || !g.Time.Equal(w.Time) {
			t.Errorf("record %d round-tripped wrong: key=%q role=%q len(text)=%d want len=%d", i, g.Key, g.Role, len(g.Text), len(w.Text))
		}
	}
}

// An ordinary message of a few hundred bytes is deflated now, and a record a
// build before the floor dropped stored raw at the same size still reads: the
// per-record flag decides, not the size, so a log mixing both needs no
// rebuild.
func TestMidSizeRecordsDeflateAndOldRawOnesStillRead(t *testing.T) {
	tbl := newRecordTables()
	text := strings.Repeat("the gravel pit pump stalls when the float sticks; ", 12)
	r := Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "assistant", Text: text, Time: time.Unix(10, 20)}
	enc := encodeRecord(r, tbl)
	_, _, _, rest, ok := recordPrefix(enc)
	if !ok || rest[0] != recordDeflated {
		t.Fatalf("a %d-byte message was not deflated (ok=%v)", len(text), ok)
	}
	if len(enc) >= len(text) {
		t.Errorf("deflated record is %d bytes for %d of text", len(enc), len(text))
	}
	short := encodeRecord(Record{Key: "claude:s1", Role: "user", Text: "tiny", Time: time.Unix(1, 0)}, tbl)
	if _, _, _, rest, _ := recordPrefix(short); rest[0] != recordRaw {
		t.Errorf("a record under the floor was deflated")
	}

	// The same record as the 8 KB floor wrote it.
	body := binary.LittleEndian.AppendUint64(nil, uint64(r.Time.UnixNano()))
	body = appendField(body, r.Text)
	old := binary.AppendUvarint(nil, tbl.intern(r.Key))
	old = binary.AppendUvarint(old, tbl.intern(r.SourcePath))
	old = binary.AppendUvarint(old, tbl.intern(r.Role))
	old = append(append(old, recordRaw), body...)
	for name, b := range map[string][]byte{"deflated": enc, "raw": old} {
		got, err := decodeRecord(b, tbl)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Text != text || got.Key != r.Key || got.Role != r.Role || !got.Time.Equal(r.Time) {
			t.Errorf("%s record read back wrong: %+v", name, got)
		}
	}
}

// A big tool dump is exactly what gets compressed, so it must still be
// searchable and still come back in full.
func TestCompressedDumpsStaySearchable(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	dump := strings.Repeat("internal/index/store_io.go:63 encodeRecord ok padding padding ", 300) + " quetzalcoatlmarker tail"
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + dump + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if len(dump) < compressFloor {
		t.Fatalf("fixture is %d bytes, below the %d floor — it would never be compressed", len(dump), compressFloor)
	}
	ss, err := Search(dir, query.Options{Query: "quetzalcoatlmarker", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("compressed dump not found: %d sessions", len(ss))
	}
	var full string
	var fullSb92 strings.Builder
	for _, m := range ss[0].Messages {
		fullSb92.WriteString(m.Text)
	}
	full += fullSb92.String()
	if !strings.Contains(full, "quetzalcoatlmarker") || len(full) < len(dump) {
		t.Fatalf("compressed dump came back truncated: %d bytes, want >= %d", len(full), len(dump))
	}
}

// A bucket directory no longer stores each token's offset; it is the running
// sum of the block lengths. Every token must still land on its own postings.
func TestBucketOffsetsAreDerivedCorrectly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ab.bin")
	want := map[string][]posting{
		"talpha": {{Off: 1, Sid: 1}, {Off: 900, Sid: 2}},
		"tbeta":  {{Off: 5, Sid: 3}},
		"tgamma": {},
		"tdelta": {{Off: 7, Sid: 4}, {Off: 8, Sid: 5}, {Off: 9000000, Sid: 6}},
	}
	if err := writeBucket(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := readBucket(p)
	if err != nil {
		t.Fatal(err)
	}
	for tok, w := range want {
		g := got[tok]
		if len(g) != len(w) {
			t.Errorf("token %q: %d postings, want %d", tok, len(g), len(w))
			continue
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("token %q posting %d = %+v, want %+v", tok, i, g[i], w[i])
			}
		}
	}
	// And reading one token directly must land on the same block.
	for tok, w := range want {
		g, err := readBucketToken(p, tok, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(g) != len(w) {
			t.Errorf("readBucketToken(%q) = %d postings, want %d", tok, len(g), len(w))
		}
	}
}

// One sentence covered four breakages, and named the wrong file in two of them
// (#2695). Each store here is broken exactly one way.
func TestDamageReasonNamesWhatBroke(t *testing.T) {
	build := func(t *testing.T) string {
		t.Helper()
		tmp := t.TempDir()
		setHome(t, filepath.Join(tmp, "home"))
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
		root := filepath.Join(tmp, "claude", "proj-p")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z",` +
			`"message":{"role":"user","content":"terraform apply --zonkoshard 7"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	healthy := build(t)
	if got := DamageReason(healthy); got != "" {
		t.Fatalf("a healthy store was called damaged: %q", got)
	}
	if Damaged(healthy) {
		t.Fatal("Damaged disagrees with DamageReason on a healthy store")
	}

	cases := []struct {
		name  string
		spoil func(t *testing.T, dir string)
		want  string
	}{
		{"a manifest that will not decode", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "manifest"},
		{"a session table that will not decode", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "sessions.bin"), []byte("not a gob"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "session table"},
		{"a record log cut short", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "records.bin"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "record log"},
		{"postings that went missing", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
				t.Fatal(err)
			}
		}, "the postings directory is not there"},
		{"a postings directory left empty", func(t *testing.T, dir string) {
			t.Helper()
			// What `rm buckets/*.bin` and a copy that skipped the contents
			// leave behind, which is not the same as losing the directory.
			buckets := filepath.Join(dir, "buckets")
			entries, err := os.ReadDir(buckets)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if err := os.Remove(filepath.Join(buckets, e.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}, "the postings directory is empty"},
		{"a record log with a tail the manifest never committed", func(t *testing.T, dir string) {
			t.Helper()
			f, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("a record from a build that crashed"); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}, "longer than the manifest committed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := build(t)
			c.spoil(t, dir)
			got := DamageReason(dir)
			if !strings.Contains(got, c.want) {
				t.Errorf("reason %q does not say %q", got, c.want)
			}
			if !Damaged(dir) {
				t.Errorf("Damaged says the store is fine while the reason is %q", got)
			}
		})
	}

	// A store that was never built is not a damaged one.
	if got := DamageReason(t.TempDir()); got != "" {
		t.Errorf("an empty directory was called damaged: %q", got)
	}
}

// A half-written index rebuilds itself, and it used to announce that with the
// same line as a routine reindex — so a disk that keeps corrupting the store
// looked ordinary every time (#1110).
func TestDamagedIndexSaysSoWhileRebuilding(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"a decision about backpressure"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var first bytes.Buffer
	if err := EnsureForSearch(dir, query.Options{}, false, &first); err != nil {
		t.Fatal(err)
	}
	half := func(name string) {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b[:len(b)/2], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"manifest.gob", "records.bin"} {
		half(name)
		var out bytes.Buffer
		if err := EnsureForSearch(dir, query.Options{}, false, &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "could not be read and is being rebuilt") {
			t.Errorf("a half-written %s rebuilt silently: %q", name, out.String())
		}
	}

	// A store that is merely stale must not borrow that wording.
	var routine bytes.Buffer
	if err := EnsureForSearch(dir, query.Options{}, false, &routine); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(routine.String(), "could not be read") {
		t.Errorf("an intact index was called damaged: %q", routine.String())
	}
}

// The next search rebuilds a damaged store, so nothing is lost permanently —
// what was wrong is the answer doctor gave: "built, up to date" about a store
// that could not return a single result (#735).
func TestDamaged(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index reported damage")
	}

	// Postings gone: a partial copy, an interrupted sync, a find -delete.
	buckets := filepath.Join(dir, "buckets")
	saved := filepath.Join(tmp, "buckets.bak")
	if err := os.Rename(buckets, saved); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("missing postings not reported")
	}
	if err := os.Rename(saved, buckets); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Error("restored postings still reported as damage")
	}

	// A truncated record log.
	if err := os.Truncate(filepath.Join(dir, "records.bin"), 10); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("truncated record log not reported")
	}

	// No index at all is "not built", which doctor reports on its own.
	if Damaged(filepath.Join(tmp, "no-such-index")) {
		t.Error("a missing index reported damage")
	}
}

// A question that names one day is a different question from one that asks
// about a stretch of days: "what did I buy ten days ago" points at a session,
// "how much did I spend last month" counts over many. Only the first may move
// the ranking, because for the second the session nearest that day is not the
// answer — measured as four demotions on LongMemEval when spans were included.
func TestOnlyAQuestionNamingOneDayResolvesToADay(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) // a Tuesday
	for _, c := range []struct {
		q    string
		want string // "" means it must not resolve
	}{
		{"what kitchen appliance did I buy 10 days ago", "2026-09-12"},
		{"I mentioned cooking something a couple of days ago", "2026-09-20"},
		{"what did I fix two weeks ago", "2026-09-08"},
		{"what did I read yesterday", "2026-09-21"},
		{"who did I meet with during the lunch last tuesday", "2026-09-15"},
		{"what did I work on last friday", "2026-09-18"},
		{"how many hours of jogging did I do last week", ""},
		// Counting questions stand down even when they do name a day: the
		// session nearest it holds one of the numbers being added up, not the
		// answer. Both of these are ranked right today and the guard is what
		// keeps them there.
		{"how much cashback did I earn at SaveMart last thursday", ""},
		{"how many pieces of writing have I completed since I started three weeks ago", ""},
		{"how many days ago did I buy a smoker", ""},
		{"how many total pieces of writing have I completed since I started", ""},
		{"what did I do in may", ""},
		{"what did I change", ""},
	} {
		got, ok := pointInTime(c.q, now)
		if c.want == "" {
			if ok {
				t.Errorf("%q resolved to %s, and it names no single day", c.q, got.Format("2006-01-02"))
			}
			continue
		}
		if !ok {
			t.Errorf("%q resolved to nothing, want %s", c.q, c.want)
			continue
		}
		if have := got.Format("2006-01-02"); have != c.want {
			t.Errorf("%q resolved to %s, want %s", c.q, have, c.want)
		}
	}
}

// "last tuesday" asked on a Tuesday means the Tuesday before, not today: a
// week back, not nothing back.
func TestLastWeekdayNeverResolvesToToday(t *testing.T) {
	tue := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	if got := lastWeekday(tue, "tuesday"); !got.Equal(tue.AddDate(0, 0, -7)) {
		t.Errorf("last tuesday on a Tuesday = %s, want a week back", got.Format("2006-01-02"))
	}
	if got := lastWeekday(tue, "sunday"); got.Weekday() != time.Sunday || !got.Before(tue) {
		t.Errorf("last sunday = %s (%s), want the most recent past Sunday", got.Format("2006-01-02"), got.Weekday())
	}
	if got := lastWeekday(tue, "sunday"); !got.Equal(tue.AddDate(0, 0, -2)) {
		t.Errorf("last sunday from Tuesday = %s, want two days back", got.Format("2006-01-02"))
	}
}

func sessionOn(id string, day time.Time) model.Session {
	return model.Session{ID: id, Started: day}
}

// Outside the span the pool covers, every candidate is equally far from the
// day, so the proximity order degenerates into "newest first" — an answer to a
// question nobody asked. The rule has to stand down there.
func TestTheDayRuleStandsDownOutsideThePoolsOwnSpan(t *testing.T) {
	base := time.Date(2023, 5, 10, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("a", base),
		sessionOn("b", base.AddDate(0, 0, 5)),
		sessionOn("c", base.AddDate(0, 0, 10)),
	}
	if dayInsideRange(ss, base.AddDate(3, 0, 0)) {
		t.Error("a day three years past the newest session counts as inside the span")
	}
	if dayInsideRange(ss, base.AddDate(0, 0, -30)) {
		t.Error("a day a month before the oldest session counts as inside the span")
	}
	if !dayInsideRange(ss, base.AddDate(0, 0, 5)) {
		t.Error("a day the pool itself covers counts as outside the span")
	}
	// And the reordering itself leaves the pool alone out there.
	out := rerankByDayProximity(ss, base.AddDate(3, 0, 0))
	for i := range ss {
		if out[i].ID != ss[i].ID {
			t.Fatalf("order changed for a day outside the span: %s", sessionIDs(out))
		}
	}
}

// The day is a hint fused with the pool's order, not a sort key: a session the
// words put first does not lose its place to a closer date alone, but a closer
// date lifts a session the words ranked below it.
func TestTheClosestDayLiftsWithoutOverridingTheWords(t *testing.T) {
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("first-by-words", day.AddDate(0, 0, 9)),
		sessionOn("second-by-words", day.AddDate(0, 0, 8)),
		sessionOn("on-the-day", day),
		sessionOn("far", day.AddDate(0, 0, 40)),
	}
	got := sessionIDs(rerankByDayProximity(ss, day))
	if got[0] != "first-by-words" && got[0] != "on-the-day" {
		t.Fatalf("head is %q: the fusion should keep one of the two votes on top, got %v", got[0], got)
	}
	// The session sitting on the day must end up above the one the words put
	// last, and above its own starting place.
	pos := map[string]int{}
	for i, id := range got {
		pos[id] = i
	}
	if pos["on-the-day"] >= 2 {
		t.Errorf("the session on the named day stayed at %d: %v", pos["on-the-day"], got)
	}
	if pos["far"] != 3 {
		t.Errorf("the session 40 days off moved to %d: %v", pos["far"], got)
	}
}

// A session with no timestamp at all must not be treated as if it happened on
// the named day; it keeps its place from the words.
func TestASessionWithNoTimeIsNotNearAnyDay(t *testing.T) {
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	ss := []model.Session{
		sessionOn("dated-far", day.AddDate(0, 0, 20)),
		{ID: "undated"},
		sessionOn("on-the-day", day),
	}
	got := sessionIDs(rerankByDayProximity(ss, day))
	if got[len(got)-1] != "undated" {
		t.Errorf("the undated session did not sink: %v", got)
	}
}

// TestSelectorLadderResolverAndCounterAlwaysAgree enforces the domain invariant
// across all six rungs of the selector ladder in retrieval_identity.go (#853,
// #921, #1049, #1620, #2401, #4667): for every selector shape, FindByPrefixIn
// resolves a session if and only if PrefixMatchesIn returns > 0.
func TestSelectorLadderResolverAndCounterAlwaysAgree(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeDir := filepath.Join(tmp, "claude", "-tmp-w")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const fullUUID = "a1b2c3d4-0000-4000-8000-00000000e1f2"
	body := `{"type":"user","sessionId":"` + fullUUID + `","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"fix the selector ladder drift"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeDir, fullUUID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	selectors := []struct {
		sel  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"nonexistent-selector", false},
		{fullUUID, true},                   // Rung 1: exact ID
		{"a1b2c3d4", true},                 // Rung 1: prefix ID
		{"a1b2c3d4…0000e1f2", true},        // Rung 4: elided ID
		{"claude:a1b2c3d4", true},          // Rung 5: composite harness:id (#921)
		{"claude:a1b2c3d4…0000e1f2", true}, // Rung 5: composite + elided
		{"codex:a1b2c3d4", false},          // Rung 5: wrong harness
		{"A1B2C3D4-0000", true},            // Rung 6: case-folded UUID (#1620)
	}

	for _, tc := range selectors {
		_, ok, err := FindByPrefix(dir, tc.sel)
		if err != nil {
			t.Fatalf("FindByPrefix(%q): %v", tc.sel, err)
		}
		n := PrefixMatches(dir, tc.sel)
		if ok != tc.want {
			t.Errorf("FindByPrefix(%q) ok = %v, want %v (PrefixMatches = %d)", tc.sel, ok, tc.want, n)
		}
		if (n > 0) != ok {
			t.Errorf("selector %q: FindByPrefix ok=%v disagrees with PrefixMatches=%d", tc.sel, ok, n)
		}
	}
}

// TestSessionTitleRankMonotonicity verifies the domain hierarchy in
// session_title.go: placeholder/plumbing (0) < tool output (1) < agent opening
// (2) < thin user greeting (3) < substantial user question (4).
func TestSessionTitleRankMonotonicity(t *testing.T) {
	r0Empty := titleRankOfRow("", false)
	r0Plumbing := titleRankOfRow(harnessOutputTitle("local stdout"), false)
	r1Tool := titleRankOfRow(toolOutputTitle("PASS ok"), false)
	r2Agent := titleRankOfRow("I inspected the repository and found two issues", true)
	r3Thin := titleRankOfRow("привет", false)
	r4Question := titleRankOfRow("почему падает тест на windows", false)

	if r0Empty != 0 || r0Plumbing != 0 {
		t.Fatalf("placeholder ranks = (%d, %d), want (0, 0)", r0Empty, r0Plumbing)
	}
	if r0Plumbing >= r1Tool || r1Tool >= r2Agent || r2Agent >= r3Thin || r3Thin >= r4Question {
		t.Fatalf("title rank hierarchy violated: plumbing=%d tool=%d agent=%d thin=%d question=%d",
			r0Plumbing, r1Tool, r2Agent, r3Thin, r4Question)
	}
}

// TestDDDStageIReadSideExclusionAndIgnoreInvariants enforces Stage I (Bugs #61-#70):
//  1. AST Invariant: Read-side manifest scanners in internal/index must use the
//     unified metaIgnoredOrExcluded / metaIgnoredOrExcludedLoad / excludedHarnesses
//     gate rather than raw pol.Ignored(...) alone or missing ignore/exclusion checks.
//  2. Behavioral Invariant: DEJA_EXCLUDE_HARNESSES hides excluded sessions from
//     EachAssistantTurnSince, KeptSince, AnyInScope, PromotedNoteMetas, and
//     relevantMetasCounts tie-breaking respects composite (Harness, ID).
func TestDDDStageIReadSideExclusionAndIgnoreInvariants(t *testing.T) {
	fset := token.NewFileSet()
	mustCallGate := map[string][]string{
		"assistant_turns.go":    {"EachAssistantTurnSince"},
		"year.go":               {"ScanYearWork"},
		"store_io.go":           {"SpanInventoryWhere"},
		"fixpair_store.go":      {"fixIgnoreGate"},
		"retrieval_postings.go": {"servableSids", "ignoredSids"},
		"friction.go":           {"PromotedNoteMetas"},
		"user_rules.go":         {"MarkUserRules"},
		"kept_since.go":         {"KeptSince"},
		"scoped.go":             {"AnyInScope"},
		"testruns.go":           {"ScanTestHistory"},
		"secrets.go":            {"ScanSecrets"},
	}

	for filename, wantFuncs := range mustCallGate {
		f, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		foundFuncs := make(map[string]bool)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, want := range wantFuncs {
				if fn.Name.Name != want {
					continue
				}
				foundFuncs[want] = true
				callsGate := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if id, ok := call.Fun.(*ast.Ident); ok {
						switch id.Name {
						case "metaIgnoredOrExcluded", "metaIgnoredOrExcludedLoad", "excludedHarnesses":
							callsGate = true
						}
					}
					return true
				})
				if !callsGate {
					t.Errorf("%s: func %s must call metaIgnoredOrExcluded / metaIgnoredOrExcludedLoad / excludedHarnesses (Stage I Read-Side Gate invariant)",
						filename, want)
				}
			}
		}
		for _, want := range wantFuncs {
			if !foundFuncs[want] {
				t.Errorf("%s: expected function %s not found", filename, want)
			}
		}
	}

	// Behavioral check: two harnesses sharing session ID "shared-1".
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeDir := filepath.Join(tmp, "claude", "-work-payments")
	codexDir := filepath.Join(tmp, "codex", "sessions", "2026", "10", "12")
	for _, d := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	now := time.Now().UTC().Truncate(time.Second)
	ts := now.Add(-10 * time.Minute).Format("2006-01-02T15:04:05Z")
	if err := os.WriteFile(filepath.Join(claudeDir, "shared-1.jsonl"), []byte(
		`{"type":"user","sessionId":"shared-1","cwd":"/work/payments","timestamp":"`+ts+`","message":{"role":"user","content":"fix retry backoff in payments"}}`+"\n"+
			`{"type":"assistant","sessionId":"shared-1","cwd":"/work/payments","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"text","text":"Updated retry backoff in client.go."}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "rollout-2026-10-12T10-00-00-shared-1.jsonl"), []byte(
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"session_id":"shared-1","id":"shared-1","cwd":"/work/payments"}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"fix retry backoff in payments"}]}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Changed codex retry logic."}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	var turns []string
	if err := EachAssistantTurnSince(dir, now.Add(-1*time.Hour), nil, func(meta SessionMeta, text string, _ time.Time) {
		turns = append(turns, meta.Harness+":"+text)
	}); err != nil {
		t.Fatalf("EachAssistantTurnSince: %v", err)
	}
	if len(turns) != 1 || !strings.HasPrefix(turns[0], "claude:") {
		t.Fatalf("Bug #61 regression: EachAssistantTurnSince with codex excluded = %v, want only claude", turns)
	}

	kept, err := KeptSince(dir, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("KeptSince: %v", err)
	}
	for _, k := range kept {
		if k.Harness == "codex" {
			t.Fatalf("Bug #70 regression: KeptSince leaked excluded codex session: %+v", k)
		}
	}

	// Stage N (Bug #86): Unscoped FindByPrefix, FindByID, and PrefixHarnesses must
	// skip excluded harnesses rather than letting an excluded harness shadow an
	// active one.
	if hs := PrefixHarnesses(dir, "shared-1"); len(hs) != 1 || hs[0] != "claude" {
		t.Fatalf("Bug #86 regression: PrefixHarnesses with codex excluded = %v, want [claude]", hs)
	}
	if s, ok, err := FindByPrefix(dir, "shared-1"); err != nil || !ok || s.Harness != "claude" {
		t.Fatalf("Bug #86 regression: FindByPrefix with codex excluded = (%s, %v, %v), want claude", s.Harness, ok, err)
	}
	if s, ok, err := FindByID(dir, "shared-1"); err != nil || !ok || s.Harness != "claude" {
		t.Fatalf("Bug #86 regression: FindByID with codex excluded = (%s, %v, %v), want claude", s.Harness, ok, err)
	}
}

// TestDDDStageCSyncImportRefreshesReceiptsAndCommandState pins Bugs #40, #41, and #43:
//  1. Sync import (`refreshImportSidecars` -> `refreshAppendedSidecars`) updates
//     `receipts.bin` for imported sessions so `evidence.Open(dir).Lookup` finds
//     backing command/output receipts for peer turns recorded before the local
//     index build (Bug #40).
//  2. Sync import updates `commands.gob`, `sessionfacts.gob`, and `commands-state.bin`
//     in lockstep via `buildCommandTables` instead of leaving `commands-state.bin`
//     stale behind `sessionfacts.gob` (Bug #41).
//  3. `writeCommandFails` removes `commandfails.gob` when the table becomes empty
//     during an in-place update (Bug #43).
func TestDDDStageCSyncImportRefreshesReceiptsAndCommandState(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claude, "exporter", "local.jsonl"),
		claudeLine("s-local", "2026-01-02T03:04:05Z", "check the exporter suite")+
			claudeBashLine("s-local", "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	inDir := filepath.Join(tmp, "sync-in")
	if err := os.MkdirAll(inDir, 0o700); err != nil {
		t.Fatal(err)
	}
	answer := "Fixed: retry_limit in payments_client was 0, set it to 5, and go test ./internal/exporter/ passed"
	batch := strings.Join([]string{
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"user","text":"payments_client gives up on the first timeout, fix it","time":"2026-01-01T10:00:00Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"command","text":"$ go test ./internal/exporter/\n[exit 0]","time":"2026-01-01T10:00:05Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"tool-output","text":"12: retry_limit = 0 // payments_client\nok  \tgithub.com/example/exporter\t0.142s","time":"2026-01-01T10:00:06Z"}`,
		`{"harness":"claude","session_id":"peer-1","project":"exporter","role":"assistant","text":"` + answer + `","time":"2026-01-01T10:00:10Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(inDir, "batch.jsonl"), []byte(batch), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := Import(dir, inDir)
	if err != nil || added != 4 {
		t.Fatalf("Import(dir, inDir) = (%d, %v); want (4, nil)", added, err)
	}

	// Bug #40: receipts.bin must cover and hold the receipt for the imported peer session.
	peerID := ImportedSessionID("claude", "peer-1")
	peerKey := "claude:" + peerID
	rc := evidence.Open(dir)
	if rc == nil {
		t.Fatal("evidence.Open(dir) = nil after Import")
	}
	lines := rc.Lookup("claude", peerID, answer)
	if len(lines) == 0 {
		t.Fatalf("receipts.bin has no receipt for imported peer session %s (Bug #40)", peerKey)
	}

	// Bug #41: commands.gob, sessionfacts.gob, and commands-state.bin must all be updated and aligned.
	if !commandsHold(dir, "go test ./internal/exporter/") {
		t.Fatalf("commands.gob does not hold recurring command after peer import")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	st, facts := carriedCommandsState(dir, m, len(m.DeadRecords), fi.Size())
	if st == nil || len(facts) == 0 {
		t.Fatalf("carriedCommandsState returned nil after Import (commands-state.bin out of sync with sessionfacts.gob, Bug #41)")
	}
	if fact, ok := facts[peerKey]; !ok || len(fact.Commands) == 0 {
		t.Fatalf("facts[%s] = (%+v, %v); want imported command fact", peerKey, fact, ok)
	}
	if st.Size != fi.Size() {
		t.Fatalf("commands-state.bin Size = %d; want records.bin size %d", st.Size, fi.Size())
	}

	// Bug #43: writeCommandFails removes commandfails.gob when the table becomes empty.
	writeCommandFails(dir, []CommandFailure{{Head: "make broken", Line: "FAIL", Sessions: 2, Project: "exporter"}})
	if _, err := os.Stat(commandFailsPath(dir)); err != nil {
		t.Fatalf("commandfails.gob not written: %v", err)
	}
	writeCommandFails(dir, nil)
	if _, err := os.Stat(commandFailsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("writeCommandFails(dir, nil) left stale commandfails.gob on disk: err=%v (Bug #43)", err)
	}
}

// deepFixture builds a real index over one claude session and returns the
// index dir plus the source file path, fully isolated from the machine.
func deepFixture(t *testing.T) (dir, src string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(proj, "s1.jsonl")
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fix the flaky auth token test"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"expiry check used < instead of <="}]}}` + "\n"
	if err := os.WriteFile(src, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, src
}

func findingKinds(r DeepReport) map[string]bool {
	kinds := map[string]bool{}
	for _, f := range r.Findings {
		kinds[f.Kind] = true
	}
	return kinds
}

func TestDeepVerifyCleanIndex(t *testing.T) {
	dir, _ := deepFixture(t)
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("fresh index must verify clean, got %#v", r.Findings)
	}
	if r.FilesChecked != 1 || r.SessionsIndexed != 1 || r.SampledFiles != 1 {
		t.Fatalf("counts = files=%d sessions=%d sampled=%d, want 1/1/1", r.FilesChecked, r.SessionsIndexed, r.SampledFiles)
	}
	if r.SampledPostings == 0 {
		t.Fatal("expected at least one posting resolved")
	}
}

func TestDeepVerifyTreatsNewAndGrownFilesAsStale(t *testing.T) {
	dir, src := deepFixture(t)
	extra := filepath.Join(filepath.Dir(src), "s2.jsonl")
	line := `{"type":"user","sessionId":"s2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"new session never indexed"}}` + "\n"
	if err := os.WriteFile(extra, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"appended after the index pass"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stale) != 2 {
		t.Fatalf("new + appended files are staleness, not drift: stale=%v findings=%#v", r.Stale, r.Findings)
	}
	if !r.Clean() {
		t.Fatalf("staleness must not read as drift, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsShrunkSource(t *testing.T) {
	dir, src := deepFixture(t)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Truncate(src, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !findingKinds(r)["shrunk-file"] {
		t.Fatalf("want shrunk-file, got stale=%v findings=%#v", r.Stale, r.Findings)
	}
}

// A transcript deleted while its directory stays is the client's cleanup, and
// the index keeps it on purpose (#2970): listed under Kept, not a finding,
// because the finding's remedy — a rebuild — is what would lose it. A tree
// that is gone whole is still drift.
func TestDeepVerifyFlagsDeletedSource(t *testing.T) {
	dir, src := deepFixture(t)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if findingKinds(r)["orphan-file"] {
		t.Fatalf("a transcript the client cleaned up was reported as drift: %#v", r.Findings)
	}
	if len(r.Kept) != 1 || r.Kept[0] != src {
		t.Fatalf("kept = %v, want the deleted transcript", r.Kept)
	}
	if err = os.RemoveAll(filepath.Dir(src)); err != nil {
		t.Fatal(err)
	}
	r, err = DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !findingKinds(r)["orphan-file"] {
		t.Fatalf("want orphan-file once the whole tree is gone, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsSessionMissingFromIndex(t *testing.T) {
	dir, _ := deepFixture(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k := range m.Sessions {
		delete(m.Sessions, k)
	}
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, f := range r.Findings {
		if f.Kind == "parse-drift" && strings.Contains(f.Detail, "absent from the index") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("want parse-drift for session missing from index, got %#v", r.Findings)
	}
}

func TestDeepVerifyFlagsTruncatedRecords(t *testing.T) {
	dir, _ := deepFixture(t)
	if err := os.Truncate(recordsPath(dir), 3); err != nil {
		t.Fatal(err)
	}
	r, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := findingKinds(r)
	if !kinds["torn-log"] && !kinds["dead-posting"] {
		t.Fatalf("truncated records.bin must surface torn-log or dead-posting, got %#v", r.Findings)
	}
}

func TestDeepVerifyDeterministicSampling(t *testing.T) {
	dir, _ := deepFixture(t)
	a, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampledFiles != b.SampledFiles || a.SampledPostings != b.SampledPostings {
		t.Fatalf("sampling must be deterministic: %d/%d vs %d/%d", a.SampledFiles, a.SampledPostings, b.SampledFiles, b.SampledPostings)
	}
}

// A truncated bucket made tokenCatalog give up on the whole vocabulary, and
// deep verification skipped its posting check without a word — a clean bill of
// health over postings it could not read.
func TestDeepVerifyFlagsUnreadableBucket(t *testing.T) {
	dir, _ := deepFixture(t)
	healthy, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !healthy.Clean() || healthy.SampledPostings == 0 {
		t.Fatalf("baseline: clean=%v postings=%d", healthy.Clean(), healthy.SampledPostings)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	var truncated bool
	for _, de := range entries {
		if strings.HasSuffix(de.Name(), ".bin") {
			if err = os.Truncate(filepath.Join(dir, "buckets", de.Name()), 7); err != nil {
				t.Fatal(err)
			}
			truncated = true
			break
		}
	}
	if !truncated {
		t.Fatal("no bucket to truncate")
	}
	got, err := DeepVerify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clean() {
		t.Fatalf("truncated bucket reported clean: %+v", got)
	}
	if !findingKinds(got)["unreadable-postings"] {
		t.Fatalf("want unreadable-postings finding, got %+v", got.Findings)
	}
}

// DefaultDir honors DEJA_INDEX_DIR first, then an absolute XDG_CACHE_HOME
// (relative values are invalid per the XDG spec and ignored), then ~/.cache.
func TestDefaultDirXDGCacheHome(t *testing.T) {
	home := t.TempDir()
	xdgCache := t.TempDir()
	override := filepath.Join(t.TempDir(), "custom-index")
	setHome(t, home)

	t.Setenv("DEJA_INDEX_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	wantHome := filepath.Join(home, ".cache", "deja", "index.db")
	if got := DefaultDir(); got != wantHome {
		t.Fatalf("default DefaultDir() = %q, want %q", got, wantHome)
	}

	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if got := DefaultDir(); got != wantHome {
		t.Fatalf("relative XDG_CACHE_HOME DefaultDir() = %q, want %q", got, wantHome)
	}

	t.Setenv("XDG_CACHE_HOME", xdgCache)
	wantXDG := filepath.Join(xdgCache, "deja", "index.db")
	if got := DefaultDir(); got != wantXDG {
		t.Fatalf("XDG_CACHE_HOME DefaultDir() = %q, want %q", got, wantXDG)
	}

	t.Setenv("DEJA_INDEX_DIR", override)
	if got := DefaultDir(); got != override {
		t.Fatalf("DEJA_INDEX_DIR DefaultDir() = %q, want %q", got, override)
	}
}

// A store that hands the whole session over on every pass — goose does, because
// its query filters on the session's updated_at as well as the message's — used
// to add the same messages again each time, so the most active session in the
// store silently deranked itself (#1304, defect 2).
func TestARedeliveredSessionIsFoldedOnce(t *testing.T) {
	ms := []model.Message{
		{Role: "user", Text: "why does the retry queue stall"},
		{Role: "assistant", Text: "the backoff is capped at one second"},
	}
	var meta SessionMeta
	extendDerived(&meta, ms)
	first := meta.Words
	if first == 0 {
		t.Fatal("nothing was counted")
	}
	for range 3 {
		extendDerived(&meta, ms)
	}
	if meta.Words != first {
		t.Errorf("the same delivery counted again: %d words after one pass, %d after four", first, meta.Words)
	}
	grown := append(append([]model.Message(nil), ms...), model.Message{Role: "user", Text: "it stalls again on staging"})
	extendDerived(&meta, grown)
	if meta.Words <= first {
		t.Errorf("a message appended to a re-delivered session was not counted: %d words", meta.Words)
	}
	if meta.Counted != 3 {
		t.Errorf("counted %d messages, session holds 3", meta.Counted)
	}
}

// A file yields only the region appended since last time, so the tail arrives
// with no overlap at all — the opposite delivery from the one above, and the
// reason a plain count could not tell them apart.
func TestATailWithNoOverlapIsFoldedWhole(t *testing.T) {
	var meta SessionMeta
	extendDerived(&meta, []model.Message{{Role: "user", Text: "why does the retry queue stall"}})
	before := meta.Words
	extendDerived(&meta, []model.Message{
		{Role: "assistant", Text: "the backoff is capped at one second"},
		{Role: "user", Text: "raise it and redeploy"},
	})
	if meta.Counted != 3 {
		t.Errorf("counted %d, three messages have arrived", meta.Counted)
	}
	if meta.Words <= before {
		t.Errorf("the appended tail was not counted: %d words", meta.Words)
	}
}

// The caps are the reason the manifest stays small, and it is read on every
// query. A plain union grew on every append (#1304, defect 4).
func TestDerivedFieldsStayCapped(t *testing.T) {
	var meta SessionMeta
	for round := range 6 {
		ms := make([]model.Message, 0, 6)
		for i := range 6 {
			ms = append(ms, model.Message{
				Role: "user",
				Text: "how do I wire the " + string(rune('a'+round)) + string(rune('a'+i)) + " adapter into the pipeline, and what should it return?",
			})
		}
		extendDerived(&meta, ms)
	}
	if len(meta.Asked) > askedQuestionCap {
		t.Errorf("Asked grew past its cap: %d > %d", len(meta.Asked), askedQuestionCap)
	}
	if len(meta.Touched) > touchedFileCap {
		t.Errorf("Touched grew past its cap: %d > %d", len(meta.Touched), touchedFileCap)
	}
}

// Touched is a ranking, and merging two ranked lists by position kept whichever
// paths came first. The file a session actually worked on hardest arrived in
// the tail and disappeared (#1304, defect 5).
func TestTheHardestWorkedFileSurvivesTheMerge(t *testing.T) {
	head := make([]model.Message, 0, 6)
	for _, p := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"} {
		head = append(head, model.Message{Role: roleFiles, Text: p})
	}
	var meta SessionMeta
	extendDerived(&meta, head)
	tail := make([]model.Message, 0, 12)
	for range 12 {
		tail = append(tail, model.Message{Role: roleFiles, Text: "hot.go"})
	}
	extendDerived(&meta, tail)
	if len(meta.Touched) == 0 || meta.Touched[0] != "hot.go" {
		t.Errorf("the file worked on hardest is not first: %v", meta.Touched)
	}
}

// Growing the loser of an id collision moved the owner's row: its Words, its
// GaveUp, and the loser's files into its Touched — the wrong conversation
// surfacing in blame (#1304, defect 3).
func TestGrowingACollidingSessionLeavesTheOwnerAlone(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	owner := filepath.Join(claude, "-aaa")
	loser := filepath.Join(claude, "-zzz")
	for _, d := range []string{owner, loser} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	line := func(text string) string {
		return `{"type":"user","sessionId":"same","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(owner, "a.jsonl"), []byte(line("the owner session discusses the retry queue")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loser, "a.jsonl"), []byte(line("the other session discusses billing")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	was := before.Sessions["claude:same"]
	// The loser grows.
	grown := line("the other session discusses billing") + line("i gave up on the billing migration and reverted it")
	if err = os.WriteFile(filepath.Join(loser, "a.jsonl"), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := after.Sessions["claude:same"]
	if now.GaveUp != was.GaveUp {
		t.Errorf("the loser's giving up was written onto the owner's row")
	}
	if now.Words != was.Words {
		t.Errorf("the owner's length moved with the loser's growth: %d then %d", was.Words, now.Words)
	}
}

// A session that repeats a line verbatim: resuming from the first copy would
// fold everything after it a second time on every re-delivery.
func TestARepeatedLineDoesNotRewindTheFold(t *testing.T) {
	same := model.Message{Role: "user", Text: "retry"}
	ms := []model.Message{same, {Role: "assistant", Text: "the backoff is capped at one second"}, same}
	var meta SessionMeta
	extendDerived(&meta, ms)
	first := meta.Words
	extendDerived(&meta, ms)
	if meta.Words != first {
		t.Errorf("a repeated line rewound the fold: %d words became %d", first, meta.Words)
	}
	if meta.Counted != len(ms) {
		t.Errorf("counted %d of %d messages", meta.Counted, len(ms))
	}
}

// Rarity has two honest units and neither works alone. Counting whole sessions
// calls a subject word common, because on a real store a handful of marathon
// sessions hold most of everything and all of them mention it. Counting
// messages, capped per session, fixes that and breaks the other direction: the
// word one long session is *about* saturates that session and reads as filler —
// which is what the name of the thing someone worked on all month looks like.
//
// So both are computed and the rarer verdict wins. This holds that: a word
// living in one long session stays rare, and a word spread thinly across the
// whole store does not become rare just because no single session repeats it.
func TestRarityTakesTheRarerOfTheTwoVerdicts(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, text string, at time.Time) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":%q}}`,
			sid, at.UTC().Format(time.RFC3339), text) + "\n"
	}
	start := time.Now().Add(-72 * time.Hour)

	// One marathon that is about quetzalcoatl and says it in every turn.
	var marathon string
	var marathonSb42 strings.Builder
	for i := range 300 {
		marathonSb42.WriteString(line("marathon", fmt.Sprintf("quetzalcoatl deploy step %d, checked the branch again", i), start.Add(time.Duration(i)*time.Minute)))
	}
	marathon += marathonSb42.String()
	if err := os.WriteFile(filepath.Join(proj, "marathon.jsonl"), []byte(marathon), 0o644); err != nil {
		t.Fatal(err)
	}
	// A hundred ordinary sessions, each saying "branch" once. Nothing repeats
	// it, so a per-session cap never binds on it.
	for i := range 100 {
		sid := fmt.Sprintf("ordinary-%d", i)
		body := line(sid, fmt.Sprintf("looked at the branch and the tests for task %d", i), start.Add(time.Duration(i)*time.Hour))
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, _, _, idfOf, err := ProjectRelevant(dir, []string{"work/app"}, []string{"quetzalcoatl", "branch"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	subject, ordinary := idfOf["quetzalcoatl"], idfOf["branch"]
	if subject <= ordinary {
		t.Fatalf("the word one session is about scores %.2f, the word everywhere scores %.2f — the signal is inverted", subject, ordinary)
	}
	if subject < dejaVuStrongIDFFloor {
		t.Errorf("a word living in a single session scores %.2f, below the strong floor %.2f — saturating one session made it read as filler",
			subject, dejaVuStrongIDFFloor)
	}
	if ordinary >= dejaVuStrongIDFFloor {
		t.Errorf("a word in a hundred sessions scores %.2f, at or above the strong floor %.2f", ordinary, dejaVuStrongIDFFloor)
	}
}

// The other direction, which is the shape a real store has: a handful of
// marathon sessions hold most of the text, an ordinary word fills them, and a
// subject word is mentioned a few times in slightly more of them. Counting
// sessions then says the subject is the commoner of the two — measured on a
// real store, pgbouncer in 16 sessions scored 1.31 while branch in 13 scored
// 1.50, the most specific word in the store ranking below the most ordinary
// one.
func TestRarityIsNotInvertedByMarathonSessions(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-200 * time.Hour)
	say := func(sid, text string, i int) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":%q}}`,
			sid, start.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), text) + "\n"
	}

	// Thirteen marathons that say "branch" all day long.
	for s := range 13 {
		sid := fmt.Sprintf("marathon-%d", s)
		body := ""
		var bodySb109 strings.Builder
		for i := range 200 {
			bodySb109.WriteString(say(sid, fmt.Sprintf("branch work, run %d", i), s*300+i))
		}
		body += bodySb109.String()
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Sixteen sessions mention "pgbouncer" three times each — more sessions
	// than "branch" lives in, a fraction of the messages.
	for s := range 16 {
		sid := fmt.Sprintf("touched-%d", s)
		body := ""
		var bodySb121 strings.Builder
		for i := range 3 {
			bodySb121.WriteString(say(sid, fmt.Sprintf("pgbouncer pool question %d", i), 5000+s*10+i))
		}
		body += bodySb121.String()
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, _, _, idfOf, err := ProjectRelevant(dir, []string{"work/app"}, []string{"pgbouncer", "branch"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	subject, ordinary := idfOf["pgbouncer"], idfOf["branch"]
	if subject <= ordinary {
		t.Fatalf("the subject scores %.2f and the word filling every marathon scores %.2f — counting whole sessions inverts the signal",
			subject, ordinary)
	}
	// The floor itself is not asserted here: it is set for a store of
	// thousands, and twenty-nine sessions cannot reach it. What this holds is
	// the ordering, which is what was upside down.
	if subject < ordinary*1.5 {
		t.Errorf("the subject scores %.2f against %.2f — separated, but by a margin a threshold cannot sit in",
			subject, ordinary)
	}
}

// seedOneWord indexes a session holding one word.
func seedOneWord(t *testing.T, word string) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/proj",
		"timestamp": "2026-08-20T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "the word " + word + " in the parser"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// deja folds encodings and does not strip diacritics: one word typed NFC or NFD
// is one word (#1098, #1914), and a word written with its marks is a different
// word from the same one written without them. Nothing on the exact tier joins
// them.
//
// The close tier does, in every script. For Latin that fell out of the edit
// limit — café and cafe are one edit apart. Arabic is normally typed without
// harakat, and each mark is a rune of its own, so the same pair sat three edits
// apart and outside the length window besides: one rule, two outcomes, and the
// reader who got nothing was the one whose script writes its vowels as marks
// (#1941). A combining mark is now free on this tier, which is what makes the
// two Arabic rows below the same shape as the two Latin ones.
func TestWhichTierJoinsAWordToItsMarkedForm(t *testing.T) {
	const (
		vowelled = "\u0643\u064e\u062a\u064e\u0628\u064e" // a fatha on each letter
		plain    = "\u0643\u062a\u0628"                   // the same word, as it is normally typed
		accented = "caf\u00e9"
		bare     = "cafe"
	)
	if vowelled == plain || accented == bare {
		t.Fatal("the fixtures are the same string, so the table below says nothing")
	}
	for _, c := range []struct {
		name, stored, query string
		hits                int
		tier                string
	}{
		{"arabic, same form", vowelled, vowelled, 1, query.TierExact},
		{"arabic, marks dropped from the query", vowelled, plain, 1, query.TierClose},
		{"arabic, marks added to the query", plain, vowelled, 1, query.TierClose},
		{"latin, same form", accented, accented, 1, query.TierExact},
		{"latin, accent dropped from the query", accented, bare, 1, query.TierClose},
		{"latin, accent added to the query", bare, accented, 1, query.TierClose},
	} {
		dir := seedOneWord(t, c.stored)
		res, err := SearchDetailed(dir, query.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != c.hits {
			t.Errorf("%s: %d hits, want %d", c.name, len(res.Sessions), c.hits)
		}
		if c.hits > 0 && res.Tier != c.tier {
			t.Errorf("%s: answered on the %q tier, want %q", c.name, res.Tier, c.tier)
		}
	}
}

// The tokenizer is where the Arabic pair was lost before the tier ever ran: a
// mark is not a letter, so it ended the word and the index held single letters
// instead of the word. Hebrew with niqqud is the same shape.
func TestAMarkContinuesTheWordItSitsOn(t *testing.T) {
	for _, c := range []struct {
		name, text, want string
	}{
		{"arabic harakat", "\u0643\u064e\u062a\u064e\u0628\u064e", "\u0643\u064e\u062a\u064e\u0628\u064e"},
		{"hebrew niqqud", "\u05e9\u05b8\u05dc\u05d5\u05b9\u05dd", "\u05e9\u05b8\u05dc\u05d5\u05b9\u05dd"},
		{"thai vowel signs", "\u0e01\u0e34\u0e19", "\u0e01\u0e34\u0e19"},
		{"devanagari matras", "\u0939\u093f\u0928\u094d\u0926\u0940", "\u0939\u093f\u0928\u094d\u0926\u0940"},
	} {
		got := tokens(c.text)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: tokens(%q) = %q, want the one word", c.name, c.text, got)
		}
	}
	// A mark with no letter in front of it still separates, so a stray one
	// cannot glue two words together.
	if got := tokens("alpha \u064e beta"); len(got) != 2 {
		t.Errorf("a leading mark joined words: %q", got)
	}
}

// One marked word in one session must not open the fuzzy tier for every short
// word a reader types. The floor exists to keep three-letter queries and CJK
// bigrams out of variant generation (#338); the exemption for a marked form is
// asked of the term, not of the corpus.
func TestAMarkedWordDoesNotOpenTheFloorForEveryShortQuery(t *testing.T) {
	const vowelled = "\u0643\u064e\u062a\u064e\u0628\u064e"
	dir := seedOneWord(t, vowelled+" the parser")
	// A three-rune typo of "the", which the corpus holds: below the floor, so
	// it must find nothing.
	typo := "t" + "eh"
	res, err := SearchDetailed(dir, query.Options{Query: typo, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("%q was expanded because the corpus held an Arabic word: tier=%q variants=%v",
			typo, res.Tier, res.Variants)
	}
	// The Arabic word itself is still reachable from the way it is typed.
	res, err = SearchDetailed(dir, query.Options{Query: "\u0643\u062a\u0628", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Errorf("the marked word became unreachable: %d hits tier=%q", len(res.Sessions), res.Tier)
	}
}

// Thai tone marks are the whole word: ขาว is white, ข่าว is news, ข้าว is rice.
// A mark being free on the close tier joins them there — that is the tier's
// job — but each must answer its own query exactly, and a word that only lost
// its marks must not outrank a real correction.
func TestThaiToneMarksStillTellWordsApart(t *testing.T) {
	const (
		white = "\u0e02\u0e32\u0e27"
		news  = "\u0e02\u0e48\u0e32\u0e27"
		rice  = "\u0e02\u0e49\u0e32\u0e27"
	)
	dir := seedOneWord(t, rice)
	res, err := SearchDetailed(dir, query.Options{Query: rice, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || res.Tier != query.TierExact {
		t.Errorf("rice did not answer its own query exactly: %d hits tier=%q", len(res.Sessions), res.Tier)
	}
	for _, other := range []string{white, news} {
		res, err := SearchDetailed(dir, query.Options{Query: other, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Tier == query.TierExact {
			t.Errorf("a different Thai word answered on the exact tier: %q", other)
		}
	}
}

// A variation selector says how to draw the rune in front of it. It is a mark
// by category, and gluing it on made an ordinary word a different token.
func TestAVariationSelectorIsNotPartOfTheWord(t *testing.T) {
	if got := tokens("widget\ufe0f"); len(got) != 1 || got[0] != "widget" {
		t.Errorf("tokens(widget+VS16) = %q, want the plain word", got)
	}
	if got := tokens("press 1\ufe0f\u20e3 to continue"); len(got) == 0 {
		t.Fatal("no tokens")
	}
}

// A query of short ASCII terms is below the fuzzy floor and cannot reach a
// marked form either — a mark strips to a non-ASCII base — so it must answer
// without reading the catalog. Reading it is what #338's floor was avoiding
// (#2898).
func TestAShortAsciiQueryDoesNotReadTheCatalog(t *testing.T) {
	dir := seedOneWord(t, "\u0643\u064e\u062a\u064e\u0628\u064e")
	before := catalogReads.Load()
	if _, _, err := fuzzyPostings(dir, []string{"abc", "xyz"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := catalogReads.Load() - before; got != 0 {
		t.Errorf("the catalog was read %d times for a query that cannot match anything", got)
	}
	// A short non-ASCII term does reach it, which is what the exemption is for.
	before = catalogReads.Load()
	if _, _, err := fuzzyPostings(dir, []string{"\u0643\u062a\u0628"}, nil); err != nil {
		t.Fatal(err)
	}
	if catalogReads.Load() == before {
		t.Error("a marked word's plain form never consulted the catalog")
	}
}

// Thai writes without spaces, so a sentence is one run of letters: the
// tokenizer keys the sentence and the reader searching for a word in it got
// nothing. Bigrams are what makes an unspaced script searchable, and Thai now
// gets the ones CJK has had (#2897).
func TestAThaiWordIsFoundInsideASentence(t *testing.T) {
	const sentence = "\u0e09\u0e31\u0e19\u0e0a\u0e2d\u0e1a\u0e01\u0e34\u0e19\u0e02\u0e49\u0e32\u0e27\u0e40\u0e0a\u0e49\u0e32\u0e17\u0e38\u0e01\u0e27\u0e31\u0e19"
	dir := seedOneWord(t, sentence)
	for _, word := range []string{
		"\u0e02\u0e49\u0e32\u0e27",             // rice
		"\u0e40\u0e0a\u0e49\u0e32",             // morning
		"\u0e17\u0e38\u0e01\u0e27\u0e31\u0e19", // every day
	} {
		res, err := SearchDetailed(dir, query.Options{Query: word, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != 1 {
			t.Errorf("%q: %d hits, want the sentence that contains it", word, len(res.Sessions))
		}
	}
	// A Thai word the sentence does not contain is still not in it.
	res, err := SearchDetailed(dir, query.Options{Query: "\u0e2a\u0e38\u0e19\u0e31\u0e02", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("an absent word matched the sentence: %d hits", len(res.Sessions))
	}
}

// German writes ä, ö, ü and ß as ae, oe, ue and ss wherever the letter is not
// available, so a name stored as Müller is asked for as mueller. muller reached
// it as one edit; mueller is two and a letter longer, and found nothing (#4690).
func TestAnUmlautMeetsItsASCIISpelling(t *testing.T) {
	for _, c := range []struct{ stored, query string }{
		{"Müller", "mueller"},
		{"Müller", "muller"},
		{"mueller", "Müller"},
		{"Straße", "strasse"},
		{"Größe", "groesse"},
		{"Ärger", "aerger"},
		{"mueller", "Mu\u0308ller"}, // typed decomposed
	} {
		dir := seedOneWord(t, c.stored)
		res, err := SearchDetailed(dir, query.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != 1 || res.Tier != query.TierClose {
			t.Errorf("%q stored, %q asked: %d hits on tier %q, want 1 on close", c.stored, c.query, len(res.Sessions), res.Tier)
		}
	}
	// The digraph is an exact spelling, not one more edit on top of the others.
	dir := seedOneWord(t, "Müller")
	if res, _ := SearchDetailed(dir, query.Options{Query: "muellex", All: true}); len(res.Sessions) != 0 {
		t.Errorf("muellex reached Müller")
	}
}

// Twice in one day a constant was added between a doc block and the function
// it documented, so Go attached the whole comment to the constant and the
// function lost its documentation entirely. Both compiled, both vetted clean,
// and both read fine in the source — the comment sits above *something*, and
// the eye does not notice that the something changed. It only shows in
// `go doc`, which nobody runs for unexported identifiers (#645).
//
// This is checkable because the repo follows one convention without exception:
// a doc block starts with the name of what it documents. So a block whose
// first word names a different declaration is a comment that drifted.
func TestDocCommentsNameWhatTheyDocument(t *testing.T) {
	roots := []string{"..", filepath.Join("..", "..", "cmd")}
	seen := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			// Other tests create and delete scratch trees while this walks, so
			// a vanished entry is normal rather than a failure.
			if err != nil {
				return nil //nolint:nilerr // a file that disappeared mid-walk is not this test's business
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." && info.Name() != ".." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if perr != nil {
				return nil //nolint:nilerr // not this test's business
			}
			for _, decl := range f.Decls {
				name, doc := declNameAndDoc(decl)
				if name == "" || doc == nil || len(doc.List) == 0 {
					continue
				}
				first := firstWord(doc.Text())
				if first == "" || first == name {
					continue
				}
				seen++
				// Only flag when the first word names some *other* declaration
				// in the same file: prose legitimately starts with any word.
				if declaredIn(f, first) {
					t.Errorf("%s: doc block above %q begins with %q, which is declared elsewhere in this file — the comment drifted off its declaration (`go doc -u` will show it on the wrong one)",
						fset.Position(decl.Pos()), name, first)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("walked no doc comments at all; the check is not looking at anything")
	}
}

func declNameAndDoc(d ast.Decl) (string, *ast.CommentGroup) {
	switch v := d.(type) {
	case *ast.FuncDecl:
		return v.Name.Name, v.Doc
	case *ast.GenDecl:
		// A grouped declaration's doc block describes the group, not its first
		// member: "// Event kinds." above a const block of six is correct and
		// naming a type that happens to exist is a coincidence.
		if len(v.Specs) != 1 {
			return "", nil
		}
		switch s := v.Specs[0].(type) {
		case *ast.TypeSpec:
			return s.Name.Name, v.Doc
		case *ast.ValueSpec:
			if len(s.Names) > 0 {
				return s.Names[0].Name, v.Doc
			}
		}
	}
	return "", nil
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`*_,.:;()")
}

func declaredIn(f *ast.File, name string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			if v.Name.Name == name {
				found = true
			}
		case *ast.TypeSpec:
			if v.Name.Name == name {
				found = true
			}
		case *ast.ValueSpec:
			for _, id := range v.Names {
				if id.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// doubledCommentHead reports the text a comment line repeats after a second
// `//` on the same line. That is what a patch leaves when it inserts at the
// head of an existing block: the guard above is satisfied — the block still
// begins with the right name — and the eye reads the first half and stops
// (#2508).
//
// Narrow on purpose. A second marker alone is ordinary: comments quote one
// ("// Event kinds."), and paths contain one (github.com/x/y//issues). Only a
// tail that begins with its own head counts, and only when that head is more
// than a word long.
func doubledCommentHead(line string) (string, bool) {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
	for i := strings.Index(body, "//"); i > 0; {
		head := strings.TrimSpace(body[:i])
		tail := strings.TrimSpace(body[i+2:])
		if len(head) >= 20 && strings.HasPrefix(tail, head) {
			return head, true
		}
		next := strings.Index(body[i+2:], "//")
		if next < 0 {
			break
		}
		i += next + 2
	}
	return "", false
}

// TestNoDocCommentRepeatsItsOwnHead walks every comment in the tree, not only
// doc blocks: a doubled head is a patch artifact and lands wherever the patch
// did.
func TestNoDocCommentRepeatsItsOwnHead(t *testing.T) {
	roots := []string{"..", filepath.Join("..", "..", "cmd")}
	seen := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a file that disappeared mid-walk is not this test's business
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." && info.Name() != ".." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if perr != nil {
				return nil //nolint:nilerr // not this test's business
			}
			for _, group := range f.Comments {
				for _, c := range group.List {
					seen++
					if head, ok := doubledCommentHead(c.Text); ok {
						t.Errorf("%s: this comment line repeats itself — %q appears twice, which is what a patch applied at the head leaves behind",
							fset.Position(c.Pos()), head)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("walked no comments at all; the check is not looking at anything")
	}
}

func TestDoubledCommentHead(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{line: "// decisionUsable rejects a session whose decision should not be reused:// decisionUsable rejects a session whose decision should not be reused: one that", want: true},
		{line: "// member: \"// Event kinds.\" above a const block of six is correct and", want: false},
		{line: "// reading github.com/x/y//issues\"). So reject a line that BEGINS like code,", want: false},
		{line: "// A // inside a string is part of the value, not a comment.", want: false},
		{line: "// short//short", want: false},
		{line: "// plain prose with no marker at all", want: false},
	} {
		if _, got := doubledCommentHead(tc.line); got != tc.want {
			t.Errorf("doubledCommentHead(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// The per-message reading is priced for a big store and skipped on a small one,
// where the session-level overlap usually ranks well enough — usually, and the
// exception is what this gate is for. A crowded top is the case where it does
// not: on LoCoMo's relevance tier a relative distance under 0.05 between the
// first two scores covers 165 questions of which 27% were answered right,
// against 94% where the distance is 0.60 or more.
//
// So the two halves both have to hold: a crowded top is doubtful, a clear one is
// not. A gate that says yes to everything is the "fuse everywhere" setting,
// which the sweep beside doubtRerankGap measures as worse on both benchmarks.
func TestACrowdedTopIsDoubtfulAndAClearOneIsNot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scores []float64
		want   bool
	}{
		{"the second is on the first's heels", []float64{4.0, 3.9}, true},
		{"the first is clear of the field", []float64{4.0, 0.9}, false},
		{"exactly at the gap is not under it", []float64{1.0, 1 - doubtRerankGap}, false},
		{"one candidate has no second to crowd it", []float64{4.0}, false},
		{"a scoreless ranking is not a doubtful one", nil, false},
		// A top score of zero divides to NaN, which compares false on its own,
		// so only a negative one shows what the guard is holding: without it
		// the distance comes out negative and every such ranking reads as
		// crowded.
		{"a zero top score says nothing either way", []float64{0, 0}, false},
		{"a negative top score is not a crowded one", []float64{-1, -2}, false},
	} {
		if got := rankingIsDoubtful(relevanceRanking{scores: tc.scores}); got != tc.want {
			t.Errorf("%s: doubtful=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// A result line elides the middle of a long id, and the text a reader copies
// carries the "…" itself — which appears in no id, so neither the prefix nor
// the substring match from #707 can ever hit it (#853).
func TestFindByPrefixReadsAnElidedId(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	other := "b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0"
	when := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
		"claude:" + long:  {ID: long, Harness: "claude", Project: "p", Started: when, Updated: when},
		"claude:" + other: {ID: other, Harness: "claude", Project: "p", Started: when, Updated: when},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	// Exactly what `short()` prints for a 40-character id.
	elided := "a1b2c3d4e…d6e7f8a9b0"
	if n := PrefixMatches(dir, elided); n != 1 {
		t.Errorf("the id printed on screen resolved to %d sessions, want 1", n)
	}
	// A head and tail that belong to no session must still miss.
	if n := PrefixMatches(dir, "zzzz…zzzz"); n != 0 {
		t.Errorf("an elision matching nothing resolved to %d sessions", n)
	}
	// A plain prefix keeps working, and so does the substring fallback.
	if n := PrefixMatches(dir, "a1b2c3d4"); n != 1 {
		t.Errorf("plain prefix broke: %d", n)
	}
	if n := PrefixMatches(dir, "c9d0e1f2"); n != 1 {
		t.Errorf("substring fallback broke: %d", n)
	}
}

// EnsureUnlessBusy skips the wait only when there is an index to answer from.
// With one on disk and the lock held it says busy at once; with none yet it
// waits for the pass that is building it, as Ensure does, because answering
// from nothing would print an empty listing over a store full of sessions.
func TestEnsureUnlessBusyWaitsOnlyWhenThereIsNothingToRead(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"busy1","timestamp":"2026-01-02T03:04:05Z",` +
		`"message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "busy1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	built := filepath.Join(tmp, "built.db")
	if err := Ensure(built, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, err := readManifest(built); err != nil || len(m.Sessions) != 1 || m.RecordsSize <= 0 {
		t.Fatalf("the seed did not index one session with records: %d sessions, %d record bytes (%v)", len(m.Sessions), m.RecordsSize, err)
	}
	unlock, err := lockDir(built)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		busy, ensureErr := EnsureUnlessBusy(built, nil)
		if ensureErr != nil {
			t.Errorf("held lock over a built index: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case busy := <-done:
		if !busy {
			t.Error("a held lock over a built index was not reported busy")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waited for the lock with an index on disk to answer from")
	}
	unlock()

	empty := filepath.Join(tmp, "empty.db")
	unlock, err = lockDir(empty)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		busy, ensureErr := EnsureUnlessBusy(empty, nil)
		if ensureErr != nil {
			t.Errorf("first build: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case <-done:
		unlock()
		t.Fatal("returned with no index on disk while another deja was building one")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case busy := <-done:
		if busy {
			t.Error("reported busy after waiting for the first build")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not finish after the lock was released")
	}
	if !ReadableSnapshot(empty) {
		t.Error("the wait did not end in a built index")
	}

	// A record log shorter than the manifest says: the sessions are listed but
	// their messages are gone, so `show` would print an empty session.
	if err = os.Truncate(filepath.Join(built, "records.bin"), 0); err != nil {
		t.Fatal(err)
	}
	unlock, err = lockDir(built)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		busy, ensureErr := EnsureUnlessBusy(built, nil)
		if ensureErr != nil {
			t.Errorf("torn record log: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case <-done:
		unlock()
		t.Fatal("answered from a torn record log instead of waiting for the rebuild")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case busy := <-done:
		if busy {
			t.Error("reported busy after waiting out the torn record log")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not finish after the lock was released")
	}
	m, err := readManifest(built)
	if err != nil || !recordsIntact(built, m) {
		t.Errorf("the wait did not end in an intact record log: %v", err)
	}
}

// The PreToolUse warning asks by command shape: a run that failed before is
// found under another spelling of the same command, one that passed is not,
// and the trust policy can hide a project's failures.
func TestCommandFailedBeforeFindsTheShapeThatFailed(t *testing.T) {
	c, tmp := newCFStore(t)
	for i := range 4 {
		c.write("app", i, "fix the store test")
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	f, ok := CommandFailedBefore(dir, "  go test ./...  ", nil)
	if !ok {
		t.Fatalf("a command that failed in every session is not on file: %+v", ReadCommandFails(dir))
	}
	if f.Head != CommandHead("go test ./...") || f.Sessions < 2 || f.Line == "" {
		t.Errorf("failure on file = %+v", f)
	}
	if _, ok := CommandFailedBefore(dir, "go build ./...", nil); ok {
		t.Error("a command that always passed reads as failed before")
	}
	if _, ok := CommandFailedBefore(dir, "cd", nil); ok {
		t.Error("a command with no shape matched a failure")
	}
	if _, ok := CommandFailedBefore(dir, "go test ./...", func(string) bool { return false }); ok {
		t.Error("a failure from a project the policy hides was handed back")
	}
}

// `deja index` says whether there was anything to do: true right after a
// build, false once a transcript grew, and false for a store that is not there.
func TestUpToDateFollowsTheTranscripts(t *testing.T) {
	c, tmp := newCFStore(t)
	c.write("app", 0, "fix the store test")
	c.write("app", 1, "fix the build")
	dir := filepath.Join(tmp, "index.db")
	if ok, n := UpToDate(dir, ""); ok || n != 0 {
		t.Errorf("before any build: UpToDate = %v, %d", ok, n)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if ok, n := UpToDate(dir, ""); !ok || n != 2 {
		t.Errorf("right after a build: UpToDate = %v, %d, want true, 2", ok, n)
	}
	c.appendTurn("app", 1, 5, "go vet ./...", "./store.go:12:2: undefined: Flock")
	if ok, n := UpToDate(dir, ""); ok || n != 2 {
		t.Errorf("after a transcript grew: UpToDate = %v, %d, want false, 2", ok, n)
	}
}

// A search inside a tool call never waits: a held lock reports busy, a free
// one refreshes the index, and a store whose lock cannot be written at all is
// answered from as it stands (#1804).
func TestEnsureForSearchNoWait(t *testing.T) {
	c, tmp := newCFStore(t)
	c.write("app", 0, "fix the store test")
	dir := filepath.Join(tmp, "locked", "idx")
	o := query.Options{Query: "store", All: true}

	unlock, ok, err := tryLockDir(dir)
	if err != nil || !ok {
		t.Fatalf("taking the lock: %v %v", ok, err)
	}
	busy, err := EnsureForSearchNoWait(dir, o, io.Discard)
	unlock()
	if err != nil || !busy {
		t.Fatalf("with the lock held: busy=%v err=%v, want busy", busy, err)
	}
	if HasManifest(dir) {
		t.Fatal("a call that reported busy built the index anyway")
	}

	busy, err = EnsureForSearchNoWait(dir, o, io.Discard)
	if err != nil || busy {
		t.Fatalf("with the lock free: busy=%v err=%v", busy, err)
	}
	if ok, n := UpToDate(dir, ""); !ok || n != 1 {
		t.Errorf("after the refresh: UpToDate = %v, %d, want true, 1", ok, n)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	_ = os.Remove(dir + ".lock")
	locked := filepath.Dir(dir)
	if err = os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if !lockUnwritable(dir) {
		t.Error("a lock in a read-only directory reads as writable")
	}
	busy, err = EnsureForSearchNoWait(dir, o, io.Discard)
	if err != nil || busy {
		t.Errorf("read-only store: busy=%v err=%v, want an answer now", busy, err)
	}
}

// Callers outside the package read a session's turns of several kinds in the
// order they were written, and only those kinds.
func TestEachRecordInRolesKeepsOnlyTheAskedKinds(t *testing.T) {
	dir := lineageStore(t)
	var order []string
	err := EachRecordInRoles(dir, []string{"user", "assistant"}, func(m SessionMeta, r Record) {
		if r.Role != "user" && r.Role != "assistant" {
			t.Errorf("record of role %q handed back", r.Role)
		}
		if m.ID == "a" {
			order = append(order, r.Role+":"+r.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"user:the retry loop spins forever", "assistant:the backoff never resets", "user:reset it on success"}
	if len(order) != len(want) {
		t.Fatalf("a's turns = %q, want %q", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("turn %d = %q, want %q", i, order[i], want[i])
		}
	}
	n := 0
	if err := EachRecordInRoles(dir, []string{"assistant"}, func(SessionMeta, Record) { n++ }); err != nil {
		t.Fatal(err)
	}
	// a, c and d each hold the one assistant turn they share.
	if n != 3 {
		t.Errorf("assistant turns = %d, want 3", n)
	}
}

// Counted over 102,735 commands and 2,951 failures in real transcripts: two
// thirds of the failures repeat an error the store has already seen, and 44% of
// those were first seen in a different session. The walls below are the ones
// that dominate that count and that nothing here recognised, so `deja friction`
// listed other errors, `deja fix` returned nothing, and the hook at the failure
// stayed silent through 89 sightings of the same shell mistake.
func TestTheWallsThatDominateTheCount(t *testing.T) {
	walls := []string{
		// zsh answering `[ "$a" == "$b" ]`. Twelve characters once the shell's
		// position marker is stripped, and the length bound dropped it.
		"(eval):1: == not found",
		"zsh:1: === not found",
		// A command that ran out of time. The wording is the harness's, and
		// "connection timed out" never covered it.
		"Command timed out after 10m 0s",
		// The harness refusing the agent's own call.
		"<tool_use_error>InputValidationError: [ { expected: string }]",
		"Error: File has been modified since read, either by the user or by a linter",
		"Error: No such tool available: Write. Write is disabled for this session",
		// The tail of a Python traceback, beyond the four exception names that
		// were listed.
		"json.decoder.JSONDecodeError: Expecting value: line 1 column 1 (char 0)",
		"ValueError: invalid literal for int() with base 10",
		"FileNotFoundError: [Errno 2] No such file",
	}
	for _, w := range walls {
		if _, ok := FrictionLine(w); !ok {
			t.Errorf("not recognised as a wall: %q", w)
		}
	}
}

// The shell marker says an error follows, not that everything after it is one.
func TestTheShellMarkerDoesNotLetJunkThrough(t *testing.T) {
	notWalls := []string{
		// deja quoting itself: its own report comes back as tool output in the
		// next session, and this is exactly the shape that taught it about
		// itself before.
		"zsh:1: command not found: timeout · 2026-01-02",
		// Source that talks about a shell error.
		`zsh:1: echo "command not found: $BIN"`,
		// Too little left to recognise.
		"zsh:1: killed",
		// A colon and a number, but not a shell.
		"config:1: retries not found",
	}
	for _, l := range notWalls {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("read as a wall: %q", l)
		}
	}
}

// writeTestStore builds a real index directory from sessions given in memory.
func writeTestStore(t *testing.T, dir string, ss ...model.Session) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func mkOptions(q string) query.Options { return query.Options{Query: q, All: true} }

// A pasted error carries paths, line numbers and goroutine ids, so an AND over
// its words matches nothing and the word ladder falls through to ranking by
// whichever token happens to be rare. The session that hit that exact error is
// already fingerprinted; match on the fingerprint.
func TestPastedErrorFindsTheSessionThatHitIt(t *testing.T) {
	dir := t.TempDir()
	hit := model.Session{
		Harness: "claude", ID: "hit", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "starting the worker"},
			{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"},
			{Role: "command", Text: "uv pip install aiokafka"},
		},
	}
	other := model.Session{
		Harness: "claude", ID: "other", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "unrelated work on the parser"},
			{Role: "assistant", Text: "renamed the token stream"},
		},
	}
	writeTestStore(t, dir, hit, other)

	// The paste is not the stored line: different file, different line number,
	// a traceback around it. Only the error itself is shared.
	paste := "Traceback (most recent call last):\n" +
		"  File \"/srv/app/main.py\", line 7, in <module>\n" +
		"ModuleNotFoundError: No module named 'aiokafka'\n"
	res, err := SearchDetailed(dir, mkOptions(paste))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the pasted error found nothing")
	}
	if res.Sessions[0].ID != "hit" {
		t.Errorf("wrong session first: %q", res.Sessions[0].ID)
	}
}

// The tier serves the neighbourhood of the error rather than the error line
// alone: what a reader needs is the command that came after it.
func TestErrorTierCarriesWhatFollowedTheError(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "hit", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "starting the worker"},
			{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"},
			{Role: "command", Text: "uv pip install aiokafka"},
		},
	})
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := errorSigSearch(dir, m, mkOptions("ModuleNotFoundError: No module named 'aiokafka'"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(res.Sessions))
	}
	var text string
	var textSb91 strings.Builder
	for _, msg := range res.Sessions[0].Messages {
		textSb91.WriteString(msg.Text + "\n")
	}
	text += textSb91.String()
	if !strings.Contains(text, "uv pip install aiokafka") {
		t.Errorf("the recovery is not in the result:\n%s", text)
	}
}

// An ordinary question must not be routed through the error tier: it has no
// error line, so the tier stays silent and the word ladder keeps the case.
func TestOrdinaryQuestionIsNotTreatedAsAnError(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"}},
	})
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := errorSigSearch(dir, m, mkOptions("how do we deploy the worker"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("a question with no error line was answered by the error tier: %+v", res.Sessions)
	}
}

// A long trace carries several error lines; the session that hit more of them
// is the closer match, ahead of a fresher session that hit only one. And the
// result reports Total/Capped honestly when more than the window matched.
func TestErrorTierRanksByHowMuchOfThePasteMatched(t *testing.T) {
	now := time.Now()
	// "broad" hits two of the pasted error lines; "narrow" is newer but hits one.
	broad := model.Session{
		Harness: "claude", ID: "broad", Project: "p", Updated: now.Add(-time.Hour),
		Messages: []model.Message{
			{Role: "tool-output", Text: "panic: runtime error: invalid memory address"},
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "restarted postgres and added a nil guard"},
		},
	}
	narrow := model.Session{
		Harness: "claude", ID: "narrow", Project: "p", Updated: now,
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "brew services start postgresql"},
		},
	}
	dir := t.TempDir()
	writeTestStore(t, dir, broad, narrow)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	paste := "panic: runtime error: invalid memory address\n...\npsql: connection refused on port 5432\n"
	res, err := errorSigSearch(dir, m, mkOptions(paste))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) < 2 {
		t.Fatalf("both sessions should match, got %d", len(res.Sessions))
	}
	if res.Sessions[0].ID != "broad" {
		t.Errorf("the session that hit more of the paste is not first: %q", res.Sessions[0].ID)
	}
	if res.Tier != query.TierError {
		t.Errorf("wrong tier %q", res.Tier)
	}
}

// Total/Capped must be honest: a pool bigger than the window says "showing N of
// M" (Capped true, Total = full count); a small pool is not capped even when the
// neighbourhood filter dropped a session (that is not the window hiding one).
func TestErrorTierTotalAndCapped(t *testing.T) {
	dir := t.TempDir()
	// 60 sessions all hitting the same error line — more than relevanceWindow.
	ss := make([]model.Session, 0, 60)
	for i := range 60 {
		id := "s" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		ss = append(ss, model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			},
		})
	}
	writeTestStore(t, dir, ss...)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := errorSigSearch(dir, m, mkOptions("psql: connection refused on port 5432"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 60 {
		t.Errorf("Total = %d, want 60 (the full match count, not the window)", res.Total)
	}
	if !res.Capped {
		t.Error("a match pool larger than the window must report Capped")
	}
	if len(res.Sessions) > relevanceWindow {
		t.Errorf("served %d sessions, past the window of %d", len(res.Sessions), relevanceWindow)
	}

	// A small pool is not capped.
	dir2 := t.TempDir()
	writeTestStore(t, dir2, model.Session{
		Harness: "claude", ID: "one", Project: "p",
		Messages: []model.Message{{Role: "tool-output", Text: "psql: connection refused on port 5432"}},
	})
	m2, err := readManifest(dir2)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := errorSigSearch(dir2, m2, mkOptions("psql: connection refused on port 5432"))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Capped {
		t.Errorf("a single-session match reported Capped: total=%d served=%d", res2.Total, len(res2.Sessions))
	}
}

// DEJA_EXCLUDE_HARNESSES kept a store out of a rebuild and nothing else: every
// incremental pass walked it and indexed what it had written since (#5394).
func TestAnExcludedStoreIsNotWalkedOnAnyPass(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CODEX_ROOT": "codex"})
	codexSession := func(id, text string) string {
		roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
		parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
			`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"`+text+`"}]}}`+"\n"+
			`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
		return roll
	}
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-app", "c1.jsonl"),
		`{"type":"user","sessionId":"c1","cwd":"/tmp/app","timestamp":"2026-07-17T08:00:00Z","message":{"role":"user","content":"why does the quokkabloom retry"}}`+"\n", false)
	const before, after = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001", "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0002"
	indexed := func(dir, harness, id string) bool {
		return sessionView(t, dir, harness, id) != "(not in the index)\n"
	}
	codexSession(before, "indexed before the store was excluded")
	dir := filepath.Join(tmp, "idx")
	parityPass(t, dir, false)
	if !indexed(dir, "codex", before) {
		t.Fatal("control: the codex session was not indexed with nothing excluded")
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	newRoll := codexSession(after, "written after the store was excluded")
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-app", "c2.jsonl"),
		`{"type":"user","sessionId":"c2","cwd":"/tmp/app","timestamp":"2026-07-17T10:00:00Z","message":{"role":"user","content":"a second claude session"}}`+"\n", false)

	codexRoot := filepath.Join(tmp, "codex") + string(filepath.Separator)
	for p := range currentFiles("") {
		if strings.HasPrefix(p, codexRoot) {
			t.Errorf("the walk listed %s from an excluded store", p)
		}
	}
	if n := HarnessUnreadCounts(dir)["codex"]; n != 0 {
		t.Errorf("doctor's unread count walked the excluded store: %d", n)
	}

	// The stale check, the search refresh and `deja index` each take the walk.
	if _, err := EnsureForSearchStale(dir, query.Options{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	parityPass(t, dir, false)
	if indexed(dir, "codex", after) {
		t.Errorf("an incremental pass indexed %s from the excluded store", filepath.Base(newRoll))
	}
	if !indexed(dir, "claude", "c2") {
		t.Error("control: the claude session written alongside was not indexed")
	}
	// What was indexed before stays until a rebuild, as with a project
	// pattern, and the pass that keeps it settles.
	if !indexed(dir, "codex", before) {
		t.Error("the incremental pass dropped the session indexed before the exclusion")
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Error("the index never settles while a store is excluded")
	}
	parityPass(t, dir, true)
	if indexed(dir, "codex", before) || indexed(dir, "codex", after) {
		t.Error("a rebuild kept the excluded store's sessions")
	}
}

// The agent's own live session holds every word of its question, because the
// question came from it, and recall leaves that session out. With it gone the
// search used to stop at the empty strict tier and answer nothing, though the
// session that settled the question ranked for it (#4766).
func TestExcludingTheOnlyFullMatchFallsThroughToTheRanking(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-pay")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("live", "what jitter constant did we pick for the payments retry backoff")
	write("old", "payments retry backoff: we settled on full jitter with a 30s cap")
	write("readme", "unrelated readme typo fix")
	write("toolchain", "bumped the go toolchain")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	q := "jitter constant payments retry backoff"
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true, ExcludeSessions: map[string]bool{"live": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "old" {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("got %v (tier %q), want the session that settled it", ids, r.Tier)
	}
	for _, s := range r.Sessions {
		if s.ID == "live" {
			t.Fatal("the excluded session came back")
		}
	}
	// A regex skips the postings and scans records, which never looked at the
	// exclusion at all.
	rx, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "jitter", Regex: true, All: true, ExcludeSessions: map[string]bool{"live": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rx.Sessions {
		if s.ID == "live" {
			t.Fatal("a regex search served the excluded session")
		}
	}
	if len(rx.Sessions) == 0 {
		t.Fatal("the regex found nothing, so the exclusion was never exercised")
	}
}

// The exit status is written as a suffix — two spaces, the marker, the digits,
// end of record — and it was read anywhere in the record, so a command whose
// own text carries a line ending that way was taken for a command that failed
// and dropped from the pairs (#2820, the trap #2048 recorded for the hook's
// reading of the same suffix).
func TestTheMarkerCountsOnlyWhereItIsWritten(t *testing.T) {
	now := time.Now()
	script := "docker compose run --rm db sh -c 'psql\n  → exit 1\nselect 1'"
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: script, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "db is up", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("a command carrying the marker in its own text was read as a failed one: %+v", pairs)
	}
}

// The parser is what the three readers share, so the decisions live here: a
// token that is not a number is prose that ends like the marker rather than a
// status (#2048), a status is read through trailing whitespace, and it counts
// only where a source writes it — at the end of the record, after the last
// line of a multi-line script (internal/sources/codex.go).
func TestTheExitOutcomeIsReadWhereItIsWritten(t *testing.T) {
	for _, c := range []struct {
		text     string
		cmd      string
		code     int
		recorded bool
	}{
		{"$ make test  → exit 2", "$ make test", 2, true},
		{"$ make test  → exit 0", "$ make test", 0, true},
		{"$ make test  → exit 2\n", "$ make test", 2, true},
		// Prose that ends like the marker: kept whole.
		{"pg_isready -h db  → exit 0x1", "pg_isready -h db  → exit 0x1", 0, false},
		{`echo "  → exit later"`, `echo "  → exit later"`, 0, false},
		// Not where it is written: a line of the command's own text.
		{"run.sh\n  → exit 1\nmore", "run.sh\n  → exit 1\nmore", 0, false},
		// Where it is written, for a script: after the last line.
		{"python - <<'EOF'\nprint(1)\nEOF  → exit 1", "python - <<'EOF'\nprint(1)\nEOF", 1, true},
		{"go build ./...", "go build ./...", 0, false},
	} {
		cmd, code, recorded := CommandExitOutcome(c.text)
		if cmd != c.cmd || code != c.code || recorded != c.recorded {
			t.Errorf("CommandExitOutcome(%q) = %q, %d, %v; want %q, %d, %v",
				c.text, cmd, code, recorded, c.cmd, c.code, c.recorded)
		}
		// And the two older readers say the same thing, because they are this
		// one.
		if got := withoutExitStatus(c.text); got != cmd {
			t.Errorf("withoutExitStatus(%q) = %q, want %q", c.text, got, cmd)
		}
		if got, ok := CommandExitStatus(c.text); got != code || ok != recorded {
			t.Errorf("CommandExitStatus(%q) = %d, %v; want %d, %v", c.text, got, ok, code, recorded)
		}
	}
}

// exportStore holds one session recall may serve and one the ignore rule keeps
// out, each saying something only it says.
func exportStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	// DEJA_POLICY_FILE, not a file under the home: this package shares one
	// XDG_CONFIG_HOME across its tests, so a policy written through
	// policy.Path() outlives the test that wrote it and every later test reads
	// it — which is how a rule of `*scratch*` silenced the default rule three
	// tests were pinning.
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id, text string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep", "k1", "the widget pipeline stalls on shard three")
	write("scratch", "s1", "scratch work nobody should see off this machine")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exported(t *testing.T, outDir string) string {
	t.Helper()
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	return all.String()
}

// The export applies the exclude list, and its comment says why: the export is
// where data leaves, and a privacy control set after the index was built has to
// hold at that boundary. The ignore rule is the same kind of control and was
// not applied, so the one tree deja never recalls from was shipped to another
// machine, where it is served as imported memory (#2654).
func TestExportLeavesTheIgnoredTreeBehind(t *testing.T) {
	dir := exportStore(t)
	out := t.TempDir()
	n, err := Export(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	body := exported(t, out)
	if strings.Contains(body, "nobody should see off this machine") {
		t.Fatalf("the ignored tree left the machine:\n%s", body)
	}
	if !strings.Contains(body, "widget pipeline stalls") {
		t.Fatalf("the session recall may serve did not travel:\n%s", body)
	}
	if n < 1 {
		t.Fatalf("nothing was exported at all")
	}
}

// Held, not settled: the exclude path keeps a record's watermark unmoved so a
// rule lifted later still sends it. The same has to hold here, or a rule set
// once would settle that work for good.
func TestExportCanStillSendWhatTheRuleStopsCovering(t *testing.T) {
	dir := exportStore(t)
	if _, err := Export(dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// The reader changes their mind.
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"ignore":["*nothing-here*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := Export(dir, out); err != nil {
		t.Fatal(err)
	}
	if body := exported(t, out); !strings.Contains(body, "nobody should see off this machine") {
		t.Fatalf("work held back by a rule that no longer covers it never travels:\n%s", body)
	}
}

// A machine that has imported a peer's work and then exports its own must send
// only its own: what arrived from elsewhere belongs to the machine it came
// from, and a second hop would spread it past whoever agreed to the first.
// The roundtrip test holds this for an index that carries nothing else
// (TestSyncRoundtripAllHarnesses); this is the case a filter would actually
// break — local work and imported work side by side (#2450).
func TestAnExportCarriesOwnWorkAndNotAPeersToo(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index.db")
	store := filepath.Join(root, "claude", "-work-ownwork")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	setHome(t, root)
	at := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	own := `{"type":"user","sessionId":"own1","timestamp":"` + at + `","cwd":"/work/ownwork",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "own1.jsonl"), []byte(own+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := filepath.Join(root, "peer-batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peer1","project":"work/peerwork","role":"user",` +
		`"text":"the client ledger cutover on the other machine","time":"` + at + `","origin":"bravo"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(peer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, batch); err != nil || n == 0 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(root, "outgoing")
	n, err := ExportFull(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("the machine's own work did not go out at all")
	}
	files, err := filepath.Glob(filepath.Join(out, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Project string `json:"project"`
				Text    string `json:"text"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("a batch line does not decode: %v", err)
			}
			if strings.Contains(rec.Project, "peerwork") || strings.Contains(rec.Text, "other machine") {
				t.Errorf("a peer's session went out in this machine's export: %s", rec.Project)
			}
		}
	}
}

// Pointing sync export at a file is the same mistake as pointing sync import at
// one, and only the import side said so; export handed back the raw
// `mkdir /…: not a directory` (#1112).
func TestExportToAFileSaysWhatItWanted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(tmp, "claude", "-tmp-p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"a decision"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Export(dir, target)
	if err == nil {
		t.Fatal("export accepted a file as its output directory")
	}
	if !strings.Contains(err.Error(), "is a file; sync export wants a directory") {
		t.Errorf("raw error came back: %v", err)
	}
}

// A first build has nothing to answer with until it finishes, and on a large
// corpus that is the wait a user decides in (#505). The newest slice is
// published first, so the wait is for the tail rather than for everything.
func TestAFirstBuildAnswersBeforeItFinishes(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	ss := make([]model.Session, 0, 500)
	for i := range 500 {
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated: now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("session %d talked about the zephyrine%d rollout", i, i)}},
		})
	}
	publishNewestFirst(dir, ss, io.Discard)

	if !HasManifest(dir) {
		t.Fatal("nothing was published, so the build still answers nothing until it ends")
	}
	// The newest sessions are there and searchable, and it is the newest they
	// are: a slice of the oldest would answer nothing anyone is about to ask.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != partialPublishSessions {
		t.Errorf("the slice holds %d sessions, want %d", len(metas), partialPublishSessions)
	}
	held := map[string]bool{}
	for _, meta := range metas {
		held[meta.ID] = true
	}
	if !held["s000"] || !held["s199"] {
		t.Errorf("the newest sessions are not the ones published: %v", ids(sessionsOfMetas(metas)))
	}
	if held["s499"] {
		t.Error("the oldest session was published ahead of newer ones")
	}
	res, err := SearchDetailed(dir, query.Options{Query: "zephyrine0", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Error("the newest session is not searchable in the published slice")
	}
	// And it claims no file coverage: the state of the files is what tells the
	// next incremental run what it may skip, and this index holds a slice.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 {
		t.Errorf("the published slice claimed %d files as parsed", len(m.Files))
	}
}

// It publishes only where the wait is real, and never over an index that
// already answers.
func TestTheEarlyPublishHoldsBackWhereItWouldNotHelp(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	now := time.Now()
	small := make([]model.Session, 0, 10)
	for i := range 10 {
		small = append(small, model.Session{
			ID: fmt.Sprintf("s%d", i), Harness: "claude", Project: "p", Updated: now,
			Messages: []model.Message{{Role: "user", Text: "a short store"}},
		})
	}
	dir := filepath.Join(tmp, "small")
	publishNewestFirst(dir, small, io.Discard)
	if HasManifest(dir) {
		t.Error("a corpus that builds fast anyway paid for a second pass")
	}

	// And where an index already exists, replacing it with a slice would be a
	// regression rather than a head start.
	dir2 := filepath.Join(tmp, "existing")
	many := make([]model.Session, 0, 500)
	for i := range 500 {
		many = append(many, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("old session %d", i)}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir2+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir2+".tmp", dir2, many, nil, ""); err != nil {
		t.Fatal(err)
	}
	res, err := SearchDetailed(dir2, query.Options{Query: "old session 499", All: true})
	if err != nil {
		t.Fatal(err)
	}
	before := len(res.Sessions)
	publishNewestFirst(dir2, many, io.Discard)
	res, err = SearchDetailed(dir2, query.Options{Query: "old session 499", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != before {
		t.Errorf("an index that already answered was replaced by a slice: %d sessions, was %d",
			len(res.Sessions), before)
	}
}

// sessionsOfMetas is for the failure message only: what got published.
func sessionsOfMetas(metas []SessionMeta) []model.Session {
	out := make([]model.Session, 0, len(metas))
	for _, meta := range metas {
		out = append(out, model.Session{ID: meta.ID, Harness: meta.Harness})
	}
	return out
}

// And the build itself says so: the line is what tells a person the wait is
// for the tail rather than for everything.
func TestARealFirstBuildSaysItIsSearchableAlready(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := filepath.Join(tmp, "claude")
	now := time.Now()
	for i := range 420 {
		at := now.Add(-time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("b%03d", i)
		writeLines(t, filepath.Join(claude, "-proj", id+".jsonl"),
			claudeLine(id, at, fmt.Sprintf("session %d about the rollout", i)))
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "idx")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "searchable now") {
		t.Errorf("a first build over 420 sessions published nothing early:\n%s", out.String())
	}
	// And the finished index holds everything, not the slice.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 420 {
		t.Errorf("the finished index holds %d sessions, want 420", len(metas))
	}
}

// The early publish writes a manifest, and a manifest write folds and drains
// what the parsers reported. A first build big enough to publish early still
// ends with the bad line it read counted, once (#5110).
func TestFirstBuildKeepsSkippedLinesAcrossNewestFirstPublish(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "30")
	bad := filepath.Join(dir, "rollout-2026-07-30T00-00-00-bad-line.jsonl")
	write(t, bad, strings.ReplaceAll(codexRolloutHead, "cx-1", "bad-line")+"{not json\n")
	for i := range partialPublishFrom + 10 {
		id := fmt.Sprintf("big-%04d", i)
		write(t, filepath.Join(dir, "rollout-2026-07-30T00-00-00-"+id+".jsonl"),
			strings.ReplaceAll(codexRolloutHead, "cx-1", id))
	}

	idx := filepath.Join(tmp, "idx")
	before := SkippedThisRun()
	var progress strings.Builder
	if err := Ensure(idx, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), "searchable now") {
		t.Fatalf("no early publish on this build:\n%s", progress.String())
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if m.PartialOf != 0 {
		t.Fatalf("index is still the early slice (PartialOf %d)", m.PartialOf)
	}
	if got := m.IngestFiles[bad].Malformed; got != 1 {
		t.Fatalf("Malformed for the bad rollout = %d, want 1", got)
	}
	if got := m.IngestHealth["codex"].MalformedLines; got != 1 {
		t.Fatalf("codex MalformedLines = %d, want 1", got)
	}
	if got := SkippedThisRun() - before; got != 1 {
		t.Fatalf("SkippedThisRun moved by %d, want 1", got)
	}
}

// A transcript sharing an id with one in another project, added after the
// first pass, was mined together with the row's copy: the error in one file
// and the remedy in the other made a second sighting of another session's
// fix, which a rebuild never saw (#5504). The tables a pass writes must be the
// ones a rebuild of the same stores writes.
func TestSharedIDCopyFixesMatchARebuild(t *testing.T) {
	for _, mode := range []string{"copy-after", "copy-before", "remove-row", "remove-other", "rewrite-copy", "append-row", "control-other-id"} {
		for _, proj := range []string{"fork2", "aa"} {
			t.Run(mode+"/"+proj, func(t *testing.T) {
				h := newTwoWayEnv(t)
				orig := hPrompt("app", "s6", 6, 0, "why does the pool fail") +
					hBash("app", "s6", 6, 1, "a1", "make migrate", "dial tcp 127.0.0.1:7432: connect: connection refused\n", true)
				copyOf := func(id string) string {
					return hPrompt(proj, id, 6, 0, "copy: the pool fails") +
						hEdit(proj, id, 6, 2, "e1", "kafka.go", "a", "b") +
						hBash(proj, id, 6, 3, "b1", "make migrate", "ok", false) +
						hBash(proj, id, 6, 4, "b2", "make seed", "dial tcp 127.0.0.1:5432: connect: connection refused\n", true) +
						hEdit(proj, id, 6, 5, "e3", "seed.go", "a", "b") +
						hBash(proj, id, 6, 6, "b3", "make seed", "ok", false)
				}
				// A second session with the same wall, so it is a known error.
				other := hPrompt("svc", "s4", 7, 0, "why does the ledger fail") +
					hBash("svc", "s4", 7, 1, "a2", "make migrate", "dial tcp 127.0.0.1:6432: connect: connection refused\n", true) +
					hEdit("svc", "s4", 7, 2, "e2", "config.go", "a", "b") +
					hBash("svc", "s4", 7, 3, "b2", "make migrate", "ok", false)
				h.put("app", "s6", orig)
				h.put("svc", "s4", other)
				if mode != "copy-after" && mode != "control-other-id" {
					h.put(proj, "s6", copyOf("s6"))
				}
				inc := filepath.Join(h.tmp, "inc")
				if err := Ensure(inc, "", false, nil); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "control-other-id":
					h.put(proj, "s66", copyOf("s66"))
				case "copy-after":
					h.put(proj, "s6", copyOf("s6"))
				case "append-row":
					// The pass appends the row's new output after the copy's
					// in the log, where a rebuild writes it before.
					h.add("app", "s6", hBash("app", "s6", 6, 8, "a9", "make migrate", "dial tcp 127.0.0.1:8432: connect: connection refused\n", true))
				case "rewrite-copy":
					h.put(proj, "s6", copyOf("s6")+hBash(proj, "s6", 6, 7, "b4", "make seed", "ok", false))
				case "remove-row", "remove-other":
					// Removing the copy the row names hands the row to the other.
					m, err := readManifest(inc)
					if err != nil {
						t.Fatal(err)
					}
					gone, kept := h.path("app", "s6"), h.path(proj, "s6")
					if m.Sessions["claude:s6"].Path != gone {
						gone, kept = kept, gone
					}
					if mode == "remove-other" {
						gone = kept
					}
					if err := os.Remove(gone); err != nil {
						t.Fatal(err)
					}
				}
				if err := Ensure(inc, "", false, nil); err != nil {
					t.Fatal(err)
				}
				if m, err := readManifest(inc); err != nil || (mode != "control-other-id" && !strings.HasPrefix(mode, "remove") && !m.Sessions["claude:s6"].Shared) {
					t.Fatalf("the two s6 transcripts do not collide, so this measures nothing: %v", err)
				}
				// Rebuilt from a copy, which keeps a removed transcript's
				// session as the index holding it does.
				fresh := filepath.Join(h.tmp, "fresh")
				copyDir(t, inc, fresh)
				if err := Ensure(fresh, "", true, nil); err != nil {
					t.Fatal(err)
				}
				a, b := takeSnap(t, inc), takeSnap(t, fresh)
				for _, k := range []string{"fixes", "fixraw", "friction texts"} {
					if a.sidecars[k] != b.sidecars[k] {
						t.Errorf("%s after the pass:\n  %s\nafter a rebuild:\n  %s", k, a.sidecars[k], b.sidecars[k])
					}
				}
			})
		}
	}
}

// floorStore builds a store and stamps its manifest back below the redaction
// floor — which is the state an upgrade finds on disk.
func floorStore(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","sessionId":"f0","cwd":"/work/app","timestamp":%q,`+
		`"message":{"role":"user","content":"the deploy failed with zxqsecretvalue in the runbook"}}`+"\n",
		"2026-09-01T10:00:00Z")
	if err := os.WriteFile(filepath.Join(proj, "f0.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
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
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = redactionFloor - 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	return dir
}

// A store below the redaction floor holds text this build would not write, and
// the ranked surfaces rebuild before answering from it. The by-identity loader
// serves whole sessions to `deja show` and to the MCP tools with no ranking to
// hang that on, and it served them as stored (#3617).
func TestAByIdentityReadRefusesAStoreBelowTheFloor(t *testing.T) {
	dir := floorStore(t)
	if _, _, err := FindByIdentity(dir, "claude", "f0"); !errors.Is(err, ErrStoreWithheld) {
		t.Fatalf("a withheld store was served: %v", err)
	}
	// And once the sources are read again, the same call answers.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := FindByIdentity(dir, "claude", "f0")
	if err != nil || !ok || len(s.Messages) == 0 {
		t.Fatalf("a current store did not serve the session: %v %v %d", err, ok, len(s.Messages))
	}
}

// And the reason the by-prefix path is safe, pinned: `deja show <prefix>` runs
// an ordinary pass and then loads, and what makes that safe is manifestFresh
// refusing a store whose version is not this build's. A freshness rule that
// compared only the files — the natural simplification, since the sources have
// not changed — would put the old records back in front of a reader.
func TestAPassDoesNotCallAWithheldStoreFresh(t *testing.T) {
	dir := floorStore(t)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("the pass left the store at version %d, want %d", m.Version, version)
	}
}

// A question asked in plain speech carries words that are filler to a reader
// and content to the index. "How many bikes do I own" ranks on many, bikes and
// own; the session that answers it holds only the rare one, and sessions about
// how many people own things hold two. Scored on the whole query the answer
// loses to them.
//
// Scoring only the rare words instead was measured and rejected: day0bench
// gained .003 and longmemeval lost 1.5 points of hit@1 and 3 of hit@20. The
// view is not wrong, it is only right sometimes, so it is kept beside the other
// one and each session takes its better place at a price.
func TestAFocusedWinTakesABetterPlaceButPaysForIt(t *testing.T) {
	// Ranked last on the whole query, first on the rare part of it.
	ranked := make([]relevanceScored, 12)
	for i := range ranked {
		ranked[i] = relevanceScored{place: int32(i), score: float64(12 - i)}
	}
	ranked[11].focus = 100

	out := fuseRanked(ranked)
	at := -1
	for i, r := range out {
		if r.place == ranked[11].place {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("the session disappeared from the ranking")
	}
	// Under reciprocal rank fusion the two views trade places instead of one
	// paying the other a fixed toll: first on the focused view is worth a lot,
	// last on the whole query little, and the sum decides. What matters is not
	// the exact place — that is the fusion constant's business — but that the
	// session climbs out of the tail without taking the front.
	if at == 0 {
		t.Error("a session convincing on the rare word alone took the front")
	}
	if at >= len(out)/2 {
		t.Errorf("a session first on the focused view sits at %d of %d: the "+
			"focused view bought it nothing", at, len(out))
	}
}

// The price is what stops the focused view from being the ranking. A session
// the whole query already ranks first must not be displaced by one that merely
// carries a rare word.
func TestTheFocusedViewCannotTakeTheFront(t *testing.T) {
	ranked := make([]relevanceScored, 6)
	for i := range ranked {
		ranked[i] = relevanceScored{place: int32(i), score: float64(6 - i)}
	}
	ranked[5].focus = 100

	out := fuseRanked(ranked)
	if out[0].place != 0 {
		t.Errorf("the front of the ranking changed hands to %d", out[0].place)
	}
}

// And a ranking where the two views agree comes back exactly as it was: this
// reorders only where they disagree.
func TestAgreementLeavesTheOrderAlone(t *testing.T) {
	ranked := make([]relevanceScored, 8)
	for i := range ranked {
		ranked[i] = relevanceScored{
			place: int32(i),
			score: float64(8 - i),
			focus: float64(8 - i),
		}
	}
	for i, r := range fuseRanked(ranked) {
		if r.place != ranked[i].place {
			t.Fatalf("agreeing views still reordered the ranking at %d: %d, want %d", i, r.place, ranked[i].place)
		}
	}
}

// The fusion constant damps how much one top place is worth. Summing 1/rank
// rather than 1/(k+rank) lets a single first place outweigh being second on
// both views, which is the behaviour reciprocal rank fusion exists to avoid
// (Cormack, Clarke & Buettcher, SIGIR 2009). In real-world stores, dropping
// the constant to zero puts the ranking back where it stood before the fusion.
func TestFusionDampsASingleTopPlace(t *testing.T) {
	ranked := make([]relevanceScored, 10)
	// "a" (0) leads the whole query and is last on the rare part of it, and "i"
	// (8) leads the rare part while sitting ninth on the whole query. "b" (1) is second
	// on both. Second twice is the better bet, and only a damped sum says so:
	// undamped, one first place carries either of the other two past it.
	focus := []float64{1, 90, 80, 70, 60, 50, 40, 30, 100, 10}
	for i := range ranked {
		ranked[i] = relevanceScored{
			place: int32(i),
			score: float64(10 - i),
			focus: focus[i],
		}
	}
	if out := fuseRanked(ranked); out[0].place != 1 {
		t.Errorf("front went to %d, want 1: second on both views beats first on one",
			out[0].place)
	}
}

// fuseRanked is fuseFocus over rows already in ranked order.
func fuseRanked(ranked []relevanceScored) []relevanceScored {
	order := make([]int32, len(ranked))
	for i := range order {
		order[i] = int32(i)
	}
	final := fuseFocus(ranked, order)
	out := make([]relevanceScored, len(final))
	for i, idx := range final {
		out[i] = ranked[idx]
	}
	return out
}

// Deriving SafeSize and PrefixHash means reading each transcript — the tail
// for the last complete line, the head for the hash. On a real store that was
// 650 ms on every command, against 13 ms of actual searching, to conclude
// nothing had moved.
func TestUnchangedFilesKeepTheirDerivedState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", dir)
	path := filepath.Join(dir, "project", "session.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))

	first := currentFilesWith("", nil)
	fs, ok := first[path]
	if !ok || fs.SafeSize == 0 || fs.PrefixSample == 0 {
		t.Fatalf("derived state missing on a first walk: %+v", fs)
	}

	// Unchanged file: the values must come back identical without being
	// recomputed, which is what makes the walk a stat pass.
	second := currentFilesWith("", first)
	if second[path].SafeSize != fs.SafeSize || second[path].PrefixSample != fs.PrefixSample {
		t.Fatalf("carried state differs: %+v vs %+v", second[path], fs)
	}

	// Changed file: stale values must not be carried, or a rewind goes
	// unnoticed and the index keeps the old text.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "rewritten"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	third := currentFilesWith("", second)
	if third[path].PrefixHash == fs.PrefixHash && third[path].SafeSize == fs.SafeSize {
		t.Fatal("a rewritten file kept the old derived state")
	}
}

// bruteCloseTokens is the definition closeTokens must not drift from: every
// token in the corpus, compared directly.
func bruteCloseTokens(query string, catalog map[string]bool) []string {
	type match struct {
		token string
		d     int
	}
	limit := 1
	if len([]rune(query)) >= 8 {
		limit = 2
	}
	var ms []match
	for token := range catalog {
		if d := damerauDistance(query, token, limit); d <= limit {
			ms = append(ms, match{token, d})
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].d == ms[j].d {
			return ms[i].token < ms[j].token
		}
		return ms[i].d < ms[j].d
	})
	if len(ms) > 8 {
		ms = ms[:8]
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.token
	}
	return out
}

// Bucketing by length must not lose a match. Only tokens within the edit
// limit of the query's length can match, but the bookkeeping — rune vs byte
// length, the overflow bucket for very long tokens — is where it would go
// wrong quietly.
func TestFuzzyLengthBucketsMatchBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(3)) //nolint:gosec // a seeded fixture, not crypto
	al := []rune("abcdefghijklmnopqrstuvwxyz")
	cyr := []rune("абвгдеёжзийклмнопрстуфхцчшщыэюя")
	catalog := map[string]bool{}
	word := func(alpha []rune, n int) string {
		b := make([]rune, n)
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
		return string(b)
	}
	for range 4000 {
		catalog[word(al, 1+rng.Intn(30))] = true
		catalog[word(cyr, 1+rng.Intn(20))] = true
	}
	// Tokens past the bucket cap must still be reachable.
	long := word(al, maxIndexedTokenLen+8)
	catalog[long] = true
	catalog[long[:len(long)-1]] = true
	catalog[word(cyr, maxIndexedTokenLen+3)] = true

	idx := newTokenIndex(catalog)
	queries := []string{"a", "ab", "abcdefgh", "мосты", "конфигурация", long, long[:20], strings.Repeat("z", 40)} //nolint:prealloc // a fixture literal the loop below grows
	for range 40 {
		queries = append(queries, word(al, 1+rng.Intn(20)), word(cyr, 1+rng.Intn(16)))
	}
	for _, q := range queries {
		want := bruteCloseTokens(q, catalog)
		got := closeTokens(q, idx)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("query %q (%d runes): bucketed=%v brute=%v", q, utf8.RuneCountInString(q), got, want)
		}
	}
}

// Every token must land in exactly one bucket, and the bucket must agree with
// the token's rune length.
func TestTokenIndexBucketsByRuneLength(t *testing.T) {
	catalog := map[string]bool{"ab": true, "abc": true, "мир": true, "конфигурация": true, strings.Repeat("x", maxIndexedTokenLen+5): true}
	idx := newTokenIndex(catalog)
	seen := map[string]int{}
	for n, bucket := range idx.byLen {
		for _, tok := range bucket {
			seen[tok]++
			want := utf8.RuneCountInString(tok)
			if want > maxIndexedTokenLen {
				want = maxIndexedTokenLen + 1
			}
			if n != want {
				t.Errorf("token %q sits in bucket %d, want %d", tok, n, want)
			}
		}
	}
	for tok := range catalog {
		if seen[tok] != 1 {
			t.Errorf("token %q appears in %d buckets", tok, seen[tok])
		}
	}
}

// A word the corpus is full of gains nothing from its neighbours: it is not a
// typo, and widening it only adds postings that discriminate nothing. This is
// what keeps an ordinary question off the candidate walk. A rare word is still
// widened even when spelled correctly, because there a near-neighbour is
// usually the same thing said differently — measured: narrowing those too cost
// longmemeval hit@5 2.5 points.
func TestFuzzyWidensRareWordsAndLeavesCommonOnesAlone(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// "deploy" lands well past the common bar; "rice" and "ice" stay rare and
	// one edit apart. The bar counts postings, which are per message, so one
	// busy session carries it — a file per posting made this the slowest test
	// in the package on Windows.
	var busy strings.Builder
	for i := range commonTokenPostings + 20 {
		fmt.Fprintf(&busy, "we deploy the service again, run %d\n", i)
	}
	writeMessages(t, proj, "common", busy.String())
	writeSession(t, proj, "rice", "jasmine rice is the one I keep buying")
	writeSession(t, proj, "ice", "the ice machine in the hotel was broken")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	_, variants, err := fuzzyPostings(dir, []string{"deploy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := variants["deploy"]; len(v) != 1 || v[0] != "deploy" {
		t.Fatalf("a word the corpus is full of was widened to %v", v)
	}

	_, variants, err = fuzzyPostings(dir, []string{"rice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants["rice"]) < 2 {
		t.Fatalf("a rare word lost its neighbours: %v", variants["rice"])
	}

	// A real typo still has to reach the word behind it.
	_, variants, err = fuzzyPostings(dir, []string{"rcie"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range variants["rcie"] {
		if v == "rice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("typo no longer reaches the word it meant: %v", variants["rcie"])
	}
}

// The generation answers one question: do record offsets still mean what they
// meant. A timestamp alone cannot answer it — the incremental path deliberately
// carries the previous stamp forward, and two rebuilds inside one tick of a
// coarse clock share one, which is how a sidecar built before a `deja forget`
// read as current on Linux (#1355).
func TestGenerationChangesWhenRecordsMove(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "the vault rotation broke staging")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "b", "the kafka consumer keeps flapping")
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("the generation is %q both before and after records grew", before)
	}
}

// And it stays put when nothing moved: re-reading an unchanged index must not
// invalidate a sidecar built for it.
func TestGenerationStaysWhenNothingMoved(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "the vault rotation broke staging")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	first, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	second, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("the generation moved from %q to %q with nothing changed", first, second)
	}
}

// writeGob opens with O_TRUNC and only then encodes, so a failed encode leaves
// the file it was overwriting destroyed — emptied, in the case below, where the
// encoder refuses the value before writing a byte.
//
// That is harmless where the file cannot already exist: the full builds write
// each sidecar into a directory they just created. It is not harmless where a
// good file has just been carried in and is being rewritten, because a reader
// treats an undecodable sidecar as having no content at all — one failed write
// silences the feature until the next full rebuild.
//
// A channel is what makes the encoder refuse. A struct with a channel field
// does not: gob quietly skips fields it cannot handle, which is worth knowing
// before writing a test that relies on one.
func TestAFailedGobWriteKeepsOrLosesWhatWasThere(t *testing.T) {
	dir := t.TempDir()
	const good = "the file that was already there"
	fail := make(chan int)

	t.Run("writeGob loses it", func(t *testing.T) {
		p := filepath.Join(dir, "plain.gob")
		if err := writeGob(p, good); err != nil {
			t.Fatal(err)
		}
		if err := writeGob(p, fail); err == nil {
			t.Fatal("the encode was expected to fail; this test no longer proves anything")
		}
		// Asserted, not skipped: the loss is the hazard this documents, and if
		// writeGob ever stops truncating first, whoever changes it should be the
		// one to retire this — not have it quietly pass.
		var back string
		if err := readGob(p, &back); err == nil && back == good {
			t.Error("writeGob preserved the previous content; the reason for the atomic call site is gone and this test should go with it")
		}
	})

	t.Run("writeGobAtomic keeps it", func(t *testing.T) {
		p := filepath.Join(dir, "atomic.gob")
		if err := writeGobAtomic(p, good); err != nil {
			t.Fatal(err)
		}
		if err := writeGobAtomic(p, fail); err == nil {
			t.Fatal("the encode was expected to fail; this test no longer proves anything")
		}
		var back string
		if err := readGob(p, &back); err != nil || back != good {
			t.Errorf("a failed atomic write lost the previous content: %q, %v", back, err)
		}
	})
}

// A store can be half-read: some sessions arrive, one file is refused. The line
// carried the refused *lines* and the missing-tool reason and said nothing
// about a refused file, so a store that gave up ten sessions and lost three
// tasks read exactly like a store with nothing wrong (#2236).
func TestAStoreNarratesTheFilesItRefusedBesideTheSessionsItRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().Add(-time.Hour).UnixMilli()
	good := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user",`+
		`"content":"the pool timed out during the migration"}]}`, stamp)
	if err := os.WriteFile(filepath.Join(cline, "good.messages.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cline, "bad.messages.json"), []byte(`[{"ts":1,`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: the store did produce a session, so the line exists and the
	// silence below is about the other file.
	if !strings.Contains(said, "cline: 1 session") {
		t.Fatalf("the readable session did not land, so this measures nothing:\n%s", said)
	}
	health := IngestHealth(dir)
	if health["cline"].FailedFiles != 1 {
		t.Fatalf("the refused file was not recorded, so this measures nothing: %v", health)
	}
	if !strings.Contains(said, "path") {
		t.Errorf("the line says nothing about the file it could not read:\n%s", said)
	}
}

// A bot token names its service exactly, so `deja secrets` lists it rather
// than folding it into the counted rules at the bottom of the screen.
func TestATelegramBotTokenIsListedByName(t *testing.T) {
	if !SecretKindNamed("telegram-bot-token") {
		t.Fatal("telegram-bot-token is counted, not listed")
	}
	got := markerKinds("BOT_TOKEN=[redacted:telegram-bot-token]")
	if len(got) != 1 || got[0] != "telegram-bot-token" {
		t.Fatalf("markerKinds = %v", got)
	}
}
