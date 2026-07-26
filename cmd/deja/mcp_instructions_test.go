package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Hosts that honour the field (copilot, behind --allow-all-mcp-server-instructions)
// read it straight out of the initialize result, so keep it wired to the handler.
func TestMCPInitializeCarriesInstructions(t *testing.T) {
	res, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "initialize"})
	if code != 0 {
		t.Fatalf("initialize failed: %d %s", code, msg)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map", res)
	}
	got, _ := m["instructions"].(string)
	if !strings.Contains(got, "recall") {
		t.Fatalf("instructions do not mention the recall tool: %q", got)
	}
}

func TestMCPInstructionsReportBuildInProgress(t *testing.T) {
	dir := t.TempDir()
	st := warmupStatus{Phase: "indexing", Total: 4, Done: 1, Updated: time.Now().UnixNano()}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(warmupStatusPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got := mcpInstructions(dir)
	if !strings.Contains(got, "indexing 25%") {
		t.Fatalf("instructions omit build progress: %q", got)
	}
	_ = os.Remove(warmupStatusPath(dir))
	if got := mcpInstructions(dir); strings.Contains(got, "still building") {
		t.Fatalf("instructions claim a build with no status file: %q", got)
	}
}
