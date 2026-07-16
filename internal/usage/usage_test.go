package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordAndToday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	Record(dir, KindRecall, 1000)
	Record(dir, KindHook, 500)
	Record(dir, KindSearch, 9999) // human search, not counted
	recalls, bytes := Today(dir)
	if recalls != 2 || bytes != 1500 {
		t.Fatalf("today = %d recalls %d bytes, want 2/1500", recalls, bytes)
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("usage log missing: %v", err)
	}
}

func TestTodayIgnoresYesterday(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	old := Event{Time: time.Now().UTC().Add(-48 * time.Hour), Kind: KindRecall, Bytes: 700}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(Path(dir), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindRecall, 300)
	recalls, bytes := Today(dir)
	if recalls != 1 || bytes != 300 {
		t.Fatalf("today = %d/%d, want 1/300", recalls, bytes)
	}
}

func TestCorruptLinesIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.WriteFile(Path(dir), []byte("{broken\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindContext, 42)
	recalls, bytes := Today(dir)
	if recalls != 1 || bytes != 42 {
		t.Fatalf("today = %d/%d, want 1/42", recalls, bytes)
	}
}

func TestRotateKeepsRecentWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	var sb strings.Builder
	oldEvent, _ := json.Marshal(Event{Time: time.Now().UTC().Add(-30 * 24 * time.Hour), Kind: KindRecall, Bytes: 1})
	newEvent, _ := json.Marshal(Event{Time: time.Now().UTC(), Kind: KindRecall, Bytes: 2})
	for sb.Len() < rotateAt {
		sb.Write(oldEvent)
		sb.WriteByte('\n')
	}
	sb.Write(newEvent)
	sb.WriteByte('\n')
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	Record(dir, KindHook, 3) // triggers rotation
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= rotateAt {
		t.Fatalf("log not rotated, size %d", fi.Size())
	}
	recalls, bytes := Today(dir)
	if recalls != 2 || bytes != 5 {
		t.Fatalf("today after rotate = %d/%d, want 2/5", recalls, bytes)
	}
}
