package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The minute after install: a build has been asked for and has published no
// progress yet. The statusline called that a quiet day and doctor told the
// reader to start what was already running — both because they required a
// manifest that a first build does not have yet (#925).
func TestFirstBuildIsNotCalledSilenceOrAbsence(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Requested just now; nothing published, no manifest — the first build.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var line bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &line); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line.String(), "indexing your history") {
		t.Errorf("statusline during the first build: %q", line.String())
	}

	var report bytes.Buffer
	doctorIndex(&report, doctorIndexReport{State: "missing", Path: dir}, dir)
	if !strings.Contains(report.String(), "building now") {
		t.Errorf("doctor during the first build:\n%s", report.String())
	}
	if strings.Contains(report.String(), "run `deja warmup`") {
		t.Errorf("doctor told the reader to start a build that is running:\n%s", report.String())
	}

	// An index nobody is building is still called what it is.
	if err := os.Remove(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatal(err)
	}
	report.Reset()
	doctorIndex(&report, doctorIndexReport{State: "missing", Path: dir}, dir)
	if !strings.Contains(report.String(), "not built") {
		t.Errorf("an idle missing index stopped saying so:\n%s", report.String())
	}
}
