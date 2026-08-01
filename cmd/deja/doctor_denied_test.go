package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A store deja is not allowed to read was reported as a harness that changed
// its format — or, when only a subdirectory was locked, as a store with no
// history at all (#802).
func TestDoctorSeparatesADeniedStoreFromAChangedFormat(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "sessions")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// No files reached the collector, exactly as the walk leaves it.
	store, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{root}, files: nil, parse: nil})
	if store.State != "denied" {
		t.Fatalf("state = %q, want denied", store.State)
	}
	if store.Denied != locked {
		t.Errorf("denied path = %q, want %q", store.Denied, locked)
	}

	var out strings.Builder
	printDoctorStoreWarnings(&out, []doctorStore{store})
	got := out.String()
	if !strings.Contains(got, "permission denied on "+locked) {
		t.Errorf("warning does not name the directory:\n%s", got)
	}
	if strings.Contains(got, "format may have changed") {
		t.Errorf("a permission problem is still reported as a format change:\n%s", got)
	}

	// A readable store with nothing in it stays quiet: "no history" is not a
	// problem to warn about.
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if store, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{empty}, files: nil, parse: nil}); store.State == "denied" {
		t.Errorf("an empty readable store was reported as denied")
	}

	// And a store that reads fine but fails to parse is still a format report.
	file := filepath.Join(empty, "s.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := doctorStoreCheck{
		name: "codex", paths: []string{empty}, files: []string{file},
		parse: func(string) ([]model.Session, error) { return nil, errUnreadableStore },
	}
	if store, _ := inspectDoctorStore(broken); store.State != "unreadable" {
		t.Errorf("state = %q, want unreadable", store.State)
	}
}
