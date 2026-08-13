package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Goose declares commands inside config.yaml, a file holding provider settings
// someone wrote by hand. Install has to leave every other line of it — and
// every other slash command — exactly where it was.
func TestGooseCommandKeepsTheRestOfConfig(t *testing.T) {
	hermeticEnv(t)
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "# my notes\nGOOSE_PROVIDER: anthropic\n\nslash_commands:\n  - command: \"mine\"\n    recipe_path: \"/tmp/mine.yaml\"\n"
	if err := os.WriteFile(cfg, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	for _, want := range []string{"# my notes", "GOOSE_PROVIDER: anthropic", `- command: "mine"`, `- command: "deja"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("config lost %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "slash_commands:") != 1 {
		t.Fatalf("slash_commands duplicated:\n%s", body)
	}
	if _, err := os.Stat(gooseRecipePath()); err != nil {
		t.Fatalf("recipe not written: %v", err)
	}

	// Re-running changes nothing, and uninstall takes back only our entry.
	if r, err := installGooseCommand("/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	// A hand-written entry in the unquoted form must be replaced, not doubled.
	if err := os.WriteFile(cfg, []byte(own+"  - command: deja\n    recipe_path: \"/old.yaml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	if n := strings.Count(string(got), `command: "deja"`) + strings.Count(string(got), "command: deja\n"); n != 1 {
		t.Fatalf("unquoted entry not replaced, %d deja commands:\n%s", n, got)
	}
	if strings.Contains(string(got), "/old.yaml") {
		t.Fatalf("the replaced entry kept its old recipe path:\n%s", got)
	}

	if _, err := installGooseCommand("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	body = string(got)
	if strings.Contains(body, `- command: "deja"`) {
		t.Fatalf("uninstall left our command:\n%s", body)
	}
	if !strings.Contains(body, `- command: "mine"`) || !strings.Contains(body, "# my notes") {
		t.Fatalf("uninstall took someone else's config with it:\n%s", body)
	}
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Fatalf("recipe survived uninstall: %v", err)
	}
}

// A config with no slash_commands key at all gets one, and a config that had
// only ours does not keep an empty key behind — Goose rejects a null value.
func TestGooseCommandAddsAndClearsTheKey(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "slash_commands:\n  - command: \"deja\"") {
		t.Fatalf("key not created:\n%s", b)
	}
	if _, err := installGooseCommand("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "slash_commands:\n\n") || strings.TrimSpace(string(b)) == "slash_commands:" {
		t.Fatalf("empty slash_commands key left behind:\n%q", b)
	}
}
