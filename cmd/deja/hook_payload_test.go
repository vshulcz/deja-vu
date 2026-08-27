package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// hookPayload builds what a harness writes to a hook's stdin.
//
// Pasting a path into a JSON string with `+` breaks the moment the path holds a
// backslash — every Windows path does — and the payload a test then feeds the
// hook is not JSON at all. Nothing says so: the hook reads no session id and
// the assertion fails several steps later, about a session that was never in a
// payload deja could parse (#2081).
func hookPayload(t *testing.T, fields map[string]string) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The escaping itself, so the helper cannot quietly stop doing it.
func TestAHookPayloadSurvivesAWindowsPath(t *testing.T) {
	payload := hookPayload(t, map[string]string{
		"source": "startup", "session_id": "ses1", "cwd": `C:\Users\me\work\api`,
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the payload is not JSON: %v: %s", err, payload)
	}
	if got["cwd"] != `C:\Users\me\work\api` {
		t.Errorf("the path did not survive: %q", got["cwd"])
	}
	if got["session_id"] != "ses1" {
		t.Errorf("the session id did not survive: %q", got["session_id"])
	}
	// And the shape a hand-built payload had: a bare backslash run, which is
	// what made it unparseable.
	if !strings.Contains(payload, `\\Users`) {
		t.Errorf("the backslashes are not escaped: %s", payload)
	}
}

// claudeRecord builds one line of a claude transcript. A record is JSON too, so
// a path pasted into one has the same hole a payload does — `filepath.ToSlash`
// covers the backslash a Windows path carries and nothing else (#2096).
func claudeRecord(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// A path can hold a quote on every Unix filesystem, and that is the character
// ToSlash does nothing about.
func TestAFixtureSurvivesAPathHoldingAQuote(t *testing.T) {
	odd := `/tmp/say "hello"/app`
	payload := hookPayload(t, map[string]string{"source": "startup", "session_id": "ses1", "cwd": odd})
	var got map[string]string
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the payload is not JSON: %v: %s", err, payload)
	}
	if got["cwd"] != odd {
		t.Errorf("the path did not survive: %q", got["cwd"])
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": odd,
		"message": map[string]any{"role": "user", "content": "the pool was exhausted"},
	})
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rec)), &line); err != nil {
		t.Fatalf("the record is not JSON: %v: %s", err, rec)
	}
	if line["cwd"] != odd {
		t.Errorf("the record's path did not survive: %v", line["cwd"])
	}
}
