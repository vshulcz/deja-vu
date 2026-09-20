package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// The saved session is one JSON file: metadata plus messages whose content is
// the Anthropic block list. A call and what it printed are work records, the
// person's words are the user turn, and the harness's own roles — system and
// developer, which is where it puts compaction and sub-agent framing — are
// nobody's speech.
func TestParseCodeWhaleSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "01JA7Q.json")
	body := `{
  "schema_version": 3,
  "metadata": {
    "id": "01JA7Q",
    "title": "the pool runs out under load",
    "created_at": "2026-09-19T10:00:00Z",
    "updated_at": "2026-09-19T10:06:00Z",
    "workspace": "/w/poollab",
    "model": "deepseek-chat"
  },
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "the pool runs out under load, where is the leak"}]},
    {"role": "system", "content": [{"type": "text", "text": "Summary of the compacted half: the caller never returns the connection."}]},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "check the acquire path first"},
      {"type": "text", "text": "Looking at the acquire path."},
      {"type": "tool_use", "id": "c1", "name": "exec_shell", "input": {"command": "go test ./internal/pool -run TestAcquire"}}
    ]},
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "c1", "content": "pool_test.go:88: leaked 4 connections", "is_error": true}]},
    {"role": "assistant", "content": [
      {"type": "tool_use", "id": "c2", "name": "edit_file", "input": {"path": "/w/poollab/internal/pool/acquire.go", "old_string": "defer conn.Close()", "new_string": "defer pool.Put(conn)"}}
    ]}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "codewhale" || s.ID != "01JA7Q" {
		t.Errorf("harness/id = %q/%q", s.Harness, s.ID)
	}
	if s.Project != "w/poollab" {
		t.Errorf("project = %q, want the workspace it was worked in", s.Project)
	}
	if s.Title != "the pool runs out under load" {
		t.Errorf("title = %q", s.Title)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole["user"]; len(got) != 1 || got[0] != "the pool runs out under load, where is the leak" {
		t.Errorf("user turns = %q", got)
	}
	if got := byRole["assistant"]; len(got) != 1 || got[0] != "Looking at the acquire path." {
		t.Errorf("assistant turns = %q — thinking is not speech", got)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./internal/pool -run TestAcquire" {
		t.Errorf("commands = %q", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "pool_test.go:88: leaked 4 connections" {
		t.Errorf("tool output = %q — a failing run is what a later search reaches for", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "/w/poollab/internal/pool/acquire.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 {
		t.Errorf("edits = %q, want the span the edit replaced", got)
	}

	// The harness's own summary is not a message of anyone's.
	for _, texts := range byRole {
		for _, text := range texts {
			if text == "Summary of the compacted half: the caller never returns the connection." {
				t.Error("a system-role record was indexed as something someone said")
			}
		}
	}

	// No per-message timestamps in the file: the session's own start is the
	// clock, one millisecond per record, so two identical turns stay two.
	for i := 1; i < len(s.Messages); i++ {
		if !s.Messages[i].Time.After(s.Messages[i-1].Time) && s.Messages[i].Time != s.Messages[i-1].Time {
			t.Fatalf("record %d goes back in time", i)
		}
	}
}

// Two identical turns are two records, which they cannot be under one stamp.
func TestCodeWhaleTellsTwoIdenticalTurnsApart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "dup.json")
	body := `{"metadata":{"id":"dup","title":"retry","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z","workspace":"/w/dup"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"run it again"}]},
 {"role":"user","content":[{"type":"text","text":"run it again"}]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if n := len(ss[0].Messages); n != 2 {
		t.Fatalf("messages = %d, want both turns", n)
	}
	if ss[0].Messages[0].Time.Equal(ss[0].Messages[1].Time) {
		t.Error("both turns carry one stamp, so the index stores one of them")
	}
}

// `codewhale fork` records the session it came from, and that file is the only
// place the edge exists.
func TestCodeWhaleForkKnowsItsParent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "child.json")
	body := `{"metadata":{"id":"child","title":"fork","created_at":"2026-09-19T11:00:00Z","updated_at":"2026-09-19T11:01:00Z","workspace":"/w/fork","parent_session_id":"parent-1"},
"messages":[{"role":"user","content":[{"type":"text","text":"carry on from the fork point"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if ss[0].Kind != "subagent" || ss[0].Parent != "parent-1" {
		t.Errorf("kind/parent = %q/%q", ss[0].Kind, ss[0].Parent)
	}
}

// The sessions directory holds bookkeeping beside the transcripts: the offline
// queue, the ownership ledger, the legacy checkpoint slot. None is a session,
// and the file list must not offer them to the parser.
func TestCodeWhaleSessionFilesSkipItsBookkeeping(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "checkpoints"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01JA7Q.json", "offline_queue.json", "owners.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "checkpoints", "latest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CodeWhaleSessionFiles()
	if len(got) != 1 || filepath.Base(got[0]) != "01JA7Q.json" {
		t.Fatalf("session files = %v, want the transcript alone", got)
	}
	if !isCodeWhaleSession(filepath.Join(root, "01JA7Q.json")) {
		t.Error("the transcript does not match its own kind")
	}
	for _, name := range []string{"offline_queue.json", "owners.json"} {
		if isCodeWhaleSession(filepath.Join(root, name)) {
			t.Errorf("%s matches as a transcript", name)
		}
	}
	if isCodeWhaleSession(filepath.Join(root, "checkpoints", "latest.json")) {
		t.Error("a checkpoint matches as a transcript")
	}
}

// The store moved with the rebrand and the harness still migrates the old root,
// so both are read — and an explicit $CODEWHALE_HOME is an isolation boundary
// in its own resolver, which deja does not reach outside of either.
func TestCodeWhaleReadsTheLegacyRootUnlessHomeIsExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEWHALE_HOME", "")
	t.Setenv("DEJA_CODEWHALE_ROOT", "")
	legacy := filepath.Join(home, ".deepseek", "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := CodeWhaleRoots()
	if len(roots) != 2 || roots[1] != legacy {
		t.Fatalf("roots = %v, want the legacy sessions directory beside the current one", roots)
	}

	t.Setenv("CODEWHALE_HOME", filepath.Join(home, "isolated"))
	if roots := CodeWhaleRoots(); len(roots) != 1 {
		t.Fatalf("roots = %v, want only the explicit home", roots)
	}
}
