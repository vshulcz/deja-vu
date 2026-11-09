package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
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

// promptSessions is the session_id each hook-prompt run was filed under, by
// the prompt it ran for.
func promptSessions(calls []fakeHookCall) map[string]string {
	out := map[string]string{}
	for _, c := range calls {
		if c.args[0] == "hook-prompt" {
			out[fmt.Sprint(c.input["prompt"])] = fmt.Sprint(c.input["session_id"])
		}
	}
	return out
}

func rxWorkspaceStore(workspace string) string {
	root := strings.TrimSpace(workspace)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	slug := strings.NewReplacer(string(os.PathSeparator), "-", "/", "-", `\`, "-", ":", "-").Replace(root)
	return filepath.Join(sources.ReasonixStateRoot(), "projects", slug, "sessions-v4")
}

func writeRxSessionDir(t *testing.T, ws, dir, manifest string) {
	t.Helper()
	p := filepath.Join(rxWorkspaceStore(ws), dir, "manifest.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A compaction is not the end of a session, and it must not hide the end of
// one either: a rotate after it starts a new session with a key of its own.
func TestReasonixExtRotateAfterCompactionStartsANewSession(t *testing.T) {
	prev := rxSessionRaceWindow
	rxSessionRaceWindow = 0
	t.Cleanup(func() { rxSessionRaceWindow = prev })
	calls := fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	h.handshake()
	h.intercept("input.receive", map[string]any{"text": "one"})
	h.intercept("compaction.prepare", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "x"}}})
	time.Sleep(10 * time.Millisecond)
	h.notify("extension/event", map[string]any{"event": "session.rotate", "payload": map[string]any{"phase": "rotate"}})
	h.intercept("input.receive", map[string]any{"text": "two"})
	keys := promptSessions(calls())
	if keys["one"] == "" || keys["one"] == keys["two"] {
		t.Errorf("session keys = %v, want the turn after the rotate filed under a new one", keys)
	}
}

// Another Reasonix in the same workspace writes its session first. It was
// created before this sidecar started, or says nothing about when: neither is
// this session.
func TestReasonixExtDoesNotAdoptAParallelSession(t *testing.T) {
	calls := fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	ws := t.TempDir()
	h.handshakeAt(ws)
	writeRxSessionDir(t, ws, "a", `{"sessionId":"NO-CLOCK"}`)
	writeRxSessionDir(t, ws, "b", `{"sessionId":"OLDER","createdAt":"`+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)+`"}`)
	h.intercept("input.receive", map[string]any{"text": "one"})
	if got := promptSessions(calls())["one"]; got == "NO-CLOCK" || got == "OLDER" {
		t.Errorf("recall was filed under another session's id %q", got)
	}
}

// Reasonix 2.x names the session in session.start ahead of the first turn and
// keeps no sessions-v4 store. The sidecar no longer scans that store, so a
// session directory there is nobody's key, whatever its manifest says (#4795).
func TestReasonixExtDoesNotScanTheRetiredStore(t *testing.T) {
	calls := fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	ws := t.TempDir()
	h.handshakeAt(ws)
	writeRxSessionDir(t, ws, "c", `{"sessionId":"FROM-THE-STORE","createdAt":"`+time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)+`"}`)
	h.intercept("input.receive", map[string]any{"text": "one"})
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start", "sessionPath": filepath.Join(t.TempDir(), "sessions", "20261007-102239.159219000-stub-model.jsonl")}})
	h.intercept("input.receive", map[string]any{"text": "two"})
	keys := promptSessions(calls())
	if keys["one"] == "FROM-THE-STORE" {
		t.Errorf("the first turn was filed under a sessions-v4 directory")
	}
	if keys["two"] != "20261007-102239.159219000-stub-model" {
		t.Errorf("the turn after session.start = %q, want the session the event named", keys["two"])
	}
}

// /new in the same process: the session it replaced was written a moment ago
// and must not be taken for the new one.
func TestReasonixExtNewSessionDoesNotAdoptTheOneItReplaced(t *testing.T) {
	prev := rxSessionRaceWindow
	rxSessionRaceWindow = 300 * time.Millisecond
	t.Cleanup(func() { rxSessionRaceWindow = prev })
	calls := fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	ws := t.TempDir()
	h.handshakeAt(ws)
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start", "sessionPath": "OLD"}})
	h.intercept("input.receive", map[string]any{"text": "one"})
	time.Sleep(400 * time.Millisecond)
	h.notify("extension/event", map[string]any{"event": "session.rotate", "payload": map[string]any{"phase": "rotate", "sessionPath": "OLD"}})
	h.intercept("input.receive", map[string]any{"text": "first turn of the new session"})
	keys := promptSessions(calls())
	if keys["one"] != "OLD" {
		t.Fatalf("first session = %q, want OLD", keys["one"])
	}
	if keys["first turn of the new session"] == "OLD" {
		t.Errorf("the new session was filed under the session it replaced")
	}
}

// Where Reasonix names the session, that name is the key: a bare id, a
// sessions-v4 directory, or a JSONL path.
func TestReasonixExtTakesTheSessionFromTheEvent(t *testing.T) {
	ws := t.TempDir()
	writeRxSessionDir(t, ws, "dir9", `{"sessionId":"FROM-MANIFEST"}`)
	for _, tc := range []struct{ path, want string }{
		{"abc123", "abc123"},
		{filepath.Join(rxWorkspaceStore(ws), "dir9"), "FROM-MANIFEST"},
		{filepath.Join(t.TempDir(), "sessions", "s-42.jsonl"), "s-42"},
	} {
		if got := reasonixSessionIDFrom(tc.path); got != tc.want {
			t.Errorf("reasonixSessionIDFrom(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	calls := fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	h.handshakeAt(ws)
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start", "sessionPath": "abc123"}})
	h.intercept("input.receive", map[string]any{"text": "one"})
	if got := promptSessions(calls())["one"]; got != "abc123" {
		t.Errorf("session id = %q, want the one session.start named", got)
	}
}

// The start event for the session being served can land after its first turn.
// It names that session; it does not start another one.
func TestReasonixExtNamesTheSessionFromALateStartEvent(t *testing.T) {
	calls := fakeRxHooks(t, func(sub string, _ map[string]any) (string, error) {
		if sub == "hook-context" {
			return "<deja-recall>\nDIGEST\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	h.intercept("input.receive", map[string]any{"text": "one"})
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"phase": "start", "sessionPath": "late-id"}})
	if r := h.intercept("input.receive", map[string]any{"text": "two"}); r.Decision != "continue" {
		t.Errorf("a late start event sent the digest again: %s", r.Replacement)
	}
	if got := promptSessions(calls())["two"]; got != "late-id" {
		t.Errorf("session id after the late start = %q, want late-id", got)
	}
}
