package sources

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claude Code names a project folder by writing every character outside
// [A-Za-z0-9] as "-", so a directory named in Cyrillic, CJK or with accents
// cannot be read back from the folder name. Every record carries the real
// directory as cwd (#4175).
func TestAClaudeSessionInANonASCIIDirectoryKeepsItsProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	work := filepath.Join(home, "w", "проект é")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	enc := claudeEncodePath(work)
	if strings.Count(enc, "-") < 9 {
		t.Fatalf("encoding looks wrong: %q", enc)
	}
	dir := filepath.Join(home, ".claude", "projects", enc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "11111111-2222-4333-8444-555555555555.jsonl")
	line := `{"type":"user","sessionId":"11111111-2222-4333-8444-555555555555","cwd":` + jsonString(work) + `,"timestamp":"2026-09-20T10:00:00Z","uuid":"u1","message":{"role":"user","content":"кэш живёт в «ёлке»"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	projectNameCache.Delete(enc)
	ss, err := parseClaudeFileFromOffset(path, 0)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if want := "w/проект é"; ss[0].Project != want {
		t.Errorf("project = %q, want %q", ss[0].Project, want)
	}
	if got := ClaudeSessionDir(path); got != work {
		t.Errorf("session dir = %q, want %q", got, work)
	}
}

// A cwd that is not the directory the folder was named for — the session
// moved somewhere else first — is not taken for it.
func TestAClaudeCWDThatIsNotTheFolderIsIgnored(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","cwd":"/elsewhere","message":{"role":"user","content":"x"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeTranscriptCWD(path, "-work-app"); got != "" {
		t.Errorf("took %q for -work-app", got)
	}
}

// An empty segment is not a directory: "007---" named a child the walk could
// not spell, and closing it as "/" handed back the parent with slashes on.
func TestResolveEncodedPathNeverReturnsEmptySegments(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "007"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := claudeEncodePath(filepath.Join(root, "007")) + "---------"
	if got := ResolveEncodedPath(base); strings.Contains(got, string(filepath.Separator)+string(filepath.Separator)) || strings.HasSuffix(got, string(filepath.Separator)) {
		t.Errorf("resolved %q to %q", base, got)
	}
}

// A folder has one project whichever of its files is read first. A long
// session whose head was written from elsewhere sits beside subagents that
// carry the folder's own directory; the name came from whichever file a run
// opened first, so a rebuild and an incremental run disagreed.
func TestAClaudeFolderHasOneProjectWhicheverFileIsReadFirst(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "w", "проект é")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", claudeEncodePath(work))
	main := filepath.Join(dir, "aaaa.jsonl")
	sub := filepath.Join(dir, "aaaa", "subagents", "agent-1.jsonl")
	for path, cwd := range map[string]string{main: "/somewhere/else", sub: work} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"type":"user","cwd":`+jsonString(cwd)+`,"message":{"role":"user","content":"x"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, first := range [][]string{{main, sub}, {sub, main}} {
		claudeCWDNameCache = sync.Map{}
		var names []string
		for _, p := range first {
			names = append(names, claudeProjectNameFor(p))
		}
		for _, n := range names {
			if n != "w/проект é" {
				t.Errorf("reading %v first named the folder %q, want w/проект é", filepath.Base(first[0]), n)
			}
		}
	}
}

// Claude Code encodes per UTF-16 unit and cuts a long name at 200 with a hash
// after it; a cwd outside the BMP or over that length is still recognised.
func TestClaudeFolderIsFollowsClaudesEncoding(t *testing.T) {
	if !claudeFolderIs("/a/😀", "-a---") {
		t.Errorf("an emoji is two UTF-16 units, so two dashes: %q", claudeEncodePath("/a/😀"))
	}
	long := "/" + strings.Repeat("x", 250)
	if !claudeFolderIs(long, claudeEncodePath(long)[:200]+"-1a2b3c") {
		t.Error("a cwd over 200 characters did not match its cut folder name")
	}
	if claudeFolderIs("/elsewhere", "-work-app") {
		t.Error("an unrelated cwd matched")
	}
}

// A folder read before any of its transcripts carries cwd — a new session
// whose only line is a file-history snapshot — is named from the folder for
// that read only. Caching the decoded name kept a long-lived process such as
// deja mcp filing the folder under its parent until restart (#4225).
func TestAClaudeFolderReadBeforeItsCWDLandsIsNamedOnceItDoes(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "w", "проект")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", claudeEncodePath(work))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeCWDNameCache = sync.Map{}
	path := filepath.Join(dir, "aaaa.jsonl")
	snap := `{"type":"file-history-snapshot","messageId":"m0","snapshot":{"trackedFileBackups":{}}}` + "\n"
	if err := os.WriteFile(path, []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	claudeProjectNameFor(path)
	user := `{"type":"user","cwd":` + jsonString(work) + `,"message":{"role":"user","content":"x"}}` + "\n"
	if err := os.WriteFile(path, []byte(snap+user), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectNameFor(path); got != "w/проект" {
		t.Errorf("after cwd landed the folder is %q, want w/проект", got)
	}
}

// A folder that named nothing is not listed again for every older transcript
// in it, and is once a transcript is written after the miss.
func TestAClaudeFolderMissIsKeptForOlderTranscripts(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "w", "проект")
	dir := filepath.Join(home, "projects", claudeEncodePath(work))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeCWDNameCache = sync.Map{}
	path := filepath.Join(dir, "aaaa.jsonl")
	snap := `{"type":"file-history-snapshot","messageId":"m0","snapshot":{"trackedFileBackups":{}}}` + "\n"
	if err := os.WriteFile(path, []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	missed := claudeProjectNameFor(path)
	user := `{"type":"user","cwd":` + jsonString(work) + `,"message":{"role":"user","content":"x"}}` + "\n"
	if err := os.WriteFile(path, []byte(snap+user), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, old, old)
	if got := claudeProjectNameFor(path); got != missed {
		t.Errorf("an older transcript read the folder again: %q", got)
	}
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(path, now, now)
	if got := claudeProjectNameFor(path); got != "w/проект" {
		t.Errorf("a transcript written after the miss is %q, want w/проект", got)
	}
}
