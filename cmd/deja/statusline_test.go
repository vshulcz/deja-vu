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
	readStatuslineInput(r)
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
	if got := out.String(); !strings.Contains(got, "███") || !strings.Contains(got, "75%") {
		t.Fatalf("statusline = %q, want a filled bar and the percentage", got)
	}
	// Once the build is done the line goes back to reporting recalls.
	_ = os.Remove(warmupStatusPath(dir))
	out.Reset()
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "███") {
		t.Fatalf("statusline still claims a build: %q", out.String())
	}
}

// Most people already run something in the status bar, so the conflict path is
// the common one. It has to hand over a line that works, not advice.
func TestStatuslineConflictOffersAWorkingCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"statusLine":{"type":"command","command":"bash /opt/theirs.sh"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := installStatusline("/bin/deja", false)
	if err == nil {
		t.Fatal("replaced someone else's statusline")
	}
	msg := err.Error()
	for _, want := range []string{
		"bash /opt/theirs.sh", // theirs still runs
		"/bin/deja statusline",
		"json=$(cat)", // Claude pipes session JSON: capture once, feed both
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict message is not a usable command, missing %q:\n%s", want, msg)
		}
	}
}
