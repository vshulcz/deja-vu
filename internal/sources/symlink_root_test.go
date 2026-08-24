package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A store reached through a symlink is the store: people put ~/.claude in a
// dotfiles repo, on an external disk, in a synced folder. Walking the link
// itself found nothing and every surface called the store found and empty
// (#1744).
func TestASymlinkedStoreRootIsWalked(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real", "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(real, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(filepath.Join(tmp, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	direct := walkFiles(filepath.Join(tmp, "real"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(direct) != 1 {
		t.Fatalf("the fixture is wrong: the real path yielded %v", direct)
	}
	through := walkFiles(link, func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(through) != 1 {
		t.Errorf("a symlinked store root yielded %v, while its real path yielded %v", through, direct)
	}
}

// A link inside a store still is not followed — that is what keeps a FIFO from
// hanging the build and a link from walking out of the store — and a loop does
// not stop the walk.
func TestLinksInsideAStoreAreStillSkipped(t *testing.T) {
	tmp := t.TempDir()
	store := filepath.Join(tmp, "store", "proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store, "linked.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	loop := filepath.Join(tmp, "store", "loop")
	if err := os.MkdirAll(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loop, filepath.Join(loop, "self")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := walkFiles(filepath.Join(tmp, "store"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(got) != 1 {
		t.Errorf("walk returned %v, want only the real file", got)
	}
}

// The paths that come back are the ones the user configured. Everything
// downstream keys on them — the kind predicates test strings.HasPrefix against
// the root, the manifest records them, the incremental pass looks them up — and
// two spellings of one file lose the file: the first attempt at #1744 walked
// the target and reported its paths, and the next incremental run indexed the
// store back down to nothing.
func TestWalkReportsPathsUnderTheConfiguredRoot(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real", "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(filepath.Join(tmp, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := walkFiles(link, func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(got) != 1 {
		t.Fatalf("walk returned %v", got)
	}
	if want := filepath.Join(link, "proj", "a.jsonl"); got[0] != want {
		t.Errorf("walk reported %q, want %q — the configured root", got[0], want)
	}
	if _, err := os.Stat(got[0]); err != nil {
		t.Errorf("the reported path does not open: %v", err)
	}
}

// cursor and gemini walk a path built under their configured root, so a link
// *at* that path — ~/.cursor/projects pointing elsewhere, a chats directory
// moved off the disk — is the case they have to follow. A link higher up was
// never broken for them: the walk starts below it and the path resolves on the
// way in.
func TestCursorAndGeminiFollowASymlinkedWalkRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))

	cursorRoot := filepath.Join(tmp, "cursor")
	if err := os.MkdirAll(cursorRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	realProjects := filepath.Join(tmp, "projects-elsewhere", "p", "agent-transcripts")
	if err := os.MkdirAll(realProjects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realProjects, "t.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "projects-elsewhere"), filepath.Join(cursorRoot, "projects")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cursorRoot)
	got := CursorTranscripts()
	want := filepath.Join(cursorRoot, "projects", "p", "agent-transcripts", "t.jsonl")
	if len(got) != 1 || got[0] != want {
		t.Errorf("cursor through a symlinked projects directory: %v, want [%s]", got, want)
	}

	geminiRoot := filepath.Join(tmp, "gemini")
	if err := os.MkdirAll(filepath.Join(geminiRoot, "tmp", "hash"), 0o755); err != nil {
		t.Fatal(err)
	}
	realChats := filepath.Join(tmp, "chats-elsewhere")
	if err := os.MkdirAll(realChats, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realChats, "c.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realChats, filepath.Join(geminiRoot, "tmp", "hash", "chats")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("DEJA_GEMINI_ROOT", geminiRoot)
	gots := GeminiChatFiles()
	wantG := filepath.Join(geminiRoot, "tmp", "hash", "chats", "c.json")
	if len(gots) != 1 || gots[0] != wantG {
		t.Errorf("gemini through a symlinked chats directory: %v, want [%s]", gots, wantG)
	}
}
