package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Opening a harness and closing it without typing leaves a file of setup
// records and no conversation. Reporting that as a parse failure sends people
// looking for a bug in deja — it happened on my own machine after taking a
// screenshot of a TUI.
func TestParsedZeroIgnoresSessionsWithNoConversation(t *testing.T) {
	dir := t.TempDir()
	setupOnly := filepath.Join(dir, "wire.jsonl")
	body := `{"type":"metadata","protocol_version":"1.4"}
{"type":"config.update","profileName":"agent","systemPrompt":"You are a coding agent"}
{"type":"tools.set_active_tools","tools":["read","write"]}
`
	if err := os.WriteFile(setupOnly, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if fileHasConversation(setupOnly) {
		t.Fatal("a file of setup records was read as a conversation")
	}

	// A file that does hold turns must still be flagged when it parses to
	// nothing: that is the case the warning exists for.
	withTurns := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(withTurns, []byte(body+`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileHasConversation(withTurns) {
		t.Fatal("a file with a user turn was read as empty")
	}

	// aider's markdown store marks turns with #### rather than JSON roles.
	md := filepath.Join(dir, "history.md")
	if err := os.WriteFile(md, []byte("# aider chat started at 2026-01-01\n\n#### fix the parser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileHasConversation(md) {
		t.Fatal("aider's turn marker was not recognised")
	}
	// An unreadable file is a real problem and must not be silenced.
	if !fileHasConversation(filepath.Join(dir, "absent.jsonl")) {
		t.Fatal("a missing file was treated as merely empty")
	}
}
