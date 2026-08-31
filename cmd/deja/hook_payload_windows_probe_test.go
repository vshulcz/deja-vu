package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A measurement, not a guard: the session-start hook records an empty session
// id on Windows and nowhere else (#2023), and the payload it reads there has
// never been looked at. This reports what arrives, so the branch that fixes it
// starts from the shape rather than from a guess. It fails on purpose — the
// suite prints nothing about a passing test — and does not belong on a
// long-lived branch.
func TestWindowsHookPayloadProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the probe is about Windows")
	}
	payload := `{"source":"startup","session_id":"ses_probe","cwd":"C:\\work"}`

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	start := time.Now()
	got := readHookStdin()
	took := time.Since(start)
	os.Stdin = old
	_ = r.Close()

	var input struct {
		Source    string `json:"source"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	unmarshalErr := json.Unmarshal(got, &input)

	direct := readHookPayload(strings.NewReader(payload), hookStdinWait)

	t.Errorf("probe: wait=%v via-stdin bytes=%d took=%v id=%q err=%v raw=%q | via-reader bytes=%d raw=%q",
		hookStdinWait, len(got), took, input.SessionID, unmarshalErr, string(got), len(direct), string(direct))
}
