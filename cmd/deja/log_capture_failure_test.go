package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// A compaction packet is the one thing in deja that cannot be rebuilt from a
// source: the host is about to discard the context, and if the capture stores
// nothing that work is gone. The journal recorded the reason from the start;
// `deja log` printed `compaction_capture 0 B` either way, so the surface a
// person reads said a lost packet was a normal one.
func TestTheLogNamesACaptureThatStoredNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage.RecordCompactionCapture(dir, usage.CompactionCapture{
		Key:       usage.CompactionKey{Session: "ok", Workspace: "/w/app", Revision: "1"},
		ToolCalls: 7,
	})
	usage.RecordCompactionCapture(dir, usage.CompactionCapture{
		Key:   usage.CompactionKey{Session: "bad", Workspace: "/w/app", Revision: "1"},
		Error: "transcript_unavailable",
	})
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	var failed, ok string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "compaction_capture") {
			continue
		}
		if strings.Contains(line, "stored nothing") {
			failed = line
			continue
		}
		ok = line
	}
	if ok == "" {
		t.Fatalf("the log does not list the capture that worked:\n%s", out)
	}
	if failed == "" {
		t.Fatalf("the log reads the same for a capture that stored nothing:\n%s", out)
	}
	if !strings.Contains(failed, "could not read") {
		t.Errorf("the failed row does not say what went wrong: %q", failed)
	}
	if strings.Contains(ok, "stored nothing") {
		t.Errorf("the row for the capture that worked claims it stored nothing: %q", ok)
	}
}

// A reason deja has not seen before is still printed, as its token, rather
// than silently dropping the only clue.
func TestAnUnknownCaptureReasonIsStillPrinted(t *testing.T) {
	e := usage.Event{Kind: usage.KindCompactionCapture, CompactionError: "something_new"}
	if got := compactionFailureNote(e); !strings.Contains(got, "something_new") {
		t.Errorf("an unknown reason was dropped: %q", got)
	}
	// And an ordinary event carries no such clause.
	if got := compactionFailureNote(usage.Event{Kind: usage.KindRecall, Bytes: 10}); got != "" {
		t.Errorf("a recall row grew a compaction clause: %q", got)
	}
}
