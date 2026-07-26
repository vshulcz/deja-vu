package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// While the first index is being built there is nothing to inject, and the
// hook used to answer with silence — indistinguishable from deja being broken.
// It should say the build is running instead.
func TestSessionStartHookReportsAnInFlightBuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	p := newFileProgress(dir)
	p.Phase("reading sessions", 100)
	p.Advance(42)
	t.Cleanup(p.done)

	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) == "" {
		t.Fatal("the hook stayed silent while a build was running")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook emitted something that is not its envelope: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "indexing") {
		t.Errorf("systemMessage = %q, want it to explain the build", resp.SystemMessage)
	}
	// The model's context must stay clean: this is a note for the human.
	if resp.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("a progress note leaked into the model's context: %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

// The plain path is injected straight into the model's context (the opencode
// plugin), where a progress line would be noise rather than a UI affordance.
func TestPlainHookStaysSilentDuringABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := newFileProgress(dir)
	p.Phase("reading sessions", 10)
	t.Cleanup(p.done)

	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("plain hook emitted %q during a build", out)
	}
}

// With no build running and nothing to recall, silence is still correct.
func TestSessionStartHookSilentWithoutABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("hook spoke with no build and no memory: %q", out)
	}
}

func captureHookStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b := make([]byte, 1<<16)
		n, _ := r.Read(b)
		done <- string(b[:n])
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
