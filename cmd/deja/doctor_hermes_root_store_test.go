package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hermes 0.17 keeps one state.db at the root and has no profiles/ directory.
// doctor's text row looked only at profiles/ and said missing while the store
// was indexed and --json said ok (#4244).
func TestDoctorFindsTheHermesStoreAtTheRoot(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".hermes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.db"), []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "hermes" && strings.Contains(line, "store") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no hermes store row:\n%s", out)
	}
	if strings.Fields(row)[1] == "missing" || strings.Contains(row, "profiles") {
		t.Errorf("a root state.db reads as %q", strings.TrimSpace(row))
	}
}

// The control: a ~/.hermes holding only its config, with no state.db and no
// profiles/, has no store, and `deja install hermes-auto` writes there on a
// machine where hermes never ran. The row fell back to the directory and read
// found with nothing in it.
func TestDoctorDoesNotCallAHermesConfigDirAStore(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".hermes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --all keeps the row: a plain missing store folds otherwise (#4625).
	out, err := captureRun(t, "doctor", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "hermes" && strings.Contains(line, "store") {
			if f[1] != "missing" {
				t.Errorf("a config-only ~/.hermes reads as %q", strings.TrimSpace(line))
			}
			return
		}
	}
	t.Fatalf("no hermes store row:\n%s", out)
}
