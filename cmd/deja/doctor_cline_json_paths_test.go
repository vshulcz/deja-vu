package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The machine form kept its own path per harness, and cline's named only the
// CLI store: on a machine whose transcripts are all in the VS Code extension
// tree the report counted files under a directory it never named, and the
// directory it did name did not exist (#3399).
func TestDoctorJSONNamesTheClineRootTheFilesCameFrom(t *testing.T) {
	legacy := clineLegacyOnlyStore(t)

	check := doctorCheckNamed(t, "cline")
	if len(check.files) != 1 {
		t.Fatalf("the walk found %d files, want the one legacy task", len(check.files))
	}
	tasks := filepath.Join(legacy, "tasks")
	found := false
	for _, p := range check.paths {
		if p == tasks {
			found = true
		}
	}
	if !found {
		t.Errorf("paths = %v, want the legacy tasks tree the transcript came from", check.paths)
	}
}

// And because paths is what the permission walk covers, a legacy tree deja may
// not read was reported as a harness nobody had used.
func TestDoctorJSONSaysDeniedForALockedClineLegacyTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	legacy := clineLegacyOnlyStore(t)
	tasks := filepath.Join(legacy, "tasks")
	if err := os.Chmod(tasks, 0o000); err != nil {
		t.Skipf("cannot lock a directory here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
	if f, err := os.Open(tasks); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	store, _ := inspectDoctorStore(doctorCheckNamed(t, "cline"))
	if store.State != "denied" {
		t.Errorf("a locked legacy tree reports state %q, want denied", store.State)
	}
	if !strings.Contains(store.Denied, "tasks") {
		t.Errorf("doctor names %q, not the directory it could not read", store.Denied)
	}
}

// clineLegacyOnlyStore is the shape of a VS Code Cline user: one task under the
// extension's globalStorage and no CLI store at all.
func clineLegacyOnlyStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000000")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(tmp, "cline", "data", "sessions"))
	body := `[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"the last attempt is not counted"}]}]`
	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return legacy
}
