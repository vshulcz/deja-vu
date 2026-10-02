package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A thread deja cannot decode reaches doctor as a skipped record of the zed
// store, with the reason. It used to leave no trace, so the store read as whole
// with half its threads missing (#4341).
func TestAZedThreadDejaCannotDecodeIsReportedWithWhy(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "good", "2026-01-01T10:00:00Z", []string{"marker-zed-good"})
	c := exec.Command("sqlite3", db)
	c.Stdin = strings.NewReader(`insert into threads (id,summary,updated_at,data_type,data) values ('later','s','2026-01-01T11:00:00Z','brotli',x'00');`)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-good") != 1 {
		t.Fatalf("the readable thread was not indexed, so this measures nothing")
	}
	if got := IngestHealth(dir)["zed"].MalformedLines; got != 1 {
		t.Fatalf("zed skipped = %d, want 1: %#v", got, IngestHealth(dir))
	}
	if r := IngestFilesReport(dir)[db].Reason; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
}

// zed is read from its watermark, so a pass after a newer thread lands does not
// hand the skipped row back. It is still in the store, still unreadable, and
// the count must not drop to zero because the cursor moved past it (#4341).
func TestAZedThreadDejaCannotDecodeStaysCountedPastTheWatermark(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "good", "2026-01-01T10:00:00Z", []string{"marker-zed-good"})
	c := exec.Command("sqlite3", db)
	c.Stdin = strings.NewReader(`insert into threads (id,summary,updated_at,data_type,data) values ('later','s','2026-01-01T11:00:00Z','brotli',x'00');`)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for i, ts := range []string{"2026-01-02T10:00:00Z", "2026-01-03T10:00:00Z"} {
		zedThread(t, db, fmt.Sprintf("next%d", i), ts, []string{fmt.Sprintf("marker-zed-next%d", i)})
		later := time.Now().Add(time.Duration(i+1) * time.Minute)
		if err := os.Chtimes(db, later, later); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if zedHits(t, dir, fmt.Sprintf("marker-zed-next%d", i)) != 1 {
			t.Fatalf("pass %d did not index the new thread, so this measures nothing", i+2)
		}
		if got := IngestHealth(dir)["zed"].MalformedLines; got != 1 {
			t.Fatalf("pass %d: zed skipped = %d, want 1", i+2, got)
		}
	}
	if r := IngestFilesReport(dir)[db].Reason; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
}

func TestZedSkipsAreNamedAsThreads(t *testing.T) {
	if got := nothingReadableNarration("zed", 2, 0); !strings.Contains(got, "2 threads could not be read") {
		t.Errorf("got %q", got)
	}
	if got := harnessNarration("zed", nil, "", 1, 0); !strings.Contains(got, "1 thread skipped") {
		t.Errorf("got %q", got)
	}
	if got := nothingReadableNarration("claude", 2, 0); !strings.Contains(got, "2 lines could not be read") {
		t.Errorf("a JSONL store lost its unit: %q", got)
	}
}
