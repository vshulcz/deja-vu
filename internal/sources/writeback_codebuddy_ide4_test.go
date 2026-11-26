package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cb4WriteBackEnv pins the CLI and 4.x roots and returns their paths plus a
// synthetic 4.x conversation index. The index is present, so no write-back
// runs restore a deleted transcript here.
func cb4WriteBackEnv(t *testing.T) (cli, index, id string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", filepath.Join(home, "legacy-data"))
	t.Setenv("DEJA_CODEBUDDY_ROOTS", filepath.Join(home, "cli-projects"))
	t.Setenv("CODEBUDDY_CONFIG_DIR", filepath.Join(home, "cli-config"))
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	id = "abcdef0123456789abcdef0123456789"
	index = filepath.Join(home, "legacy-data", "u1", "CodeBuddyIDE", "u1", "history",
		"0123456789abcdef0123456789abcdef", id, "index.json")
	if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "cli-projects"), index, id
}

// A 4.x conversation's index.json is not a deleted CodeBuddy Code
// transcript. Writing a .jsonl back for one would leave a file `codebuddy -r`
// still cannot open, so the refusal names the store and the conversion.
func TestWriteBackCodeBuddyIDE4RefusesClearly(t *testing.T) {
	_, index, id := cb4WriteBackEnv(t)
	s := writeBackSample("codebuddy", id, index)
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	for _, want := range []string{"CodeBuddy IDE 4.x", "CodeBuddy IDE 5.x converts it"} {
		if !strings.Contains(r.Reason, want) {
			t.Errorf("reason does not say %q: %q", want, r.Reason)
		}
	}
	if strings.Contains(r.Reason, "index has no <id>.jsonl path") {
		t.Errorf("reason gives the CLI transcript excuse for a 4.x conversation: %q", r.Reason)
	}
}

// Once IDE 5.x converted the conversation, a CodeBuddy Code transcript is
// already on disk. Write-back restores a deleted transcript; it must not
// create a second copy or rewrite the IDE's index.
func TestWriteBackCodeBuddyIDE4ConvertedRefuses(t *testing.T) {
	cli, index, id := cb4WriteBackEnv(t)
	marker := filepath.Join(cli, "folder", id, ".codebuddy-ide-4-conversion.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{"conversationId":"`+id+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(cli, "folder", id+".jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyIDE4ConvertedTranscript(index); got != transcript {
		t.Fatalf("converted transcript = %q, want %q", got, transcript)
	}
	_, err := RenderWriteBack(writeBackSample("codebuddy", id, index))
	var r *WriteBackRefusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	for _, want := range []string{"already has a CodeBuddy Code transcript", "write-back only restores a deleted one"} {
		if !strings.Contains(r.Reason, want) {
			t.Errorf("reason does not say %q: %q", want, r.Reason)
		}
	}
}
