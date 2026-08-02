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
	saved := indexFormatDirection
	indexFormatDirection = func(string) int { return -1 }
	t.Cleanup(func() { indexFormatDirection = saved })
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

// The direction matters: an index from a newer deja means the binary was
// rolled back, and calling it old sends that reader the wrong way (#890).
func TestDoctorTellsAnOldIndexFromANewOne(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	saved := indexFormatDirection
	t.Cleanup(func() { indexFormatDirection = saved })

	line := func(direction int) string {
		indexFormatDirection = func(string) int { return direction }
		var out bytes.Buffer
		doctorIndex(&out, doctorComponent{State: "ok", Path: dir}, dir)
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.Contains(l, "format ") {
				return l
			}
		}
		return ""
	}

	if got := line(-1); !strings.Contains(got, "written by an older deja") {
		t.Errorf("older index: %q", got)
	}
	got := line(1)
	if !strings.Contains(got, "written by a newer deja") {
		t.Errorf("newer index: %q", got)
	}
	if strings.Contains(got, "older") {
		t.Errorf("a rolled-back binary was told its index is old: %q", got)
	}
	if got := line(0); got != "" {
		t.Errorf("a matching format still printed: %q", got)
	}
}
