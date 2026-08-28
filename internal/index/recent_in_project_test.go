package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// RecentInProject is the scoped sibling of RecentProject: it answers with the
// sessions of this project, not of every project whose name contains the
// string. `deja handoff` packaged a client's acme/api from a directory named
// api through the loose one (#2336).
func TestRecentInProjectStaysInsideTheProject(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	write := func(dirName, id, cwd string, hoursAgo int) {
		d := filepath.Join(root, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":"a question about %s"}}`, id, at, cwd, id)
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Three sessions in the client's project, all newer than the one that
	// belongs to this checkout: a window taken before scoping would hold none
	// of ours.
	write("-clients-acme-api", "acme1", "/clients/acme/api", 1)
	write("-clients-acme-api", "acme2", "/clients/acme/api", 2)
	write("-clients-acme-api", "acme3", "/clients/acme/api", 3)
	write("-work-api", "mine", "/work/api", 9)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	loose, err := RecentProject(dir, "api", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(loose) != 4 {
		t.Fatalf("the loose helper found %d sessions, want all four — otherwise this measures nothing", len(loose))
	}

	got, err := RecentInProject(dir, "work/api", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("RecentInProject = %v, want this checkout's own session", got)
	}
	// The limit still applies within the project.
	got, err = RecentInProject(dir, "acme/api", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "acme1" {
		t.Fatalf("RecentInProject with a limit = %v, want the two newest of that project", got)
	}
	// A name nothing answers to is empty rather than everything.
	got, err = RecentInProject(dir, "nothing/here", 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("RecentInProject for an unknown project = %v, err %v", got, err)
	}
}
