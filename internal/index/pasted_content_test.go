package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const wrappedPaste = "<pasted_content id=\"dd73\">\nSet up the zorblax reminder hook\n</pasted_content id=\"dd73\">"

func pasteTestStore(t *testing.T) (tmp, dir string) {
	t.Helper()
	tmp = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return tmp, dir
}

// claudeSession returns the stored session and its manifest row.
func claudeSession(t *testing.T, dir, id string) (string, SessionMeta) {
	t.Helper()
	ss, err := FindManyByIdentity(dir, []Identity{{Harness: "claude", ID: id}})
	if err != nil || len(ss) != 1 {
		t.Fatalf("session %s: %v, %d found", id, err, len(ss))
	}
	var text []string
	for _, m := range ss[0].Messages {
		text = append(text, m.Text)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "claude" && m.ID == id {
			return strings.Join(text, "\n"), m
		}
	}
	t.Fatalf("no manifest row for %s", id)
	return "", SessionMeta{}
}

// A peer on an older build sends Claude Code prompts inside their
// <pasted_content> wrapper; they land as the pasted text, the way this
// machine's own reader stores them.
func TestASyncedPasteArrivesUnwrapped(t *testing.T) {
	tmp, dir := pasteTestStore(t)
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lines := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"<pasted_content id=\"dd73\">\nSet up the zorblax reminder hook\n</pasted_content id=\"dd73\">","time":"` + when + `"}` + "\n"
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-peer.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, m := range metas {
		if m.OrigID == "peer1" {
			id = m.ID
		}
	}
	if id == "" {
		t.Fatal("the synced session was not imported")
	}
	text, meta := claudeSession(t, dir, id)
	if text != "Set up the zorblax reminder hook" {
		t.Errorf("stored text = %q, want the pasted text alone", text)
	}
	if meta.Title != "Set up the zorblax reminder hook" {
		t.Errorf("title = %q", meta.Title)
	}
}

// Sessions a rebuild cannot re-read — a transcript the client deleted, one
// compacted away, one imported — are rebuilt from records.bin, which holds
// what the older reader stored. The version bump that takes the wrapper out
// has to take it out of those too, title included.
func TestARebuildUnwrapsAPasteAnOlderBuildStored(t *testing.T) {
	_, dir := pasteTestStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	const key = "claude:old1"
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	recs := map[string][]Record{key: {
		{Key: key, Role: "user", Text: wrappedPaste, Time: when, SourcePath: syncImportPath},
		{Key: key, Role: "assistant", Text: "<pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays", Time: when.Add(time.Minute), SourcePath: syncImportPath},
	}}
	metas := map[string]SessionMeta{key: {
		ID: "old1", Harness: "claude", Project: importedProjectPrefix + "svc", Path: syncImportPath,
		Title: truncateTitle(wrappedPaste, 60), Started: when, Updated: when.Add(time.Minute),
	}}
	if err := appendImportedRecords(dir, &m, recs, metas); err != nil {
		t.Fatal(err)
	}
	if _, meta := claudeSession(t, dir, "old1"); !strings.Contains(meta.Title, "pasted_content") {
		t.Fatalf("premise: the planted title is %q", meta.Title)
	}

	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	text, meta := claudeSession(t, dir, "old1")
	if want := "Set up the zorblax reminder hook\n<pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays"; text != want {
		t.Errorf("stored text = %q, want %q", text, want)
	}
	if meta.Title != "Set up the zorblax reminder hook" {
		t.Errorf("title = %q, want it derived again from the unwrapped prompt", meta.Title)
	}
}
