package usage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The memo says what one read found, and it has to stop saying it. An event
// stamped in the past — a clock that stepped back, a log carried from another
// machine — arrives behind the memo, and without an expiry it would wait out
// the whole fourteen-day window before anything looked again (#1972).
func TestABackdatedEventIsStillDropped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12000; i++ {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	// The memo is in hand now: the log is over the threshold with nothing to
	// drop, so the read that just happened answers for the next few minutes.
	old, err := json.Marshal(Event{Time: time.Now().UTC().Add(-100 * 24 * time.Hour), Kind: KindRecall, Bytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(Path(dir), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(old, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	nothingToDrop.Delete(Path(dir)) // stand in for the memo expiring
	RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)

	for _, e := range read(Path(dir)) {
		if e.Time.Before(time.Now().UTC().Add(-keepWindow)) {
			t.Errorf("an event %v old survived the rotation", time.Since(e.Time).Round(time.Hour))
			break
		}
	}
}

// And the memo does not outlive the file it describes: a rotation by another
// process shrinks it, and what this process remembers is about the file that
// was replaced.
func TestAShrunkLogIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12000; i++ {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	before, ok := nothingToDrop.Load(Path(dir))
	if !ok {
		t.Fatal("no memo was stored for a log with nothing to drop")
	}
	m := before.(rotationMemo)
	if skipRotation(Path(dir), m.size-1) {
		t.Error("a log that shrank under this process was taken on trust")
	}
	if !skipRotation(Path(dir), m.size) {
		t.Error("a log that only grew was read again for nothing")
	}
}

// A memo older than its window is not used, whatever it says.
func TestAStaleMemoIsNotUsed(t *testing.T) {
	dir := t.TempDir()
	p := Path(dir)
	nothingToDrop.Store(p, rotationMemo{
		oldest: time.Now().UTC(),
		size:   0,
		at:     time.Now().UTC().Add(-memoWindow - time.Minute),
	})
	if skipRotation(p, 1<<20) {
		t.Error("a memo older than its window was still trusted")
	}
}

// The counters still answer after all this, which is what the log is for.
func TestTheLogStillAnswersAfterABusyFortnight(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2000; i++ {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	recalls, bytes, _, _ := Week(dir)
	if recalls == 0 || bytes == 0 {
		t.Errorf("the week reads %d recalls and %d bytes", recalls, bytes)
	}
	if !strings.Contains(Path(dir), ".usage.jsonl") {
		t.Errorf("the log moved: %s", Path(dir))
	}
}
