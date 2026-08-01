package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func TestNewestFirstMetaIsATotalOrder(t *testing.T) {
	early := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	meta := func(h, id string, at time.Time) SessionMeta {
		return SessionMeta{Harness: h, ID: id, Updated: at}
	}
	cases := []struct {
		name string
		a, b SessionMeta
		want bool
	}{
		{"newer first", meta("claude", "z", late), meta("claude", "a", early), true},
		{"older last", meta("claude", "a", early), meta("claude", "z", late), false},
		{"tie falls to harness", meta("claude", "z", early), meta("cursor", "a", early), true},
		{"tie falls to harness, reversed", meta("cursor", "a", early), meta("claude", "z", early), false},
		{"tie falls to id", meta("claude", "a", early), meta("claude", "b", early), true},
		{"tie falls to id, reversed", meta("claude", "b", early), meta("claude", "a", early), false},
		{"identical is not less", meta("claude", "a", early), meta("claude", "a", early), false},
	}
	for _, c := range cases {
		if got := newestFirstMeta(c.a, c.b); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
		s := func(m SessionMeta) model.Session {
			return model.Session{Harness: m.Harness, ID: m.ID, Updated: m.Updated}
		}
		if got := newestFirstSession(s(c.a), s(c.b)); got != c.want {
			t.Errorf("%s (session): got %v", c.name, got)
		}
	}
}

// A store where several sessions share a stamp — a day's work imported in one
// go, a harness that stamps per-day, transcripts restored from a backup —
// reordered itself on every run: six calls to `deja last`, six answers (#713).
func TestRecentIsStableWhenTimestampsTie(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Enough sessions that map order actually varies between reads.
	for i := 0; i < 60; i++ {
		id := "s" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"work ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	first, err := RecentMatching(dir, 5, query.Options{})
	if err != nil || len(first) != 5 {
		t.Fatalf("%d sessions err=%v", len(first), err)
	}
	for run := 0; run < 20; run++ {
		got, err := RecentMatching(dir, 5, query.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i].ID != first[i].ID {
				t.Fatalf("run %d position %d: %q, first run had %q", run, i, got[i].ID, first[i].ID)
			}
		}
	}
}
