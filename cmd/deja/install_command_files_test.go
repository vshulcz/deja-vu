package main

import (
	"os"
	"strings"
	"testing"
)

// Commands are not part of the Agent Skills standard, so every harness has its
// own directory and its own file format. A command written in the wrong shape
// is not rejected — it is simply never offered when someone types "/".
func TestCommandFilesMatchEachHarnessShape(t *testing.T) {
	hermeticEnv(t)
	for _, h := range []string{"opencode", "cursor", "roo", "gemini"} {
		r, err := installCommandFile(h, "/bin/deja", false)
		if err != nil || r.Path == "" {
			t.Fatalf("%s command = %#v, %v", h, r, err)
		}
		b, err := os.ReadFile(r.Path)
		if err != nil {
			t.Fatalf("%s: %v", h, err)
		}
		body := string(b)
		if !strings.Contains(body, "/bin/deja") {
			t.Errorf("%s command does not name the binary:\n%s", h, body)
		}
		if h == "gemini" {
			// Gemini reads TOML with a prompt key, and substitutes {{args}}.
			// Without the placeholder it appends the user's words as a separate
			// paragraph, which reads as an unrelated instruction.
			if !strings.HasPrefix(body, "description = ") || !strings.Contains(body, "prompt = ") {
				t.Errorf("gemini command is not the documented TOML shape:\n%s", body)
			}
			if !strings.Contains(body, "{{args}}") {
				t.Errorf("gemini command has no {{args}} placeholder:\n%s", body)
			}
			continue
		}
		if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "description:") {
			t.Errorf("%s command has no markdown frontmatter:\n%s", h, body)
		}
		if !strings.Contains(body, "$ARGUMENTS") {
			t.Errorf("%s command has no $ARGUMENTS placeholder:\n%s", h, body)
		}

		// Uninstall takes back exactly what was written.
		if r, err = installCommandFile(h, "/bin/deja", true); err != nil || r.Action != "removed" {
			t.Fatalf("%s uninstall = %#v, %v", h, r, err)
		}
		if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
			t.Errorf("%s command survived uninstall", h)
		}
	}
}
