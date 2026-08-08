package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A trust rule that withholds imported content must hold on the direct-access
// commands too, not only search and last. show, share and handoff each take an
// id and reveal a whole session, so before this they leaked a peer's content a
// rule had hidden — ctx already refused here (#1026).
func TestDirectAccessHonoursTrustPolicy(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv blanks XDG_CONFIG_HOME; point it at a real dir so the policy
	// file lands where policy.Load reads it.
	cfg := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)

	// A local session and an imported one carrying a distinctive marker.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-loc")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := `{"type":"user","message":{"role":"user","content":"LOCALMARKER work"},"timestamp":"2026-07-25T10:00:00Z","sessionId":"locz","cwd":"/loc"}`
	if err := os.WriteFile(filepath.Join(store, "l.jsonl"), []byte(loc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Hand-write a foreign sync batch and import it.
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peerz","project":"svc","role":"user","text":"IMPORTEDMARKER secret","time":"2026-07-22T10:00:00Z"}`
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-p.jsonl"), []byte(peer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", shared); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writePolicy := func(imported bool) {
		body := `{"activations":{"search":{"imported":true}}}`
		if !imported {
			body = `{"activations":{"search":{"imported":false}}}`
		}
		if err := os.WriteFile(policyPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Find the imported id while the rule allows it.
	writePolicy(true)
	last, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	impID := ""
	for _, f := range strings.Fields(last) {
		if strings.HasPrefix(f, "imported-") {
			impID = strings.Trim(f, "]·")
			break
		}
	}
	if impID == "" {
		t.Fatalf("no imported id in last output:\n%s", last)
	}

	// Deny: every direct-access command must withhold the imported content.
	writePolicy(false)
	for _, args := range [][]string{{"show", impID}, {"share", impID}, {"handoff", impID}} {
		out, _ := captureRun(t, args...)
		if strings.Contains(out, "IMPORTEDMARKER") {
			t.Errorf("%s leaked imported content under deny-imported:\n%s", args[0], out)
		}
	}
	// promote --to wrote the imported content to a file the reader named, the
	// one leak the others do not have; the whole command must refuse first.
	exp := filepath.Join(t.TempDir(), "promoted.md")
	if out, _ := captureRun(t, "promote", impID, "--to", exp); strings.Contains(out, "IMPORTEDMARKER") {
		t.Errorf("promote leaked imported content under deny-imported:\n%s", out)
	}
	if b, err := os.ReadFile(exp); err == nil && strings.Contains(string(b), "IMPORTEDMARKER") {
		t.Errorf("promote --to wrote imported content to the export file:\n%s", string(b))
	}
	// The local session stays reachable — the rule is about origin, not access.
	if out, _ := captureRun(t, "show", "locz"); !strings.Contains(out, "LOCALMARKER") {
		t.Errorf("show over-blocked a local session under deny-imported:\n%s", out)
	}
	// Allow: the imported session comes back.
	writePolicy(true)
	if out, _ := captureRun(t, "show", impID); !strings.Contains(out, "IMPORTEDMARKER") {
		t.Errorf("show withheld imported content even when the rule allows it:\n%s", out)
	}
}
