package index

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A WorkBuddy session indexed before #4735 holds the chat folder as its
// project, "WorkBuddy AI/<timestamp>", and the transcript has not changed, so
// incremental ingest would keep it. The version bump re-reads it under the
// workspace root, where the hooks now look.
func TestStaleIndexRefilesAWorkBuddyChatUnderItsRoot(t *testing.T) {
	if version < 67 {
		t.Fatalf("version %d: an index built before chat folders were named for their root keeps one project per chat", version)
	}
	root, dir := allHarnessEnv(t)
	home := filepath.Join(root, "home")
	cwd := filepath.Join(home, "WorkBuddy AI", "2026-10-05-14-52-46")
	id := "72784911-4fa0-423d-b43f-685d8a6fa2b7"
	ts := strconv.FormatInt(time.Date(2026, 10, 5, 14, 53, 0, 0, time.UTC).UnixMilli(), 10)
	write(t, filepath.Join(home, ".workbuddy-ai", "projects", "WorkBuddy AI-2026-10-05-14-52-46", id+".jsonl"),
		`{"id":"u1","timestamp":`+ts+`,"type":"message","role":"user","content":[{"type":"input_text","text":"payments retry backoff"}],"sessionId":"`+id+`","cwd":`+strconv.Quote(cwd)+"}\n")

	o := search.Options{Query: "backoff", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "codebuddy:" + id
	meta, ok := m.Sessions[key]
	if !ok {
		t.Fatalf("session not in manifest: %v", m.Sessions)
	}
	want := meta.Project
	if want != "home/WorkBuddy AI" {
		t.Fatalf("project = %q, want home/WorkBuddy AI", want)
	}

	// What the previous version left: the chat folder as the project.
	meta.Project = "WorkBuddy AI/2026-10-05-14-52-46"
	m.Sessions[key] = meta
	m.Version = 66
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if m, err = readManifest(dir); err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions[key].Project; got != want {
		t.Errorf("after upgrade project = %q, want %q", got, want)
	}
}
