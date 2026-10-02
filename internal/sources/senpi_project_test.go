package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Senpi names a session folder after its cwd with every / turned into -, so
// .../sg/my-app and .../sg/my/app share one folder name. The header records
// the real cwd, and that is what names the project: decoding the folder picked
// my/app whenever that directory existed (#4427). pi names its folders the
// same way.
func TestSenpiProjectComesFromTheHeaderCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_SENPI_ROOT", filepath.Join(root, "sessions"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	t.Setenv("DEJA_KIMCHI_ROOT", filepath.Join(root, "kimchi-sessions"))
	cwd := filepath.Join(root, "sg", "my-app")
	for _, d := range []string{cwd, filepath.Join(root, "sg", "my", "app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	quoted, _ := json.Marshal(cwd)
	head := `{"type":"session","version":3,"id":"b1000000-0000-7000-8000-000000000001","timestamp":"2026-10-01T23:00:00Z","cwd":` + string(quoted) + `}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-10-01T23:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-10-01T23:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"capped at five"}]}}` + "\n"
	for _, c := range []struct {
		root  string
		parse func(string, int64) ([]model.Session, error)
	}{
		{filepath.Join(root, "sessions"), ParseSenpiFileFromOffset},
		{filepath.Join(root, "pi-sessions"), ParsePiFileFromOffset},
		// Kimchi took the header's cwd already, then encoded it and decoded
		// it again, which is the same guess.
		{filepath.Join(root, "kimchi-sessions"), ParseKimchiFileFromOffset},
	} {
		dir := filepath.Join(c.root, "--"+pathToProjectKey(cwd)[1:]+"--")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "2026-10-01T23-00-00-000Z_b1000000.jsonl")
		if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, off := range []int64{0, int64(len(head) + len(first))} {
			ss, err := c.parse(path, off)
			if err != nil || len(ss) != 1 {
				t.Fatalf("%s at %d: %v, %d sessions", path, off, err, len(ss))
			}
			if ss[0].Project != "sg/my-app" {
				t.Errorf("%s read at %d: project = %q, want sg/my-app from the header", filepath.Base(c.root), off, ss[0].Project)
			}
		}
	}
}
