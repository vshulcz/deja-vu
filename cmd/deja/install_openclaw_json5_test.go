package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// OpenClaw reads openclaw.json as JSON5. A file with unquoted keys was refused
// with a JSON error pointing at a key or at the comment above it, and one with
// trailing commas installed and then could not be uninstalled (#4557).
func TestOpenClawJSON5Config(t *testing.T) {
	seed := func(t *testing.T, text string) string {
		t.Helper()
		hermeticEnv(t)
		p := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("unquoted keys are refused naming JSON5", func(t *testing.T) {
		for _, text := range []string{
			"{\n  agents: {defaults: {model: 'anthropic/x'}},\n}\n",
			"// mine\n{\n  agents: {defaults: {model: 'anthropic/x'}},\n}\n",
		} {
			for _, target := range []string{"openclaw", "openclaw-auto"} {
				p := seed(t, text)
				_, err := captureRun(t, "install", target, "--no-index", "--no-guidance")
				// Past the path: the test's own temp dir has the word in it.
				if err == nil || !strings.Contains(err.Error(), "as JSON5") {
					t.Errorf("%s: refusal does not name JSON5: %v", target, err)
				}
				if got, _ := os.ReadFile(p); string(got) != text {
					t.Errorf("%s: config changed:\n%s", target, got)
				}
			}
		}
	})
	t.Run("trailing commas uninstall the way they install", func(t *testing.T) {
		text := "{\n  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n}\n"
		p := seed(t, text)
		if _, err := captureRun(t, "install", "openclaw-auto", "--no-index", "--no-guidance"); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
			t.Fatalf("uninstall refused what install accepted: %v", err)
		}
		if got, _ := os.ReadFile(p); string(got) != text {
			t.Errorf("uninstall did not give the file back:\n%q\nwant\n%q", got, text)
		}
	})
}
