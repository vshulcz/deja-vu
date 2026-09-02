package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The parsed writer deleted `hooks.internal.enabled` whenever the entries block
// emptied, including when the reader had set it themselves and deja only
// overwrote it — so an install switched internal hooks on for someone who had
// switched them off, and the uninstall took the setting away rather than
// giving it back (#2830, the parity the JSONC path gained in #2825).
func TestTheParsedPathGivesBackASwitchTheReaderSet(t *testing.T) {
	for _, c := range []struct {
		name  string
		start map[string]any
		// want is what hooks.internal.enabled should hold afterwards; nil for
		// "the key should be gone".
		want any
	}{
		{
			name:  "the reader had it off",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"enabled": false}}},
			want:  false,
		},
		{
			name:  "the reader had it on",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"enabled": true}}},
			want:  true,
		},
		{
			name:  "deja turned it on itself",
			start: map[string]any{},
			want:  nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before, err := json.MarshalIndent(c.start, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(before, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			// Twice: reinstalling after an upgrade is ordinary, and the second
			// install reads the switch deja itself set — recording that would
			// hand the reader back deja's own value as if it were theirs.
			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			// While installed the switch is on whatever the reader had.
			if on := readOpenClawSwitch(t, path); on != true {
				t.Errorf("the hook was wired with the switch %v, so it never runs", on)
			}
			if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if got := readOpenClawSwitch(t, path); got != c.want {
				t.Errorf("after uninstall the switch is %v, want %v", got, c.want)
			}
		})
	}
}

func readOpenClawSwitch(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("the config no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	if internal == nil {
		return nil
	}
	v, ok := internal["enabled"]
	if !ok {
		return nil
	}
	return v
}
