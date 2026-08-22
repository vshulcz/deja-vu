package index

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Grok Build writes the spawn edge into summary.json itself. deja treated every
// directory as an unrelated session, so after a hit on the parent there was no
// way to reach the child that did the work, and after a hit on the child no way
// to name what asked for it (#1385). Fixture fields are the ones the report
// quoted from a real store.
func TestGrokSpawnEdgeSurvivesTheIndex(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	root := filepath.Join(tmp, "grok", "sessions", url.PathEscape("/work/spawn"))
	write := func(id, summary, text string) {
		t.Helper()
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
			t.Fatal(err)
		}
		line := `{"timestamp":1785000001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"},"_meta":{"promptIndex":0}}}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("01a00a65-parent", `{"info":{"id":"01a00a65-parent"},"generated_title":"Parent run","created_at":"2026-08-16T21:30:00Z","updated_at":"2026-08-16T21:30:01Z"}`, "spawnparentneedle the plan")
	write("01a00a65-child", `{"info":{"id":"01a00a65-child"},"generated_title":"Child run","created_at":"2026-08-16T21:31:19Z","updated_at":"2026-08-16T21:31:20Z","session_kind":"subagent_fork","parent_session_id":"01a00a65-parent","agent_name":"general-purpose","forked_at":"2026-08-16T21:31:19.371006700Z"}`, "spawnchildneedle the actual work")
	// A plain subagent records no parent. deja must keep it searchable and say
	// nothing about who spawned it.
	write("01a00a65-orphan", `{"info":{"id":"01a00a65-orphan"},"generated_title":"Sibling run","created_at":"2026-08-16T21:32:00Z","updated_at":"2026-08-16T21:32:01Z","session_kind":"subagent"}`, "spawnorphanneedle a sibling")

	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "spawnchildneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}

	child, ok, err := FindByID(dir, "01a00a65-child")
	if err != nil || !ok {
		t.Fatalf("child missing: ok=%v err=%v", ok, err)
	}
	if child.Parent != "01a00a65-parent" || child.Kind != "subagent_fork" || child.Agent != "general-purpose" {
		t.Errorf("spawn fields lost in the index: kind=%q parent=%q agent=%q", child.Kind, child.Parent, child.Agent)
	}

	kids, err := ChildrenOf(dir, "01a00a65-parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].ID != "01a00a65-child" {
		t.Fatalf("children of the parent: %#v", kids)
	}

	orphan, ok, err := FindByID(dir, "01a00a65-orphan")
	if err != nil || !ok {
		t.Fatalf("orphan missing: ok=%v err=%v", ok, err)
	}
	if orphan.Kind != "subagent" {
		t.Errorf("kind = %q, want the harness's own word", orphan.Kind)
	}
	if orphan.Parent != "" {
		t.Errorf("parent = %q — deja invented an edge the harness did not record", orphan.Parent)
	}
	if kids, err := ChildrenOf(dir, "01a00a65-child"); err != nil || len(kids) != 0 {
		t.Errorf("a leaf reported children: %#v err=%v", kids, err)
	}
}
