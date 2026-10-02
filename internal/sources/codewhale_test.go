package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	for _, name := range []string{"01JA7Q.json", "offline_queue.json", "session_boot_owners.json", "notes.txt"} {
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
	for _, name := range []string{"offline_queue.json", "session_boot_owners.json"} {
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

// Since 0.9.6 new CodeWhale turns use one small toolbox — read, write, edit,
// bash — and edit takes edits[{oldText,newText}]. Knowing only the older names
// left a session with its commands and none of the files it read or changed
// (#4360).
func TestCodeWhaleReadsTheSmallToolbox(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "0d6f.json")
	body := `{"metadata":{"id":"0d6f","title":"retry","created_at":"2026-09-30T10:00:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop in client.go"}]},
 {"role":"assistant","content":[
  {"type":"tool_use","id":"call_1","name":"bash","input":{"command":"go test ./...","cwd":"/tmp/proj"}},
  {"type":"tool_use","id":"call_2","name":"read","input":{"path":"/tmp/proj/client.go"}},
  {"type":"tool_use","id":"call_3","name":"edit","input":{"path":"/tmp/proj/client.go","edits":[{"oldText":"for i := 0; i <= max; i++","newText":"for i := 0; i < max; i++"}]}},
  {"type":"tool_use","id":"call_4","name":"write","input":{"path":"/tmp/proj/retry_test.go","content":"func TestRetryStopsAtMax(t *testing.T) { if got := retry(3); got != 3 { t.Fatal(got) } }\n"}}
 ]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./..." {
		t.Errorf("commands = %q", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "/tmp/proj/client.go\n/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want what read, edit and write named", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/tmp/proj/client.go\nfor i := 0; i <= max; i++" {
		t.Errorf("edits = %q, want the span from edits[].oldText", got)
	}
	if got := byRole[RoleWrote]; len(got) != 2 {
		t.Errorf("wrote = %q, want the edit's newText and the written file", got)
	}
}

// CodeWhale keeps session_boot_owners.json, a map of session id to boot id,
// beside the transcripts. Under any other name it was walked as a transcript
// and doctor counted one file too many (#4361).
func TestCodeWhaleSkipsTheBootOwnersLedger(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	for name, body := range map[string]string{
		"0d6f.json":                `{"metadata":{"id":"0d6f"},"messages":[]}`,
		"session_boot_owners.json": `{"0d6f":"boot_1a2b3c4d5e6f"}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := CodeWhaleSessionFiles(); len(got) != 1 || filepath.Base(got[0]) != "0d6f.json" {
		t.Errorf("session files = %v, want the transcript alone", got)
	}
	ledger := filepath.Join(root, "session_boot_owners.json")
	if isCodeWhaleSession(ledger) {
		t.Error("the boot-owners ledger matches as a transcript")
	}
	if got := CodeWhaleSidecarFiles(); len(got) != 1 || got[0] != ledger {
		t.Errorf("sidecars = %v, want the ledger placed", got)
	}
}

// edit takes its edits the way models send them, and CodeWhale folds two
// shapes onto edits[] before running: the array as a JSON string, and a single
// top-level oldText/newText. The transcript keeps what the model sent, so
// reading only the array lost those edits (#4360).
func TestCodeWhaleReadsAnEditInEveryShapeItRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	path := filepath.Join(root, "0d70.json")
	body := `{"metadata":{"id":"0d70","title":"retry","created_at":"2026-09-30T10:00:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","content":[
  {"type":"tool_use","id":"call_1","name":"edit","input":{"path":"/tmp/proj/client.go","edits":"[{\"oldText\":\"i <= max\",\"newText\":\"for attempt := 0; attempt < maxRetries; attempt++\"},{\"oldText\":\"sleep(1)\",\"newText\":\"time.Sleep(backoff * time.Duration(attempt))\"}]"}},
  {"type":"tool_use","id":"call_2","name":"edit","input":{"path":"/tmp/proj/server.go","oldText":"retries := 0","newText":"retries := defaultRetriesForServer"}}
 ]}
]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, wrote []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	want := []string{"/tmp/proj/client.go\ni <= max", "/tmp/proj/client.go\nsleep(1)", "/tmp/proj/server.go\nretries := 0"}
	if len(edits) != len(want) || edits[0] != want[0] || edits[1] != want[1] || edits[2] != want[2] {
		t.Errorf("edits = %q, want %q", edits, want)
	}
	if len(wrote) != 3 {
		t.Errorf("wrote = %q, want one per edit", wrote)
	}
}

// CodeWhale's edit_file takes search/replace as its own names and folds the
// other harnesses' spellings onto them (tools/file.rs EDIT_ALIASES). Reading
// old_string alone left a search/replace call with a files record and no edit
// or wrote record (#4404).
func TestCodeWhaleReadsAnEditWrittenWithSearchAndReplace(t *testing.T) {
	for _, keys := range [][2]string{{"search", "replace"}, {"old_string", "new_string"}, {"old_str", "new_str"}, {"oldText", "newText"}, {"old_text", "replacement"}} {
		raw := `[{"type":"tool_use","id":"t1","name":"edit_file","input":{"path":"/tmp/proj/client.go","` +
			keys[0] + `":"for i := 0; i <= max; i++","` + keys[1] + `":"for i := 0; i < max; i++"}}]`
		var edit, wrote int
		for _, m := range codeWhaleWorkRecords(json.RawMessage(raw), time.Time{}) {
			switch m.Role {
			case RoleEdit:
				edit++
			case RoleWrote:
				wrote++
			}
		}
		if edit != 1 || wrote != 1 {
			t.Errorf("%s/%s: %d edit and %d wrote records, want one of each", keys[0], keys[1], edit, wrote)
		}
	}
}
