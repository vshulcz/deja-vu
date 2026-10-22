package index

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

const (
	dshV0Header = `{"type":"session","version":0,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
`
	dshV4Header = `{"type":"session","version":4,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
`
	dshOldTurn = `{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
`
	dshNewTurn = `{"type":"user/message","seq":2,"time":1790593322600,"data":{"content":[{"type":"text","text":"v4needle raise it to 60"}],"source":{"kind":"user"},"role":"user"}}
`
)

// writeDshLog writes body raw, or as dsh frames it: one zstd frame per line.
func writeDshLog(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out := []byte(body)
	if strings.HasSuffix(path, ".zstd") {
		var b bytes.Buffer
		for _, line := range strings.SplitAfter(body, "\n") {
			if line == "" {
				continue
			}
			cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
			cmd.Stdin = strings.NewReader(line)
			frame, err := cmd.Output()
			if err != nil {
				t.Fatalf("zstd: %v", err)
			}
			b.Write(frame)
		}
		out = b.Bytes()
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dsh 2.0.15 migrates a session into session.v4.jsonl beside its old log and
// appends there from then on. The incremental pass has to move the session onto
// the new file: keeping the old one back as a deleted transcript held its turns
// twice and left the session on the frozen path (#4600). Cherry Studio runs dsh
// over its own store, which takes the same path.
func TestDeepSeekV4LogTakesOverFromTheLegacyLog(t *testing.T) {
	for _, tc := range []struct{ name, harness, ext string }{
		{"deepseek", "deepseek", ""},
		{"deepseek-zstd", "deepseek", ".zstd"},
		{"cherrystudio", "cherrystudio", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ext != "" {
				if _, err := exec.LookPath("zstd"); err != nil {
					t.Skip("zstd CLI not available")
				}
			}
			tmp := t.TempDir()
			home := filepath.Join(tmp, "home")
			setHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
			t.Setenv("DEJA_DEEPSEEK_ROOT", filepath.Join(tmp, "dsh", "sessions"))
			root := filepath.Join(tmp, "dsh", "sessions")
			if tc.harness == "cherrystudio" {
				root = filepath.Join(cherryStudioDir(home), "Data", "Agents", ".dsh", "sessions")
			}
			dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f")
			writeDshLog(t, filepath.Join(dir, "session.jsonl"+tc.ext), dshV0Header+dshOldTurn)
			idx := filepath.Join(tmp, "index.db")
			if err := Ensure(idx, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if n := len(sessionsOf(t, idx, tc.harness)); n != 1 {
				t.Fatalf("first build: %d %s sessions, want 1", n, tc.harness)
			}

			v4 := filepath.Join(dir, "session.v4.jsonl"+tc.ext)
			writeDshLog(t, v4, dshV4Header+dshOldTurn+dshNewTurn)
			var progress bytes.Buffer
			if err := Ensure(idx, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(progress.String(), "no longer on disk") {
				t.Errorf("the migrated log was kept as a deleted transcript:\n%s", progress.String())
			}
			for _, q := range []string{"oldneedle", "v4needle"} {
				ss, err := Search(idx, search.Options{Query: q, All: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(ss) != 1 {
					t.Fatalf("%q is in %d sessions, want 1\n%s", q, len(ss), progress.String())
				}
				n := 0
				for _, m := range ss[0].Messages {
					if strings.Contains(m.Text, q) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%q is in %d messages, want 1\n%s", q, n, progress.String())
				}
			}
			metas := sessionsOf(t, idx, tc.harness)
			if len(metas) != 1 {
				t.Fatalf("%d %s sessions, want 1", len(metas), tc.harness)
			}
			for _, m := range metas {
				if m.Path != v4 {
					t.Errorf("session path = %s, want %s", m.Path, v4)
				}
			}
		})
	}
}

// cherryStudioDir is Cherry Studio's default data directory under home.
func cherryStudioDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "CherryStudio")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "CherryStudio")
	}
	return filepath.Join(home, ".config", "CherryStudio")
}
