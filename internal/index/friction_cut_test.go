package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A wall the manifest remembers but the store can no longer show must not
// spend a slot in the answer. `meta.Hit` is a union that never forgets — the
// comment on extendDerived says so on purpose — while the text is recovered by
// reading the records back, so a transcript that was rewritten (a compact, a
// harness that rewrites its rows) leaves hashes nothing yields. TopFriction cut
// to n first and dropped those after, so `FindFriction`, which asks for one,
// came back empty with a second wall standing right behind it (#2544).
func TestAWallWithNoTextLeftDoesNotSpendTheSlot(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-tmp-friction")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two walls in every session: the loud one is hit by four sessions, the
	// quiet one by three, so the loud one ranks first.
	write := func(i int, loud bool) {
		sid := fmt.Sprintf("fr%02d", i)
		day := fmt.Sprintf("2026-01-%02d", i+1)
		out := func(at, text string) string {
			return `{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + day + `T` + at +
				`Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + text + `"}]}}`
		}
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + day +
				`T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
			out("03:06:05", "(eval):1: command not found: quokkactl"),
		}
		if loud {
			lines = append(lines, out("03:05:05", "undefined: snorblefunc in vendor/blarg/api.go"))
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		write(i, true)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: the loud wall is what the one-wall surface shows.
	if f, ok := FindFriction(dir, nil); !ok || f.Text != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Fatalf("before the rewrite: ok=%v text=%q", ok, f.Text)
	}

	// A hash the manifest holds and the records do not: on this machine's index
	// 25 sessions carry one, and the two clusters they build are the two the
	// listing loses. How they got there is a separate question — `meta.Hit` is
	// a union that never forgets, and transcripts are rewritten under it — but
	// the state is real, and the ranking has to survive it.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	ghost := frictionHash("undefined: quenchfunc in vendor/blarg/api.go")
	for key, meta := range m.Sessions {
		meta.Hit = append(meta.Hit, ghost)
		m.Sessions[key] = meta
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	f, ok := FindFriction(dir, nil)
	if !ok {
		t.Fatalf("the wall that is still in the store was not reported at all")
	}
	if f.Text != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Fatalf("text = %q", f.Text)
	}
	// And asking for two comes back with both real walls rather than one.
	if got := TopFriction(dir, 2, nil); len(got) != 2 {
		t.Errorf("TopFriction(2) returned %d walls, want the two the store can show", len(got))
	}
}
