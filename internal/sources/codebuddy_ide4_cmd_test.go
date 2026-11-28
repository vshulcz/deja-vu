package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// Doctor asks this of every .json file under the 4.x root it did not place.
// Only a conversation's own index.json is a transcript; the rest of the store
// is the IDE's, and a file outside the root is nobody's business here.
func TestCodeBuddyIDE4BookkeepingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", root)
	ws := filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "history", "0f1e2d3c4b5a69788796a5b4c3d2e1f0")
	conv := filepath.Join(ws, "a1b2c3d4e5f60718293a4b5c6d7e8f90")
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"a conversation's index", filepath.Join(conv, "index.json"), false},
		{"a message body", filepath.Join(conv, "messages", "m1.json"), true},
		{"an asset", filepath.Join(conv, "assets", "shot.json"), true},
		{"the index backup", filepath.Join(conv, ".index_bak.json"), true},
		{"the workspace index", filepath.Join(ws, "index.json"), true},
		{"the workspace's current conversation", filepath.Join(ws, "current.json"), true},
		{"the workspace's recent conversations", filepath.Join(ws, "recent.json"), true},
		{"the IDE's sign-in state", filepath.Join(root, "u1", "CodeBuddyIDE_mcp_ide_oauth.json"), true},
		{"the shared auth state", filepath.Join(root, "Public", "auth", "auth.json"), true},
		{"a plan", filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "plan-task", "ws", "id", "meta.json"), true},
		{"a workspace snapshot", filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "file-tree", "ws", "id", "file-tree.json"), true},
		{"a checkpoint", filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "check-point", "ws", "id", "meta.json"), true},
		{"a snapshot of a written JSONL file", filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "file-tree", "ws", "id", "files", "call_1_out.jsonl"), true},
		{"anything under a message directory", filepath.Join(conv, "messages", "m1.txt"), true},
		{"not JSON", filepath.Join(conv, "notes.txt"), false},
		{"outside the root", filepath.Join(t.TempDir(), "history", "ws", "index.json"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeBuddyIDE4BookkeepingFile(tc.path, CodeBuddyIDE4Roots()); got != tc.want {
				t.Errorf("CodeBuddyIDE4BookkeepingFile(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// The index tells a 4.x conversation index by its path alone, since a row it
// holds may name a file that is gone: under a directory named history
// anywhere, or at a conversation's depth under a configured root by any name.
func TestCodeBuddyIDE4IndexPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "copy")
	roots := []string{root}
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"under a history directory", filepath.Join("elsewhere", "history", "ws", "conv", "index.json"), true},
		{"under a renamed history root", filepath.Join(root, "ws", "conv", "index.json"), true},
		{"under a workspace root", filepath.Join(root, "conv", "index.json"), true},
		{"the root's own index", filepath.Join(root, "index.json"), false},
		{"deeper than a conversation", filepath.Join(root, "a", "ws", "conv", "index.json"), false},
		{"a message file", filepath.Join(root, "conv", "messages", "m1.json"), false},
		{"outside every root", filepath.Join("elsewhere", "ws", "conv", "index.json"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeBuddyIDE4IndexPath(tc.path, roots); got != tc.want {
				t.Errorf("CodeBuddyIDE4IndexPath(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// A conversion is told by the converter's marker beside where the transcript
// was, so a transcript already deleted is still told, and nothing else that
// shares the id is taken for one.
func TestCodeBuddyIDE4ConversionTranscript(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cli")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", cli)
	const id = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	mark := func(project, names string) {
		t.Helper()
		dir := filepath.Join(cli, project, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, codeBuddyIDE4Marker), []byte(`{"conversationId":"`+names+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mark("converted", id)
	mark("other", "f0e1d2c3b4a5968778695a4b3c2d1e0f")
	if err := os.MkdirAll(filepath.Join(cli, "unmarked"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"the transcript the marker names, gone", filepath.Join(cli, "converted", id+".jsonl"), true},
		{"a transcript with no marker", filepath.Join(cli, "unmarked", id+".jsonl"), false},
		{"a marker that names another conversation", filepath.Join(cli, "other", id+".jsonl"), false},
		{"outside the CLI roots", filepath.Join(tmp, "claude", "converted", id+".jsonl"), false},
		{"a sub-agent transcript", filepath.Join(cli, "converted", id, "subagents", "agent-"+id+".jsonl"), false},
		{"an id the store would not name a file by", filepath.Join(cli, "converted", "a b.jsonl"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeBuddyIDE4ConversionTranscript(tc.path); got != tc.want {
				t.Errorf("CodeBuddyIDE4ConversionTranscript(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
