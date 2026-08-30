package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One sentence covered four breakages, and named the wrong file in two of them
// (#2695). Each store here is broken exactly one way.
func TestDamageReasonNamesWhatBroke(t *testing.T) {
	build := func(t *testing.T) string {
		t.Helper()
		tmp := t.TempDir()
		setHome(t, filepath.Join(tmp, "home"))
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
		root := filepath.Join(tmp, "claude", "proj-p")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z",` +
			`"message":{"role":"user","content":"terraform apply --zonkoshard 7"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	healthy := build(t)
	if got := DamageReason(healthy); got != "" {
		t.Fatalf("a healthy store was called damaged: %q", got)
	}
	if Damaged(healthy) {
		t.Fatal("Damaged disagrees with DamageReason on a healthy store")
	}

	cases := []struct {
		name  string
		spoil func(t *testing.T, dir string)
		want  string
	}{
		{"a manifest that will not decode", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "manifest"},
		{"a session table that will not decode", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "sessions.gob"), []byte("not a gob"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "session table"},
		{"a record log cut short", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "records.bin"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "record log"},
		{"postings that went missing", func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
				t.Fatal(err)
			}
		}, "the postings directory is not there"},
		{"a postings directory left empty", func(t *testing.T, dir string) {
			// What `rm buckets/*.bin` and a copy that skipped the contents
			// leave behind, which is not the same as losing the directory.
			buckets := filepath.Join(dir, "buckets")
			entries, err := os.ReadDir(buckets)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if err := os.Remove(filepath.Join(buckets, e.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}, "the postings directory is empty"},
		{"a record log with a tail the manifest never committed", func(t *testing.T, dir string) {
			f, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte("a record from a build that crashed")); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}, "longer than the manifest committed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := build(t)
			c.spoil(t, dir)
			got := DamageReason(dir)
			if !strings.Contains(got, c.want) {
				t.Errorf("reason %q does not say %q", got, c.want)
			}
			if !Damaged(dir) {
				t.Errorf("Damaged says the store is fine while the reason is %q", got)
			}
		})
	}

	// A store that was never built is not a damaged one.
	if got := DamageReason(t.TempDir()); got != "" {
		t.Errorf("an empty directory was called damaged: %q", got)
	}
}
