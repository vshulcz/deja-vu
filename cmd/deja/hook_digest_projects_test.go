package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The environment block is built from every project's walls and names none of
// them, and the hook recorded it with no projects — so forgetting the project
// it came from left the text in the injection log (#2349).
func TestTheEnvironmentBlockRecordsWhereItCameFrom(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-clients-acme-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		at := time.Now().Add(-time.Duration(2+i) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":"acme%d","timestamp":%q,"cwd":"/clients/acme/api",`+
				`"message":{"role":"user","content":"the acme cutover keeps failing"}}`, i, at),
			fmt.Sprintf(`{"type":"user","sessionId":"acme%d","timestamp":%q,"cwd":"/clients/acme/api",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"panic: acme ledger overflow in ledger/quaxbolt.go"}]}}`, i, at),
		}
		name := fmt.Sprintf("acme%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(store, name), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(tmp, "fresh", "checkout")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	out := hookContextFor(t, dir, fmt.Sprintf(
		`{"hook_event_name":"SessionStart","cwd":%q,"session_id":"a1"}`, fresh))
	if !strings.Contains(out, "acme ledger overflow") {
		t.Fatalf("the environment block did not fire, so this measures nothing:\n%s", out)
	}

	// The record knows which project the walls came from.
	snaps := usage.Snapshots(dir, 0)
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want the one the hook just wrote", len(snaps))
	}
	if len(snaps[0].Projects) == 0 {
		t.Errorf("the environment block records no projects, so forget cannot reach it")
	}

	// And forgetting that project takes the stored text with it.
	if _, err := captureRun(t, "forget", "--project", "acme/api"); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(left), "acme ledger overflow") {
		t.Errorf("the forgotten project's wall is still in the injection log")
	}
}

// A digest of the project's own sessions records that project too, so the same
// sweep reaches it without reading its prose.
func TestTheSessionStartDigestRecordsItsProjects(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		at := time.Now().Add(-time.Duration(3+i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":"mine%d","timestamp":%q,"cwd":"/work/api",`+
			`"message":{"role":"user","content":"my own retry budget question %d"}}`, i, at, i)
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("mine%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := hookContextFor(t, dir, `{"hook_event_name":"SessionStart","cwd":"/work/api","session_id":"a1"}`)
	if !strings.Contains(out, "retry budget") {
		t.Fatalf("the project digest did not fire, so this measures nothing:\n%s", out)
	}
	snaps := usage.Snapshots(dir, 0)
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want one", len(snaps))
	}
	if got, _ := json.Marshal(snaps[0].Projects); !strings.Contains(string(got), "work/api") {
		t.Errorf("projects = %s, want the project the digest was built from", got)
	}
}
