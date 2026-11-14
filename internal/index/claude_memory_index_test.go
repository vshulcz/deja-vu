package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestClaudeAutoMemoryFullLifecycleInIndex(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	claudeRoot := filepath.Join(home, ".claude", "projects")
	memDir := filepath.Join(claudeRoot, "-Users-alex-svc", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CLAUDE_MEM_DB", filepath.Join(tmp, "no-claude-mem.db"))
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(tmp, "no-xcode"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(tmp, "no-cc-mirror"))

	notePath := filepath.Join(memDir, "release_gate.md")
	writeNote := func(body string, stamp time.Time) {
		t.Helper()
		if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(notePath, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	writeNote("---\nname: release-gate\ndescription: Deployment freeze rules\ntype: project\n---\nFirst paragraph: GlacierTokenOne.\n\nSecond paragraph: GlacierTokenTwo deep in body.\n", t0)

	dir := filepath.Join(tmp, "index")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("initial Ensure: %v", err)
	}

	// Both paragraphs from full body must be searchable.
	for _, q := range []string{"GlacierTokenOne", "GlacierTokenTwo"} {
		ss, err := Search(dir, search.Options{Query: q})
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(ss) != 1 {
			t.Fatalf("Search(%q) returned %d sessions, want 1: %#v", q, len(ss), ss)
		}
	}

	// Update in place (longer text, newer mtime): ParseFrom is nil so replacePass
	// replaces prior content cleanly without duplicating old paragraphs.
	t1 := t0.Add(time.Minute)
	writeNote("---\nname: release-gate\ndescription: Updated deployment rules\ntype: project\n---\nReplaced paragraph: GlacierTokenThree only.\n\nExtra trailing paragraph for larger file size.\n", t1)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("update Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenTwo"}); err != nil || len(ss) != 0 {
		t.Fatalf("stale GlacierTokenTwo remained after edit: ss=%#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenThree"}); err != nil || len(ss) != 1 {
		t.Fatalf("updated GlacierTokenThree = %#v, err=%v", ss, err)
	}

	// Editing frontmatter to veto the note drops its indexed records.
	t2 := t1.Add(time.Minute)
	writeNote("---\nname: release-gate\nstate: rejected\n---\nGlacierTokenFour must be vetoed.\n", t2)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("veto Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenThree"}); err != nil || len(ss) != 0 {
		t.Fatalf("GlacierTokenThree remained after veto: ss=%#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFour"}); err != nil || len(ss) != 0 {
		t.Fatalf("vetoed GlacierTokenFour indexed: ss=%#v err=%v", ss, err)
	}

	// Re-enable, index, then delete the .md file from disk: deletion removes it
	// cleanly rather than keeping it like an expired transcript.
	t3 := t2.Add(time.Minute)
	writeNote("---\nname: release-gate\n---\nGlacierTokenFive active note.\n", t3)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("re-enable Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFive"}); err != nil || len(ss) != 1 {
		t.Fatalf("GlacierTokenFive before delete = %#v, err=%v", ss, err)
	}
	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatalf("post-delete Ensure: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "GlacierTokenFive"}); err != nil || len(ss) != 0 {
		t.Fatalf("deleted note remained in index: ss=%#v err=%v", ss, err)
	}
}
