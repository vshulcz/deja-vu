package embed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func heldIDs(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

func noteMatch(id string, score float64) match {
	return match{
		session: model.Session{ID: id, Harness: "deja"},
		record:  index.Record{Text: "pool cap settled at 40"},
		score:   score,
	}
}

// A promoted note is one distilled line where its transcript is a whole
// session, so it is often worded further from the query than the source it was
// made from. The floor then cut the note while serving the source — and the
// answer `promote` says it just put in front was not in the result at all,
// which is the "not in fifty results" case of #2803.
func TestANoteBelowTheFloorIsServedWithItsSource(t *testing.T) {
	out := []search.Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}, Score: 0.81},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 0.62},
	}
	held := map[string]match{
		"deja:deja-note-claude-sess1": noteMatch("deja-note-claude-sess1", 0.30),
		// A note whose source nobody asked about stays out.
		"deja:deja-note-claude-elsewhere": noteMatch("deja-note-claude-elsewhere", 0.40),
	}
	got := withHeldNotes(out, held)
	search.LiftNotesAboveTheirSource(got)

	ids := heldIDs(got)
	want := []string{"other", "deja-note-claude-sess1", "sess1"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
}

// Nothing held, nothing changed — and a held note whose source is absent is not
// a result on its own.
func TestHeldNotesTravelOnlyWithTheirSource(t *testing.T) {
	out := []search.Hit{{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 0.7}}
	if got := withHeldNotes(out, nil); len(got) != 1 {
		t.Errorf("nothing was held and the answer changed: %v", heldIDs(got))
	}
	held := map[string]match{"deja:deja-note-claude-gone": noteMatch("deja-note-claude-gone", 0.4)}
	if got := withHeldNotes(out, held); len(got) != 1 {
		t.Errorf("a note whose source is not in the answer was served anyway: %v", heldIDs(got))
	}
}

func TestHeldRepoAndSyncedNotesAreServedWithTheirSource(t *testing.T) {
	out := []search.Hit{
		{Session: model.Session{ID: "imported-src", OrigID: "sess1", Harness: "claude"}, Score: 0.65},
	}
	held := map[string]match{
		"deja:deja-repo-note-claude-sess1": {
			session: model.Session{ID: "imported-note", OrigID: "deja-repo-note-claude-sess1", Harness: "deja"},
			record:  index.Record{Text: "repo decision"},
			score:   0.35,
		},
	}
	got := withHeldNotes(out, held)
	search.LiftNotesAboveTheirSource(got)
	ids := heldIDs(got)
	want := []string{"imported-note", "imported-src"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("got %v, want %v", ids, want)
	}
}

func TestSemanticSearchReadsWinningOffsetsWithoutFullLogScan(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	notesFile := filepath.Join(tmp, "notes.jsonl")
	for key, value := range map[string]string{
		"HOME": tmp, "USERPROFILE": tmp, "DEJA_CLAUDE_ROOT": claudeRoot,
		"DEJA_NOTES_FILE": notesFile, "DEJA_CODEX_ROOT": filepath.Join(tmp, "codex"),
		"DEJA_OPENCODE_DB": filepath.Join(tmp, "open.db"), "DEJA_AIDER_ROOTS": filepath.Join(tmp, "aider"),
		"DEJA_GEMINI_ROOT": filepath.Join(tmp, "gemini"), "DEJA_CURSOR_ROOT": filepath.Join(tmp, "cursor"),
		"DEJA_CURSOR_CLI_ROOT": filepath.Join(tmp, "cursor-cli"), "DEJA_ANTIGRAVITY_ROOT": filepath.Join(tmp, "antigravity"),
		"DEJA_GROK_ROOT": filepath.Join(tmp, "grok"), "DEJA_QWEN_ROOT": filepath.Join(tmp, "qwen"),
	} {
		t.Setenv(key, value)
	}
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		id := fmt.Sprintf("s%02d", i)
		line := fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"cwd\":\"/work/app\",\"timestamp\":\"2026-03-01T10:%02d:00Z\",\"message\":{\"role\":\"user\",\"content\":\"session %s pool tuning detail\"}}\n", id, i, id)
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	noteID := model.PromotedNoteID("claude:s11")
	noteLine := `{"ts":"2026-03-01T10:30:00Z","kind":"promoted","session":"claude:s11","state":"accepted","text":"pool cap settled at 40"}` + "\n"
	if err := os.WriteFile(notesFile, []byte(noteLine), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	vectors := make([]Vector, 0, len(records))
	for _, r := range records {
		v := []float32{0.8, 0.6}
		switch r.Record.Key {
		case "claude:s11":
			v = []float32{1, 0}
		case model.SessionKey(notesHarness, noteID):
			v = []float32{0.3, 0.9539392}
		}
		vectors = append(vectors, Vector{
			Offset: r.Offset,
			Key:    r.Record.Key,
			Values: v,
		})
	}
	sidecar := Sidecar{Generation: gen, Dim: 2, Covered: len(vectors), Vectors: vectors}
	before := index.RecordLogScans()
	hits, err := semanticHitsForQuery(dir, search.Options{Query: "pool", All: true}, sidecar, []float32{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	if index.RecordLogScans() != before {
		t.Fatalf("semanticHitsForQuery walked the full record log (%d -> %d)", before, index.RecordLogScans())
	}
	if len(hits) != 10 {
		t.Fatalf("len(hits) = %d, want 10", len(hits))
	}
	if hits[0].Session.ID != noteID || !strings.Contains(hits[0].Snippets[0], "pool cap settled at 40") {
		t.Fatalf("hits[0] = %+v, want lifted promoted note %s", hits[0], noteID)
	}
	if hits[1].Session.ID != "s11" || !strings.Contains(hits[1].Snippets[0], "session s11 pool tuning detail") {
		t.Fatalf("hits[1] = %+v, want source session s11", hits[1])
	}
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := semanticHitsForQuery(dir, search.Options{Query: "pool", All: true}, sidecar, []float32{1, 0}); err == nil {
		t.Fatal("missing records.bin was accepted")
	}
}
