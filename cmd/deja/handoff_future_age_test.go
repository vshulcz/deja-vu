package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The handoff receipt exists for the reader to check what is being handed off
// before it lands. A timestamp ahead of the clock — a store synced from a
// machine whose clock runs fast — put a negative number on that line, and
// every negative duration fell into the minutes branch: a session dated a year
// ahead read "-576000m old".
func TestHumanAgeOnATimestampAheadOfTheClock(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-30 * time.Minute, "unknown age"},
		{-3 * time.Hour, "unknown age"},
		{-400 * 24 * time.Hour, "unknown age"},
		// The boundary stays where it was: now is an age, not a mystery.
		{0, "0m old"},
		{90 * time.Minute, "1h old"},
		{50 * time.Hour, "2d old"},
	} {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
	// Nothing may print a negative number, whatever the shape of the input.
	for _, d := range []time.Duration{-time.Nanosecond, -time.Hour, -99999 * time.Hour} {
		if strings.Contains(humanAge(d), "-") {
			t.Errorf("humanAge(%s) = %q", d, humanAge(d))
		}
	}
}

// End to end: the receipt line the user reads.
func TestHandoffReceiptOnAFutureSession(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-f")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	ahead := time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"type":"user","sessionId":"f1","cwd":"/w/f","timestamp":"` + ahead +
		`","message":{"role":"user","content":"the retry queue stalls on staging and the workers wake together"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "f1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRunStderr(t, "handoff", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "handing off") {
		t.Fatalf("no receipt: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "handing off") && strings.Contains(line, "m old") && strings.Contains(line, "-") {
			t.Errorf("the receipt claims a negative age: %q", line)
		}
	}
	if !strings.Contains(out, "unknown age") {
		t.Errorf("the receipt does not say the age is unknown: %q", out)
	}
}
