package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A directory the trust policy says is not to be recalled must be unreachable
// from every surface, not from the one that happens to filter. `WithoutIgnored`
// was called at a single call site — the CLI's own search — so `deja doctor`
// printed "not recalled */.claude/jobs/*" while the per-prompt hook injected
// those sessions into every message.
func TestIgnoredSessionsAreOutOfReachEverywhere(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "home", ".claude", "jobs", "abc", "projects")
	proj := filepath.Join(root, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		return `{"type":"` + role + `","sessionId":"scratch","cwd":"/w/app",` +
			`"timestamp":"2026-01-02T03:04:05Z","message":{"role":"` + role +
			`","content":"` + text + `"}}`
	}
	if err := os.WriteFile(filepath.Join(proj, "scratch.jsonl"),
		[]byte(strings.Join([]string{
			line("user", "the frobnicator keeps dropping its widgets under load"),
			line("assistant", "Decision: we pinned the frobnicator to one shard."),
		}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The scoring path, which is what `deja search` walks.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "frobnicator", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 0 {
		t.Errorf("an ignored session came back from search: %d", len(r.Sessions))
	}

	// The relevance path, which is what a sentence-shaped question reaches.
	r, err = SearchWithRecoveryDetailed(dir,
		query.Options{Query: "why does the frobnicator drop widgets under load", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 0 {
		t.Errorf("an ignored session came back from the relevance tier: %d", len(r.Sessions))
	}

	// The per-prompt hook's own ranking, which is the most automatic surface
	// deja has and the one this rule most has to hold on.
	ss, _, _, _, err := ProjectRelevantSkipping(dir, []string{"w/app", "app"},
		[]string{"frobnicator", "widgets"}, 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("an ignored session was ranked for injection: %d", len(ss))
	}
}
