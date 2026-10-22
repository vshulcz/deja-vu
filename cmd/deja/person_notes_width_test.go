package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// checkPersonLines fails when the lines deja shows the person run past one
// terminal line each or past most lines in all.
func checkPersonLines(t *testing.T, what, msg string, most int) {
	t.Helper()
	lines := strings.Split(msg, "\n")
	if msg == "" {
		lines = nil
	}
	if len(lines) > most {
		t.Errorf("%s is %d lines, want at most %d:\n%s", what, len(lines), most, msg)
	}
	for _, l := range lines {
		if w := termwidth.Columns(l); w > noteWidth {
			t.Errorf("%s has a %d-column line, over %d: %q", what, w, noteWidth, l)
		}
	}
}

// The first session starts on a freshly built machine, where every once-only
// note is due at the same time, were four or five lines in every host:
// receipt, built note, docs, the GitHub line. A session start now says the
// receipt and at most one note, and the notes left over arrive on later starts.
func TestSessionStartSaysAtMostTwoShortLines(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "beta", "drain.jsonl"), "drainpass", []string{
		`{"type":"user","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"user","content":"how should the zorbit queue drain on deploy"}}`,
		`{"type":"assistant","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"we settled on draining the zorbit queue with --grace 40 before each deploy"}]}}`,
	})
	for i, topic := range []string{"css grid layout for the settings page", "upgrade the postgres driver", "flaky login test on ci", "rename the billing module", "add a healthcheck endpoint", "cache the avatar thumbnails"} {
		id := "bg" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(root, "beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"user","content":"` + topic + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"done: ` + topic + `"}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	start := func(sid string) string {
		payload, err := json.Marshal(map[string]any{"session_id": sid, "cwd": cwd, "source": "startup"})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		out := captureStdout(t, func() { _ = runHookContext(dir, false) })
		var resp sessionStartHookResponse
		if strings.TrimSpace(out) == "" {
			return ""
		}
		if err = json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("not the hook envelope: %v\n%s", err, out)
		}
		return resp.SystemMessage
	}
	first := start("width-1")
	if !strings.Contains(first, "deja recalled") || !strings.Contains(first, "deja indexed") {
		t.Fatalf("the first start should carry the receipt and the built note, or this test proves nothing:\n%s", first)
	}
	checkPersonLines(t, "the first session start", first, 2)
	second := start("width-2")
	if !strings.Contains(second, "github.com/vshulcz/deja-vu") {
		t.Fatalf("the GitHub line the first start held back never came:\n%s", second)
	}
	checkPersonLines(t, "the second session start", second, 2)

	payload, err := json.Marshal(map[string]any{"prompt": "what grace should the zorbit queue drain use before deploy", "cwd": cwd, "session_id": "width-3"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookPromptMode(dir, bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, out.String())
	}
	if !strings.Contains(resp.SystemMessage, "you have been here") {
		t.Fatalf("no déjà vu line to measure: %q", resp.SystemMessage)
	}
	checkPersonLines(t, "the déjà vu line", resp.SystemMessage, 1)
}

// Every note at the sizes a real machine reaches, from the functions that
// write it: long topics, several targets, large counts, a home-directory path.
func TestEachPersonNoteFitsOneLine(t *testing.T) {
	long := model.Session{ID: "l", Project: "app", Updated: time.Now().Add(-90 * 24 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "why does the nightly exporter drop the last batch of rows at midnight on the replica"}}}
	imported := long
	imported.Project = "imported:app"
	const home = "/Users/alexandra/Library/Caches/deja"
	notes := map[string]string{
		"rewire":       rewireNote([]string{"claude-auto", "codex-auto", "gemini", "opencode", "copilot"}),
		"stuck":        stuckWiringNote([]string{"claude-auto", "codex-auto", "gemini", "opencode", "copilot"}),
		"déjà vu":      dejaVuLine(long, "exporter", "replica", "nightly"),
		"imported":     dejaVuLine(imported, "exporter", "replica", "nightly"),
		"star":         starText,
		"built":        builtLine(123456, 41, 1234, true),
		"indexing":     indexingNote,
		"progress":     (&warmupStatus{Phase: "sessions", Done: 42, Total: 100}).line(),
		"index cannot": indexCannotAnswerNote,
		"unmounted":    unmountedIndexNote("/Volumes/ext/caches"),
		"unwritable":   unwritableIndexNote(home + "/index.db"),
		"withheld":     withheldNote("local-only+imported:team-laptop", 1234),
		"stale":        staleIndexNote(home),
	}
	for name, note := range notes {
		if note == "" {
			t.Errorf("%s is empty, so this measures nothing", name)
		}
		checkPersonLines(t, name, note, 1)
	}
	if got := fitList([]string{"a.go", "b.go", "c.go"}, 48); got != "a.go, b.go, c.go" {
		t.Errorf("fitList dropped names that fit: %q", got)
	}
	if got := fitList([]string{"internal/index/ingest.go", "cmd/deja/hook_context.go", "README.md"}, 48); got != "internal/index/ingest.go +2" {
		t.Errorf("fitList = %q, want the names that fit and a count of the rest", got)
	}
}

// A warning names its path and its command whole. When a path is so long
// that even the short form passes noteWidth, the line is allowed to run long
// rather than cut either: that is the one exception to the width.
func TestAWarningWithALongPathKeepsThePathAndTheCommand(t *testing.T) {
	const path = "/Volumes/Team Archive Drive 2026/shared/agent-history/caches/deja/index.db"
	for name, c := range map[string]struct{ note, command string }{
		"unmounted":  {unmountedIndexNote(path), "DEJA_INDEX_DIR"},
		"unwritable": {unwritableIndexNote(path), "`deja index`"},
		"stale":      {staleIndexNote(path), ""},
	} {
		if !strings.Contains(c.note, path) {
			t.Errorf("%s cut the path: %q", name, c.note)
		}
		if !strings.Contains(c.note, c.command) {
			t.Errorf("%s lost its command %s: %q", name, c.command, c.note)
		}
		if strings.Contains(c.note, "\n") {
			t.Errorf("%s is more than one line: %q", name, c.note)
		}
	}
	// With a short path the long form is kept, prose and all.
	if got := unmountedIndexNote("/Volumes/ext"); !strings.Contains(got, "reconnect it") {
		t.Errorf("a short path still lost the prose: %q", got)
	}
}

// The re-used session's name is the part of the receipt only it says (#579),
// so on a day with a tally it is the tally that goes, not the name.
func TestReceiptKeepsTheReusedNameOverTheTally(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	for range 3 {
		usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	}
	svc := serviceReceipt(dir)
	if svc == "" {
		t.Fatal("no tally on a day with three injections, so the test proves nothing")
	}
	title := "how should the zorbit queue drain on deploy day"
	got := receiptLine("deja recalled 3 prior sessions from this project", reusedMemory{Title: title, Times: 4}, svc)
	if !strings.Contains(got, "most re-used: \"how should the zorbit") || !strings.HasSuffix(got, " 4×") {
		t.Errorf("the re-used name did not survive: %q", got)
	}
	checkPersonLines(t, "receipt", got, 1)
	// Without a re-used session the tally is what follows the claim.
	if got := receiptLine("deja recalled 3 prior sessions from this project", reusedMemory{}, svc); !strings.HasSuffix(got, svc) {
		t.Errorf("the tally was dropped with room for it: %q", got)
	}
}
