package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettledWallsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(dir, "policy.json"))
	if got := SettledPath(); got != filepath.Join(dir, "settled.json") {
		t.Fatalf("settled file at %s, want beside the policy file", got)
	}
	if LoadSettled() != nil {
		t.Fatal("no file settled something")
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := SettleWall("3F9A01C2", at); err != nil {
		t.Fatal(err)
	}
	s := LoadSettled()
	if !s.Hides("3f9a01c2", at.Add(-time.Hour)) || !s.Hides("3f9a01c2", at) {
		t.Error("a settle does not hide a wall whose sessions all came before it")
	}
	if s.Hides("3f9a01c2", at.Add(time.Hour)) {
		t.Error("a session started after the settle did not bring the wall back")
	}
	if s.Hides("00000000", at.Add(-time.Hour)) {
		t.Error("a wall nobody settled is hidden")
	}
	if had, err := UnsettleWall("3f9a01c2"); err != nil || !had {
		t.Fatalf("unsettle: %v %v", had, err)
	}
	if LoadSettled().Hides("3f9a01c2", at.Add(-time.Hour)) {
		t.Error("an unsettled wall is still hidden")
	}
	if had, err := UnsettleWall("3f9a01c2"); err != nil || had {
		t.Errorf("unsettling twice: %v %v", had, err)
	}
}

func TestAMalformedSettledFileSettlesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(dir, "policy.json"))
	if err := os.WriteFile(filepath.Join(dir, "settled.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LoadSettled() != nil {
		t.Error("a malformed file settled something")
	}
}

func TestSettleSaysWhenItCannotWrite(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "DEJA_POLICY_FILE"} {
		t.Setenv(k, "")
	}
	if SettledPath() != "" {
		t.Fatalf("a machine with no home put settles at %q", SettledPath())
	}
	if err := SettleWall("3f9a01c2", time.Now()); err == nil {
		t.Error("a settle with nowhere to go reported success")
	}

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(blocker, "deja", "policy.json"))
	if err := SettleWall("3f9a01c2", time.Now()); err == nil {
		t.Error("a settle under a regular file reported success")
	}

	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "policy.json"))
	if err := SettleWall("3f9a01c2", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("a time JSON cannot hold was written")
	}
}
