package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Goose writes its session header first and fills the description in later,
// once it has something to call the session. The reader takes the description
// from that header line, and an incremental pass starts past it — so what a
// listing shows depends on which build path touched the session last, the
// disagreement #R11 named for friction signatures (#2556).
func TestGooseDescriptionArrivesAfterTheFirstPass(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "goose")
	dir := filepath.Join(tmp, "index.db")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	t.Setenv("DEJA_INDEX_DIR", dir)
	setHome(t, filepath.Join(tmp, "home"))
	path := filepath.Join(sessions, "g1.jsonl")

	header := func(desc string) string {
		return `{"id":"g1","working_dir":"/w/app","description":"` + desc +
			`","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}`
	}
	turn := func(role, text, at string) string {
		return `{"role":"` + role + `","created":"` + at + `","content":[{"type":"text","text":"` + text + `"}]}`
	}
	write := func(lines ...string) {
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// First pass: goose has not named the session yet.
	write(header(""), turn("user", "the pool starves under load", "2026-08-01T10:00:00Z"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["goose:g1"].Title; got != "the pool starves under load" {
		t.Fatalf("precondition: first pass titled it %q", got)
	}

	// Goose names the session and the work goes on.
	write(header("pgbouncer pool sizing"),
		turn("user", "the pool starves under load", "2026-08-01T10:00:00Z"),
		turn("assistant", "raised pool_size to 40", "2026-08-01T10:05:00Z"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	incremental := m.Sessions["goose:g1"].Title

	// What the same store says after a rebuild, which reads the header again.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := m.Sessions["goose:g1"].Title
	if incremental != rebuilt {
		t.Errorf("the listing depends on which build path ran: incremental %q, rebuilt %q", incremental, rebuilt)
	}
	if rebuilt != "pgbouncer pool sizing" {
		t.Errorf("the name goose settled on is not what deja shows: %q", rebuilt)
	}
}
