package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
)

func setupPeriodicEmbedTest(t *testing.T, everyTOML string) (dir string, spawned *[]*exec.Cmd) {
	t.Helper()
	hermeticEnv(t)
	unsetTestEnv(t, "DEJA_EMBED", "DEJA_EMBED_OFF", "DEJA_EMBED_URL", "DEJA_EMBED_EVERY", "DEJA_HOOK_EMBED", "DEJA_EMBED_ASYNC_BATCH")

	dir = filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}
	if !index.HasManifest(dir) {
		t.Fatalf("expected index manifest in %s", dir)
	}

	if everyTOML != "" {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		writeTestConfig(t, cfgPath, everyTOML)
		t.Setenv("DEJA_CONFIG_FILE", cfgPath)
	}

	var cmds []*exec.Cmd
	prevStart := startEmbedWorker
	startEmbedWorker = func(cmd *exec.Cmd) (int, error) {
		cmds = append(cmds, cmd)
		return os.Getpid(), nil
	}
	t.Cleanup(func() { startEmbedWorker = prevStart })

	return dir, &cmds
}

func TestRequestPeriodicEmbedGuards(t *testing.T) {
	// Unset interval -> no spawn.
	dir, spawned := setupPeriodicEmbedTest(t, "")
	requestPeriodicEmbed(dir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns without embed.every, got %d", len(*spawned))
	}

	// embed.every = "off" -> no spawn.
	dir, spawned = setupPeriodicEmbedTest(t, "[embed]\nevery = \"off\"\n")
	requestPeriodicEmbed(dir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns for embed.every=off, got %d", len(*spawned))
	}

	// embed.Off() via DEJA_EMBED_OFF=1 -> no spawn.
	dir, spawned = setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")
	t.Setenv("DEJA_EMBED_OFF", "1")
	requestPeriodicEmbed(dir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns when DEJA_EMBED_OFF=1, got %d", len(*spawned))
	}

	// DEJA_HOOK_EMBED=1 recursion guard -> no spawn.
	dir, spawned = setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")
	t.Setenv("DEJA_HOOK_EMBED", "1")
	requestPeriodicEmbed(dir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns inside DEJA_HOOK_EMBED=1, got %d", len(*spawned))
	}

	// Missing manifest -> no spawn.
	emptyDir := filepath.Join(t.TempDir(), "empty-index")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, spawned = setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")
	requestPeriodicEmbed(emptyDir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns when index manifest missing, got %d", len(*spawned))
	}

	// State sidecars live beside dir, not inside it, so rebuild swaps and archive
	// sealing leave them alone.
	parentDir := filepath.Dir(filepath.Clean(dir))
	for _, sidecar := range []string{embedRunningPath(dir), embedLastPath(dir), embedRetryPath(dir)} {
		if filepath.Dir(sidecar) != parentDir {
			t.Fatalf("sidecar path %q must live in %q beside %q", sidecar, parentDir, dir)
		}
	}

	// Empty dir returns early without invoking embedRunner or touching CWD.
	wd := t.TempDir()
	t.Chdir(wd)
	called := false
	prevRunner := embedRunner
	embedRunner = func(string) error { called = true; return nil }
	t.Cleanup(func() { embedRunner = prevRunner })
	runHookEmbed("")
	if called {
		t.Fatal("runHookEmbed(\"\") invoked embedRunner")
	}
}

func TestRequestPeriodicEmbedSpawnsDedupsReclaimsAndRespectsLast(t *testing.T) {
	dir, spawned := setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")

	// First call creates .embed-running and spawns detached `hook-embed`.
	requestPeriodicEmbed(dir)
	if len(*spawned) != 1 {
		t.Fatalf("expected 1 spawn on initial trigger, got %d", len(*spawned))
	}
	cmd := (*spawned)[0]
	if !slices.Contains(cmd.Args, "hook-embed") {
		t.Fatalf("spawned Args = %v, want \"hook-embed\"", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "DEJA_HOOK_EMBED=1") {
		t.Fatalf("spawned Env missing DEJA_HOOK_EMBED=1")
	}
	if !slices.Contains(cmd.Env, "DEJA_INDEX_DIR="+dir) {
		t.Fatalf("spawned Env missing DEJA_INDEX_DIR=%s", dir)
	}
	running := embedRunningPath(dir)
	if _, err := os.Stat(running); err != nil {
		t.Fatalf("expected .embed-running sentinel after spawn: %v", err)
	}

	// Live sentinel owner (current test PID recorded by startEmbedWorker) blocks a second spawn.
	requestPeriodicEmbed(dir)
	if len(*spawned) != 1 {
		t.Fatalf("expected live sentinel dedupe to skip spawn, got %d spawns", len(*spawned))
	}

	// Stale sentinel with dead PID is reclaimed immediately even before 2m modtime fallback.
	host := strings.Join(strings.Fields(warmupHost()), "_")
	writeClaim(t, running, 10*time.Second, "owner 999999 0 "+host+"\n")

	requestPeriodicEmbed(dir)
	if len(*spawned) != 2 {
		t.Fatalf("expected stale dead sentinel to be reclaimed, got %d spawns", len(*spawned))
	}

	// Clear .embed-running and test .embed-last cooldown window.
	_ = os.Remove(running)
	fresh := time.Now().Add(-5 * time.Minute)
	if err := os.WriteFile(embedLastPath(dir), []byte(strconv.FormatInt(fresh.UnixNano(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPeriodicEmbed(dir)
	if len(*spawned) != 2 {
		t.Fatalf("expected fresh .embed-last within 30m to skip spawn, got %d", len(*spawned))
	}

	// Expired .embed-last triggers again.
	expired := time.Now().Add(-45 * time.Minute)
	if err := os.WriteFile(embedLastPath(dir), []byte(strconv.FormatInt(expired.UnixNano(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPeriodicEmbed(dir)
	if len(*spawned) != 3 {
		t.Fatalf("expected expired .embed-last to re-trigger spawn, got %d", len(*spawned))
	}
}

func TestRunHookEmbedBackoffFailureAndSuccess(t *testing.T) {
	dir, spawned := setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")
	if err := os.WriteFile(embedRunningPath(dir), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}

	prevRunner := embedRunner
	t.Cleanup(func() { embedRunner = prevRunner })

	// Failure sets .embed-retry, removes .embed-running, leaves .embed-last absent.
	embedRunner = func(string) error { return errors.New("endpoint unreachable") }
	runHookEmbed(dir)

	if _, err := os.Stat(embedRunningPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-running removed after failure, err=%v", err)
	}
	if _, err := os.Stat(embedLastPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-last untouched on failure, err=%v", err)
	}
	if _, err := os.Stat(embedRetryPath(dir)); err != nil {
		t.Fatalf("expected .embed-retry written on failure: %v", err)
	}

	// Subsequent trigger inside retry window skips spawning.
	requestPeriodicEmbed(dir)
	if len(*spawned) != 0 {
		t.Fatalf("expected 0 spawns during retry backoff window, got %d", len(*spawned))
	}

	// Success writes .embed-last, clears .embed-retry and .embed-running.
	if err := os.WriteFile(embedRunningPath(dir), []byte("456"), 0o600); err != nil {
		t.Fatal(err)
	}
	embedRunner = func(string) error { return nil }
	runHookEmbed(dir)

	if _, err := os.Stat(embedRunningPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-running removed after success, err=%v", err)
	}
	if _, err := os.Stat(embedRetryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-retry removed after success, err=%v", err)
	}
	if _, err := os.Stat(embedLastPath(dir)); err != nil {
		t.Fatalf("expected .embed-last written on success: %v", err)
	}
}

func TestHookSessionEndTriggersPeriodicEmbed(t *testing.T) {
	dir, spawned := setupPeriodicEmbedTest(t, "[embed]\nevery = \"30m\"\n")
	t.Setenv("DEJA_INDEX_DIR", dir)

	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"s-end-embed","cwd":"/tmp/proj"}`))
	if len(*spawned) != 1 {
		t.Fatalf("expected runHookSessionEnd to trigger periodic embed, got %d spawns", len(*spawned))
	}
}

func newAsyncBatchTestServer(t *testing.T, completeOnPoll int32, onBatchCreated func()) *httptest.Server {
	t.Helper()
	var (
		mu        sync.Mutex
		customIDs []string
		polls     atomic.Int32
	)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/embeddings":
			t.Errorf("unexpected synchronous /v1/embeddings call in async batch mode")
			http.Error(w, "sync forbidden", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/files":
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("FormFile: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer func() { _ = f.Close() }()
			raw, err := io.ReadAll(f)
			if err != nil {
				t.Errorf("ReadAll batch file: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var ids []string
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				if line == "" {
					continue
				}
				var req struct {
					CustomID string `json:"custom_id"`
				}
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					t.Errorf("unmarshal batch line: %v", err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				ids = append(ids, req.CustomID)
			}
			mu.Lock()
			customIDs = ids
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"file_in_1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batches":
			if onBatchCreated != nil {
				defer onBatchCreated()
			}
			_, _ = w.Write([]byte(`{"id":"batch_1","status":"validating","input_file_id":"file_in_1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/batches/batch_1":
			n := polls.Add(1)
			if n >= completeOnPoll {
				_, _ = w.Write([]byte(`{"id":"batch_1","status":"completed","input_file_id":"file_in_1","output_file_id":"file_out_1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"batch_1","status":"in_progress","input_file_id":"file_in_1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file_out_1/content":
			mu.Lock()
			ids := append([]string(nil), customIDs...)
			mu.Unlock()
			for _, id := range ids {
				fmt.Fprintf(w, "{\"custom_id\":%q,\"response\":{\"status_code\":200,\"body\":{\"data\":[{\"embedding\":[1,0]}]}}}\n", id)
			}
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/files/"):
			_, _ = w.Write([]byte(`{"deleted":true}`))
		default:
			t.Errorf("unexpected async batch request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestRunHookEmbedAsyncBatchLifecycle(t *testing.T) {
	hermeticEnv(t)
	unsetTestEnv(t, "DEJA_EMBED", "DEJA_EMBED_OFF", "DEJA_EMBED_URL", "DEJA_EMBED_EVERY", "DEJA_HOOK_EMBED", "DEJA_EMBED_ASYNC_BATCH", "DEJA_CONFIG_FILE")

	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"async hook embed session"}}`,
	})

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	ts := newAsyncBatchTestServer(t, 2, nil)
	defer ts.Close()

	t.Setenv("DEJA_EMBED_URL", ts.URL+"/v1/embeddings")
	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")

	// Pass 1: submits batch and exits without blocking; .embed-batch exists, .embed-last does not.
	if err := os.WriteFile(embedRunningPath(dir), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookEmbed(dir)

	if _, err := os.Stat(embedRunningPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-running removed after pass 1, err=%v", err)
	}
	if _, err := os.Stat(embed.BatchStatePath(dir)); err != nil {
		t.Fatalf("expected .embed-batch after submit, err=%v", err)
	}
	if _, err := os.Stat(embedLastPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no .embed-last while batch is validating, err=%v", err)
	}
	if _, err := os.Stat(embedRetryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no .embed-retry while batch is queued, err=%v", err)
	}

	// Pass 2: polls in_progress, clears any stale .embed-retry, keeps .embed-last unset.
	if err := os.WriteFile(embedRetryPath(dir), []byte("999"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookEmbed(dir)

	if _, err := os.Stat(embed.BatchStatePath(dir)); err != nil {
		t.Fatalf("expected .embed-batch to stay during in_progress poll, err=%v", err)
	}
	if _, err := os.Stat(embedRetryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected stale .embed-retry cleared during pending poll, err=%v", err)
	}
	if _, err := os.Stat(embedLastPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no .embed-last during in_progress poll, err=%v", err)
	}

	// Pass 3: polls completed, absorbs vectors, removes .embed-batch, and writes .embed-last.
	runHookEmbed(dir)

	if _, err := os.Stat(embed.BatchStatePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected .embed-batch removed on completion, err=%v", err)
	}
	if _, err := os.Stat(embedLastPath(dir)); err != nil {
		t.Fatalf("expected .embed-last written on completion, err=%v", err)
	}
	if _, err := os.Stat(embedRetryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no .embed-retry after completion, err=%v", err)
	}
	s, err := embed.Read(dir)
	if err != nil || s.Covered != 1 || len(s.Vectors) != 1 {
		t.Fatalf("embed.Read = (%+v, %v), want Covered=1 len(Vectors)=1", s, err)
	}
}

func TestRunEmbedContextAsyncBatchCancelAndResume(t *testing.T) {
	hermeticEnv(t)
	unsetTestEnv(t, "DEJA_EMBED", "DEJA_EMBED_OFF", "DEJA_EMBED_URL", "DEJA_EMBED_EVERY", "DEJA_HOOK_EMBED", "DEJA_EMBED_ASYNC_BATCH", "DEJA_CONFIG_FILE")

	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"async cancel resume session"}}`,
	})

	dir := index.DefaultDir()
	submitted := make(chan struct{})
	var closeOnce sync.Once
	ts := newAsyncBatchTestServer(t, 1, func() {
		closeOnce.Do(func() { close(submitted) })
	})
	defer ts.Close()

	t.Setenv("DEJA_EMBED_URL", ts.URL+"/v1/embeddings")
	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-submitted
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(embed.BatchStatePath(dir)); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()

	var errBuf bytes.Buffer
	err := runEmbedContext(ctx, dir, nil, &errBuf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runEmbedContext err = %v, want context.Canceled", err)
	}
	if !strings.Contains(errBuf.String(), "async embed batch stays queued") {
		t.Fatalf("expected resume notice on cancel, got %q", errBuf.String())
	}
	if _, statErr := os.Stat(embed.BatchStatePath(dir)); statErr != nil {
		t.Fatalf("expected .embed-batch preserved on cancel: %v", statErr)
	}

	if err := runEmbed(dir, nil); err != nil {
		t.Fatalf("resume runEmbed: %v", err)
	}
	if _, statErr := os.Stat(embed.BatchStatePath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("expected .embed-batch removed on resume, err=%v", statErr)
	}
	if s, rErr := embed.Read(dir); rErr != nil || s.Covered != 1 {
		t.Fatalf("resumed sidecar = (%+v, %v), want Covered=1", s, rErr)
	}
}
