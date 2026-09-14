package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// floorStore builds a store and stamps its manifest back below the redaction
// floor — which is the state an upgrade finds on disk.
func floorStore(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","sessionId":"f0","cwd":"/work/app","timestamp":%q,`+
		`"message":{"role":"user","content":"the deploy failed with zxqsecretvalue in the runbook"}}`+"\n",
		"2026-09-01T10:00:00Z")
	if err := os.WriteFile(filepath.Join(proj, "f0.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = redactionFloor - 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	return dir
}

// A store below the redaction floor holds text this build would not write, and
// the ranked surfaces rebuild before answering from it. The by-identity loader
// serves whole sessions to `deja show` and to the MCP tools with no ranking to
// hang that on, and it served them as stored (#3617).
func TestAByIdentityReadRefusesAStoreBelowTheFloor(t *testing.T) {
	dir := floorStore(t)
	if _, _, err := FindByIdentity(dir, "claude", "f0"); !errors.Is(err, ErrStoreWithheld) {
		t.Fatalf("a withheld store was served: %v", err)
	}
	// And once the sources are read again, the same call answers.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := FindByIdentity(dir, "claude", "f0")
	if err != nil || !ok || len(s.Messages) == 0 {
		t.Fatalf("a current store did not serve the session: %v %v %d", err, ok, len(s.Messages))
	}
}

// And the reason the by-prefix path is safe, pinned: `deja show <prefix>` runs
// an ordinary pass and then loads, and what makes that safe is manifestFresh
// refusing a store whose version is not this build's. A freshness rule that
// compared only the files — the natural simplification, since the sources have
// not changed — would put the old records back in front of a reader.
func TestAPassDoesNotCallAWithheldStoreFresh(t *testing.T) {
	dir := floorStore(t)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("the pass left the store at version %d, want %d", m.Version, version)
	}
}
