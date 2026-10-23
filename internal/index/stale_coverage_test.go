package index

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The early slice of a first build records how many sessions the build found,
// so an answer from it can say the oldest are what is missing (#5390).
func TestTheEarlySliceKnowsWhatItIsASliceOf(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	ss := make([]model.Session, 0, 450)
	for i := range 450 {
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("session %d", i)}},
		})
	}
	publishNewestFirst(dir, ss, io.Discard)
	c := SnapshotCoverage(dir)
	if c.Sessions != partialPublishSessions || c.PartialOf != 450 {
		t.Errorf("the slice reads as %d sessions of %d, want %d of 450", c.Sessions, c.PartialOf, partialPublishSessions)
	}

	// A complete index is not a slice of anything.
	full := filepath.Join(tmp, "full")
	if err := os.MkdirAll(filepath.Join(full+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(full+".tmp", full, ss, map[string]FileState{"/x.jsonl": {}}, ""); err != nil {
		t.Fatal(err)
	}
	if c := SnapshotCoverage(full); c.PartialOf != 0 || c.Sessions != 450 {
		t.Errorf("a complete index reads as %d sessions of %d", c.Sessions, c.PartialOf)
	}
}

// A refresh handed to the warmup reports how many transcripts it left unread
// and how many the walk found.
func TestAHandedOffRefreshCountsWhatIsPending(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(i int, text string) {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", i, text)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		write(i, "a short turn")
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	inlineAppendMax = 1 << 10
	t.Cleanup(func() { inlineAppendMax = 8 << 20 })
	for i := 3; i < 5; i++ {
		write(i, strings.Repeat("tool output ", 200))
	}
	c, err := EnsureForSearchCoverage(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Stale || c.Pending != 2 || c.Found != 5 || c.Sessions != 3 || c.At.IsZero() {
		t.Errorf("coverage %+v, want stale, 2 of 5 pending, 3 sessions, a date", c)
	}
}
