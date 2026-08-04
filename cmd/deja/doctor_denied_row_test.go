package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A directory deja cannot open loses its sessions from recall, and the row
// people read called it `found` — the same state `doctor --json` has reported
// as `denied` since #802/#816 (#993).
func TestDoctorRowSaysDeniedForAStoreItCannotOpen(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen")
	projects := filepath.Join(qwen, "projects", "p1")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "s.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", qwen)

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	if row := harnessRow(t, out.String(), "qwen"); !strings.Contains(row, "found") {
		t.Fatalf("a readable store was not reported as found: %q", row)
	}

	if err := os.Chmod(filepath.Join(qwen, "projects"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(qwen, "projects"), 0o755) })

	out.Reset()
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	row := harnessRow(t, out.String(), "qwen")
	if strings.Contains(row, "found") {
		t.Errorf("a store deja cannot open is still called found: %q", row)
	}
	if !strings.Contains(row, "denied") {
		t.Errorf("the row does not name the state: %q", row)
	}
	// The row stays short because the warning block, which is built from the
	// same inspection, names the path and what to do about it.
	var warn bytes.Buffer
	printDoctorStoreWarnings(&warn, collectDoctorReport(func() (string, bool) { return "", false }, filepath.Join(tmp, "index.db")).Stores)
	if !strings.Contains(warn.String(), "permission denied on") {
		t.Errorf("the warning that names the path is gone:\n%s", warn.String())
	}
}
