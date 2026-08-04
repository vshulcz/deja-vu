package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// runHookCapturing feeds the hook one event on stdin and returns its stdout.
func runHookCapturing(t *testing.T, dir, event string) string {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- string(b)
	}()
	go func() {
		_, _ = inW.Write([]byte(event + "\n"))
		_ = inW.Close()
	}()
	if err := runHookContext(dir, false); err != nil {
		t.Fatal(err)
	}
	_ = outW.Close()
	return <-done
}

// The size on the first screen was a ceiling to whole kilobytes, so 631 bytes
// read as "~1KB" while statusline and stats counted the real figure — the one
// number a reader sees first was the one no command could reproduce (#991).
func TestHookSizeMatchesWhatTheCountersReport(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The helper swaps the process's stdin and stdout for pipes; on
		// windows that stalls the hook's own reader instead of returning.
		t.Skip("stdin/stdout swapping is not reliable on windows")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"session about the ticker window and the pool cap"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"w1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "w1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	back, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(back) })

	out := runHookCapturing(t, dir, `{"hook_event_name":"SessionStart","session_id":"probe","cwd":"`+work+`"}`)
	var resp struct {
		SystemMessage string `json:"systemMessage"`
		Hook          struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%s", err, out)
	}
	if resp.SystemMessage == "" {
		t.Skip("no memory to recall in this environment")
	}
	if strings.Contains(resp.SystemMessage, "KB)") && !strings.Contains(resp.SystemMessage, ".") {
		t.Errorf("the size is still rounded to whole kilobytes: %q", resp.SystemMessage)
	}
	// The number has to be the same one the counters accumulate.
	stats, err := captureRun(t, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Recall struct {
			Bytes int64 `json:"bytes"`
		} `json:"recall"`
	}
	if err := json.Unmarshal([]byte(stats), &s); err != nil {
		t.Fatal(err)
	}
	if want := humanBytes(s.Recall.Bytes); !strings.Contains(resp.SystemMessage, "("+want+")") {
		t.Errorf("the first screen says something else than stats counts (%s):\n%s", want, resp.SystemMessage)
	}
}
