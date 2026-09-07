package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Discovery no longer walks the editor's User folder; it asks for the
// directories VS Code puts transcripts in. That is faster by 65x on a real
// machine and it is also narrower, so every layout it has to cover is named
// here — a shape this forgets is a store that silently stops being read.
func TestCopilotChatFindsEveryLayoutWithoutWalking(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)

	write := func(parts ...string) string {
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"kind":0}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	want := []string{
		// A workspace's own sessions.
		write("workspaceStorage", "abc123", "chatSessions", "s1.jsonl"),
		// A window opened on no folder.
		write("globalStorage", "emptyWindowChatSessions", "s2.jsonl"),
		// And the same two again inside a named profile, which keeps its own
		// copy of both one level further down.
		write("profiles", "-1a2b3c", "workspaceStorage", "def456", "chatSessions", "s3.jsonl"),
		write("profiles", "-1a2b3c", "globalStorage", "emptyWindowChatSessions", "s4.jsonl"),
	}

	// Things that live in the same tree and are not transcripts. The walk this
	// replaces read all of them.
	write("History", "1a2b3c", "entries.json")
	write("workspaceStorage", "abc123", "state.vscdb")
	write("globalStorage", "some.extension", "cache.json")
	write("workspaceStorage", "abc123", "chatSessions", "notes.txt")

	got := CopilotChatSessionFiles()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("discovery found\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A root in none of those shapes still gets walked, so a layout nobody has seen
// is slow rather than invisible.
func TestCopilotChatWalksARootItDoesNotRecognise(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	odd := filepath.Join(root, "somewhere", "else", "deeper", "chatSessions")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(odd, "s1.jsonl")
	if err := os.WriteFile(p, []byte(`{"kind":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CopilotChatSessionFiles()
	if len(got) != 1 || got[0] != p {
		t.Fatalf("an unrecognised layout was not walked: %v", got)
	}

	// And once the root looks like VS Code's, the targeted read is used — which
	// is the trade this makes, and the reason the shapes above are pinned.
	if err := os.MkdirAll(filepath.Join(root, "workspaceStorage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CopilotChatSessionFiles(); len(got) != 0 {
		t.Fatalf("a recognised root still walked the whole tree: %v", got)
	}
}
