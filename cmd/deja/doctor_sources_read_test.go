package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// doctor has to be able to say when deja last read this machine's stores, and
// not only when the index was last written. On a machine that imports from a
// peer on a timer those are different days: the import rewrites the index and
// opens no transcript, and every surface that compared against the build time
// reported nothing to do (#3747).
func TestDoctorReportsWhenTheStoresWereLastRead(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	root := filepath.Join(t.TempDir(), "claude", "projects", "-work-pool")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Dir(root))
	session := `{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the pool hands out dead connections after a deploy"}}
{"type":"assistant","sessionId":"aaa1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the proxy closes idle backends at five minutes"}}
`
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	report := inspectDoctorIndex(dir, nil)
	if report.SourcesReadAt == "" {
		t.Fatal("doctor --json does not say when the stores were read")
	}
	if report.SourcesReadAt == "never" {
		t.Fatalf("a build that read a transcript reports %q", report.SourcesReadAt)
	}
	if _, err := time.Parse(time.RFC3339, report.SourcesReadAt); err != nil {
		t.Errorf("sources_read_at is %q, which is not RFC 3339: %v", report.SourcesReadAt, err)
	}

	// A store this fresh has nothing to report beyond the build time, so the
	// one-line form stays: the second date is for the machines where the two
	// have drifted apart.
	var out bytes.Buffer
	doctorIndex(&out, report, dir)
	if got := out.String(); strings.Contains(got, "stores never read") {
		t.Errorf("the text report calls a store that was just read unread:\n%s", got)
	}
}
