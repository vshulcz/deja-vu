package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func setupPeriodicEmbedTest(t *testing.T, everyTOML string) (dir string, spawned *[]*exec.Cmd) {
	t.Helper()
	hermeticEnv(t)
	unsetTestEnv(t, "DEJA_EMBED", "DEJA_EMBED_OFF", "DEJA_EMBED_URL", "DEJA_EMBED_EVERY", "DEJA_HOOK_EMBED")

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
