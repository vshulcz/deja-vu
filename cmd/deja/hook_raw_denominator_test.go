package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The hook logged the transcript volume of every candidate it loaded, not of
// the sessions the digest kept. With three near-duplicate sessions the digest
// serves one and the log claimed all three — and that figure is the
// denominator of the "N× less context" line `deja stats` prints about itself.
func TestHookRawCountsOnlyTheSessionsItServed(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Near-duplicates by word set, so injection keeps the newest and drops
	// the rest. Sizes differ so the served figure is unambiguous.
	sessions := []struct {
		id    string
		when  string
		lines int
	}{
		{"old1", "2026-08-01T10:00:00Z", 4},
		{"old2", "2026-08-01T11:00:00Z", 3},
		{"new1", "2026-08-01T12:00:00Z", 1},
	}
	const text = "the retry loop in the ticker window backs off before the pool cap"
	var total int64
	for _, s := range sessions {
		body := ""
		for i := 0; i < s.lines; i++ {
			body += `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + s.when + `","sessionId":"` + s.id + `","cwd":"/proj"}` + "\n"
			total += int64(len(text))
		}
		if err := os.WriteFile(filepath.Join(store, s.id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	back, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(back) })

	digest, served, raw, _, _, _ := hookDigestResult(dir)
	if digest == "" || served == 0 {
		t.Skip("no memory to recall in this environment")
	}
	if served != 1 {
		t.Fatalf("fixture served %d sessions, want the near-duplicate filter to keep 1", served)
	}
	if want := int64(len(text)); raw != want {
		t.Errorf("raw = %d, want %d — the served session's transcript, not all %d bytes of candidates", raw, want, total)
	}
}
