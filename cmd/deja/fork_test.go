package main

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func indexedCollisionHits(tb testing.TB, n int, unique bool) (string, []search.Hit) {
	tb.Helper()
	home := tb.TempDir()
	root := filepath.Join(home, "claude")
	project := filepath.Join(root, "project")
	dir := filepath.Join(home, "index")
	for key, value := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"APPDATA": filepath.Join(home, "roaming"), "LOCALAPPDATA": filepath.Join(home, "local"),
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"),
		"XDG_CACHE_HOME": filepath.Join(home, "cache"), "XDG_STATE_HOME": filepath.Join(home, "state"),
		"DEJA_CLAUDE_ROOT": root, "CLAUDE_CONFIG_DIR": root,
		"DEJA_INDEX_DIR": dir, "DEJA_NO_AUTO_INDEX": "",
		"DEJA_STORES": "claude", "DEJA_EMBED_OFF": "1", "DEJA_OFFLINE": "1", "DEJA_NO_UPDATE_NOTICE": "1",
		"DEJA_NOTES_FILE":      filepath.Join(home, "notes.jsonl"),
		"DEJA_WARMUP_SENTINEL": filepath.Join(home, "warmup"),
	} {
		tb.Setenv(key, value)
	}
	if err := os.MkdirAll(project, 0o700); err != nil {
		tb.Fatal(err)
	}
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	ids := make([]index.Identity, n)
	for i := range n {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		ids[i] = index.Identity{Harness: "claude", ID: id}
		var body []byte
		for j := range 12 {
			text := fmt.Sprintf("marmotquill identical indexed turn %d", j)
			if unique && j == 11 {
				text += fmt.Sprintf(" peer %d", i)
			}
			record, err := json.Marshal(map[string]any{
				"type": "user", "sessionId": id, "uuid": strconv.Itoa(j), "cwd": home,
				"timestamp": at.Add(time.Duration(j) * time.Second).Format(time.RFC3339Nano),
				"message":   map[string]string{"role": "user", "content": text},
			})
			if err != nil {
				tb.Fatal(err)
			}
			body = append(body, record...)
			body = append(body, '\n')
		}
		if err := os.WriteFile(filepath.Join(project, id+".jsonl"), body, 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		tb.Fatal(err)
	}
	ss, err := index.FindManyByIdentity(dir, ids)
	if err != nil || len(ss) != n {
		tb.Fatalf("indexed sessions: got %d, want %d, error %v", len(ss), n, err)
	}
	hits := make([]search.Hit, n)
	for i, s := range ss {
		if len(s.Messages) != 12 {
			tb.Fatalf("session %s has %d messages, want 12", s.ID, len(s.Messages))
		}
		hits[i].Session = s
	}
	return dir, hits
}

// Reintroducing a pending entry for every peer pair exhausts this budget even
// though the index holds one message shape and none of the peers is a copy.
func TestDropCopiedHitsIndexedPeersMemory(t *testing.T) {
	dir, hits := indexedCollisionHits(t, 2400, false)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := dropCopiedHits(dir, hits)
	runtime.ReadMemStats(&after)
	if len(got) != len(hits) {
		t.Fatalf("identical indexed peers lost: got %d, want %d", len(got), len(hits))
	}
	for i := range got {
		if got[i].Session.ID != hits[i].Session.ID {
			t.Fatalf("peer order changed at %d", i)
		}
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("2400 indexed peers allocated %d bytes", allocated)
	if allocated > 128<<20 {
		t.Fatalf("allocated %d bytes for identical indexed peers; want at most 128 MiB", allocated)
	}
}

func TestCopySourcesIndexedInterleavedClasses(t *testing.T) {
	dir, hits := indexedCollisionHits(t, 6, false)
	// Positions 0 and 2 are sources. The other positions interleave two
	// different continuations of those sources, all with the same matched turn.
	for _, i := range []int{1, 3, 4, 5} {
		text := "continuation B"
		if i == 3 || i == 5 {
			text = "continuation C"
		}
		s := hits[i].Session
		record, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": s.ID, "uuid": "continuation", "cwd": os.Getenv("HOME"),
			"timestamp": s.Updated.Add(time.Second).Format(time.RFC3339Nano),
			"message":   map[string]string{"role": "user", "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", s.ID+".jsonl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, record...)
		body = append(body, '\n')
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	ss := make([]model.Session, len(hits))
	for i, h := range hits {
		ss[i] = h.Session
		ss[i].Held = 12
		if i != 0 && i != 2 {
			ss[i].Held++
		}
		ss[i].Messages = ss[i].Messages[:1]
	}
	maybe := func(src, c int) bool { return c%2 == 0 || src != 0 }
	want := map[int]int{1: 2, 3: 2, 4: 0, 5: 2}
	got := copySources(dir, ss, func(src, c int, _ bool) bool { return maybe(src, c) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved sources: got %v, want %v", got, want)
	}
	for round := range 100 {
		maybe := func(src, c int) bool { return (src*7+c*11+round)%5 < 3 }
		want := referenceCopySources(dir, ss, maybe)
		got := copySources(dir, ss, func(src, c int, _ bool) bool { return maybe(src, c) })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: got %v, want %v", round, got, want)
		}
	}
}

func BenchmarkDropCopiedHitsIndexed(b *testing.B) {
	for _, unique := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			for _, n := range []int{100, 800, 2400} {
				b.Run(fmt.Sprintf("unique=%v/partial=%v/n=%d", unique, partial, n), func(b *testing.B) {
					dir, hits := indexedCollisionHits(b, n, unique)
					if partial {
						for i := range hits {
							hits[i].Session.Held = len(hits[i].Session.Messages)
							hits[i].Session.Messages = hits[i].Session.Messages[:1]
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if got := dropCopiedHits(dir, hits); len(got) != n {
							b.Fatalf("got %d peers, want %d", len(got), n)
						}
					}
				})
			}
		}
	}
}

func BenchmarkDropCopiedHitsIndexedStartOutlier(b *testing.B) {
	for _, n := range []int{100, 800, 2400} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			dir, hits := indexedCollisionHits(b, n, false)
			// Matching turns can tie while one session's metadata starts earlier.
			hits[n-1].Session.Started = hits[n-1].Session.Started.Add(-time.Second)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if got := dropCopiedHits(dir, hits); len(got) != 1 || got[0].Session.ID != hits[n-1].Session.ID {
					b.Fatalf("earlier source lost: got %d peers", len(got))
				}
			}
		})
	}
}

// nearCopyHits is indexedCollisionHits with one distinct reply appended to the
// full record of every session extend picks: the matched turns stay equal.
func nearCopyHits(t *testing.T, n int, extend func(i int) bool) (string, []search.Hit) {
	t.Helper()
	dir, hits := indexedCollisionHits(t, n, false)
	for i, h := range hits {
		if !extend(i) {
			continue
		}
		s := h.Session
		record, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": s.ID, "uuid": "reply", "cwd": os.Getenv("HOME"),
			"timestamp": s.Updated.Add(time.Second).Format(time.RFC3339Nano),
			"message":   map[string]string{"role": "user", "content": fmt.Sprintf("distinct reply %d", i)},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", s.ID+".jsonl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(append(body, record...), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, hits
}

func countCopyOf(t *testing.T) *int {
	t.Helper()
	calls := 0
	t.Cleanup(func() { copyOf = search.CopyOf })
	copyOf = func(c, src model.Session) bool {
		calls++
		return search.CopyOf(c, src)
	}
	return &calls
}

// Sessions that match on the same turns and each go on with a reply of their
// own are siblings. Comparing every pair of their full records made 6000 of
// them take 4.7 s to search (#5523); a class is compared only with the classes
// whose last turn it holds.
func TestDropCopiedHitsNearCopiesCompareLinearly(t *testing.T) {
	const n = 400
	dir, hits := nearCopyHits(t, n, func(int) bool { return true })
	calls := countCopyOf(t)
	got := dropCopiedHits(dir, hits)
	if len(got) != n {
		t.Fatalf("siblings dropped: got %d hits, want %d", len(got), n)
	}
	t.Logf("%d near-copies, %d full-record comparisons", n, *calls)
	if *calls > n {
		t.Fatalf("%d near-copies made %d full-record comparisons, want at most %d", n, *calls, n)
	}
}

// Sessions that stop at the shared turns are sources of the ones that went on;
// the lookup has to find the same sources the pairwise scan found.
func TestCopySourcesNearCopiesMatchReference(t *testing.T) {
	const n = 60
	dir, hits := nearCopyHits(t, n, func(i int) bool { return i%4 != 1 })
	ss := make([]model.Session, n)
	for i, h := range hits {
		ss[i] = h.Session
	}
	for round := range 20 {
		maybe := func(src, c int) bool { return round == 0 || (src*7+c*11+round)%5 < 3 }
		want := referenceCopySources(dir, ss, maybe)
		got := copySources(dir, ss, func(src, c int, _ bool) bool { return maybe(src, c) })
		if round == 0 && len(want) != n-n/4 {
			t.Fatalf("reference found %d copies, want %d", len(want), n-n/4)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: got %v, want %v", round, got, want)
		}
	}
}

func collisionHits(n int, unique bool) []search.Hit {
	hits := make([]search.Hit, n)
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for i := range hits {
		s := model.Session{ID: strconv.Itoa(i), Harness: "codex", Started: at}
		for j := range 12 {
			text := fmt.Sprintf("WARMUP_SENTINEL native synthetic %d", j)
			if unique {
				text += strconv.Itoa(i)
			}
			s.Messages = append(s.Messages, model.Message{Role: "user", Time: at, Text: text})
		}
		hits[i].Session = s
	}
	return hits
}

func TestDropCopiedHitsCollisionAllocations(t *testing.T) {
	hits := collisionHits(100, false)
	allocs := testing.AllocsPerRun(1, func() {
		if got := dropCopiedHits("", hits); len(got) != len(hits) {
			t.Fatalf("identical peers lost: got %d, want %d", len(got), len(hits))
		}
	})
	if allocs > 10000 {
		t.Fatalf("100 colliding sessions allocated %.0f objects; turn keys must be reused across pairs", allocs)
	}
}

func TestDropCopiedHitsRetainsForkAttribution(t *testing.T) {
	hits := collisionHits(3, false)
	hits[1].Session.Kind, hits[1].Session.Parent = "fork", hits[0].Session.ID
	hits[2].Session.Kind, hits[2].Session.Parent = "fork", hits[1].Session.ID
	if got := dropCopiedHits("", hits); !reflect.DeepEqual(got, hits[:1]) {
		t.Fatalf("copied fork lineage changed: got %#v", got)
	}
	hits[1].Session.Messages = append(hits[1].Session.Messages, model.Message{
		Role: "user", Time: hits[1].Session.Started.Add(time.Second), Text: "fork owns this turn",
	})
	if got := dropCopiedHits("", hits); !reflect.DeepEqual(got, hits[:2]) {
		t.Fatalf("fork's own matching turn lost: got %#v", got)
	}
}

func TestDropCopiedHitsDropsIdenticalImportedTwin(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	local := search.Hit{
		Session: model.Session{
			ID:      "orig-42",
			Harness: "claude",
			Messages: []model.Message{
				{Role: "user", Time: t0, Text: "keep   timeout   at 5s"},
				{Role: "assistant", Time: t0.Add(time.Second), Text: "Updated retry budget."},
			},
		},
	}
	identicalTwin := search.Hit{
		Session: model.Session{
			ID:      "imported-orig-42-1",
			OrigID:  "orig-42",
			Harness: "claude",
			Messages: []model.Message{
				// Whitespace & zero-timestamp drift must still collapse into local twin.
				{Role: "user", Text: "keep timeout at 5s"},
			},
		},
	}
	divergentTwin := search.Hit{
		Session: model.Session{
			ID:      "imported-orig-42-2",
			OrigID:  "orig-42",
			Harness: "claude",
			Messages: []model.Message{
				{Role: "user", Time: t0.Add(2 * time.Second), Text: "actually use 10s on staging"},
			},
		},
	}
	got := dropCopiedHits("", []search.Hit{local, identicalTwin, divergentTwin})
	if len(got) != 2 || got[0].Session.ID != "orig-42" || got[1].Session.ID != "imported-orig-42-2" {
		t.Fatalf("dropCopiedHits kept %#v, want local + divergent twin only", got)
	}
}

func BenchmarkDropCopiedHitsCollisions(b *testing.B) {
	for _, unique := range []bool{false, true} {
		for _, n := range []int{5, 20, 100, 800} {
			b.Run(fmt.Sprintf("unique=%v/n=%d", unique, n), func(b *testing.B) {
				hits := collisionHits(n, unique)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if got := dropCopiedHits("", hits); len(got) != n {
						b.Fatalf("got %d peers, want %d", len(got), n)
					}
				}
			})
		}
	}
}

func TestCopyMessageShapesExact(t *testing.T) {
	at := time.Date(2026, 10, 21, 1, 2, 3, 4, time.UTC)
	zero := time.Time{}
	wrapped := time.Unix(0, zero.UnixNano())
	if wrapped.IsZero() || wrapped.UnixNano() != zero.UnixNano() {
		t.Fatal("fixture must share UnixNano while differing in IsZero")
	}
	m := model.Message{Role: "user", Time: at, Text: "quote\" slash\\ NUL\x00 мир"}
	other := m
	other.Text += "!"
	variants := [][]model.Message{
		nil, {}, {m}, {m}, {other}, {m, other}, {other, m}, {m, m},
		{{Role: "user", Time: zero, Text: "same"}},
		{{Role: "user", Time: wrapped, Text: "same"}},
		{{Role: "user\x00", Time: at, Text: "x"}},
		{{Role: "user", Time: at, Text: "\x00x"}},
		{{Role: "assistant", Time: at, Text: "No response requested."}},
		{{Role: "summary", Time: at, Text: " "}},
		{{Role: "user", Time: at.In(time.FixedZone("elsewhere", 7200)), Text: m.Text}},
	}
	ss := make([]model.Session, len(variants))
	for i, messages := range variants {
		ss[i] = model.Session{ID: "duplicate", Messages: messages}
	}
	classes := copyMessageShapes(ss)
	for i := range ss {
		for j := range ss {
			want := i == j || (i < 2 && j < 2) || ((i == 2 || i == 3 || i == 14) && (j == 2 || j == 3 || j == 14))
			if got := classes[i] == classes[j]; got != want {
				t.Fatalf("shape equality (%d,%d): got %v want %v", i, j, got, want)
			}
			if classes[i] == classes[j] && (search.CopyOf(ss[i], ss[j]) || search.CopyOf(ss[j], ss[i])) {
				t.Fatalf("equal shape changed CopyOf (%d,%d)", i, j)
			}
		}
	}
}

// referenceCopySources preserves the frozen candidate's selection and full-record fallback.
func referenceCopySources(dir string, ss []model.Session, maybe func(src, c int) bool) map[int]int {
	out := map[int]int{}
	var unread [][2]int
	for i, c := range ss {
		for j, src := range ss {
			if i == j || c.Harness != src.Harness || !maybe(j, i) {
				continue
			}
			if search.SourceOf(ss, src, c, src.Started) && !search.SourceOf(ss, c, src, c.Started) {
				out[i] = j
				break
			}
			unread = append(unread, [2]int{i, j})
		}
	}
	if len(unread) == 0 || dir == "" {
		return out
	}
	var ids []index.Identity
	seen := map[int]bool{}
	for _, p := range unread {
		for _, k := range p {
			if !seen[k] {
				seen[k] = true
				ids = append(ids, index.Identity{Harness: ss[k].Harness, ID: ss[k].ID})
			}
		}
	}
	full, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return out
	}
	whole := map[string]model.Session{}
	for _, s := range full {
		whole[s.Harness+":"+s.ID] = s
	}
	for _, p := range unread {
		i, j := p[0], p[1]
		if _, done := out[i]; done {
			continue
		}
		c, okc := whole[ss[i].Harness+":"+ss[i].ID]
		src, oks := whole[ss[j].Harness+":"+ss[j].ID]
		if okc && oks && search.CopyOf(c, src) {
			out[i] = j
		}
	}
	return out
}

func TestCopySourcesShapesDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(23)) //nolint:gosec // seeded for a reproducible differential
	at := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	times := []time.Time{{}, at, at.Add(time.Second), time.Unix(0, time.Time{}.UnixNano())}
	roles := []string{"user", "assistant", "summary", "user\x00"}
	texts := []string{"same", " ", "No response requested.", "\"\\\x00мир"}
	kinds := []string{"", "fork", "subagent", "subagent_fork"}
	for round := range 300 {
		ss := make([]model.Session, 8)
		for i := range ss {
			ss[i] = model.Session{ID: strconv.Itoa(rng.Intn(5)), Harness: strconv.Itoa(rng.Intn(2)), Kind: kinds[rng.Intn(len(kinds))], Parent: strconv.Itoa(rng.Intn(5)), Started: times[rng.Intn(len(times))]}
			if i > 0 && rng.Intn(2) == 0 {
				ss[i].Messages = ss[rng.Intn(i)].Messages
			} else {
				for range rng.Intn(5) {
					ss[i].Messages = append(ss[i].Messages, model.Message{Role: roles[rng.Intn(len(roles))], Time: times[rng.Intn(len(times))], Text: texts[rng.Intn(len(texts))]})
				}
			}
		}
		maybe := func(src, c int) bool { return (src+c+round)%3 != 0 }
		want := referenceCopySources("", ss, maybe)
		got := copySources("", ss, func(src, c int, _ bool) bool { return maybe(src, c) })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d got %v want %v sessions=%#v", round, got, want, ss)
		}
	}
}

func TestCopySourcesShapesFullRecordFallback(t *testing.T) {
	dir := claudeForkStore(t, false)
	if err := cmdSearch(dir, []string{"zebracount", "--json"}, ""); err != nil {
		t.Fatal(err)
	}
	ids := []index.Identity{
		{Harness: "claude", ID: "aaaaaaaa-0000-4000-8000-00000000000a"},
		{Harness: "claude", ID: "ffffffff-0000-4000-8000-00000000000f"},
	}
	ss, err := index.FindManyByIdentity(dir, ids)
	if err != nil || len(ss) != 2 {
		t.Fatalf("full records: %d, %v", len(ss), err)
	}
	for i := range ss {
		ss[i].Messages = ss[i].Messages[:1]
	}
	if classes := copyMessageShapes(ss); classes[0] != classes[1] {
		t.Fatal("partial records must have equal shapes")
	}
	maybe := func(_, _ int) bool { return true }
	want := referenceCopySources(dir, ss, maybe)
	got := copySources(dir, ss, func(_, _ int, _ bool) bool { return true })
	if len(want) != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("partial-equal/full-divergent: got %v want %v", got, want)
	}
}

// claudeForkStore writes a source and a fork that holds the source's four
// records, same uuids and times, plus two turns of its own, into a sandbox
// Claude store. marked adds the forkedFrom a /branch writes.
func claudeForkStore(t *testing.T, marked bool) string {
	t.Helper()
	return claudeForkStoreWent(t, marked, false)
}

// claudeForkStoreWent is claudeForkStore with the source going on after the
// fork when went is set, with a turn the fork never saw.
func claudeForkStoreWent(t *testing.T, marked, went bool) string {
	t.Helper()
	home := hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "idx")
	t.Setenv("DEJA_INDEX_DIR", dir)
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	cwd := filepath.Join(home, "work", "app")
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-26 * time.Hour).UTC()
	rec := func(session, uuid, role, text string, sec int, forkedFrom string) string {
		var content any = text
		if role == "assistant" {
			content = []map[string]string{{"type": "text", "text": text}}
		}
		m := map[string]any{
			"type": role, "sessionId": session, "uuid": uuid, "cwd": cwd,
			"timestamp": at.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano),
			"message":   map[string]any{"role": role, "content": content},
		}
		if forkedFrom != "" {
			m["forkedFrom"] = map[string]string{"sessionId": forkedFrom}
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	copied := func(session, forkedFrom string) string {
		return rec(session, "u1", "user", "the marmotquill exporter drops rows, add a zebracount", 0, forkedFrom) +
			rec(session, "u2", "assistant", "added zebracount, paging fixes the exporter", 5, forkedFrom) +
			rec(session, "u3", "user", "and the rows past 4096?", 10, forkedFrom) +
			rec(session, "u4", "assistant", "paged, all rows come out now", 15, forkedFrom)
	}
	from := ""
	if marked {
		from = src
	}
	srcBody := copied(src, "")
	if went {
		srcBody += rec(src, "s1", "user", "back on the source: the ospreyquill cache drops writes", 900, "") +
			rec(src, "s2", "assistant", "ospreyquill flushes now, zebracount untouched", 905, "")
	}
	writeFile(t, filepath.Join(proj, src+".jsonl"), srcBody)
	writeFile(t, filepath.Join(proj, fork+".jsonl"), copied(fork, from)+
		rec(fork, "f1", "user", "now look at the importer", 600, "")+
		rec(fork, "f2", "assistant", "the importer reads the whole file", 605, ""))
	return dir
}

// A fork opens with a copy of its source's turns, and search listed both as
// hits with the same quotes while last listed two rows under one title, marked
// or not (#5320).
func TestSearchAndLastDoNotRepeatAForksCopiedTurns(t *testing.T) {
	for _, marked := range []bool{true, false} {
		dir := claudeForkStore(t, marked)
		out := captureStdout(t, func() {
			if err := cmdSearch(dir, []string{"zebracount"}, ""); err != nil {
				t.Fatal(err)
			}
		})
		if strings.Contains(out, "ffffffff") || !strings.Contains(out, "aaaaaaaa") {
			t.Errorf("marked=%v: search answered with the fork's copy of its source's turns:\n%s", marked, out)
		}
		// Control: what the fork added is its own and still found.
		out = captureStdout(t, func() {
			if err := cmdSearch(dir, []string{"importer"}, ""); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "ffffffff") {
			t.Errorf("marked=%v: the fork's own turn is not found:\n%s", marked, out)
		}
		out = captureStdout(t, func() {
			if err := cmdLast(dir, nil, ""); err != nil {
				t.Fatal(err)
			}
		})
		var forkRow string
		for ln := range strings.SplitSeq(out, "\n") {
			if strings.Contains(ln, "ffffffff") {
				forkRow = ln
			}
		}
		if !strings.Contains(forkRow, "fork of aaaaaaaa") {
			t.Errorf("marked=%v: last does not mark the fork:\n%s", marked, out)
		}
	}
}

// A copy dropped after the cap still took a slot and counted: --limit 1
// said "showing 1 of 2" with the second a fork repeating the first (#5335).
// And MCP recall listed the fork beside its source (#5320).
func TestACappedSearchAndMCPRecallDropAForkBeforeCounting(t *testing.T) {
	dir := claudeForkStore(t, false)
	out := captureStdout(t, func() {
		if err := cmdSearch(dir, []string{"zebracount", "--limit", "1", "--json"}, ""); err != nil {
			t.Fatal(err)
		}
	})
	var got struct {
		Total  int  `json:"total"`
		Capped bool `json:"capped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Total != 1 || got.Capped {
		t.Errorf("total %d capped %v, want 1 and not capped:\n%s", got.Total, got.Capped, out)
	}
	rec, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zebracount"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec, "ffffffff") || !strings.Contains(rec, "aaaaaaaa") {
		t.Errorf("MCP recall listed the fork's copy beside its source:\n%s", rec)
	}
}

// Hits that open on the same turn but each matched a turn of their own hold
// none of each other's turns. Asking every pair whether one holds the other
// made 2400 of them take 1.7 s to search; only the holders of a hit's rarest
// turn are asked.
func TestDropCopiedHitsUniquePeersCompareLinearly(t *testing.T) {
	const n = 400
	dir, hits := indexedCollisionHits(t, n, true)
	calls := 0
	t.Cleanup(func() { heldTurnsOf = heldTurns })
	heldTurnsOf = func(src, c map[string]bool) bool {
		calls++
		return heldTurns(src, c)
	}
	got := dropCopiedHits(dir, hits)
	if len(got) != n {
		t.Fatalf("unique peers dropped: got %d hits, want %d", len(got), n)
	}
	t.Logf("%d unique peers, %d turn-set comparisons", n, calls)
	if calls > n {
		t.Fatalf("%d unique peers made %d turn-set comparisons, want at most %d", n, calls, n)
	}
}

// Asking only the holders of the rarest turn finds the same sources as asking
// every position, over random turn sets, ancestry and starts.
func TestCopySourcesRarestTurnMatchesEveryPair(t *testing.T) {
	rng := rand.New(rand.NewSource(5)) //nolint:gosec // seeded for a reproducible differential
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	starts := []time.Time{{}, at, at.Add(time.Second)}
	kinds := []string{"", "fork", "subagent"}
	found := 0
	for round := range 500 {
		ss := make([]model.Session, 2+rng.Intn(10))
		turns := make([]map[string]bool, len(ss))
		for i := range ss {
			ss[i] = model.Session{ID: strconv.Itoa(i), Harness: "codex", Kind: kinds[rng.Intn(len(kinds))], Parent: strconv.Itoa(rng.Intn(len(ss))), Started: starts[rng.Intn(len(starts))]}
			if i > 0 && rng.Intn(3) == 0 {
				ss[i].Messages = ss[rng.Intn(i)].Messages
			} else {
				for range rng.Intn(4) {
					ss[i].Messages = append(ss[i].Messages, model.Message{Role: "user", Time: at.Add(time.Duration(rng.Intn(4)) * time.Second), Text: strconv.Itoa(rng.Intn(3))})
				}
			}
			turns[i] = map[string]bool{}
			for _, m := range ss[i].Messages {
				turns[i][m.Role+"\x00"+strconv.FormatInt(m.Time.UnixNano(), 10)+"\x00"+m.Text] = true
			}
		}
		maybe := func(src, c int, sameShape bool) bool {
			return (sameShape && len(turns[c]) > 0) || heldTurns(turns[src], turns[c])
		}
		want := copySources("", ss, maybe)
		buckets, rarest := holdersOfRarest(turns)
		got := copySourcesAmong("", ss, maybe, buckets, rarest)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: got %v, want %v sessions=%#v", round, got, want, ss)
		}
		if len(want) > 0 {
			found++
		}
	}
	if found < 100 {
		t.Fatalf("only %d of 500 rounds had a copy; the comparison is too easy", found)
	}
}

// A fork whose source went on gets the source back for what the source did
// after the fork, and not for the turns the fork holds as its own copy: the
// whole source came back, copied turns included (#5335).
func TestMCPGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		markSessionLive(dir, fork)
		for _, tool := range []string{"recall", "recall_context"} {
			out, err := callMCPTool(dir, tool, json.RawMessage(`{"query":"zebracount"}`))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "marmotquill exporter drops rows") {
				t.Errorf("marked=%v %s: the fork got its own copied turns back from the source:\n%s", marked, tool, out)
			}
			out, err = callMCPTool(dir, tool, json.RawMessage(`{"query":"ospreyquill"}`))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, src[:8]) {
				t.Errorf("marked=%v %s: what the source did after the fork is missing:\n%s", marked, tool, out)
			}
		}
		endSessionLive(dir, fork)
		// Control: asked from elsewhere, the source answers with its copied turns.
		out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zebracount"}`))
		if err != nil || !strings.Contains(out, "marmotquill exporter drops rows") {
			t.Fatalf("marked=%v: control not answered with the opening, so this proves nothing (%v):\n%s", marked, err, out)
		}
	}
}

// The per-prompt recall the same way: a fork asking about what it copied is
// not handed its source, which went on after the fork and kept every copied
// turn (#5335).
func TestPromptRecallGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(os.Getenv("HOME"), "work", "app")
		ask := func(sid, prompt string) string {
			payload := mustMarshal(t, map[string]string{"session_id": sid, "cwd": cwd, "prompt": prompt})
			var out strings.Builder
			if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		const q = "the marmotquill exporter drops rows past 4096 again, what fixed the marmotquill exporter?"
		if out := ask(fork, q); strings.Contains(out, src[:8]) {
			t.Errorf("marked=%v: the fork was handed its source for turns it copied:\n%s", marked, out)
		}
		if out := ask("someone-else", q); !strings.Contains(out, src[:8]) {
			t.Errorf("marked=%v: the control was not told about the source, so this proves nothing:\n%s", marked, out)
		}
	}
}

// And the start digest of a fork resumed: the source is there for what it did
// after the fork, not for the turns the fork holds already (#5335).
func TestStartDigestGivesAForkOnlyWhatItsSourceDidAfterTheFork(t *testing.T) {
	const fork = "ffffffff-0000-4000-8000-00000000000f"
	for _, marked := range []bool{true, false} {
		dir := claudeForkStoreWent(t, marked, true)
		if err := index.Ensure(dir, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(os.Getenv("HOME"), "work", "app")
		run := func(sid string) string {
			payload := mustMarshal(t, map[string]string{"session_id": sid, "source": "resume", "cwd": cwd})
			withHookStdin(t, string(payload))
			return captureStdout(t, func() {
				if err := runHookContext(dir, true); err != nil {
					t.Error(err)
				}
			})
		}
		out := run(fork)
		if !strings.Contains(out, "ospreyquill flushes now") || strings.Contains(out, "paged, all rows come out now") {
			t.Errorf("marked=%v: the fork's digest is not cut at the fork point:\n%s", marked, out)
		}
		if out := run("someone-else"); !strings.Contains(out, "paged, all rows come out now") {
			t.Errorf("marked=%v: the control's digest lacks the opening, so this proves nothing:\n%s", marked, out)
		}
	}
}

// clineForkStore holds a Cline CLI session and a /fork of it written after the
// build: the fork's first turn builds its rules and runs the prompt builder
// before any build has seen it. A fork is a copy of its source's messages,
// ids and timestamps included, under a new session id, and its manifest names
// the source in metadata.fork.forkedFromSessionId (CLI 3.0.73, observed live).
func clineForkStore(t *testing.T) (dir, source, fork string) {
	t.Helper()
	return clineForkStoreWith(t, false)
}

// clineForkStoreWith also indexes a sibling fork of the source that went on
// past what it copied, when sibling is set.
func clineForkStoreWith(t *testing.T, sibling bool) (dir, source, fork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "cline-data")
	t.Setenv("CLINE_DATA_DIR", root)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")
	t.Setenv("DEJA_CLINE_ROOT", "")
	sessions := filepath.Join(root, "sessions")
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	write := func(id, from, user, reply string, at time.Time, more ...any) {
		t.Helper()
		d := filepath.Join(sessions, id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := map[string]any{"title": user}
		if from != "" {
			meta["fork"] = map[string]any{"forkedFromSessionId": from, "forkedAt": t0.Format(time.RFC3339Nano), "source": "cli"}
		}
		man := mustMarshal(t, map[string]any{
			"version": 1, "session_id": id, "source": "cli", "status": "idle",
			"started_at": at.Format(time.RFC3339Nano), "cwd": "/w/p", "workspace_root": "/w/p",
			"prompt": user, "metadata": meta,
		})
		msgs := mustMarshal(t, map[string]any{
			"version": 1, "agent": "lead", "sessionId": id,
			"messages": append([]any{
				map[string]any{"id": "u-" + id[:6], "role": "user", "ts": at.UnixMilli(),
					"content": []any{map[string]any{"type": "text", "text": `<user_input mode="act">` + user + `</user_input>`}}},
				map[string]any{"id": "a-" + id[:6], "role": "assistant", "ts": at.Add(5 * time.Second).UnixMilli(),
					"content": []any{map[string]any{"type": "text", "text": reply}}},
			}, more...),
		})
		if err := os.WriteFile(filepath.Join(d, id+".json"), man, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, id+".messages.json"), msgs, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		write(fmt.Sprintf("17900000%05d_old%02d", i, i), "", fmt.Sprintf("rename table%d in the billing schema", i), "done", t0.Add(-time.Duration(i+2)*time.Hour))
	}
	write("1790000099999_prior", "", "the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", t0.Add(-time.Hour))
	source = "1790000100000_srcaa"
	// The fork's copy is written by hand below with the source's own times;
	// the source is indexed first.
	write(source, "", forkQuestion, "Looking at the retry loop in fetch.go now.", t0)

	if sibling {
		write(clineSibling, source, forkQuestion, "Looking at the retry loop in fetch.go now.", t0,
			map[string]any{"id": "u-more", "role": "user", "ts": t0.Add(time.Minute).UnixMilli(),
				"content": []any{map[string]any{"type": "text", "text": "use exponential backoff with jitter there"}}},
			map[string]any{"id": "a-more", "role": "assistant", "ts": t0.Add(time.Minute + 5*time.Second).UnixMilli(),
				"content": []any{map[string]any{"type": "text", "text": "Switched fetch.go to exponential backoff with jitter."}}})
	}
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "1790000200000_forkb"
	write(fork, source, forkQuestion, "Looking at the retry loop in fetch.go now.", t0)
	return dir, source, fork
}

const clineSibling = "1790000150000_sibfk"

// A Cline /fork's first turn got a digest holding the session it was forked
// from, whose turns it already has: Cline's plugin sends no transcript path,
// and the hook had no way to read a Cline session off its store by id.
func TestAClineForksDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	digest := func(sid string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "cwd": "/w/p"})
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	// The fork first: a session once shown sits out the project's cooldown.
	if out := digest(fork); !strings.Contains(out, "<deja-recall>") || strings.Contains(out, source) {
		t.Errorf("a Cline fork was handed its own source as history:\n%s", out)
	}
	if out := digest("1790000300000_ctrlc"); !strings.Contains(out, source) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The prompt builder's recall on the fork's first prompt reads the same head.
func TestAClineForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	ask := func(sid string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := ask(fork); strings.Contains(out, source) {
		t.Errorf("a Cline fork's prompt recall named its own source:\n%s", out)
	}
	if out := ask("1790000300000_ctrlc"); !strings.Contains(out, source) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Once indexed, the fork names its source in the index: the harness said so.
func TestAClineForkIsIndexedAsAForkOfItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByID(dir, fork)
	if err != nil || !ok {
		t.Fatalf("fork not indexed: %v", err)
	}
	if s.Kind != "fork" || s.Parent != source {
		t.Errorf("fork indexed as kind %q parent %q, want fork of %s", s.Kind, s.Parent, source)
	}
	if src, _, err := index.FindByID(dir, source); err != nil || src.Kind != "" || src.Parent != "" {
		t.Errorf("the source itself is not a fork: kind %q parent %q err %v", src.Kind, src.Parent, err)
	}
}

// A sibling fork that went on past the source holds work the new fork never
// saw, so the new fork's first prompt still gets it; the source stays out.
func TestAClineForksFirstPromptKeepsASiblingThatWentOn(t *testing.T) {
	dir, _, fork := clineForkStoreWith(t, true)
	payload := mustMarshal(t, map[string]string{"session_id": fork, "cwd": "/w/p", "prompt": "did we add exponential backoff with jitter to fetch.go"})
	var out strings.Builder
	if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), clineSibling) {
		t.Errorf("a sibling fork that went on was left out of a new fork's recall:\n%s", out.String())
	}
}

// The MCP tool reads the live stamps: a fork stamped before any build has seen
// it leaves out its source and keeps a sibling that went on.
func TestALiveClineForkLeavesOutItsSourceOverMCP(t *testing.T) {
	dir, source, fork := clineForkStoreWith(t, true)
	markSessionLive(dir, fork)
	live := liveLineage(dir)
	if !live[source] {
		t.Errorf("a live Cline fork's MCP lineage misses its source: %v", live)
	}
	if live[clineSibling] {
		t.Errorf("a sibling that went on was hidden from a live fork's MCP recall: %v", live)
	}
}

// copilotForkStore holds a Copilot CLI session and the /fork of it, written
// after the build: the fork's first prompt comes before any build has seen
// it. A fork opens with the source's events under its own session.start.
func copilotForkStore(t *testing.T) (dir, source, fork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "copilot")
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	write := func(id, body string) {
		t.Helper()
		p := filepath.Join(root, id, "events.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func(id string, when string) string {
		return fmt.Sprintf(`{"type":"session.start","data":{"sessionId":%q,"startTime":%q,"context":{"cwd":"/w/p"}},"timestamp":%q}`, id, when, when) + "\n"
	}
	turn := func(user, reply string, d time.Duration) string {
		return fmt.Sprintf(`{"type":"user.message","data":{"content":%q},"timestamp":%q}`, user, at(d)) + "\n" +
			fmt.Sprintf(`{"type":"assistant.message","data":{"content":%q},"timestamp":%q}`, reply, at(d+5*time.Second)) + "\n"
	}
	for i := range 20 {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		write(id, start(id, at(-time.Duration(i+2)*time.Hour))+turn(fmt.Sprintf("rename table%d in the billing schema", i), "done", -time.Duration(i+2)*time.Hour))
	}
	prior := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	write(prior, start(prior, at(-time.Hour))+turn("the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", -time.Hour))
	source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	write(source, start(source, at(0))+turn(forkQuestion, "Looking at the retry loop in fetch.go now.", 0))

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(fork, start(fork, at(20*time.Minute))+turn(forkQuestion, "Looking at the retry loop in fetch.go now.", 0)+
		fmt.Sprintf(`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"x\" (%s) as \"x (fork)\" (%s)."},"timestamp":%q}`, source, fork, at(20*time.Minute))+"\n")
	return dir, source, fork
}

// A Copilot CLI fork's first session-start digest listed the session it was
// forked from, whose turns the fork already holds. Copilot's hooks name the
// session and not its file, so the unindexed fork had nothing to be read off
// (#5137).
func TestACopilotForksDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork := copilotForkStore(t)
	digest := func(sid string) string {
		withHookStdin(t, `{"sessionId":"`+sid+`","source":"resume","cwd":"/w/p"}`)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	short := source[:8]
	// The fork first: a session once shown sits out the project's cooldown.
	if out := digest(fork); strings.Contains(out, short) {
		t.Errorf("a Copilot fork was handed its own source as history:\n%s", out)
	}
	if out := digest("cccccccc-cccc-4ccc-8ccc-cccccccccccc"); !strings.Contains(out, short) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The per-prompt recall too: Copilot runs userPromptSubmitted before
// sessionStart, on the fork's first prompt.
func TestACopilotForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork := copilotForkStore(t)
	ask := func(sid string) string {
		payload := mustMarshal(t, map[string]string{"sessionId": sid, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	short := source[:8]
	if out := ask(fork); strings.Contains(out, short) {
		t.Errorf("a Copilot fork's prompt recall named its own source:\n%s", out)
	}
	if out := ask("cccccccc-cccc-4ccc-8ccc-cccccccccccc"); !strings.Contains(out, short) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// A Hermes /branch copies the conversation into a new session, with new
// timestamps, and names its source in model_config._branched_from. The
// branch's recall answered with that source as hit 1 once the source's live
// stamp was gone, after a --resume and /branch or twenty minutes on (#5141).
func TestAHermesBranchsRecallLeavesOutItsSource(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	db := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("DEJA_HERMES_DB", db)
	sqlite := func(script string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite: %v %s", err, out)
		}
	}
	turns := func(id string, at int) string {
		return `insert into sessions values('` + id + `','cli',NULL,'/w/app','{}');
insert into messages (session_id,role,content,timestamp) values
 ('` + id + `','user','password hashing uses bcrypt cost 12, marker ospreyquill',` + itoa(at) + `),
 ('` + id + `','assistant','ok, bcrypt cost 12 it is',` + itoa(at+1) + `);
`
	}
	sqlite(`create table sessions (id text primary key, source text, parent_session_id text, cwd text, model_config text);
create table messages (id integer primary key autoincrement, session_id text not null, role text not null, content text,
 tool_call_id text, tool_calls text, tool_name text, timestamp real not null, token_count integer, finish_reason text);
` + turns("20261018_012943_14524c", 1792200000))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The branch, after the build: its first turn comes before any build.
	sqlite(strings.Replace(turns("20261018_013350_8f1cbe", 1792200100),
		`'cli',NULL,'/w/app','{}'`, `'cli','20261018_012943_14524c',NULL,'{"_branched_from":"20261018_012943_14524c"}'`, 1))

	q := json.RawMessage(`{"query":"ospreyquill"}`)
	markSessionLive(dir, "20261018_020000_dddddd")
	if out, err := callMCPTool(dir, "recall", q); err != nil || !strings.Contains(out, "20261018_012943_14524c") {
		t.Fatalf("the control was not answered with the source, so this proves nothing (%v):\n%s", err, out)
	}
	endSessionLive(dir, "20261018_020000_dddddd")

	markSessionLive(dir, "20261018_013350_8f1cbe")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "20261018_012943_14524c") {
		t.Errorf("the branch's recall answered with the session it was branched from:\n%s", out)
	}
}

// An opencode fork's first MCP recall answered with its source: the fork is
// not indexed on its first turn, and the MCP exclude set was built from the
// index alone, so nothing tied the fork to the session it copied. The hooks
// read such a fork off opencode's db and left the source out (#5129).
func TestAnUnindexedOpencodeForksMCPRecallLeavesOutItsSource(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	db := filepath.Join(t.TempDir(), "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	sqlite := func(script string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite: %v %s", err, out)
		}
	}
	// opencode copies the source's messages into the fork with their times.
	session := func(id, msg string) string {
		return `insert into session values('` + id + `', '/w/oc', '2026-10-17T21:20:00Z', '2026-10-17T21:20:05Z');
insert into message values('` + msg + `u', '` + id + `', 1792271200000, '{"role":"user"}');
insert into part values('` + msg + `pu', '` + msg + `u', '{"type":"text","text":"retry the marmot upload with token quokkaflux"}');
insert into message values('` + msg + `a', '` + id + `', 1792271201000, '{"role":"assistant"}');
insert into part values('` + msg + `pa', '` + msg + `a', '{"type":"text","text":"Retrying the marmot upload now."}');
`
	}
	sqlite(`create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
` + session("ses_source", "s") + session("ses_sibling", "b") + `
insert into message values('bu2', 'ses_sibling', 1792271300000, '{"role":"user"}');
insert into part values('bpu2', 'bu2', '{"type":"text","text":"the sibling went on with token wombatzeal"}');`)
	// The source edited a file: the index files its diff store after its
	// turns, and the fork's own store carries no diff.
	diffs := filepath.Join(filepath.Dir(db), "storage", "session_diff")
	if err := os.MkdirAll(diffs, 0o755); err != nil {
		t.Fatal(err)
	}
	diff := `[{"file": "up.go", "status": "modified", "additions": 1, "deletions": 1, "patch": "--- a/up.go\n+++ b/up.go\n@@ -1,1 +1,1 @@\n-retries := 3\n+retries := 5\n"}]`
	if err := os.WriteFile(filepath.Join(diffs, "ses_source.json"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	sqlite(session("ses_fork", "f"))

	q := json.RawMessage(`{"query":"quokkaflux"}`)
	// Control: another live session is answered with the source.
	markSessionLive(dir, "ses_someone_else")
	if out, err := callMCPTool(dir, "recall", q); err != nil || !strings.Contains(out, "ses_source") {
		t.Fatalf("the control was not answered with the source, so this proves nothing (%v):\n%s", err, out)
	}
	endSessionLive(dir, "ses_someone_else")

	markSessionLive(dir, "ses_fork")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ses_source") {
		t.Errorf("the fork's first recall answered with the session it was forked from:\n%s", out)
	}
	// A sibling fork that went on holds work this fork never saw.
	out, err = callMCPTool(dir, "recall", json.RawMessage(`{"query":"wombatzeal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ses_sibling") {
		t.Errorf("a sibling fork that went on was left out of the fork's recall:\n%s", out)
	}
}

// qwenForkStore holds a Qwen Code session and the /branch of it, written
// after the build: the branch's SessionStart (source "branch") and first
// prompt come before any build has seen it. A branch is a copy of its
// source's records under its own sessionId, each marked forkedFrom.
func qwenForkStore(t *testing.T) (dir, source, fork, forkPath string) {
	t.Helper()
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "-w-p", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	record := func(id, from, typ, role, text string, d time.Duration) string {
		r := map[string]any{
			"uuid": fmt.Sprintf("%s-%s-%d", id[:8], typ, d), "sessionId": id, "timestamp": at(d),
			"type": typ, "cwd": "/w/p", "version": "0.20.0",
			"message": map[string]any{"role": role, "parts": []any{map[string]any{"text": text}}},
		}
		if from != "" {
			r["forkedFrom"] = map[string]any{"sessionId": from, "messageUuid": r["uuid"]}
		}
		b := mustMarshal(t, r)
		return string(b) + "\n"
	}
	turn := func(id, from, user, reply string, d time.Duration) string {
		return record(id, from, "user", "user", user, d) + record(id, from, "assistant", "model", reply, d+5*time.Second)
	}
	write := func(id, body string) string {
		t.Helper()
		p := filepath.Join(chats, id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for i := range 20 {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		d := -time.Duration(i+2) * time.Hour
		write(id, turn(id, "", fmt.Sprintf("rename table%d in the billing schema", i), "done", d))
	}
	prior := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	write(prior, turn(prior, "", "the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", -time.Hour))
	source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	write(source, turn(source, "", forkQuestion, "Looking at the retry loop in fetch.go now.", 0))

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	forkPath = write(fork, turn(fork, source, forkQuestion, "Looking at the retry loop in fetch.go now.", 0))
	return dir, source, fork, forkPath
}

// A Qwen Code /branch opened on a digest that led with the session it was
// branched from, whose turns it already holds: the hook read no head off a
// Qwen transcript, so the unindexed branch had nothing to match its source by.
func TestAQwenBranchsDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork, forkPath := qwenForkStore(t)
	digest := func(sid, transcript string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": transcript, "cwd": "/w/p", "hook_event_name": "SessionStart", "source": "branch"})
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	short := source[:8]
	// The branch first: a session once shown sits out the project's cooldown.
	if out := digest(fork, forkPath); strings.Contains(out, short) {
		t.Errorf("a Qwen branch was handed its own source as history:\n%s", out)
	}
	if out := digest("cccccccc-cccc-4ccc-8ccc-cccccccccccc", ""); !strings.Contains(out, short) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The per-prompt recall on the branch's first prompt reads the same head.
func TestAQwenBranchsPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork, forkPath := qwenForkStore(t)
	ask := func(sid, transcript string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": transcript, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	short := source[:8]
	if out := ask(fork, forkPath); strings.Contains(out, short) {
		t.Errorf("a Qwen branch's prompt recall named its own source:\n%s", out)
	}
	if out := ask("cccccccc-cccc-4ccc-8ccc-cccccccccccc", ""); !strings.Contains(out, short) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Once indexed, the branch names its source in the index: the harness said so.
func TestAQwenBranchIsIndexedAsAForkOfItsSource(t *testing.T) {
	dir, source, fork, _ := qwenForkStore(t)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByID(dir, fork)
	if err != nil || !ok {
		t.Fatalf("branch not indexed: %v", err)
	}
	if s.Kind != "fork" || s.Parent != source {
		t.Errorf("branch indexed as kind %q parent %q, want fork of %s", s.Kind, s.Parent, source)
	}
	if src, _, err := index.FindByID(dir, source); err != nil || src.Kind != "" || src.Parent != "" {
		t.Errorf("the source itself is not a fork: kind %q parent %q err %v", src.Kind, src.Parent, err)
	}
}

const forkQuestion = "the retry loop in fetch.go spins again on HTTP 500 - what did we do last time?"

// forkStore holds a Claude Code session and a Codex thread that were forked,
// each with the session that settled the question long before. The sources
// asked the same question their forks copied; prior sessions answered it.
// The Claude fork is written after the build when unindexed is set, the way a
// fork's first prompts arrive before any build has seen it.
func forkStore(t *testing.T, unindexed bool) (dir, claudeFork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond).Add(123 * time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claude := filepath.Join(tmp, "claude", "-w-p")
	// Other work, so the question's words are rare enough to identify it.
	for i := range 40 {
		id := fmt.Sprintf("other-%d", i)
		write(filepath.Join(claude, id+".jsonl"),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)+"\n")
	}
	write(filepath.Join(claude, "prior-c.jsonl"),
		`{"type":"user","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"the retry loop in fetch.go spins on HTTP 500 again"}}`+"\n"+
			`{"type":"assistant","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:02:00Z","message":{"role":"assistant","content":"Capped the retry loop in fetch.go at MaxAttempts five, so an HTTP 500 stops after five tries."}}`+"\n")
	turn := func(id string) string {
		return fmt.Sprintf(`{"type":"user","uuid":"u-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":%q}}`, id, at(0), forkQuestion) + "\n" +
			fmt.Sprintf(`{"type":"assistant","uuid":"a-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"Looking at the retry loop in fetch.go now."}}`, id, at(5*time.Second)) + "\n"
	}
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	claudeFork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(filepath.Join(claude, source+".jsonl"), turn(source))
	fork := turn(claudeFork) +
		fmt.Sprintf(`{"type":"user","uuid":"u-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"keep going in the branch"}}`, claudeFork, at(20*time.Minute)) + "\n"
	if !unindexed {
		write(filepath.Join(claude, claudeFork+".jsonl"), fork)
	}

	codex := filepath.Join(tmp, "codex", "sessions", "2026", "10", "02")
	write(filepath.Join(codex, "rollout-2026-08-04T09-00-00-prior-x.jsonl"),
		`{"timestamp":"2026-08-04T09:00:00Z","type":"session_meta","payload":{"id":"prior-x","cwd":"/w/q"}}`+"\n"+
			`{"timestamp":"2026-08-04T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the retry loop in fetch.go spins on HTTP 500 again"}]}}`+"\n"+
			`{"timestamp":"2026-08-04T09:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Capped the retry loop in fetch.go at MaxAttempts five."}]}}`+"\n")
	codexTurn := fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, at(0), forkQuestion) + "\n"
	write(filepath.Join(codex, "rollout-2026-10-02T07-00-00-source-x.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"source-x","cwd":"/w/q"}}`, at(0))+"\n"+codexTurn)
	// codex 0.149.0 writes the fork's own session_meta first, naming the
	// thread it came from, then the source's history.
	write(filepath.Join(codex, "rollout-2026-10-02T07-20-00-fork-x.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"fork-x","forked_from_id":"source-x","cwd":"/w/q"}}`, at(20*time.Minute))+"\n"+
			fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"source-x","cwd":"/w/q"}}`, at(0))+"\n"+codexTurn)

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if unindexed {
		write(filepath.Join(claude, claudeFork+".jsonl"), fork)
	}
	return dir, claudeFork
}

// A fork's MCP recall answered with the session it was forked from, whose
// turns are already in the fork (#4549, #4251). Codex names the source in
// forked_from_id; a Claude Code fork opens on the source's own first turn.
func TestAForksRecallLeavesOutItsSource(t *testing.T) {
	dir, fork := forkStore(t, false)
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)

	before, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(before, "aaaaaaaa") || !strings.Contains(before, "source-x") {
		t.Fatalf("the fixture never served the sources, so this proves nothing:\n%s", before)
	}

	markSessionLive(dir, fork)
	markSessionLive(dir, "fork-x")
	after, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, src := range []string{"aaaaaaaa", "source-x"} {
		if strings.Contains(after, src) {
			t.Errorf("a fork's recall answered with its own source (%s):\n%s", src, after)
		}
	}
	for _, prior := range []string{"prior-c", "prior-x"} {
		if !strings.Contains(after, prior) {
			t.Errorf("an older session that settled it (%s) is not on the fork's page:\n%s", prior, after)
		}
	}
}

// The per-prompt recall in a fork named the source's own earlier prompt as
// "asked here before", on the fork's first prompt, before any build had seen
// the fork (#4549). The payload names the transcript, whose first records say
// which turn the fork opens with.
func TestAForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, fork := forkStore(t, true)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	ask := func(sid, path string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": path, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// The fork first: what one session is shown sits out the project's
	// cooldown, so asking the control first would hide the source either way.
	if out := ask(fork, transcript); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork was handed its own source as history:\n%s", out)
	}
	// Control: another session asking the same thing is told about the source.
	if out := ask("someone-else", ""); !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Claude Code starts a fork with source "fork", and the fork already carries
// the digest its source opened with. A second one led with the source (#4549).
func TestAForkOpensWithoutASecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	run := func(payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	if out := run(`{"session_id":"someone-else","source":"startup","cwd":"/w/p"}`); !strings.Contains(out, "retry loop") {
		t.Fatalf("the control got no digest, so this proves nothing:\n%s", out)
	}
	if out := run(`{"session_id":"` + fork + `","source":"fork","cwd":"/w/p"}`); strings.TrimSpace(out) != "" {
		t.Errorf("a fork got a second session-start digest:\n%s", out)
	}
	if !liveSessionIDs(dir)[fork] {
		t.Errorf("the fork was not stamped live")
	}
}

// A Codex fork records its source as its parent, and `deja show` said the
// source had spawned it.
func TestShowSaysAForkWasForked(t *testing.T) {
	var w strings.Builder
	printSpawnEdges(&w, "", model.Session{ID: "fork-x", Kind: "fork", Parent: "01a0fb73-70b2-7572-bae8-690bebe8820f"})
	if got := w.String(); !strings.Contains(got, "forked from 01a0fb73") || strings.Contains(got, "spawned") {
		t.Errorf("a fork reads as spawned:\n%s", got)
	}
}

// The other way round: the source resumed is not the fork. A fork that went on
// past the turns it copied holds work its source never saw, and the source's
// recall left it out because the two open alike (#4549).
func TestASourcesRecallKeepsAForkThatWentOn(t *testing.T) {
	dir, fork := forkStore(t, false)
	q := json.RawMessage(`{"query":"keep going in the branch"}`)
	if before, _ := callMCPTool(dir, "recall", q); !strings.Contains(before, fork[:8]) {
		t.Fatalf("the fixture never served the fork, so this proves nothing:\n%s", before)
	}
	markSessionLive(dir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, fork[:8]) {
		t.Errorf("the resumed source's recall left out a fork's own later work:\n%s", out)
	}
	// Control: the fork asking still leaves its source out.
	endSessionLive(dir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	markSessionLive(dir, fork)
	if out, _ := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork's recall answered with its source:\n%s", out)
	}
}

// A real Claude Code fork opens on records the first 64 KB do not get past —
// a 46 KB attachment ahead of the first user turn — and a fork of a compacted
// session opens on the harness's own preamble, which the index strips before
// it fingerprints anything. Read off the transcript, the fork either had no
// opening or one the index never matched, and its first prompt was answered
// with the source (#4549).
func TestALongHeadedForksPromptRecallLeavesOutItsSource(t *testing.T) {
	tmp := hermeticEnv(t)
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond).Add(316 * time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	claude := filepath.Join(tmp, "claude", "-w-p")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(claude, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		id := fmt.Sprintf("other-%d", i)
		write(id+".jsonl", fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)+"\n")
	}
	opening := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n" + forkQuestion
	session := func(id string, more string) string {
		pad := strings.Repeat("x", 70<<10)
		return fmt.Sprintf(`{"type":"attachment","uuid":"att-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"attachment":{"type":"file","content":%q}}`, id, at(time.Second), pad) + "\n" +
			fmt.Sprintf(`{"type":"user","uuid":"u-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"isCompactSummary":true,"message":{"role":"user","content":%q}}`, id, at(0), opening) + "\n" +
			// The summary is served only when asked for, so the source's own
			// words are what the control is answered with.
			fmt.Sprintf(`{"type":"assistant","uuid":"a-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"Looking at the retry loop in fetch.go that spins on HTTP 500 now."}}`, id, at(5*time.Second)) + "\n" + more
	}
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(source+".jsonl", session(source, ""))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	write(fork+".jsonl", session(fork, fmt.Sprintf(`{"type":"user","uuid":"u-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":%q}}`, fork, at(20*time.Minute), forkQuestion)+"\n"))
	ask := func(sid, path string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": path, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := ask(fork, filepath.Join(claude, fork+".jsonl")); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork was handed its own source as history:\n%s", out)
	}
	if out := ask("someone-else", ""); !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Moving a Claude Code session to the background forks it with
// --fork-session and ends the source, and the source goes on to gain a "No
// response requested." turn and a task notification the fork never sees. The
// fork's recall then answered with the source: its last record was not one the
// fork held (#4251).
func TestABackgroundedForksRecallLeavesOutASourceThatWentOn(t *testing.T) {
	dir, fork := forkStore(t, false)
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", source+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-30 * time.Minute)
	more := fmt.Sprintf(`{"type":"assistant","uuid":"a-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"No response requested."}}`, source, t0.Add(3*time.Minute).Format(time.RFC3339Nano)) + "\n" +
		fmt.Sprintf(`{"type":"user","uuid":"u-3","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"<task-notification><task-id>b1</task-id><status>completed</status></task-notification>"}}`, source, t0.Add(9*time.Minute).Format(time.RFC3339Nano)) + "\n"
	if err = os.WriteFile(path, append(b, more...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)
	if before, _ := callMCPTool(dir, "recall", q); !strings.Contains(before, "aaaaaaaa") {
		t.Fatalf("the fixture never served the source, so this proves nothing:\n%s", before)
	}
	if err = runHookPrompt(dir, strings.NewReader(`{"session_id":"`+fork+`","hook_event_name":"UserPromptSubmit","cwd":"/w/p","prompt":"keep going in the background"}`), io.Discard); err != nil {
		t.Fatal(err)
	}
	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"`+source+`","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`))
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(out, "aaaaaaaa") {
		t.Errorf("a backgrounded fork's recall answered with its own source:\n%s", out)
	}
	if !strings.Contains(out, "prior-c") {
		t.Errorf("the older session that settled it is not on the fork's page:\n%s", out)
	}
}
