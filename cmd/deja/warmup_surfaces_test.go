package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// doctor and the hook both say when the index is being rebuilt; the statusline
// said "no recalls yet today" and the MCP instructions said nothing at all —
// a normal day, while the index on disk is one this build cannot read (#879).
func TestEverySurfaceSaysWhenTheIndexIsBeingRebuilt(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	statusline := func() string {
		var out bytes.Buffer
		if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/proj"}`), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// Healthy: neither surface mentions a build.
	if got := statusline(); strings.Contains(got, "rebuilding") {
		t.Errorf("a healthy index was called a rebuild: %q", got)
	}
	if got := mcpInstructions(dir); strings.Contains(got, "being rebuilt") {
		t.Errorf("a healthy index was called a rebuild: %q", got)
	}

	// A build asked for moments ago that has not published progress yet.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(warmupStatusPath(dir))

	if got := statusline(); !strings.Contains(got, "rebuilding the index") {
		t.Errorf("statusline claims an ordinary day during a rebuild: %q", got)
	}
	if got := mcpInstructions(dir); !strings.Contains(got, "being rebuilt right now") {
		t.Errorf("MCP instructions say nothing about the rebuild: %q", got)
	}

	// A machine with no index is not told its index is being rebuilt.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := statusline(); strings.Contains(got, "rebuilding") {
		t.Errorf("a machine with no index was told about a rebuild: %q", got)
	}
	if got := mcpInstructions(dir); strings.Contains(got, "being rebuilt") {
		t.Errorf("a machine with no index was told about a rebuild: %q", got)
	}
}
