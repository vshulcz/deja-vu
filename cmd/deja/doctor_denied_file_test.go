package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript deja is not allowed to open is a permission problem, not a
// harness that changed its format: the advice, and the path, have to match the
// fault (#1747).
func TestAnUnreadableFileIsDeniedAndNamed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "readable.jsonl"), []byte(rec("ok1", "flumberjack readable")), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(proj, "locked.jsonl")
	// Newer than the readable one, so it is the file doctor inspects.
	if err := os.WriteFile(locked, []byte(rec("locked1", "flumberjack locked")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a file here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "claude" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State != "denied" {
		t.Errorf("an unreadable transcript reports state %q, want denied", store.State)
	}
	if !strings.Contains(store.Denied, "locked.jsonl") {
		t.Errorf("doctor names %q, not the file it could not read", store.Denied)
	}
	if !store.Partial {
		t.Error("the rest of the store indexed, so this is a partly readable store")
	}
	_ = tmp
}

// "Partly readable" has to mean something read. A store whose files are all
// locked is not partly anything.
func TestAWhollyLockedStoreIsNotCalledPartlyReadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var locked []string
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		p := filepath.Join(proj, name)
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"flumberjack"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o000); err != nil {
			t.Skipf("cannot lock a file here: %v", err)
		}
		locked = append(locked, p)
	}
	t.Cleanup(func() {
		for _, p := range locked {
			_ = os.Chmod(p, 0o600)
		}
	})
	if f, err := os.Open(locked[0]); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "claude" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State != "denied" {
		t.Errorf("state %q, want denied", store.State)
	}
	if store.Partial {
		t.Error("every file is locked, so the store is not partly readable")
	}
}
