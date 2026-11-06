package sources

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func repoMemFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "work", "webhooks")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	return root
}

func TestRepoMemoryWritesSortedLinesWithFixedKeys(t *testing.T) {
	root := repoMemFixture(t)
	in := []RepoEntry{
		{Kind: "rejected", Text: "retry with jitter broke idempotency", Date: "2026-10-01", From: "claude 2026-08-03"},
		{Kind: "decision", Text: "the outbox table owns retries", Date: "2026-10-01"},
		{Kind: "gotcha", Text: "staging needs the vpn", Date: "2026-10-02"},
	}
	added, err := AddRepoEntries(root, in)
	if err != nil || len(added) != 3 {
		t.Fatalf("added %d, err %v", len(added), err)
	}
	b, err := os.ReadFile(RepoMemoryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", b)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, `{"id":"`) || !strings.Contains(l, `","kind":"`) {
			t.Errorf("line %d keys out of order: %s", i, l)
		}
		if i > 0 && lines[i-1] > l {
			t.Errorf("lines not sorted by id: %s before %s", lines[i-1], l)
		}
	}
	want := RepoEntryID("rejected", "retry with jitter broke idempotency")
	if !strings.Contains(string(b), `"id":"`+want+`"`) {
		t.Errorf("id %s not in file:\n%s", want, b)
	}
	if got := RepoEntryID("rejected", "  Retry with   jitter broke idempotency "); got != want {
		t.Errorf("id should fold case and spacing: %s vs %s", got, want)
	}
	attrs, err := os.ReadFile(filepath.Join(root, RepoMemoryDir, ".gitattributes"))
	if err != nil || string(attrs) != RepoGitattributes {
		t.Errorf(".gitattributes = %q, %v", attrs, err)
	}
	// The same entry again is not a second line.
	again, err := AddRepoEntries(root, in[:1])
	if err != nil || len(again) != 0 {
		t.Errorf("re-adding: %d added, err %v", len(again), err)
	}
}

func TestRepoMemoryReadmeIsWrittenOnce(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "one", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(root, RepoMemoryDir, "README.md")
	b, err := os.ReadFile(readme)
	if err != nil || string(b) != RepoReadme || !strings.Contains(string(b), "https://github.com/vshulcz/deja-vu") {
		t.Fatalf("README = %q, %v", b, err)
	}
	// Edited: kept as the person left it.
	if err := os.WriteFile(readme, []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "two", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(readme); string(b) != "ours\n" {
		t.Errorf("an edited README was rewritten: %q", b)
	}
	// Deleted, along with the attributes: neither comes back.
	for _, name := range []string{"README.md", ".gitattributes"} {
		if err := os.Remove(filepath.Join(root, RepoMemoryDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "three", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", ".gitattributes"} {
		if _, err := os.Lstat(filepath.Join(root, RepoMemoryDir, name)); err == nil {
			t.Errorf("a deleted %s was recreated", name)
		}
	}
}

func TestRepoMemoryUnionMergeDedupes(t *testing.T) {
	root := repoMemFixture(t)
	a := RepoEntry{Kind: "decision", Text: "pgx, not database/sql", Date: "2026-10-01"}
	b := RepoEntry{Kind: "rejected", Text: "a second connection pool", Date: "2026-10-02"}
	if _, err := AddRepoEntries(root, []RepoEntry{a}); err != nil {
		t.Fatal(err)
	}
	ours, _ := os.ReadFile(RepoMemoryPath(root))
	if _, err := AddRepoEntries(root, []RepoEntry{b}); err != nil {
		t.Fatal(err)
	}
	theirs, _ := os.ReadFile(RepoMemoryPath(root))
	// What a union merge leaves when both sides held a: a twice, b once,
	// out of order.
	merged := string(theirs) + string(ours)
	if err := os.WriteFile(RepoMemoryPath(root), []byte(merged), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(entries) != 2 {
		t.Fatalf("entries %v bad %d err %v", entries, bad, err)
	}
	if entries[0].ID > entries[1].ID {
		t.Error("read is not sorted")
	}
	// The next write puts the file back in order with one line each.
	c := RepoEntry{Kind: "gotcha", Text: "tests need docker", Date: "2026-10-03"}
	if _, err := AddRepoEntries(root, []RepoEntry{c}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(RepoMemoryPath(root))
	if n := strings.Count(string(out), "\n"); n != 3 {
		t.Errorf("want 3 lines after rewrite, got %d:\n%s", n, out)
	}
}

func TestRepoMemoryMalformedLineBlocksWrites(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "one", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	path := RepoMemoryPath(root)
	b, _ := os.ReadFile(path)
	conflict := "<<<<<<< HEAD\n" + string(b) + "=======\n" + `{"id":"x","kind":"wat","text":"t"}` + "\n>>>>>>> other\n"
	if err := os.WriteFile(path, []byte(conflict), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, bad, err := ReadRepoMemory(path)
	if err != nil || len(entries) != 1 || bad != 4 {
		t.Fatalf("entries %d bad %d err %v", len(entries), bad, err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "two", Date: "2026-10-01"}}); !errors.Is(err, ErrRepoMemoryMalformed) {
		t.Fatalf("want ErrRepoMemoryMalformed, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != conflict {
		t.Error("a refused write changed the file")
	}
}

func TestRepoMemoryRefusesBadEntriesAndSymlinks(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "opinion", Text: "x"}}); err == nil {
		t.Error("unknown kind was written")
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: " "}}); err == nil {
		t.Error("empty text was written")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, RepoMemoryDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "x"}}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("wrote through a symlinked .deja: %v", err)
	}
}

func TestRepoMemorySessionsAreMarkedRepo(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "rejected", Text: "retry with\tjitter\nbroke it", Date: "2026-08-03", From: "codex 2026-08-01"}}); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRepoMemoryFile(RepoMemoryPath(root))
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions %v err %v", ss, err)
	}
	s := ss[0]
	name := ClaudeProjectName(root)
	if s.Harness != "deja" || s.Project != model.RepoProjectPrefix+name || !strings.HasPrefix(s.ID, "deja-repo-"+noteIDProject(name)+"-") {
		t.Errorf("session = %+v", s)
	}
	body := s.Messages[0].Text
	if body != "[rejected] retry with jitter broke it (in .deja/memory.jsonl, from codex 2026-08-01)" {
		t.Errorf("body = %q", body)
	}
	if s.Updated.Format("2006-01-02") != "2026-08-03" {
		t.Errorf("dated %v", s.Updated)
	}
	s.SetSource("")
	if s.Source.Origin != "repo" {
		t.Errorf("origin = %q", s.Source.Origin)
	}
	if model.BareProject(s.Project) != name || model.BareProject("imported:a/b") != "a/b" {
		t.Error("BareProject")
	}
	if !IsRepoMemoryFile(RepoMemoryPath(root)) || IsRepoMemoryFile(filepath.Join(root, "memory.jsonl")) {
		t.Error("IsRepoMemoryFile")
	}
	if KindForPath(RepoMemoryPath(root)) != "deja-repo" {
		t.Errorf("kind = %q", KindForPath(RepoMemoryPath(root)))
	}
}

func TestRepoMemoryReaderCapsWhatARepoCanHand(t *testing.T) {
	long := strings.Repeat("a", RepoTextMax+50)
	var b strings.Builder
	b.WriteString(`{"kind":"decision","text":"` + long + `"}` + "\n")
	b.WriteString(`{"kind":"gotcha","text":"no id, no date"}` + "\n")
	b.WriteString("\n")
	entries, bad, err := readRepoMemory(strings.NewReader(b.String()))
	if err != nil || bad != 0 || len(entries) != 2 {
		t.Fatalf("entries %d bad %d err %v", len(entries), bad, err)
	}
	for _, e := range entries {
		if e.ID == "" {
			t.Error("missing id not filled")
		}
		if n := len([]rune(e.Text)); n > RepoTextMax+1 {
			t.Errorf("text not capped: %d runes", n)
		}
	}
	var many strings.Builder
	for i := range repoEntriesMax + 20 {
		many.WriteString(`{"kind":"decision","text":"entry ` + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + `-` + itoa(i) + `"}` + "\n")
	}
	entries, _, _ = readRepoMemory(strings.NewReader(many.String()))
	if len(entries) != repoEntriesMax {
		t.Errorf("read %d entries, cap is %d", len(entries), repoEntriesMax)
	}
}

func TestRepoMemoryFilesRemembersCheckouts(t *testing.T) {
	root := repoMemFixture(t)
	if got := RepoMemoryFiles(); len(got) != 0 {
		t.Fatalf("files before any entry: %v", got)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "x", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	// Remembered by the write, and read from anywhere afterwards.
	t.Chdir(t.TempDir())
	got := RepoMemoryFiles()
	if len(got) != 1 || filepath.Base(filepath.Dir(got[0])) != RepoMemoryDir {
		t.Fatalf("files = %v", got)
	}
	ss := LoadRepoMemory()
	if len(ss) != 1 {
		t.Errorf("LoadRepoMemory = %d sessions", len(ss))
	}
	// A checkout deleted later drops out without an error.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if got := RepoMemoryFiles(); len(got) != 0 {
		t.Errorf("deleted checkout still listed: %v", got)
	}
}

func TestRemoveRepoEntries(t *testing.T) {
	root := repoMemFixture(t)
	added, err := AddRepoEntries(root, []RepoEntry{
		{Kind: "decision", Text: "keep sqlite for local cache", Date: "2026-10-01"},
		{Kind: "rejected", Text: "dropped boltdb lock contention", Date: "2026-10-02"},
	})
	if err != nil || len(added) != 2 {
		t.Fatalf("AddRepoEntries: %d, %v", len(added), err)
	}
	removed, err := RemoveRepoEntries(root, []string{added[0].ID})
	if err != nil || len(removed) != 1 || removed[0].ID != added[0].ID {
		t.Fatalf("RemoveRepoEntries: %v, %v", removed, err)
	}
	left, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(left) != 1 || left[0].ID != added[1].ID {
		t.Fatalf("ReadRepoMemory after remove: %v, bad=%d, err=%v", left, bad, err)
	}
}

func TestEvolveRepoEntriesAndAutoToggle(t *testing.T) {
	root := repoMemFixture(t)
	keepFile := filepath.Join(root, "internal", "outbox.go")
	goneFile := filepath.Join(root, "internal", "legacy.go")
	if err := os.MkdirAll(filepath.Dir(keepFile), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{keepFile, goneFile} {
		if err := os.WriteFile(p, []byte("package internal\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	added, updated, pruned, err := EvolveRepoEntries(root, []RepoEntry{
		{Kind: "decision", Text: "outbox owns retry scheduling", Files: []string{"internal/outbox.go"}, Sessions: 1, Date: "2026-10-01"},
		{Kind: "rejected", Text: "legacy polling loop caused lock contention", Files: []string{"internal/legacy.go"}, Sessions: 1, Date: "2026-10-01"},
	}, 50)
	if err != nil || added != 2 || updated != 0 || pruned != 0 {
		t.Fatalf("initial evolve: +%d ~%d -%d err=%v", added, updated, pruned, err)
	}

	// Delete legacy.go and reinforce the outbox entry (even under a different kind).
	if rmErr := os.Remove(goneFile); rmErr != nil {
		t.Fatal(rmErr)
	}
	added, updated, pruned, err = EvolveRepoEntries(root, []RepoEntry{
		{Kind: "gotcha", Text: "outbox owns retry scheduling", Files: []string{"internal/outbox.go"}, Commit: "1489a47b1", Sessions: 4, Date: "2026-10-02"},
	}, 50)
	if err != nil || added != 0 || updated != 1 || pruned != 1 {
		t.Fatalf("second evolve: +%d ~%d -%d err=%v", added, updated, pruned, err)
	}

	entries, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(entries) != 1 {
		t.Fatalf("entries after prune+reinforce: %+v bad=%d err=%v", entries, bad, err)
	}
	if entries[0].Commit != "1489a47b1" || entries[0].Sessions != 4 || len(entries[0].Files) != 1 || entries[0].Files[0] != "internal/outbox.go" {
		t.Errorf("reinforced entry = %+v", entries[0])
	}

	ss, err := ParseRepoMemoryFile(RepoMemoryPath(root))
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 || ss[0].Messages[1].Role != "edit" {
		t.Fatalf("ParseRepoMemoryFile messages = %+v, err=%v", ss, err)
	}

	if RepoAutoDisabled(root) {
		t.Error("auto sync disabled by default")
	}
	if err := SetRepoAutoDisabled(root, true); err != nil || !RepoAutoDisabled(root) {
		t.Fatalf("SetRepoAutoDisabled(true): disabled=%v err=%v", RepoAutoDisabled(root), err)
	}
	if err := SetRepoAutoDisabled(root, false); err != nil || RepoAutoDisabled(root) {
		t.Fatalf("SetRepoAutoDisabled(false): disabled=%v err=%v", RepoAutoDisabled(root), err)
	}
}
