package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineRecentSearchAndExactRead(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_SOURCE_INSTANCE", "test-workstation")
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"machine-one", "machine-two"} {
		writeClaudeFixture(t, filepath.Join(root, "machine", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"machinecontract first"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"machinecontract second"}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:06:05Z","message":{"role":"user","content":"machinecontract third"}}`,
		})
	}

	out, err := captureRun(t, "last", "1", "--json", "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	var recent recentJSON
	if err := json.Unmarshal([]byte(out), &recent); err != nil {
		t.Fatalf("recent json: %v\n%s", err, out)
	}
	if recent.SchemaVersion != 1 || len(recent.Sessions) != 1 || recent.Sessions[0].Source.Origin != "local" || recent.Sessions[0].Source.Instance != "test-workstation" || recent.Sessions[0].Messages != nil {
		t.Fatalf("recent = %#v", recent)
	}

	out, err = captureRun(t, "--json", "--no-embed", "--limit", "1", "machinecontract")
	if err != nil {
		t.Fatal(err)
	}
	var hits []struct {
		Session struct {
			ID     string `json:"id"`
			Source struct {
				Origin   string `json:"origin"`
				Instance string `json:"instance"`
			} `json:"source"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("search json: %v\n%s", err, out)
	}
	if len(hits) != 1 || hits[0].Session.Source.Origin != "local" || hits[0].Session.Source.Instance != "test-workstation" {
		t.Fatalf("hits = %#v", hits)
	}

	out, err = captureRun(t, "show", hits[0].Session.ID, "--harness", "claude", "--json", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	var exact sessionJSON
	if err := json.Unmarshal([]byte(out), &exact); err != nil {
		t.Fatalf("show json: %v\n%s", err, out)
	}
	if exact.SchemaVersion != 1 || exact.Session.ID != hits[0].Session.ID || exact.Session.Harness != "claude" || exact.Session.Source.Instance != "test-workstation" || exact.Window.Offset != 1 || exact.Window.Limit != 1 || exact.Window.Total != 3 || exact.Window.Returned != 1 || len(exact.Session.Messages) != 1 || !strings.Contains(exact.Session.Messages[0].Text, "second") {
		t.Fatalf("exact = %#v", exact)
	}
}

func TestMachineFlagValidation(t *testing.T) {
	tests := [][]string{
		{"show", "abc", "--json"},
		{"show", "abc", "--json", "--harness", "claude", "--limit", "201"},
		{"--json", "--limit", "0", "needle"},
		{"--json", "--limit", "101", "needle"},
	}
	for _, args := range tests {
		if _, err := captureRun(t, args...); err == nil {
			t.Fatalf("run(%q) succeeded", args)
		}
	}
}
