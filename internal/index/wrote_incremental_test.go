package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The written side has to arrive through the incremental path too. A new
// session in any store triggers one, and a derived file that only a full
// rebuild produces is missing for exactly the sessions a reader has just
// finished working in — the sidecar mistake of #3500, which #3773 asked not to
// repeat.
func TestTheWrittenSideSurvivesAnIncrementalUpdate(t *testing.T) {
	const first = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	const appended = "pool, err := pgxpool.NewWithConfig(ctx, cfg) // one per process"

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-wrote")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(at, written string) string {
		return `{"type":"assistant","sessionId":"s1","cwd":"/w","timestamp":"` + at +
			`","message":{"role":"assistant","content":[{"type":"tool_use","input":` +
			`{"file_path":"/w/pool.go","old_string":"x","new_string":"` + written + `"}}]}}`
	}
	file := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(file, []byte(turn("2026-01-02T03:04:05Z", first)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The turn that arrives after the index was built: appended to the same
	// transcript, which is what an incremental pass reads from where it
	// stopped.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(turn("2026-01-02T04:04:05Z", appended) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	held := map[uint64]bool{}
	ids := []Identity{{Harness: "claude", ID: "s1"}}
	sessions, err := FindManyByIdentity(dir, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		for _, m := range s.Messages {
			if m.Role != sources.RoleWrote {
				continue
			}
			for _, h := range strings.Fields(strings.SplitN(m.Text, "\n", 2)[1]) {
				held[parseHexForTest(t, h)] = true
			}
		}
	}
	for what, line := range map[string]string{"the first pass": first, "the incremental pass": appended} {
		h, ok := sources.HashWrittenLine(line)
		if !ok {
			t.Fatalf("%s: the line is not evidence", what)
		}
		if !held[h] {
			t.Errorf("%s: the written line is not in the index", what)
		}
	}
}

func parseHexForTest(t *testing.T, s string) uint64 {
	t.Helper()
	var v uint64
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint64(c-'a') + 10
		default:
			t.Fatalf("not a hash: %q", s)
		}
	}
	return v
}
