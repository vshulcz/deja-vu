package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A sync record carries the exporting machine's UTC offset. Formatting it as
// it arrived dated a UTC+14 session "Aug 7" on the import receipt while `last`
// and search on the same machine put it under Aug 6 — three days for one
// record, seconds apart (#1047).
func TestImportReceiptDatesArrivalsInTheReadersZone(t *testing.T) {
	tmp := hermeticEnv(t)
	// time.Local is resolved at process start, so TZ= in the environment does
	// not move it; the reader's zone is set on the package clock instead.
	savedLocal := time.Local
	time.Local = time.FixedZone("test-11", -11*60*60)
	t.Cleanup(func() { time.Local = savedLocal })

	// 2026-08-06 14:09 UTC: Aug 7 for the sender at +14, Aug 6 for the reader.
	dir := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"claude","session_id":"s_now","project":"proj/p","role":"user",` +
		`"text":"the connection pool exhausted under load","time":"2026-08-07T04:09:20+14:00"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "deja-sync-aaaa-1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	receipt, err := captureRunStderr(t, "sync", "import", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt, "the connection pool exhausted under load") {
		t.Fatalf("receipt did not list the imported session:\n%s", receipt)
	}
	if strings.Contains(receipt, "Aug 7") {
		t.Errorf("receipt dates the arrival in the sender's zone:\n%s", receipt)
	}
	if !strings.Contains(receipt, "Aug 6") {
		t.Errorf("receipt does not date the arrival in the reader's zone:\n%s", receipt)
	}

	// The day the receipt names is the day the rest of the machine lists it
	// under — that is the whole point.
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2026-08-06") {
		t.Fatalf("last disagrees about the day:\n%s", out)
	}
}
