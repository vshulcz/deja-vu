package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index written by a layout this binary cannot read is unreadable to it —
// the hook paths refuse it and ask for a rebuild, which is why memory goes
// quiet after an upgrade. doctor called that "up to date" (#877).
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
	doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
	if got := out.String(); strings.Contains(got, "older deja") {
		t.Errorf("a current index was called old:\n%s", got)
	}

	// An index this build cannot read.
	saved := indexReadState
	indexReadState = func(string) index.ReadState { return index.ReadStateUnreadable }
	t.Cleanup(func() { indexReadState = saved })
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "written by an older deja") {
		t.Errorf("doctor does not mention the format:\n%s", got)
	}
	if !strings.Contains(got, "deja index") {
		t.Errorf("doctor does not say what to do:\n%s", got)
	}
}

// Four states, four sentences. The direction matters — an index from a newer
// deja means the binary was rolled back, and calling it old sends that reader
// the wrong way (#890) — and so does what the build may do with it: a store one
// content version behind reads and answers, and "cannot read it" told someone
// looking for why nothing is recalled that their index was gone (#3597).
func TestDoctorTellsWhatItMayDoWithTheIndex(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	saved := indexReadState
	t.Cleanup(func() { indexReadState = saved })

	line := func(state index.ReadState) string {
		indexReadState = func(string) index.ReadState { return state }
		var out bytes.Buffer
		doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.Contains(l, "format ") {
				return l
			}
		}
		return ""
	}

	if got := line(index.ReadStateUnreadable); !strings.Contains(got, "cannot read it") {
		t.Errorf("unreadable index: %q", got)
	}

	// Readable and answering. The row must not claim otherwise, and must not
	// leave the reader thinking nothing is pending either.
	got := line(index.ReadStateOlderRules)
	if strings.Contains(got, "cannot read") {
		t.Errorf("an index that answers was called unreadable: %q", got)
	}
	if !strings.Contains(got, "still answers") || !strings.Contains(got, "re-read") {
		t.Errorf("an older-rules index does not say what is pending: %q", got)
	}

	// Readable, but withholding until the re-read — the one state of the three
	// that does stop recall, and it is not the same sentence as unreadable.
	got = line(index.ReadStateWithheld)
	if strings.Contains(got, "cannot read") || strings.Contains(got, "still answers") {
		t.Errorf("a withheld index borrowed another state's sentence: %q", got)
	}
	if !strings.Contains(got, "mask") {
		t.Errorf("a withheld index does not say why it is quiet: %q", got)
	}

	got = line(index.ReadStateNewer)
	if !strings.Contains(got, "written by a newer deja") {
		t.Errorf("newer index: %q", got)
	}
	if strings.Contains(got, "older") {
		t.Errorf("a rolled-back binary was told its index is old: %q", got)
	}

	if got := line(index.ReadStateCurrent); got != "" {
		t.Errorf("a matching format still printed: %q", got)
	}
}
