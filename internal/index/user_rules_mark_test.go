package index

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func userRulesStore(t *testing.T) (dir string, write func(project, id, entrypoint string, lines ...string)) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write = func(project, id, entrypoint string, lines ...string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for i, l := range lines {
			at := "2026-10-0" + string(rune('1'+len(id)%5)) + "T10:00:0" + string(rune('0'+i)) + "Z"
			if _, err := f.WriteString(`{"type":"user","sessionId":"` + id + `","entrypoint":"` + entrypoint + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"user","content":"` + l + `"}}` + "\n" +
				`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return filepath.Join(home, "index.db"), write
}

func marked(t *testing.T, dir, id string, scope []string) model.Session {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions["claude:"+id]
	if !ok {
		t.Fatalf("no row for %s", id)
	}
	s := sessionFromMeta(row)
	MarkUserRules(dir, &s, scope)
	return s
}

// A rule is marked only for its own project, only from an interactive
// session, and not once a later session took it back.
func TestMarkUserRulesKeepsToTheProjectAndTheLatestWord(t *testing.T) {
	dir, write := userRulesStore(t)
	write("app", "s1", "cli",
		"always run ./scripts/check.sh before you call it done",
		"after you edit docs, run ./scripts/lintdocs.sh to check the links")
	write("app", "s22", "cli", "don't run ./scripts/lintdocs.sh anymore, it is broken")
	write("app", "s333", "sdk-cli", "always run ./scripts/verify.sh before you call it done")
	write("other", "s4444", "cli", "always run ./scripts/spellcheck.sh before you call it done")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	app := []string{"w/app"}
	s1 := marked(t, dir, "s1", app)
	if !slices.Equal(s1.UserRules, []string{"always run ./scripts/check.sh before you call it done"}) {
		t.Errorf("s1 rules = %q", s1.UserRules)
	}
	if !slices.Contains(s1.TakenBack, "lintdocs.sh") {
		t.Errorf("s1 taken back = %q", s1.TakenBack)
	}
	if s := marked(t, dir, "s333", app); len(s.UserRules) != 0 {
		t.Errorf("a print-mode session's turn was marked: %q", s.UserRules)
	}
	if s := marked(t, dir, "s4444", app); len(s.UserRules) != 0 {
		t.Errorf("another project's rule was marked here: %q", s.UserRules)
	}
	if s := marked(t, dir, "s4444", []string{"w/other"}); len(s.UserRules) != 1 {
		t.Errorf("a project's own rule was not marked there: %q", s.UserRules)
	}
	if s := marked(t, dir, "s1", nil); len(s.UserRules) != 0 {
		t.Errorf("a reader with no project got marks: %q", s.UserRules)
	}
	imported := s1
	imported.From = "laptop"
	MarkUserRules(dir, &imported, app)
	if len(imported.UserRules) != 0 {
		t.Errorf("a session from another machine was marked: %q", imported.UserRules)
	}

	// The same session, appended to: a later turn takes its rule back.
	write("app", "s1", "cli", "stop running ./scripts/check.sh, CI does it now")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if s := marked(t, dir, "s1", app); len(s.UserRules) != 0 {
		t.Errorf("a rule taken back in an appended turn is still marked: %q", s.UserRules)
	}
}
