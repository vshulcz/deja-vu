package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The session OpenClaw wrote to last is read off the store, not the index,
// so an MCP call from a hookless TUI can tell which conversation is asking
// (#5271).
func TestOpenClawNewestSessionReadsTheStore(t *testing.T) {
	root, _ := openclawTestDB(t)
	at := time.UnixMilli(1784813602000)
	id, when := OpenClawNewestSession(time.UnixMilli(1784538000000))
	if id != "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e" || !when.Equal(at) {
		t.Fatalf("newest = %q at %v, want the reset window's at %v", id, when, at)
	}
	if id, _ := OpenClawNewestSession(at); id != "" {
		t.Errorf("nothing was written after %v, got %q", at, id)
	}

	// A transcript file written after the store's last event wins.
	p := filepath.Join(root, "main", "sessions", "tui-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"type":"session","version":3,"id":"tui-1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if id, when := OpenClawNewestSession(at.Add(-time.Hour)); id != "tui-1" || !when.Equal(later) {
		t.Errorf("newest = %q at %v, want tui-1 at %v", id, when, later)
	}
}
