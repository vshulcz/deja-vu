package sources

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// allocatedBy is how many bytes fn allocates.
func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Counting the corpus must not copy it: the dedupe set used the text itself
// as its key, a second copy of every message held at once, and a full build
// counts three times.
func TestCountMessagesDoesNotCopyTheText(t *testing.T) {
	const msgs, size = 400, 64 << 10
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ss := []model.Session{{Harness: "claude", ID: "s1"}}
	for i := 0; i < msgs; i++ {
		text := strings.Repeat(string(rune('a'+i%26)), size-1) + string(rune('A'+i/26))
		ss[0].Messages = append(ss[0].Messages, model.Message{Role: "user", Text: text, Time: at.Add(time.Duration(i) * time.Second)})
	}
	// A twin of one message is still counted once.
	ss[0].Messages = append(ss[0].Messages, ss[0].Messages[7])
	var n int
	got := allocatedBy(func() { n = CountMessages(ss) })
	if n != msgs {
		t.Fatalf("CountMessages = %d, want %d", n, msgs)
	}
	text := uint64(msgs * size)
	if got > text/20 {
		t.Fatalf("CountMessages allocated %d bytes over %d bytes of text; the dedupe set holds a copy of it", got, text)
	}
}
