package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// Cursor CLI's second store is unread, which is free while a transcript sits
// beside every chat and blindness the day one stops being written. The count
// has to key on the chat's own uuid: both layouts name a chat the same way,
// and comparing anything else would either cry wolf or stay silent (#3772).
func TestCursorChatsWithoutATranscriptAreCounted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	covered := "77a811d0-b626-4860-9684-009a16ef334d"
	orphan := "90eb0569-7d34-42df-bfa0-e2a79c249987"
	for _, id := range []string{covered, orphan} {
		dir := filepath.Join(home, "chats", "wshash", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A chat with no store.db is an empty chat, not a gap: 17 of the 36 here
	// were that, and counting them would report a loss that never happened.
	if err := os.MkdirAll(filepath.Join(home, "chats", "wshash", "empty-chat"), 0o755); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(home, "projects", "-Users-me-work", "agent-transcripts", covered)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, covered+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := len(CursorChatStores()); got != 2 {
		t.Fatalf("CursorChatStores() found %d stores, want the two with a store.db", got)
	}
	if got := CursorChatsWithoutTranscript(); got != 1 {
		t.Fatalf("CursorChatsWithoutTranscript() = %d, want only the chat with no transcript beside it", got)
	}

	// The quiet case: once the transcript exists, the count says nothing.
	tdir = filepath.Join(home, "projects", "-Users-me-work", "agent-transcripts", orphan)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, orphan+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CursorChatsWithoutTranscript(); got != 0 {
		t.Fatalf("CursorChatsWithoutTranscript() = %d with a transcript for every chat, want 0", got)
	}
}

// Nothing to say on a machine without Cursor CLI, and no walk of a root that
// is not there.
func TestCursorChatsAreSilentWithoutTheStore(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(t.TempDir(), "absent"))
	if got := CursorChatStores(); got != nil {
		t.Errorf("CursorChatStores() = %v, want none", got)
	}
	if got := CursorChatsWithoutTranscript(); got != 0 {
		t.Errorf("CursorChatsWithoutTranscript() = %d, want 0", got)
	}
}
