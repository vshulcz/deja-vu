package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `update` stages into `.deja-update-*` beside the binary and renames over it;
// a crash between the two strands a whole binary's worth of bytes under a name
// deja itself wrote, and every later run walked past it (#1109).
func TestDoctorNoticesAStrandedUpdateStaging(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path here")
	}
	dir := filepath.Dir(exe)
	staged := filepath.Join(dir, ".deja-update-audit-probe")
	if err := os.WriteFile(staged, []byte("half a binary"), 0o644); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(staged) })

	n, got := strandedUpdateStagings()
	if n < 1 {
		t.Errorf("a stranded staging file was not counted (dir %s)", got)
	}
	if got != dir {
		t.Errorf("counted in %s, want %s", got, dir)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	// Nothing left: the line must not appear on a tidy machine.
	if n, _ := strandedUpdateStagings(); n != 0 {
		t.Errorf("a tidy directory reported %d leftovers", n)
	}
}
