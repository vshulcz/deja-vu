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
			Skill   bool   `json:"skill"`
			Command bool   `json:"command"`
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
	// aider is auto-capable without an -auto target: the wrapper refreshes the
	// read-only file, which aider re-reads on every message.
	autoCapable := map[string]bool{"claude": true, "codex": true, "opencode": true, "aider": true}
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
			// aider has an install target but no MCP client: what it writes is
			// the read: key, and recall arrives through `deja aider`.
			gotMCP = false
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

		// Skill: guidance is a skill file only where install owns the whole
		// file. Everywhere else it is a marked block inside a file the user
		// owns, which is loaded for the whole session rather than on demand.
		if got := guidanceOwnsWholeFile(id); got != c.Skill {
			t.Fatalf("%s: registry skill=%v, guidanceOwnsWholeFile(%q)=%v", h.ID, c.Skill, id, got)
		}

		// Command: read it off the artifact install actually generates, not a
		// list kept beside it — a list would only ever agree with itself.
		// Claude Code gets a file in commands/; cline, hermes and pi register
		// theirs inside the plugin deja writes for them.
		gotCommand := false
		switch h.ID {
		case "claude":
			gotCommand = strings.Contains(claudeCommandMD("/bin/deja"), "deja")
		case "cline":
			gotCommand = strings.Contains(clinePluginJS("/bin/deja"), "registerCommand")
		case "hermes":
			gotCommand = strings.Contains(hermesPluginManifest, "provides_commands")
		case "pi":
			gotCommand = strings.Contains(piExtensionTS("/bin/deja"), "registerCommand")
		}
		if gotCommand != c.Command {
			t.Fatalf("%s: registry command=%v, generated install artifact says %v", h.ID, c.Command, gotCommand)
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
			if handoffPasteOnly[h.ID] {
				t.Fatalf("%s: registry says handoff exec, but handoffPasteOnly lists it", h.ID)
			}
		case "paste":
			if execOK {
				t.Fatalf("%s: registry says paste-only, but an exec entry exists", h.ID)
			}
			if !handoffPasteOnly[h.ID] {
				t.Fatalf("%s: registry says paste-only, but handoffPasteOnly does not list it", h.ID)
			}
		default:
			t.Fatalf("%s: unknown handoff kind %q", h.ID, c.Handoff)
		}
	}
	if seen != len(handoffTargets())+len(handoffPasteOnly) {
		t.Fatalf("registry covers %d harnesses, handoff targets %d + %d paste-only", seen, len(handoffTargets()), len(handoffPasteOnly))
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
