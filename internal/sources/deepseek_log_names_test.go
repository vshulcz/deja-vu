package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// dsh moved its session log to session.v3.jsonl(.zstd), and a session begun
// before that can keep a header-only file under the old name beside the new
// log. Discovery and the kind the incremental index matches on must both accept
// every name: a store of v3 logs otherwise reads as empty, or indexes on a
// rebuild and then never again.
func TestDeepSeekAcceptsEveryLogName(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	var kind FileKind
	for _, h := range Registry() {
		if h.Name == "deepseek" {
			kind = h.Kinds[0]
		}
	}
	if kind.Match == nil {
		t.Fatal("no deepseek kind in the registry")
	}

	workspace := filepath.Join(root, "--work-pgbouncer-lab--")
	cases := []struct {
		dir, name string
		log       bool
	}{
		{"session-a", "session.jsonl", true},
		{"session-b", "session.jsonl.zstd", true},
		{"session-c", "session.v3.jsonl", true},
		{"session-d", "session.v3.jsonl.zstd", true},
		{"session-d", "session.lock", false},
		{"session-e", "session.v3.jsonl.bak", false},
	}
	for _, c := range cases {
		dir := filepath.Join(workspace, c.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found := map[string]bool{}
	for _, f := range DeepSeekSessionFiles() {
		found[filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f))] = true
	}
	for _, c := range cases {
		path := filepath.Join(workspace, c.dir, c.name)
		if got := found[filepath.Join(c.dir, c.name)]; got != c.log {
			t.Errorf("DeepSeekSessionFiles found %s = %v, want %v", c.name, got, c.log)
		}
		if got := kind.Match(path); got != c.log {
			t.Errorf("deepseek kind matches %s = %v, want %v", c.name, got, c.log)
		}
	}
}

func TestParseDeepSeekFileReadsAV3Log(t *testing.T) {
	root := t.TempDir()
	raw := writeDeepSeekSession(t, root, "v3", deepSeekLog, false)
	v3 := filepath.Join(filepath.Dir(raw), "session.v3.jsonl")
	if err := os.Rename(raw, v3); err != nil {
		t.Fatal(err)
	}
	paths := []string{v3}
	if ZstdAvailable() {
		framed := v3 + ".zstd"
		if err := exec.Command("zstd", "-q", "-k", "-o", framed, v3).Run(); err != nil {
			t.Fatalf("zstd: %v", err)
		}
		paths = append(paths, framed)
	}
	for _, path := range paths {
		ss, err := ParseDeepSeekFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || len(ss[0].Messages) != 5 {
			t.Fatalf("%s read as %+v", filepath.Base(path), ss)
		}
		if ss[0].ID != "eaf5c9ac-0e47-4d2f-b982-8bae306062d1" || ss[0].Project != "work/pgbouncer-lab" {
			t.Errorf("%s: id %q, project %q", filepath.Base(path), ss[0].ID, ss[0].Project)
		}
	}
}
