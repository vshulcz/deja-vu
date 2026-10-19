package index

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// DEJA_EXCLUDE_HARNESSES kept a store out of a rebuild and nothing else: every
// incremental pass walked it and indexed what it had written since (#5394).
func TestAnExcludedStoreIsNotWalkedOnAnyPass(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CODEX_ROOT": "codex"})
	codexSession := func(id, text string) string {
		roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
		parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
			`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"`+text+`"}]}}`+"\n"+
			`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
		return roll
	}
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-app", "c1.jsonl"),
		`{"type":"user","sessionId":"c1","cwd":"/tmp/app","timestamp":"2026-07-17T08:00:00Z","message":{"role":"user","content":"why does the quokkabloom retry"}}`+"\n", false)
	const before, after = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001", "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0002"
	indexed := func(dir, harness, id string) bool {
		return sessionView(t, dir, harness, id) != "(not in the index)\n"
	}
	codexSession(before, "indexed before the store was excluded")
	dir := filepath.Join(tmp, "idx")
	parityPass(t, dir, false)
	if !indexed(dir, "codex", before) {
		t.Fatal("control: the codex session was not indexed with nothing excluded")
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	newRoll := codexSession(after, "written after the store was excluded")
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-app", "c2.jsonl"),
		`{"type":"user","sessionId":"c2","cwd":"/tmp/app","timestamp":"2026-07-17T10:00:00Z","message":{"role":"user","content":"a second claude session"}}`+"\n", false)

	codexRoot := filepath.Join(tmp, "codex") + string(filepath.Separator)
	for p := range currentFiles("") {
		if strings.HasPrefix(p, codexRoot) {
			t.Errorf("the walk listed %s from an excluded store", p)
		}
	}
	if n := HarnessUnreadCounts(dir)["codex"]; n != 0 {
		t.Errorf("doctor's unread count walked the excluded store: %d", n)
	}

	// The stale check, the search refresh and `deja index` each take the walk.
	if _, err := EnsureForSearchStale(dir, query.Options{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	parityPass(t, dir, false)
	if indexed(dir, "codex", after) {
		t.Errorf("an incremental pass indexed %s from the excluded store", filepath.Base(newRoll))
	}
	if !indexed(dir, "claude", "c2") {
		t.Error("control: the claude session written alongside was not indexed")
	}
	// What was indexed before stays until a rebuild, as with a project
	// pattern, and the pass that keeps it settles.
	if !indexed(dir, "codex", before) {
		t.Error("the incremental pass dropped the session indexed before the exclusion")
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Error("the index never settles while a store is excluded")
	}
	parityPass(t, dir, true)
	if indexed(dir, "codex", before) || indexed(dir, "codex", after) {
		t.Error("a rebuild kept the excluded store's sessions")
	}
}
