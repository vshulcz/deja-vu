package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestStatuslineEmpty(t *testing.T) {
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "deja · no recalls yet today · 0 B injected" {
		t.Fatalf("empty statusline = %q", got)
	}
}

type chunkReader struct {
	chunks []string
	reads  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.reads >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.reads])
	r.reads++
	return n, nil
}

func TestDrainStdinNonFileReader(t *testing.T) {
	r := &chunkReader{chunks: []string{"{}", "ignored"}}
	drainStdin(r)
	if r.reads != len(r.chunks) {
		t.Fatalf("reads = %d, want %d", r.reads, len(r.chunks))
	}
}

func TestStatuslineMissingUsageFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "deja · no recalls yet today · 0 B injected" {
		t.Fatalf("statusline = %q", got)
	}
}

func TestStatuslineCountsRecalls(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	usage.Record(dir, usage.KindRecall, 2048)
	usage.Record(dir, usage.KindHook, 1024)
	usage.Record(dir, usage.KindSearch, 4096) // human search, excluded
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(`{"session_id":"x"}`), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "2 recalls") || !strings.Contains(got, "3.0 KB ctx") || !strings.Contains(got, "1.0 KB injected") {
		t.Fatalf("statusline = %q", got)
	}
}

func TestStatuslineSingular(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	usage.Record(dir, usage.KindContext, 100)
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 recall ·") {
		t.Fatalf("statusline = %q", out.String())
	}
}

// While the first index builds there is nothing to report but the build
// itself, and the status bar is where the user is already looking.
func TestStatuslineReportsColdBuild(t *testing.T) {
	dir := t.TempDir()
	st := warmupStatus{Phase: "indexing", Total: 4, Done: 3, Updated: time.Now().UnixNano()}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "building memory") || !strings.Contains(got, "75%") {
		t.Fatalf("statusline = %q, want the build and its progress", got)
	}
	// Once the build is done the line goes back to reporting recalls.
	_ = os.Remove(warmupStatusPath(dir))
	out.Reset()
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "building memory") {
		t.Fatalf("statusline still claims a build: %q", out.String())
	}
}
