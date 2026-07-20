package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWeekWindowAndEmptySkip(t *testing.T) {
	idx := filepath.Join(t.TempDir(), "index.db")
	var buf []byte
	for _, e := range []Event{
		{Time: time.Now().Add(-2 * 24 * time.Hour), Kind: KindHook, Bytes: 100},
		{Time: time.Now().Add(-9 * 24 * time.Hour), Kind: KindRecall, Bytes: 999},
		{Time: time.Now().Add(-time.Hour), Kind: KindRecall, Bytes: 40},
		{Time: time.Now().Add(-2 * time.Hour), Kind: KindRecall, Bytes: 7, Empty: true},
	} {
		b, _ := json.Marshal(e)
		buf = append(append(buf, b...), '\n')
	}
	if err := os.WriteFile(Path(idx), buf, 0o644); err != nil {
		t.Fatal(err)
	}
	recalls, bytes, injected, injBytes := Week(idx)
	if recalls != 1 || bytes != 40 {
		t.Fatalf("Week demand = %d, %d; want 1, 40", recalls, bytes)
	}
	if injected != 1 || injBytes != 100 {
		t.Fatalf("Week injected = %d, %d; want 1, 100", injected, injBytes)
	}
	if r, b, i, _ := Week(filepath.Join(t.TempDir(), "none")); r != 0 || b != 0 || i != 0 {
		t.Fatalf("missing log Week = %d, %d, %d", r, b, i)
	}
}
