package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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

	// Some files readable, one directory not: the store is only partly
	// readable, which is quieter than losing it all — sessions disappear from
	// recall while everything else looks whole (#816).
	readable := filepath.Join(root, "seen.jsonl")
	if err := os.WriteFile(readable, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	partial, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{root}, files: []string{readable}, parse: nil})
	if partial.State != "denied" || !partial.Partial {
		t.Fatalf("state = %q partial = %v, want denied and partial", partial.State, partial.Partial)
	}
	var partialOut strings.Builder
	printDoctorStoreWarnings(&partialOut, []doctorStore{partial})
	if !strings.Contains(partialOut.String(), "only partly readable") {
		t.Errorf("the partial case does not say so:\n%s", partialOut.String())
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

// The warning under the row has said "only partly readable" since #816, and
// `doctor --json` carries `partial` — the row itself folded both states into
// "cannot be read", over a store that search was still answering from (#1034).
func TestDoctorRowSeparatesAPartlyReadableStore(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the widget alignment"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var out strings.Builder
	doctorHarnesses(&out, dir)
	row := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "claude ") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no claude row:\n%s", out.String())
	}
	if !strings.Contains(row, "partly unreadable") {
		t.Errorf("the row does not say the store is only partly unreadable: %q", row)
	}
	if strings.Contains(row, "cannot be read") {
		t.Errorf("the row still calls a partly readable store unreadable: %q", row)
	}
}
