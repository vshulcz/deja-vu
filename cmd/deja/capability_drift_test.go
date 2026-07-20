package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

type capRegistry struct {
	Harnesses []struct {
		ID           string `json:"id"`
		DisplayName  string `json:"display_name"`
		Capabilities *struct {
			MCP     bool   `json:"mcp"`
			Auto    bool   `json:"auto"`
			Resume  bool   `json:"resume"`
			Handoff string `json:"handoff"`
		} `json:"capabilities"`
	} `json:"harnesses"`
}

// The capability matrix in README/site is generated from the registry; this
// test pins the registry to what the code actually does, so the published
// matrix cannot drift from behavior.
func TestCapabilityRegistryMatchesCode(t *testing.T) {
	hermeticEnv(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg capRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	installID := map[string]string{"claude": "claude-code"}
	autoCapable := map[string]bool{"claude": true, "codex": true, "opencode": true}
	seen := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		if h.Capabilities == nil {
			t.Fatalf("registry entry %q has no capabilities block", h.ID)
		}
		if h.DisplayName == "" {
			t.Fatalf("registry entry %q has no display_name", h.ID)
		}
		seen++
		c := h.Capabilities

		// MCP: an install target must exist and write real wiring.
		id := h.ID
		if v, ok := installID[h.ID]; ok {
			id = v
		}
		r, err := installTarget(id, "/bin/deja", false)
		gotMCP := err == nil && r.Action != "" && r.Action != "guidance-only"
		if h.ID == "aider" {
			gotMCP = false // aider has no MCP client and no install target
			if _, err := installTarget("aider", "/bin/deja", false); err == nil {
				t.Fatal("aider unexpectedly grew an install target — update the registry")
			}
		}
		if gotMCP != c.MCP {
			t.Fatalf("%s: registry mcp=%v, code says %v", h.ID, c.MCP, gotMCP)
		}

		// Auto-recall hooks exist only where an -auto target installs.
		if c.Auto != autoCapable[h.ID] {
			if _, err := installTarget(h.ID+"-auto", "/bin/deja", false); (err == nil) != c.Auto {
				t.Fatalf("%s: registry auto=%v disagrees with install targets", h.ID, c.Auto)
			}
		}

		// Resume: resumeCommand must succeed for a plausible session.
		s := model.Session{ID: "abc123", Harness: h.ID, Project: "p", Path: "/tmp/x.jsonl"}
		_, _, rerr := resumeCommand(s)
		if (rerr == nil) != c.Resume {
			t.Fatalf("%s: registry resume=%v, resumeCommand err=%v", h.ID, c.Resume, rerr)
		}

		// Handoff: exec targets come from the command table; paste-only is the rest.
		_, execOK := handoffCommand(h.ID, "P")
		switch c.Handoff {
		case "exec":
			if !execOK {
				t.Fatalf("%s: registry says handoff exec, command table disagrees", h.ID)
			}
		case "paste":
			if execOK {
				t.Fatalf("%s: registry says paste-only, but an exec entry exists", h.ID)
			}
		default:
			t.Fatalf("%s: unknown handoff kind %q", h.ID, c.Handoff)
		}
	}
	if seen != len(handoffTargets())+1 { // +1: antigravity is paste-only
		t.Fatalf("registry covers %d harnesses, handoff targets %d", seen, len(handoffTargets()))
	}

	// The published README matrix must contain a row for every harness.
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		if !strings.Contains(string(readme), "| "+h.DisplayName+" |") {
			t.Fatalf("README matrix missing row for %s — run `go run ./scripts/genmatrix`", h.DisplayName)
		}
	}
}
