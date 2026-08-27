package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

// #2023 says the session-start hook records an empty session id on Windows,
// which means the payload it read was not the payload the test wrote. The
// evidence for that is a CI failure two steps downstream, so this asks the
// question directly, on every platform, and prints what arrived.
func TestTheHookReadsThePayloadItWasGiven(t *testing.T) {
	payload := `{"source":"startup","session_id":"ses_from_harness","cwd":"/tmp/beta"}`
	withHookStdin(t, payload)
	got := string(readHookStdin())
	t.Logf("%s: wrote %d bytes, read %d: %q", runtime.GOOS, len(payload), len(got), got)
	if got != payload {
		t.Errorf("the hook read %q, not the payload it was given", got)
	}

	// The same through the decoder the doors use, since that is where an id
	// would go missing rather than in the read.
	withHookStdin(t, payload)
	var input struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	raw := readHookStdin()
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatalf("decoding %q: %v", raw, err)
	}
	t.Logf("%s: decoded session_id=%q cwd=%q", runtime.GOOS, input.SessionID, input.CWD)
	if input.SessionID != "ses_from_harness" {
		t.Errorf("the session id decoded as %q", input.SessionID)
	}

	// And with the line endings a Windows harness may write, which is one of
	// the two suspects the issue names.
	withHookStdin(t, strings.ReplaceAll(payload, "}", "}\r\n"))
	crlf := string(readHookStdin())
	t.Logf("%s: with CRLF, read %d bytes: %q", runtime.GOOS, len(crlf), crlf)
	if !strings.Contains(crlf, "ses_from_harness") {
		t.Errorf("a payload with CRLF came back as %q", crlf)
	}

	// The other suspect: stdin that is a file rather than a pipe, which is how
	// some hosts hand a payload over.
	f, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	fromFile := string(readHookStdin())
	os.Stdin = old
	_ = f.Close()
	t.Logf("%s: from a file, read %d bytes: %q", runtime.GOOS, len(fromFile), fromFile)
	if fromFile != payload {
		t.Errorf("a payload handed over as a file came back as %q", fromFile)
	}
}
