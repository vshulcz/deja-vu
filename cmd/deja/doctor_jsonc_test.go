package main

import (
	"os"
	"path/filepath"
	"testing"
)

// opencode takes comments in opencode.json, and the dead-binary check read
// the file as strict JSON: on a commented config the entry's command — a list
// in opencode's shape — was never found, so a server pointing at a deleted
// binary read `wired` (#4197).
func TestDoctorFindsTheDejaCommandInACommentedConfig(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "moved", "deja")
	p := filepath.Join(dir, "opencode.json")
	body := `{
  "$schema": "https://opencode.ai/config.json",
  // the reader's own note
  "model": "shim/luna",
  "mcp": {
    "deja": {"type":"local","command":[` + jsonString(gone) + `,"mcp"]},
  },
}
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(p); got != gone {
		t.Fatalf("dejaCommandMissing = %q, want %q", got, gone)
	}
	if !doctorJSONWiredIn(doctorOpencodeServers)(p) {
		t.Fatal("a commented config with the entry read as not wired")
	}
}
