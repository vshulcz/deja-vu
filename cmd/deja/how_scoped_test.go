package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

func writeScopedBashSession(t *testing.T, claudeRoot, slug, sid, cwd, cmd string, ts time.Time) {
	t.Helper()
	path := filepath.Join(claudeRoot, slug, sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := fmt.Sprintf(
		`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":"run command"}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":%q}}]}}`+"\n"+
			`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"ok","is_error":false}]}}`+"\n",
		sid, ts.Format(time.RFC3339), cwd,
		sid, ts.Add(time.Second).Format(time.RFC3339), cwd, cmd,
		sid, ts.Add(2*time.Second).Format(time.RFC3339), cwd,
	)
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHowProjectScopeAvoidsMachineWideRecordScan(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	t0 := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	writeScopedBashSession(t, claudeRoot, "-work-target-app", "t-1", "/work/target-app", "pytest tests/target", t0)
	probeCmd := "cargo check unique_probe_cmd"
	writeScopedBashSession(t, claudeRoot, "-work-other-0", "o-0", "/work/other-0", probeCmd, t0.Add(time.Minute))
	for i := 1; i <= 4; i++ {
		writeScopedBashSession(t, claudeRoot, fmt.Sprintf("-work-other-%d", i), fmt.Sprintf("o-%d", i),
			fmt.Sprintf("/work/other-%d", i), "cargo test --workspace", t0.Add(time.Duration(i+1)*time.Minute))
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	// Corrupt the encoding flag of one unrelated project's command record while
	// preserving frame lengths so any code path decoding unrelated sessions
	// fails, while a scoped offset read succeeds without touching them.
	recPath := filepath.Join(dir, "records.bin")
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	probe := []byte("$ " + probeCmd)
	pos := bytes.Index(raw, probe)
	if pos < 10 || raw[pos-10] != 0 {
		t.Fatalf("unexpected raw command layout around probe: pos=%d", pos)
	}
	raw[pos-10] = 0xff
	if werr := os.WriteFile(recPath, raw, 0o600); werr != nil {
		t.Fatal(werr)
	}

	entries, _, _, widened, err := howEntriesWithFallback(dir, []string{"pytest"}, []string{"work/target-app"}, true, policy.ActivationAuto)
	if err != nil {
		t.Fatalf("scoped howEntriesWithFallback read unrelated session: %v", err)
	}
	if widened {
		t.Fatal("expected widened=false on in-project match")
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Command, "pytest tests/target") {
		t.Fatalf("entries = %+v, want [pytest tests/target]", entries)
	}
}

func TestHowWidenFallbackRunsOnlyWhenScopedMisses(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	t0 := time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC)

	writeScopedBashSession(t, claudeRoot, "-work-target-app", "t-1", "/work/target-app", "go test ./...", t0)
	for i := range 5 {
		writeScopedBashSession(t, claudeRoot, fmt.Sprintf("-work-other-%d", i), fmt.Sprintf("o-%d", i),
			fmt.Sprintf("/work/other-%d", i), "cargo test --workspace", t0.Add(time.Duration(i+1)*time.Minute))
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	entries, _, _, widened, err := howEntriesWithFallback(dir, []string{"cargo"}, []string{"work/target-app"}, true, policy.ActivationAuto)
	if err != nil {
		t.Fatalf("widened howEntriesWithFallback: %v", err)
	}
	if !widened {
		t.Fatal("expected widened=true when scoped project missed")
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Command, "cargo test --workspace") || entries[0].Runs != 5 {
		t.Fatalf("entries = %+v, want 1 entry with Runs=5", entries)
	}
}
