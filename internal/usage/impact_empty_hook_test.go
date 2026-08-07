package usage

import (
	"testing"
	"time"
)

// A session start in a project with no history still injects the environment
// block, and the hook logs that event empty. Impact counted it, so a machine
// that had never recalled a single project session reported "1 session starts
// began with project memory".
func TestImpactSkipsHooksThatCarriedNoSession(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 423, Empty: true})

	got := Impact(dir)
	if got.Injections != 0 {
		t.Errorf("injections = %d, want 0 — the event carried no project memory", got.Injections)
	}
	if got.ServedBytes != 0 {
		t.Errorf("served bytes = %d, want 0", got.ServedBytes)
	}

	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 500, Sessions: 2, RawBytes: 4000})
	if got := Impact(dir); got.Injections != 1 || got.ServedBytes != 500 || got.RawBytes != 4000 {
		t.Errorf("a real injection stopped counting: %+v", got)
	}
}
