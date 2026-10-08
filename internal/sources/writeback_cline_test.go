package sources

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clineWriteBackStore(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", data)
	t.Setenv("DEJA_CLINE_ROOT", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWriteBackClineTakesTheWorkspaceFromTheHookLog(t *testing.T) {
	data := clineWriteBackStore(t)
	const id = "1791454692202_zb19s"
	if err := os.MkdirAll(filepath.Join(data, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	logLine := `{"sessionContext":{"rootSessionId":"` + id + `"},"workspaceRoots":["/work/api"],"workspaceInfo":{"rootPath":"/work/api"},"hookName":"agent_start"}` + "\n"
	if err := os.WriteFile(filepath.Join(data, "logs", "hooks.jsonl"), []byte(logLine), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "sessions", id, id+".messages.json")
	s := writeBackSample("cline", id, path)
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil || len(sidecars) != 1 || len(appends) != 0 {
		t.Fatalf("extras: %d sidecars, %d appends, %v", len(sidecars), len(appends), err)
	}
	if want := filepath.Join(data, "sessions", id, id+".json"); sidecars[0].Path != want {
		t.Errorf("manifest at %s, want %s", sidecars[0].Path, want)
	}
	var man map[string]any
	if err := json.Unmarshal(sidecars[0].Data, &man); err != nil {
		t.Fatal(err)
	}
	// Cline 3.0.69 refuses a manifest with any of these empty.
	for _, k := range []string{"cwd", "workspace_root", "provider", "model", "session_id", "status", "source", "started_at"} {
		if v, _ := man[k].(string); v == "" {
			t.Errorf("manifest %s is empty", k)
		}
	}
	if man["cwd"] != filepath.FromSlash("/work/api") && man["cwd"] != "/work/api" {
		t.Errorf("cwd %v, want the hook log's workspace", man["cwd"])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecars[0].Path, sidecars[0].Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %d sessions, %v", len(ss), err)
	}
	assertTurns(t, ss[0].Messages)
	if ss[0].Project != projectName("/work/api") {
		t.Errorf("project %q, want the workspace's", ss[0].Project)
	}
}

func TestWriteBackClineRefusesWithNoWorkspace(t *testing.T) {
	data := clineWriteBackStore(t)
	const id = "1791454692202_zb19s"
	s := writeBackSample("cline", id, filepath.Join(data, "sessions", id, id+".messages.json"))
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = WriteBackExtras(s, f)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("got %v, want a refusal naming the missing directory", err)
	}
}

func TestWriteBackClineRefusesAnExtensionTask(t *testing.T) {
	clineWriteBackStore(t)
	s := writeBackSample("cline", "cline-task-1", filepath.Join(t.TempDir(), "tasks", "1", "api_conversation_history.json"))
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "taskHistory.json") {
		t.Fatalf("got %v, want a refusal naming taskHistory.json", err)
	}
}
