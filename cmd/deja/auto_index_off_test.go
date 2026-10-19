package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

const autoOffWord = "zanzibarquux"

// autoOffStore is forkStore with a transcript grown after the build, the
// warmup spawner recorded rather than run, and the test guard on warmups
// lifted so requestWarmup is the code under test.
func autoOffStore(t *testing.T) (dir string, spawned *int) {
	t.Helper()
	dir, _ = forkStore(t, false)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	n := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	saidManualIndex = sync.Once{}
	t.Cleanup(func() { saidManualIndex = sync.Once{} })
	growPriorSession(t, autoOffWord)
	return dir, &n
}

// growPriorSession appends a turn holding word to a session already indexed.
func growPriorSession(t *testing.T, word string) {
	t.Helper()
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", "prior-c.jsonl")
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"type":"user","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:05:00Z","message":{"role":"user","content":"` + word + ` showed up after the build"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

type manifestStamp struct {
	body  []byte
	mtime int64
}

func stampManifest(t *testing.T, dir string) manifestStamp {
	t.Helper()
	p := filepath.Join(dir, "manifest.gob")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return manifestStamp{b, fi.ModTime().UnixNano()}
}

// Every place deja updates the index on its own leaves it alone with
// DEJA_NO_AUTO_INDEX=1: CLI search and show, the TUI refresh, MCP tools, the
// session-start hook, its detached digest refresh and the warmup it asks for
// (#5385).
func TestNoAutoIndexEveryTriggerLeavesTheIndexAlone(t *testing.T) {
	dir, spawned := autoOffStore(t)
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	before := stampManifest(t, dir)

	triggers := []struct {
		name string
		run  func()
	}{
		{"search", func() { _ = ensureForCLISearch(dir, search.Options{Query: autoOffWord}, false, io.Discard) }},
		{"show/last", func() { _ = ensureForCLIRead(dir) }},
		{"warmup", func() { _ = cmdWarmup(dir, nil) }},
		{"hook-refresh", func() { runHookRefresh(dir) }},
		{"request warmup", func() { requestWarmup(dir) }},
		{"session start", func() {
			withHookStdin(t, `{"session_id":"s-new","source":"startup","cwd":"/w/p"}`)
			_ = captureStdout(t, func() { _ = runHookContext(dir, true) })
		}},
		{"mcp recall", func() { _, _ = callMCPTool(dir, "recall", json.RawMessage(`{"query":"`+autoOffWord+`"}`)) }},
		{"mcp recall_context", func() { _, _ = callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"`+autoOffWord+`"}`)) }},
		{"mcp blame", func() { _, _ = callMCPTool(dir, "blame", json.RawMessage(`{"path":"fetch.go"}`)) }},
		{"mcp orient", func() { _, _ = callMCPTool(dir, "orient", json.RawMessage(`{}`)) }},
		{"mcp remember", func() { _, _ = callMCPTool(dir, "remember", json.RawMessage(`{"text":"a note"}`)) }},
	}
	// Each trigger gets a change of its own to pick up, so one that refreshed
	// cannot hide the next.
	for i, tr := range triggers {
		if i > 0 {
			growPriorSession(t, fmt.Sprintf("%s%d", autoOffWord, i))
		}
		before := stampManifest(t, dir)
		_ = captureStderr(t, tr.run)
		if after := stampManifest(t, dir); !bytes.Equal(after.body, before.body) || after.mtime != before.mtime {
			t.Errorf("%s rewrote the index with automatic indexing off", tr.name)
		}
	}
	if *spawned != 0 {
		t.Errorf("%d detached warmup(s) started with automatic indexing off", *spawned)
	}
	if _, err := os.Stat(filepath.Join(dir, "warmup.sentinel")); err == nil {
		t.Error("a warmup was asked for with automatic indexing off")
	}
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 0 {
		t.Errorf("the change reached the index: %d sessions (%v)", len(r.Sessions), err)
	}

	// `deja index` is the one writer left, and its warmup-child form is not.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(t.TempDir(), "warmup.sentinel"))
	_ = captureStderr(t, func() { _ = cmdIndex(dir, nil) })
	if after := stampManifest(t, dir); !bytes.Equal(after.body, before.body) {
		t.Error("a detached warmup's `deja index` rewrote the index with automatic indexing off")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	_ = captureStderr(t, func() {
		if err := cmdIndex(dir, nil); err != nil {
			t.Errorf("deja index: %v", err)
		}
	})
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 1 {
		t.Errorf("deja index did not pick the change up: %d sessions (%v)", len(r.Sessions), err)
	}
}

// The control: without the switch the same search picks the change up, so the
// test above is not passing on a store that never refreshes.
func TestDefaultModeSearchStillRefreshes(t *testing.T) {
	dir, _ := autoOffStore(t)
	before := stampManifest(t, dir)
	_ = captureStderr(t, func() {
		if err := ensureForCLISearch(dir, search.Options{Query: autoOffWord}, false, io.Discard); err != nil {
			t.Error(err)
		}
	})
	if after := stampManifest(t, dir); bytes.Equal(after.body, before.body) {
		t.Error("a search in the default mode did not refresh the index")
	}
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 1 {
		t.Errorf("the change is not searchable after the refresh: %d sessions (%v)", len(r.Sessions), err)
	}
}

// Answers say how old the index is: the CLI search, MCP recall and
// recall_context, and the session-start digest. The default mode says nothing.
func TestNoAutoIndexAnswersCarryTheIndexDate(t *testing.T) {
	dir, _ := autoOffStore(t)
	const dated = "automatic indexing is off — answering from the index of "
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)

	plain, err := callMCPTool(dir, "recall", q)
	if err != nil || strings.Contains(plain, dated) {
		t.Fatalf("default-mode recall carried the date line (%v):\n%s", err, plain)
	}

	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	var progress bytes.Buffer
	if err := ensureForCLISearch(dir, search.Options{Query: "retry"}, false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), dated) || strings.Count(progress.String(), "\n") != 1 {
		t.Errorf("search said %q, want one line with the index date", progress.String())
	}
	for _, tool := range []string{"recall", "recall_context"} {
		out, err := callMCPTool(dir, tool, q)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if !strings.HasPrefix(out, "deja: "+dated) || !strings.Contains(out, "<deja-recall>") {
			t.Errorf("%s did not answer with the index date:\n%s", tool, out)
		}
	}
	withHookStdin(t, `{"session_id":"s-dated","source":"startup","cwd":"/w/p"}`)
	out := captureStdout(t, func() { _ = runHookContext(dir, true) })
	if !strings.Contains(out, "deja-recall") || !strings.Contains(out, dated) {
		t.Errorf("the session-start digest did not carry the index date:\n%s", out)
	}
}

// With no index at all nothing builds; each surface says so and names the
// command.
func TestNoAutoIndexWithNoIndexSaysHowToBuildIt(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	n := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", "a.jsonl"),
		`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"retry loop"}}`+"\n")
	// The policy file, so the other way to set the switch is covered too.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	write(pol, `{"auto_index": false}`)
	dir := index.DefaultDir()

	err := ensureForCLISearch(dir, search.Options{Query: "retry"}, false, io.Discard)
	if err == nil || !strings.Contains(ensureError(dir, err).Error(), "build it with: deja index") || strings.HasPrefix(ensureError(dir, err).Error(), "ensure:") {
		t.Errorf("search over no index = %v", err)
	}
	if out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry"}`)); err != nil || !strings.Contains(out, "deja index") || strings.Contains(out, "ask again") {
		t.Errorf("recall over no index = %q, %v", out, err)
	}
	withHookStdin(t, `{"session_id":"s1","source":"startup","cwd":"/w/p"}`)
	out := captureStdout(t, func() { _ = runHookContext(dir, false) })
	if !strings.Contains(out, "build it with: deja index") {
		t.Errorf("session start over no index said:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the index directory was created: %v", err)
	}
	if n != 0 {
		t.Errorf("%d warmup(s) started", n)
	}
}

// The switch reaches hooks a host starts with a stripped environment: the
// launcher carries it from install time, as it does DEJA_INDEX_DIR (#5323).
func TestLauncherCarriesNoAutoIndex(t *testing.T) {
	got := launcherEnv([]string{"DEJA_NO_AUTO_INDEX=1"})
	if len(got) != 1 || got[0] != [2]string{"DEJA_NO_AUTO_INDEX", "1"} {
		t.Errorf("launcher env = %v", got)
	}
}

// After an upgrade that only bumps the content version the index still
// answers; with automatic indexing off MCP recall answers from it, as search
// does, rather than telling the agent to wait for a rebuild.
func TestNoAutoIndexRecallAnswersAcrossAVersionBump(t *testing.T) {
	dir, spawned := autoOffStore(t)
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`))
	if err != nil || !strings.Contains(out, "<deja-recall>") || !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("recall over an older content version (%v):\n%s", err, out)
	}
	if *spawned != 0 {
		t.Error("a warmup was started")
	}
}
