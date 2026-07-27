package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The build reports from several goroutines at once — harness stores are
// parsed concurrently — so publishing must not race for its temporary file.
func TestWarmupStatusSurvivesConcurrentReports(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := newFileProgress(dir)
	p.Phase("reading sessions", 100)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				p.Advance(1)
				p.Harness("claude", 1, 1)
			}
		}()
	}
	wg.Wait()
	st := readWarmupStatus(dir)
	if st == nil {
		t.Fatal("no status was published")
	}
	if st.Phase != "reading sessions" {
		t.Errorf("phase = %q", st.Phase)
	}
	p.done()
	if readWarmupStatus(dir) != nil {
		t.Error("the status outlived the build")
	}
}

// It lives beside the index, never inside: a first build has not created the
// index directory yet, and the atomic swap that publishes a rebuild replaces
// everything within it.
func TestWarmupStatusLivesOutsideTheIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if strings.HasPrefix(warmupStatusPath(dir), dir+string(os.PathSeparator)) {
		t.Fatalf("status path %q is inside the index directory", warmupStatusPath(dir))
	}
	p := newFileProgress(dir)
	p.Phase("reading sessions", 3)
	if readWarmupStatus(dir) == nil {
		t.Fatal("status could not be written before the index directory exists")
	}
}

// A warmup killed mid-build must not leave agents claiming forever that
// memory is on its way.
func TestWarmupStatusExpires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := newFileProgress(dir)
	p.Phase("indexing messages", 10)
	if readWarmupStatus(dir) == nil {
		t.Fatal("fresh status was not read back")
	}
	old := time.Now().Add(-2 * warmupStatusStale)
	if err := os.Chtimes(warmupStatusPath(dir), old, old); err != nil {
		t.Fatal(err)
	}
	// Staleness is judged by the stamp inside the record, not the mtime, so
	// rewrite the record with an old stamp.
	b, err := os.ReadFile(warmupStatusPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(b), `"updated":`, `"updated_ignored":`, 1)
	if err := os.WriteFile(warmupStatusPath(dir), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if readWarmupStatus(dir) != nil {
		t.Error("a record with no timestamp was treated as a live build")
	}
}

func TestWarmupStatusLineReadsLikeASentence(t *testing.T) {
	st := &warmupStatus{Phase: "reading sessions", Done: 30, Total: 60}
	line := st.line()
	for _, want := range []string{"deja", "reading sessions", "50%"} {
		if !strings.Contains(line, want) {
			t.Errorf("status line %q is missing %q", line, want)
		}
	}
	// An unknown total must not print a nonsense percentage.
	if strings.Contains((&warmupStatus{Phase: "starting"}).line(), "%") {
		t.Error("a phase with no total claimed a percentage")
	}
}

// opencode injects hook output into the model's context, so the build note
// cannot ride along with it. The plugin asks a separate command and shows a
// TUI toast — the channel meant for the person, not the prompt.
func TestOpencodePluginAnnouncesTheBuildInTheTUI(t *testing.T) {
	js := opencodePluginJS("/usr/local/bin/deja")
	for _, want := range []string{
		"warmup-status",        // asks whether a build is running
		"client.tui.showToast", // says so where a human will see it
		"told.has(key)",        // and only once per session
	} {
		if !strings.Contains(js, want) {
			t.Errorf("generated plugin is missing %q", want)
		}
	}
	// The status must never be pushed into the model's context.
	sysPush := strings.Index(js, "output.system.push")
	statusCall := strings.Index(js, "warmup-status")
	if sysPush == -1 || statusCall == -1 || statusCall < sysPush {
		t.Fatal("the plugin asks for build status before deciding it has real context to inject")
	}
	if strings.Contains(js, "output.system.push(status") {
		t.Error("the build note leaks into the model's context")
	}
	// The plugin needs the client to reach the TUI at all.
	if !strings.Contains(js, "{ $, client }") {
		t.Error("the plugin does not receive the client it needs for a toast")
	}
}

// warmup-status prints one line while a build runs, nothing otherwise: hosts
// branch on emptiness, so a stray newline would read as "still building".
func TestWarmupStatusCommandIsSilentWithoutABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	out := captureHookStdout(t, func() {
		if err := cmdWarmupStatus(dir, nil); err != nil {
			t.Fatal(err)
		}
	})
	if out != "" {
		t.Errorf("printed %q with no build running", out)
	}
	p := newFileProgress(dir)
	p.Phase("reading sessions", 8)
	p.Advance(2)
	t.Cleanup(p.done)
	out = captureHookStdout(t, func() {
		if err := cmdWarmupStatus(dir, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "indexing") {
		t.Errorf("printed %q during a build", out)
	}
}

// Thirteen of the sixteen harnesses have no hook and reach deja only over
// MCP. A recall during the first build must not answer with a confident
// negative, or the agent tells the user they have no history at all.
func TestMCPRecallDistinguishesEmptyFromStillBuilding(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	quiet := emptyRecallAnswer(dir, "jwt skew")
	if !strings.Contains(quiet, "No prior deja sessions matched") {
		t.Errorf("with no build running the answer should be a plain negative, got %q", quiet)
	}

	p := newFileProgress(dir)
	p.Phase("reading sessions", 10)
	p.Advance(3)
	t.Cleanup(p.done)

	building := emptyRecallAnswer(dir, "jwt skew")
	if strings.Contains(building, "No prior deja sessions matched") {
		t.Fatalf("a recall during the build still reads as a negative: %q", building)
	}
	// Not asserting the percentage here: publishing is throttled to 250ms, so
	// a freshly reported Advance may not have reached the file yet. The
	// percentage itself is covered by TestWarmupProgressFragment.
	for _, want := range []string{"building", "reading sessions", "ask again"} {
		if !strings.Contains(building, want) {
			t.Errorf("answer %q is missing %q", building, want)
		}
	}
}

func TestWarmupProgressFragment(t *testing.T) {
	if got := (&warmupStatus{Phase: "indexing messages", Done: 1, Total: 4}).progress(); got != "indexing messages 25%" {
		t.Errorf("progress = %q", got)
	}
	// No total means no invented percentage.
	if got := (&warmupStatus{Phase: "starting"}).progress(); got != "starting" {
		t.Errorf("progress with unknown total = %q", got)
	}
}

// opencode has no place to put a receipt in the prompt, so the toast is the
// only sign the user gets that memory arrived. It fires once per session and
// only when there is something to announce.
func TestOpencodePluginToastsTheRecallReceipt(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	for _, want := range []string{
		`hook-context`,         // JSON form: the receipt rides with the context
		"hookSpecificOutput",   // context is read from the envelope
		"systemMessage",        // receipt is read from the envelope
		"told.add(key)",        // once per session
		"client.tui.showToast", // opencode's own channel to the user
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated plugin missing %q:\n%s", want, src)
		}
	}
	// --plain would drop the receipt on the floor.
	if strings.Contains(src, "hook-context --plain") {
		t.Fatal("plugin still asks for the plain digest, which carries no receipt")
	}
}

// Claude Code gets a relevance pass on every prompt; opencode has the same
// opening in messages.transform, and without it recall there is only ever as
// good as the session digest.
func TestOpencodePluginRecallsPerPrompt(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	for _, want := range []string{
		"experimental.chat.messages.transform",
		`info?.role === "user"`, // the last user turn, not the last message
		"hook-prompt",
		"additionalContext",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated plugin missing %q:\n%s", want, src)
		}
	}
	// It appends to the prompt rather than replacing it.
	if !strings.Contains(src, `parts[parts.length - 1].text += `) {
		t.Fatalf("recall does not append to the user's own text:\n%s", src)
	}
}

// Compaction throws away the working transcript. Claude Code gets it indexed
// first through PreCompact; opencode fires experimental.session.compacting at
// the same moment and was going unused.
func TestOpencodePluginIndexesBeforeCompaction(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	if !strings.Contains(src, "experimental.session.compacting") {
		t.Fatalf("compaction passes without indexing:\n%s", src)
	}
	if !strings.Contains(src, "hook-precompact") {
		t.Fatalf("compaction hook does not index:\n%s", src)
	}
}
