package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBackPiFamilyReadsBack(t *testing.T) {
	at := time.Date(2026, 10, 8, 10, 13, 56, 0, time.UTC)
	for _, tc := range []struct {
		harness, env string
		parse        func(string) ([]model.Session, error)
	}{
		{"pi", "DEJA_PI_ROOT", ParsePiFile},
		{"omp", "DEJA_OMP_ROOT", ParseOmpFile},
		{"gjc", "DEJA_GJC_ROOT", ParseGjcFile},
		{"kimchi", "DEJA_KIMCHI_ROOT", ParseKimchiFile},
		{"senpi", "DEJA_SENPI_ROOT", ParseSenpiFile},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(tc.env, root)
			project := filepath.Join(root, "--work-app--")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.harness == "gjc" {
				scope := `{"schemaVersion":1,"canonicalPath":"/work/app"}`
				if err := os.WriteFile(filepath.Join(project, ".gjc-managed-session-scope.v2.json"), []byte(scope), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			id := "01a11b01-5c09-747e-ae33-289cbad33c6c"
			s := model.Session{
				ID: id, Harness: tc.harness, Project: "app", Started: at,
				Path: filepath.Join(project, "2026-10-08T10-13-56-362Z_"+id+".jsonl"),
				Messages: []model.Message{
					{Role: "user", Text: "the pool exhausts on deploy", Time: at},
					{Role: "command", Text: "$ go test ./...", Time: at.Add(time.Second)},
					{Role: "tool-output", Text: "FAIL pool", Time: at.Add(2 * time.Second)},
					{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
					{Role: "user", Text: "done?", Time: at.Add(4 * time.Second)},
				},
			}
			f, err := RenderWriteBack(s)
			if err != nil {
				t.Fatal(err)
			}
			if f.Path != s.Path || f.Root != root || f.Turns != 3 {
				t.Fatalf("file = %q in %q with %d turns", f.Path, f.Root, f.Turns)
			}
			if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
				t.Fatal(err)
			}
			ss, err := tc.parse(f.Path)
			if err != nil || len(ss) != 1 {
				t.Fatalf("parse: %v, %d sessions", err, len(ss))
			}
			got := ss[0]
			if got.ID != id {
				t.Errorf("id = %q", got.ID)
			}
			var turns []string
			for _, m := range got.Messages {
				turns = append(turns, m.Role+": "+m.Text)
			}
			want := "user: the pool exhausts on deploy|assistant: raise MaxOpenConns to 50|user: done?"
			if strings.Join(turns, "|") != want {
				t.Errorf("turns = %q", strings.Join(turns, "|"))
			}
			if tc.harness == "gjc" && !strings.Contains(string(f.Data), `"cwd":"/work/app"`) {
				t.Errorf("gjc header lost the scope file's directory: %s", f.Data)
			}
		})
	}
}

func TestWriteBackGjcRefusesWithoutItsScopeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GJC_ROOT", root)
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "01a11b05-9d97-7010-a005-4a6f511b00fb", Harness: "gjc",
		Path:     filepath.Join(root, "v2-abc", "2026-10-08T10-18-35-287Z_01a11b05-9d97-7010-a005-4a6f511b00fb.jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi", Time: at}},
	}
	_, err := RenderWriteBack(s)
	r, ok := err.(*WriteBackRefusal)
	if !ok || !strings.Contains(r.Reason, "scope file") {
		t.Fatalf("err = %v", err)
	}
}
