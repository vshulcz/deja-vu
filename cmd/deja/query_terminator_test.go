package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// search, fix and how take `--` and read the rest as the query; ctx, blame and
// remember refused it, so nothing a caller could write reached them with a
// leading dash. `--no-verify` is a thing people ask about, and the refusal that
// came back is indistinguishable, through a plugin, from an empty store.
func TestTerminatorMakesADashLeadingQueryReachTheStore(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"zz1","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"we banned --no-verify in this repo, the hooks catch the secrets"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "zz1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "ctx", "--", "--no-verify")
	if err != nil {
		t.Fatalf("ctx -- --no-verify: %v", err)
	}
	if !strings.Contains(out, "we banned") {
		t.Errorf("ctx behind the terminator missed the session that discusses it: %q", out)
	}

	out, err = captureRun(t, "search", "--", "--no-verify")
	if err != nil {
		t.Fatalf("search -- --no-verify: %v", err)
	}
	if !strings.Contains(out, "we banned") {
		t.Errorf("search behind the terminator missed the session: %q", out)
	}
}

// Only a leading `--`: a flag that comes first is still the mistake #721 is
// about, and the refusal has to keep naming it.
func TestCtxStillRefusesFlagsWithoutTheTerminator(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "ctx", "pool", "--json")
	if err == nil || !strings.Contains(err.Error(), "ctx takes no flags") {
		t.Fatalf("a flag with no terminator must still be named: %v", err)
	}
	// The terminator on its own says nothing about what to look for.
	if _, err := captureRun(t, "ctx", "--"); err == nil || !strings.Contains(err.Error(), "needs a query") {
		t.Errorf("bare terminator: %v", err)
	}
}

// The /deja command deja installs into dsh handed the text over as deja's first
// word, and the bare-query path dispatches a first word that is a command:
// `/deja version` printed a version number instead of searching for the word.
func TestDSHCommandSearchesRatherThanDispatching(t *testing.T) {
	js := dshCommandJS("/bin/deja")
	if strings.Contains(js, "execFileSync(DEJA, [query]") {
		t.Error("the command still hands the query to deja as its first word")
	}
	if !strings.Contains(js, `["search", query]`) {
		t.Error("the command does not name search")
	}
	if !strings.Contains(js, `["search", "--", query]`) {
		t.Error("a query that starts with a dash is still read as a flag")
	}
}

func TestBlameTakesAPathBehindTheTerminator(t *testing.T) {
	path, o, jsonOut, err := parseBlame([]string{"--json", "--", "-report.md"})
	if err != nil {
		t.Fatalf("parseBlame: %v", err)
	}
	if path != "-report.md" {
		t.Errorf("path = %q, want -report.md", path)
	}
	if !jsonOut {
		t.Error("--json before the terminator was dropped")
	}
	if o.All {
		t.Error("--all was never given")
	}
	// Without it the dash is still a flag, and the message still names it.
	if _, _, _, err := parseBlame([]string{"-report.md"}); err == nil ||
		!strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("no terminator: %v", err)
	}
	// A second path is one too many, terminator or not.
	if _, _, _, err := parseBlame([]string{"a.go", "--", "b.go"}); err == nil ||
		!strings.Contains(err.Error(), "one path") {
		t.Errorf("two paths: %v", err)
	}
	if _, _, _, err := parseBlame([]string{"--"}); err == nil ||
		!strings.Contains(err.Error(), "needs a path") {
		t.Errorf("bare terminator: %v", err)
	}
}

func TestRememberTakesANoteBehindTheTerminator(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	if err := runRemember(index.DefaultDir(), []string{"--", "--no-verify is banned here"}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	stored, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "--no-verify is banned here") {
		t.Errorf("the note behind the terminator was not stored: %s", stored)
	}
	// Without it the note is still refused as a flag, which is what tells
	// somebody typing by hand that they meant the terminator.
	if err := runRemember(index.DefaultDir(), []string{"--no-verify is banned here"}); err == nil ||
		!strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("no terminator: %v", err)
	}
	if err := runRemember(index.DefaultDir(), []string{"--"}); err == nil ||
		!strings.Contains(err.Error(), "text required") {
		t.Errorf("bare terminator: %v", err)
	}
	// The flags it does take still work in front of it.
	if err := runRemember(index.DefaultDir(), []string{"--tag", "policy", "--", "--force is never used on main"}); err != nil {
		t.Fatalf("flag before the terminator: %v", err)
	}
}
