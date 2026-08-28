package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The lookup hashes whole error lines, so the shortened form a person types
// matches nothing — and the answer blamed the machine for it while the store
// held the pair (#2365).
func TestFixNamesTheErrorItDoesHold(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-clients-acme-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		base := time.Now().Add(-time.Duration(6+i) * time.Hour).UTC()
		rows := []string{
			fmt.Sprintf(`{"type":"assistant","sessionId":"acme%d","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make acme-migrate"}}]}}`,
				i, base.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"user","sessionId":"acme%d","timestamp":%q,"cwd":"/clients/acme/api","message":{"role":"user","content":[{"type":"tool_result","content":"panic: quaxbolt overflow in ledger/quaxbolt.go"}]}}`,
				i, base.Add(2*time.Minute).Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":"acme%d","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make acme-widen-cast"}}]}}`,
				i, base.Add(5*time.Minute).Format(time.RFC3339)),
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

	// The premise: the whole line answers.
	whole, err := captureRun(t, "fix", "panic: quaxbolt overflow in ledger/quaxbolt.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole, "make acme-widen-cast") {
		t.Fatalf("the pair is not in the store, so this measures nothing:\n%s", whole)
	}

	// The head of it is what a person types.
	short, err := captureRun(t, "fix", "panic: quaxbolt overflow")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(short, "no session on this machine ran a command after that error") {
		t.Errorf("deja reports the machine empty for an error it holds:\n%s", short)
	}
	if !strings.Contains(short, "ledger/quaxbolt.go") {
		t.Errorf("deja does not name the error it does hold:\n%s", short)
	}

	// A line nothing resembles still gets the plain answer.
	none, err := captureRun(t, "fix", "connection refused talking to the widget service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none, "no session on this machine ran a command after that error") {
		t.Errorf("an error nothing recorded lost its answer:\n%s", none)
	}
}
