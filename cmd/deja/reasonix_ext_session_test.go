package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReasonixExtAnnouncesRecallOncePerSession(t *testing.T) {
	prevWindow := rxSessionRaceWindow
	rxSessionRaceWindow = 0
	t.Cleanup(func() { rxSessionRaceWindow = prevWindow })
	fakeRxHooks(t, func(sub string, _ map[string]any) (string, error) {
		if sub == "hook-prompt" {
			return "<deja-recall>\n- **proj** `abc-…-def` · 2026-09-01\n  - User: x\n- **proj** `ghi-…-jkl` · 2026-09-02\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	h.intercept("input.receive", map[string]any{"text": "one"})
	h.intercept("input.receive", map[string]any{"text": "two"})
	notes := func() []string {
		var out []string
		for _, p := range h.published() {
			if p["kind"] == "notification" {
				payload, _ := p["payload"].(map[string]any)
				out = append(out, fmt.Sprint(payload["title"]))
				if p["sessionId"] != "boot-1" {
					t.Errorf("published for session %v, want the host's own id", p["sessionId"])
				}
			}
		}
		return out
	}
	if got := notes(); len(got) != 1 || got[0] != "recalled 2 prior sessions" {
		t.Fatalf("notifications = %q, want one naming two sessions", got)
	}
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start"}})
	h.intercept("input.receive", map[string]any{"text": "three"})
	if got := notes(); len(got) != 2 {
		t.Errorf("notifications after a new session = %q, want a second one", got)
	}
}

func TestReasonixExtIgnoresASessionEventThatTrailsItsTurn(t *testing.T) {
	calls := fakeRxHooks(t, func(sub string, _ map[string]any) (string, error) {
		if sub == "hook-context" {
			return "<deja-recall>\nDIGEST\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	h.intercept("input.receive", map[string]any{"text": "one"})
	// The event for the session that turn opened, overtaken on the host's
	// queue: it must not read as a second session.
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start"}})
	if r := h.intercept("input.receive", map[string]any{"text": "two"}); r.Decision != "continue" {
		t.Errorf("a trailing session event sent the digest twice: %s", r.Replacement)
	}
	n := 0
	for _, c := range calls() {
		if c.args[0] == "hook-context" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("hook-context ran %d times, want 1", n)
	}
}

func TestRecalledSessionCountReadsBothBlockShapes(t *testing.T) {
	prompt := "<deja-recall>\nheader\n- **Users/me** `d2c192f7-…e82f83c589` · 2026-09-05\n  - Assistant: said `x`\n- **me** `ses_227f3…JBlkzaTmIc` · 2026-07-06\n</deja-recall>"
	digestBlock := "<deja-recall>\n✓ recalled from opencode session · 6 days ago\n  - Session: **me** `ses_f45e7…kJwmJSlBln`\n  - User: q\n</deja-recall>"
	if n := recalledSessionCount([]string{prompt, digestBlock}); n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestReasonixExtShowsTheFirstBuildOnTheStatusLine(t *testing.T) {
	fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	h.handshake()
	now := time.Now().UnixNano()
	status := fmt.Sprintf(`{"phase":"index","done":3,"total":10,"stores":1,"started":%d,"updated":%d}`, now, now)
	if err := os.WriteFile(warmupStatusPath(h.dir), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	h.intercept("input.receive", map[string]any{"text": "one"})
	h.intercept("input.receive", map[string]any{"text": "two"})
	if err := os.Remove(warmupStatusPath(h.dir)); err != nil {
		t.Fatal(err)
	}
	h.intercept("input.receive", map[string]any{"text": "three"})
	h.intercept("input.receive", map[string]any{"text": "four"})
	var details []string
	for _, p := range h.published() {
		if p["kind"] == "status" {
			payload, _ := p["payload"].(map[string]any)
			details = append(details, fmt.Sprint(payload["detail"]))
		}
	}
	if len(details) != 2 || !strings.HasPrefix(details[0], "indexing your history") || details[1] != rxIndexReady {
		t.Fatalf("status lines = %q, want the build once, then ready once", details)
	}
}

func TestReasonixLiveSessionNeedsExactlyOneFreshSession(t *testing.T) {
	store := t.TempDir()
	write := func(dir, name, body string, at time.Time) {
		p := filepath.Join(store, dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	boundary := time.Now().Add(-time.Minute)
	write("old", "events.frames", "x", boundary.Add(-time.Hour))
	write("s1", "manifest.json", `{"sessionId":"sess-real-1"}`, boundary.Add(time.Second))
	if got := reasonixLiveSession(store, boundary); got != "sess-real-1" {
		t.Errorf("one fresh session = %q, want its manifest id", got)
	}
	write("s2", "events.frames", "x", boundary.Add(2*time.Second))
	if got := reasonixLiveSession(store, boundary); got != "" {
		t.Errorf("two fresh sessions = %q, want none: the sidecar cannot tell them apart", got)
	}
	if got := reasonixLiveSession("", boundary); got != "" {
		t.Errorf("no store = %q", got)
	}
}
