package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A transcript whose chat is gone from chats/ re-read all of chats/ on every
// lookup, so a rebuild with 500 of them read it 500 times (#4226). A miss is
// answered from the index while it is fresh, and a chat written after the
// scan, in a new bucket or an existing one, is still found.
func TestCursorChatMissesDoNotRescanChats(t *testing.T) {
	cli := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	chats := filepath.Join(cli, "chats")
	writeChat := func(cwd, id string) {
		t.Helper()
		dir := filepath.Join(chats, CursorChatBucket(cwd), id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"cwd":`+fmt.Sprintf("%q", cwd)+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for b := 0; b < 20; b++ {
		for c := 0; c < 5; c++ {
			writeChat(fmt.Sprintf("/work/p%d", b), fmt.Sprintf("chat-%d-%d", b, c))
		}
	}
	// Seeded an hour ago, so a chat added below moves an mtime even on a
	// filesystem with a coarse clock.
	old := time.Now().Add(-time.Hour)
	dirs, _ := os.ReadDir(chats)
	for _, d := range dirs {
		_ = os.Chtimes(filepath.Join(chats, d.Name()), old, old)
	}
	_ = os.Chtimes(chats, old, old)

	transcript := func(id string) string {
		return filepath.Join(cli, "projects", "work-x", "agent-transcripts", id, id+".jsonl")
	}
	before := cursorChatIndex.scans
	if cwd, ok := CursorChatCWD(transcript("chat-3-1")); cwd != "/work/p3" || !ok {
		t.Fatalf("present chat = %q, %v", cwd, ok)
	}
	for i := 0; i < 50; i++ {
		if cwd, _ := CursorChatCWD(transcript(fmt.Sprintf("gone-%d", i))); cwd != "" {
			t.Fatalf("gone chat %d = %q", i, cwd)
		}
	}
	if n := cursorChatIndex.scans - before; n != 1 {
		t.Fatalf("1 present + 50 gone lookups scanned chats/ %d times, want 1", n)
	}

	// A chat added to a bucket in the clock tick of the scan leaves the
	// bucket's mtime as it was; on HFS+ or FAT that tick is a second or two.
	writeChat("/work/p7", "late-same-bucket")
	_ = os.Chtimes(filepath.Join(chats, CursorChatBucket("/work/p7")), old, old)
	cursorChatIndex.scanned = cursorChatIndex.scanned.Add(-cursorChatFresh)
	if cwd, ok := CursorChatCWD(transcript("late-same-bucket")); cwd != "/work/p7" || !ok {
		t.Fatalf("chat added to an existing bucket, once the index aged out = %q, %v", cwd, ok)
	}
	writeChat("/work/new", "late-new-bucket")
	if cwd, ok := CursorChatCWD(transcript("late-new-bucket")); cwd != "/work/new" || !ok {
		t.Fatalf("chat added in a new bucket = %q, %v", cwd, ok)
	}
}

// A chats/ mtime ahead of the clock — a ~/.cursor copied from a machine whose
// clock ran fast, a network share with skew — read as changed at every miss,
// and each miss rescanned all of chats/ again.
func TestCursorChatMissesTrustAChatsDirFromTheFuture(t *testing.T) {
	cli := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	chats := filepath.Join(cli, "chats")
	dir := filepath.Join(chats, CursorChatBucket("/work/p"), "chat-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ahead := time.Now().Add(time.Hour)
	_ = os.Chtimes(chats, ahead, ahead)
	before := cursorChatIndex.scans
	for i := 0; i < 20; i++ {
		CursorChatCWD(filepath.Join(cli, "projects", "work-p", "agent-transcripts", fmt.Sprintf("gone-%d", i), fmt.Sprintf("gone-%d.jsonl", i)))
	}
	if n := cursorChatIndex.scans - before; n != 1 {
		t.Fatalf("20 misses with chats/ an hour ahead scanned it %d times, want 1", n)
	}
}
