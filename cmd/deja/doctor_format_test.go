package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index written by an older format is unreadable to this binary — the hook
// paths refuse it and ask for a rebuild, which is why memory goes quiet after
// an upgrade. doctor called that "up to date" (#877).
func TestDoctorNamesAnIndexFromAnOlderFormat(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A current index says nothing about its format.
	var out bytes.Buffer
	doctorIndex(&out, doctorComponent{State: "ok", Path: dir}, dir)
	if got := out.String(); strings.Contains(got, "older deja") {
		t.Errorf("a current index was called old:\n%s", got)
	}

	// An index this build cannot read.
	saved := indexOlderFormat
	indexOlderFormat = func(string) bool { return true }
	t.Cleanup(func() { indexOlderFormat = saved })
	out.Reset()
	doctorIndex(&out, doctorComponent{State: "ok", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "written by an older deja") {
		t.Errorf("doctor does not mention the format:\n%s", got)
	}
	if !strings.Contains(got, "deja index") {
		t.Errorf("doctor does not say what to do:\n%s", got)
	}
}
