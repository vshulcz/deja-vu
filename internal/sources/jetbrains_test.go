package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jetBrainsFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "jetbrains"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_JETBRAINS_ROOT", root)
	return filepath.Join(root, "IntelliJIdea2026.2", "workspace", "2xQ7mN4bV8cK1pR6tY3wZ9aL0dF.xml")
}

func TestJetBrainsReadsChatsAndAgentTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := jetBrainsFixture(t)
	if files := JetBrainsSessionFiles(); len(files) != 1 || !isJetBrainsWorkspace(files[0]) {
		t.Fatalf("workspace files = %v", files)
	}
	ss, err := ParseJetBrainsFile(ws)
	if err != nil {
		t.Fatal(err)
	}
	// The codex-acp chat is left to Codex's own store.
	if len(ss) != 2 {
		t.Fatalf("%d sessions, want the chat and the local agent's task", len(ss))
	}
	project := projectName(filepath.Join(home, "src", "notes-api"))
	byID := map[string][]string{}
	for _, s := range ss {
		if s.Harness != "jetbrains" || s.Project != project || s.Started.IsZero() {
			t.Fatalf("session = %q %q %q %v", s.ID, s.Harness, s.Project, s.Started)
		}
		for _, m := range s.Messages {
			byID[s.ID] = append(byID[s.ID], m.Role+": "+strings.SplitN(m.Text, "\n", 2)[0])
		}
	}
	chat := strings.Join(byID["7c1e9a52-3b4d-4f60-8a71-2d9e0b4c6f13"], "\n")
	if chat != "user: how do I add cursor pagination to GET /notes\nassistant: Return a next_cursor with the last note id and filter on id > cursor in the query." {
		t.Fatalf("chat:\n%s", chat)
	}
	task := strings.Join(byID["b84f2c10-6e9a-4d37-9c25-1a7e3f8d0b46"], "\n")
	for _, want := range []string{
		"user: the notes list test fails one run in five, find out why",
		"assistant: Running the test a few times to catch the failure.",
		"command: $ go test -count=5 ./notes  → exit 1",
		"files: " + filepath.Join(home, "src", "notes-api", "notes", "list.go"),
		"edit: " + filepath.Join(home, "src", "notes-api", "notes", "list.go"),
		"command: $ go test -count=5 ./notes",
		"assistant: List returned map order",
	} {
		if !strings.Contains(task, want) {
			t.Fatalf("task records lack %q:\n%s", want, task)
		}
	}
	if _, ok := byID["e2a7d913-58c4-4b0f-a6e1-9f3c2b7d4a85"]; ok {
		t.Fatal("read a chat whose agent keeps its own store")
	}
}

func TestJetBrainsSkipsAWorkspaceWithNoChat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "workspace", "abc.xml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`<project version="4"><component name="ChangeListManager"/></project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseJetBrainsFile(p)
	if err != nil || len(ss) != 0 {
		t.Fatalf("got %d sessions, %v", len(ss), err)
	}
}
