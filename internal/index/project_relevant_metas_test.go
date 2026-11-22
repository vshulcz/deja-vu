package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestProjectRelevantMetasSkipsRecordLogAndMatchesRanking(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessions := []model.Session{
		{
			Harness: "claude",
			ID:      "prm-a1",
			Project: "proj-alpha",
			Path:    "/work/proj-alpha/a1.jsonl",
			Title:   "quokkacache invalidation backoff",
			Started: base,
			Updated: base.Add(2 * time.Hour),
			Touched: []string{"cache/store.go", "cache/retry.go"},
			Messages: []model.Message{
				{Role: "user", Time: base, Text: "fix quokkacache invalidation backoff retry"},
				{Role: "edit", Time: base.Add(time.Minute), Text: "cache/store.go\nfunc invalidate() {}"},
				{Role: "assistant", Time: base.Add(2 * time.Minute), Text: "Resolved quokkacache invalidation backoff."},
			},
		},
		{
			Harness: "claude",
			ID:      "prm-a2",
			Project: "proj-alpha",
			Path:    "/work/proj-alpha/a2.jsonl",
			Title:   "quokkacache metrics gauge",
			Started: base.Add(-2 * time.Hour),
			Updated: base.Add(-time.Hour),
			Touched: []string{"metrics/gauge.go"},
			Messages: []model.Message{
				{Role: "user", Time: base.Add(-2 * time.Hour), Text: "add quokkacache metrics"},
				{Role: "assistant", Time: base.Add(-time.Hour), Text: "Added gauge metric."},
			},
		},
		{
			Harness: "claude",
			ID:      "prm-skip",
			Project: "proj-alpha",
			Path:    "/work/proj-alpha/skip.jsonl",
			Title:   "quokkacache invalidation backoff duplicate",
			Started: base.Add(time.Hour),
			Updated: base.Add(3 * time.Hour),
			Touched: []string{"cache/skip.go"},
			Messages: []model.Message{
				{Role: "user", Time: base.Add(time.Hour), Text: "quokkacache invalidation backoff retry"},
			},
		},
		{
			Harness: "claude",
			ID:      "prm-beta",
			Project: "proj-beta",
			Path:    "/work/proj-beta/b1.jsonl",
			Title:   "quokkacache invalidation backoff beta",
			Started: base.Add(4 * time.Hour),
			Updated: base.Add(4 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Time: base.Add(4 * time.Hour), Text: "quokkacache invalidation backoff"},
			},
		},
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	skip := map[string]bool{"prm-skip": true}
	terms := []string{"quokkacache", "invalidation", "backoff"}
	wantSessions, wantMatched, wantStrong, _, _, err := ProjectRelevantNamingCut(dir, []string{"proj-alpha"}, terms, 10, skip, nil)
	if err != nil {
		t.Fatalf("ProjectRelevantNamingCut: %v", err)
	}
	if len(wantSessions) == 0 {
		t.Fatal("ProjectRelevantNamingCut returned no sessions")
	}

	gotMetas, gotMatched, gotStrong, err := ProjectRelevantMetas(dir, []string{"proj-alpha"}, terms, 10, skip)
	if err != nil {
		t.Fatalf("ProjectRelevantMetas before removing records.bin: %v", err)
	}
	if len(gotMetas) != len(wantSessions) {
		t.Fatalf("len(gotMetas) = %d, want %d", len(gotMetas), len(wantSessions))
	}
	for i := range wantSessions {
		if gotMetas[i].Harness != wantSessions[i].Harness || gotMetas[i].ID != wantSessions[i].ID {
			t.Fatalf("meta[%d] = %s:%s, want %s:%s", i, gotMetas[i].Harness, gotMetas[i].ID, wantSessions[i].Harness, wantSessions[i].ID)
		}
		if gotMatched[i] != wantMatched[i] || gotStrong[i] != wantStrong[i] {
			t.Fatalf("counts[%d] = (%d,%d), want (%d,%d)", i, gotMatched[i], gotStrong[i], wantMatched[i], wantStrong[i])
		}
	}

	if rerr := os.Remove(filepath.Join(dir, "records.bin")); rerr != nil {
		t.Fatal(rerr)
	}
	_ = os.Remove(filepath.Join(dir, "recoffsets.bin"))

	afterMetas, afterMatched, afterStrong, err := ProjectRelevantMetas(dir, []string{"proj-alpha"}, terms, 10, skip)
	if err != nil {
		t.Fatalf("ProjectRelevantMetas without records.bin: %v", err)
	}
	if len(afterMetas) != len(gotMetas) {
		t.Fatalf("len(afterMetas) = %d, want %d", len(afterMetas), len(gotMetas))
	}
	for i := range gotMetas {
		if afterMetas[i].Key() != gotMetas[i].Key() || afterMatched[i] != gotMatched[i] || afterStrong[i] != gotStrong[i] {
			t.Fatalf("without records.bin mismatch at %d: got (%s,%d,%d), want (%s,%d,%d)",
				i, afterMetas[i].Key(), afterMatched[i], afterStrong[i], gotMetas[i].Key(), gotMatched[i], gotStrong[i])
		}
	}
}
