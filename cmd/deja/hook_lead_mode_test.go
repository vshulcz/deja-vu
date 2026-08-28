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

// Aggressive recall reaches past the checkout on purpose. The lead sentence
// said "from this project's recent history" either way, so a client's sessions
// arrived described as this project's (#2343).
func TestTheLeadSaysHowWideTheRecallReached(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(dirName, id, cwd, text string, hoursAgo int) {
		d := filepath.Join(root, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":%q}}`, id, at, cwd, text)
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("-work-api", "mine", "/work/api", "my own retry budget question, long enough to rank", 20)
	for i := 0; i < 3; i++ {
		write("-clients-acme-api", fmt.Sprintf("acme%d", i), "/clients/acme/api",
			"the acme ledger cutover and the client contact list", i+1)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"SessionStart","cwd":"/work/api","session_id":"a1"}`

	out := hookContextFor(t, dir, payload)
	if !strings.Contains(out, "from this project's recent history") {
		t.Fatalf("the default lead changed, so this measures nothing:\n%s", out)
	}

	t.Setenv("DEJA_RECALL", "aggressive")
	wide := hookContextFor(t, dir, payload)
	if !strings.Contains(wide, "acme") {
		t.Fatalf("aggressive recall did not reach past the checkout, so this measures nothing:\n%s", wide)
	}
	if strings.Contains(wide, "from this project's recent history") {
		t.Errorf("another project's sessions are described as this project's:\n%s", wide)
	}
	if !strings.Contains(wide, "this machine") {
		t.Errorf("the wide lead does not say how far it reached:\n%s", wide)
	}
}
