package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// Antigravity writes the file a step touched as a `file://` URI, and deja
// stripped the scheme textually — which leaves the percent-encoding where it
// was. That is the same shape that put `/c%3A/Users/me/my%20app/main.go` into a
// Copilot Chat files record until #3498, and it is invisible here: of 329
// `file://` URIs in this machine's Antigravity transcripts, none is encoded,
// because no path on it has a space (#3505).
func TestAntigravityDecodesAnEncodedFilePath(t *testing.T) {
	for name, tc := range map[string]struct{ line, want string }{
		"a space": {
			line: "File Path: file:///Users/me/my%20app/main.go",
			want: "/Users/me/my app/main.go",
		},
		"a drive letter and a space": {
			line: "File Path: file:///c%3A/Users/me/my%20app/main.go",
			want: "/c:/Users/me/my app/main.go",
		},
		"nothing to decode": {
			line: "File Path: file:///Users/me/app/main.go",
			want: "/Users/me/app/main.go",
		},
		// A per-cent that is not an escape stays as it is: there the text was
		// never encoded, and decoding it would invent a path.
		"a literal per-cent": {
			line: "File Path: file:///Users/me/app/report%.md",
			want: "/Users/me/app/report%.md",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", filepath.Join(root, "home"))
			ag := filepath.Join(root, "antigravity")
			t.Setenv("DEJA_ANTIGRAVITY_ROOT", ag)
			dir := filepath.Join(ag, "brain", "traj", ".system_generated", "logs")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "transcript.jsonl")
			// A CODE_ACTION step is the one that names a file, and its kind is
			// what decides that — not its source (#3331).
			rec := `{"source":"MODEL","type":"CODE_ACTION","created_at":"2026-01-02T03:04:05Z","content":` +
				jsonString("Edited file\n"+tc.line+"\nthe retry budget is three now") + "}\n"
			if err := os.WriteFile(path, []byte(rec), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseAntigravityFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range ss {
				for _, m := range s.Messages {
					if m.Role == RoleFiles {
						got = append(got, m.Text)
					}
				}
			}
			if len(got) == 0 {
				t.Fatalf("no files record at all: %#v", ss)
			}
			for _, p := range got {
				if p != tc.want {
					t.Errorf("files record has %q, want %q", p, tc.want)
				}
			}
		})
	}
}
