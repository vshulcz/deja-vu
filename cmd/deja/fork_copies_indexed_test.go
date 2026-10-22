package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
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
