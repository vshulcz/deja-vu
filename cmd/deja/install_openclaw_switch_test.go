package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The switch that turns openclaw's internal hooks on is the reader's setting,
// and deja only borrows it. Uninstall restored it only when no other entry was
// left, so a reader with a hook of their own and the switch off got it back on
// — deja gone, their hook running where it had not been. The JSONC writer
// restored it either way, so the two spellings of one file disagreed (#3204).
func TestOpenClawUninstallGivesTheSwitchBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   string
		want   any // what "enabled" must be afterwards; nil = the key is gone
		theirs bool
	}{
		{
			name:   "off, with an entry of their own",
			seed:   `{"hooks":{"internal":{"enabled":false,"entries":{"theirs":{"command":"x"}}}}}`,
			want:   false,
			theirs: true,
		},
		{
			name: "off, with nothing else",
			seed: `{"hooks":{"internal":{"enabled":false}}}`,
			want: false,
		},
		{
			name:   "on already, with an entry of their own",
			seed:   `{"hooks":{"internal":{"enabled":true,"entries":{"theirs":{"command":"x"}}}}}`,
			want:   true,
			theirs: true,
		},
		{
			name: "no switch at all — deja's to remove",
			seed: `{"hooks":{"internal":{"entries":{}}}}`,
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.seed+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := setOpenClawHookEnabled(true); err != nil {
				t.Fatal(err)
			}
			if _, err := setOpenClawHookEnabled(false); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatalf("%v: %s", err, b)
			}
			hooks, _ := root["hooks"].(map[string]any)
			internal, _ := hooks["internal"].(map[string]any)
			got, present := internal["enabled"]
			if tc.want == nil {
				if present {
					t.Errorf("deja left a switch it had added: %s", b)
				}
			} else if got != tc.want {
				t.Errorf("enabled = %v, want %v — the reader's setting changed:\n%s", got, tc.want, b)
			}
			// And their own entry is still there.
			if tc.theirs {
				entries, _ := internal["entries"].(map[string]any)
				if _, ok := entries["theirs"]; !ok {
					t.Errorf("uninstall took the reader's hook with it:\n%s", b)
				}
			}
			// Never ours.
			if entries, _ := internal["entries"].(map[string]any); entries != nil {
				if _, ok := entries[openclawHookName]; ok {
					t.Errorf("deja's own entry survived uninstall:\n%s", b)
				}
			}
		})
	}
}
