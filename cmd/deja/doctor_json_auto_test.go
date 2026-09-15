package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type autoRecallRow struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	Path          string `json:"path"`
	BinaryMissing bool   `json:"binary_missing"`
}

func autoRecallRows(t *testing.T) map[string]autoRecallRow {
	t.Helper()
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		AutoRecall []autoRecallRow `json:"auto_recall"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, out)
	}
	if len(report.AutoRecall) == 0 {
		t.Fatalf("doctor --json carries no auto_recall section:\n%s", out)
	}
	rows := map[string]autoRecallRow{}
	for _, row := range report.AutoRecall {
		rows[row.Name] = row
	}
	return rows
}

// The text report has named the state of every auto-recall wiring since the
// rows existed; the JSON a script reads carried the MCP half and not this one,
// so a machine watching its own install could see a missing sqlite3 and not a
// hook running a binary that is gone — which is what an upgrade leaves behind,
// with every hook exiting 127 (#3502, #3510).
func TestDoctorJSONCarriesTheAutoRecallRows(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// opencode's plugin is one of those rows and the simplest to write by
	// hand: a file that is there, and a file that calls deja.
	plugin := filepath.Join(home, ".config", "opencode", "plugins", "deja.js")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing written yet.
	if got := autoRecallRows(t)["opencode"]; got.State != "missing" {
		t.Errorf("with no plugin file, opencode reads %q, want missing", got.State)
	}

	// A file with no deja call in it: the install looks done and nothing runs.
	if err := os.WriteFile(plugin, []byte("export const hooks = {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := autoRecallRows(t)["opencode"]; got.State != "stale" {
		t.Errorf("with a plugin that calls nothing, opencode reads %q, want stale", got.State)
	}

	// A plugin whose binary is gone: wired, and dead. The path has to be
	// absolute on the platform running the test — `/nowhere/deja` is not one on
	// windows, and the check that reads these files skips anything relative,
	// because a relative path resolves against wherever the reader is standing.
	gone := filepath.Join(home, "nowhere", "deja")
	dead := "const DEJA = \"" + filepath.ToSlash(gone) + "\";\nawait spawn(DEJA, [\"hook-context\"]);\n"
	if err := os.WriteFile(plugin, []byte(dead), 0o644); err != nil {
		t.Fatal(err)
	}
	got := autoRecallRows(t)["opencode"]
	if got.State != "wired" {
		t.Errorf("a plugin that calls deja reads %q, want wired", got.State)
	}
	if !got.BinaryMissing {
		t.Errorf("the row says nothing about a binary that is not there: %+v", got)
	}
	if got.Path != plugin {
		t.Errorf("row path = %q, want %q", got.Path, plugin)
	}

	// And one calling a binary that is there is not reported dead. A real file
	// named the way deja names itself, because the check only looks at paths
	// that end in its own name — the test binary does not.
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	live := "const DEJA = \"" + filepath.ToSlash(bin) + "\";\nawait spawn(DEJA, [\"hook-context\"]);\n"
	if err := os.WriteFile(plugin, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := autoRecallRows(t)["opencode"]; got.BinaryMissing {
		t.Errorf("a plugin calling a binary that exists was reported dead: %+v", got)
	}
}

// The two rows this section did not have. doctor's text report has named the
// Claude Code and codex hook wirings since they existed, and the JSON a script
// reads carried neither, because both predate the autoWirings table. So a
// machine watching its own install saw nothing at all about the harness most
// people run — including the state an upgrade leaves, where the entries are
// wired, name a deja that is gone, and every hook exits 127 (#3502, #3510).
func TestDoctorJSONCarriesTheClaudeAndCodexHookRows(t *testing.T) {
	hermeticEnv(t)

	// Nothing installed: both named, and named as missing rather than left out
	// of the report, which reads the same as a harness deja cannot wire.
	rows := autoRecallRows(t)
	for _, name := range []string{"claude-code", "codex-hook"} {
		row, ok := rows[name]
		if !ok {
			t.Fatalf("doctor --json has no %s row", name)
		}
		if row.State != "missing" {
			t.Errorf("%s with nothing installed reads %q, want missing", name, row.State)
		}
		if row.Path == "" {
			t.Errorf("%s row names no file: %+v", name, row)
		}
	}

	var all []string
	for _, h := range claudeHookWiring {
		all = append(all, h.Event)
	}

	// Everything the installer writes, naming a binary that is there.
	writeClaudeSettings(t, all...)
	got := autoRecallRows(t)["claude-code"]
	if got.State != "wired" {
		t.Errorf("a complete claude wiring reads %q, want wired", got.State)
	}
	if got.BinaryMissing {
		t.Errorf("a wiring naming the running binary was reported dead: %+v", got)
	}

	// The same wiring after the binary moved away.
	gone := filepath.Join(os.Getenv("HOME"), "gone", "deja")
	writeClaudeSettingsNaming(t, gone, all...)
	got = autoRecallRows(t)["claude-code"]
	if got.State != "wired" {
		t.Errorf("a wiring whose binary is gone reads %q, want wired", got.State)
	}
	if !got.BinaryMissing {
		t.Errorf("the row says nothing about a binary that is not there: %+v", got)
	}

	// And an older install that has some of the events: the JSON says which
	// state it is in, not just that a file is present.
	writeClaudeSettings(t, all[:1]...)
	if got := autoRecallRows(t)["claude-code"].State; got != "out of date" {
		t.Errorf("a one-of-%d wiring reads %q, want out of date", len(all), got)
	}
}
