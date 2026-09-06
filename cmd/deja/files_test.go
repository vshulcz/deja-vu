package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestTopicTimesNeedsHalfTheWords(t *testing.T) {
	at := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	ms := []model.Message{
		{Role: "user", Text: "the block compression floor", Time: at},
		{Role: "user", Text: "unrelated talk", Time: at.Add(time.Minute)},
		{Role: "files", Text: "/repo/block.go", Time: at},
		{Role: "assistant", Text: "no timestamp here"},
	}
	got := topicTimes(ms, []string{"block", "compression"})
	if len(got) != 1 || !got[0].Equal(at) {
		t.Fatalf("want the one message that carries the words, got %v", got)
	}
	// Half the words is enough: a turn saying "block layout" is still that work.
	if len(topicTimes(ms, []string{"block", "layout"})) != 1 {
		t.Fatal("half the words should match")
	}
	if len(topicTimes(ms, nil)) != 0 {
		t.Fatal("no needles, no anchors")
	}
}

func TestTopicTimesSkipsFileAndCommandRecords(t *testing.T) {
	at := time.Now()
	ms := []model.Message{
		{Role: "files", Text: "/repo/singbox/service.go", Time: at},
		{Role: "command", Text: "grep singbox service", Time: at},
	}
	if got := topicTimes(ms, []string{"singbox", "service"}); len(got) != 0 {
		t.Fatalf("a path is not someone discussing the subject, got %v", got)
	}
}

func TestWithinTime(t *testing.T) {
	at := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if !withinTime(at.Add(5*time.Minute), []time.Time{at}, filesWindow) {
		t.Fatal("five minutes is inside a twenty minute window")
	}
	if withinTime(at.Add(time.Hour), []time.Time{at}, filesWindow) {
		t.Fatal("an hour later is different work")
	}
	if withinTime(time.Time{}, []time.Time{at}, filesWindow) {
		t.Fatal("a record with no time cannot be placed")
	}
}

func TestFilesWindowIsATimeSpan(t *testing.T) {
	// This is the whole feature: an untyped 20 here is twenty nanoseconds and
	// nothing is ever near anything.
	if filesWindow < time.Minute {
		t.Fatalf("filesWindow = %v, which is not a usable span", filesWindow)
	}
}

func TestIsScratch(t *testing.T) {
	for _, p := range []string{
		"/Users/x/.claude/projects/a.jsonl",
		"/Users/x/work/scratchpad/probe.py",
		"/Users/x/repo/node_modules/lib/index.js",
		"/Users/x/repo/build.log",
	} {
		if !isScratch(p) {
			t.Errorf("%s should be scratch", p)
		}
	}
	if isScratch("/Users/x/repo/internal/index/store.go") {
		t.Error("source in a repo is not scratch")
	}
}

func TestInRepositoryWalksUpForGit(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "internal", "index")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if inRepositoryUncached(filepath.Join(deep, "store.go")) {
		t.Fatal("no .git anywhere above it yet")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !inRepositoryUncached(filepath.Join(deep, "store.go")) {
		t.Fatal("a file under a .git root belongs to a repository")
	}
	other := t.TempDir()
	if inRepositoryUncached(filepath.Join(other, "note.md")) {
		t.Fatal("a loose file beside no repository should be dropped")
	}
}

func TestTrimPath(t *testing.T) {
	// The head that was dropped is marked, or two files under one directory
	// read as relative paths starting in different places (#727).
	got := trimPath("/Users/x/coding/goprojects/deja-vu/internal/index/store.go")
	if got != "…/deja-vu/internal/index/store.go" {
		t.Fatalf("got %q", got)
	}
	if got := trimPath("/a/b.go"); got != "a/b.go" {
		t.Fatalf("short paths keep their shape, got %q", got)
	}
}

func TestLowerAllDropsShortWords(t *testing.T) {
	got := lowerAll([]string{"Sing", "Box", "in", "GO"})
	if len(got) != 2 || got[0] != "sing" || got[1] != "box" {
		t.Fatalf("got %v", got)
	}
}

// `deja files --json` (#1930). Every sibling command has one; this one printed
// prose only, so a caller scripting against it had to parse a padded column.
func TestFilesJSONEnvelope(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	out, err := captureRun(t, "files", "--json", "parser")
	if err != nil {
		t.Fatalf("files --json err=%v out=%q", err, out)
	}
	var env struct {
		SchemaVersion   int    `json:"schema_version"`
		Query           string `json:"query"`
		SessionsScanned int    `json:"sessions_scanned"`
		Matched         int    `json:"matched"`
		ReadCapped      bool   `json:"read_capped"`
		Truncated       bool   `json:"truncated"`
		Filtered        int    `json:"filtered"`
		Withheld        int    `json:"withheld"`
		Ignored         int    `json:"ignored"`
		Files           []struct {
			Path     string `json:"path"`
			Near     int    `json:"near"`
			Sessions int    `json:"sessions"`
			Total    int    `json:"total"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("files --json is not valid JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version=%d want %d", env.SchemaVersion, jsonout.Version)
	}
	if env.Query != "parser" {
		t.Fatalf("query=%q want %q", env.Query, "parser")
	}
	// The list may legitimately be empty on this fixture; what must hold is
	// that every row is well formed and that `total` is never smaller than
	// `near`, since near-touches are a subset of the touches counted.
	for _, f := range env.Files {
		if f.Path == "" {
			t.Fatalf("row with no path: %+v", f)
		}
		if f.Total < f.Near {
			t.Fatalf("total %d < near %d for %s", f.Total, f.Near, f.Path)
		}
		if f.Sessions <= 0 {
			t.Fatalf("row claims no sessions: %+v", f)
		}
	}
}

// A miss must still be an envelope, not prose on stdout: a consumer that got
// one shape on a hit and a sentence on a miss would have to sniff the output
// before parsing it.
func TestFilesJSONOnAMissIsStillAnEnvelope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(t.TempDir(), "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	out, err := captureRun(t, "files", "--json", "nothing-mentions-this-topic")
	if err != nil {
		t.Fatalf("files --json on a miss err=%v out=%q", err, out)
	}
	var env struct {
		SchemaVersion int   `json:"schema_version"`
		Files         []any `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("miss is not valid JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version=%d want %d", env.SchemaVersion, jsonout.Version)
	}
	if len(env.Files) != 0 {
		t.Fatalf("miss returned %d files", len(env.Files))
	}
}

// `files` refuses unknown flags by design (#1628); --json must not have opened
// a hole in that.
func TestFilesStillRefusesUnknownFlags(t *testing.T) {
	if err := runFiles(t.TempDir(), []string{"--jsonn", "topic"}, io.Discard); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if err := runFiles(t.TempDir(), []string{"--json"}, io.Discard); err == nil {
		t.Fatal("--json with no topic accepted")
	}
}
