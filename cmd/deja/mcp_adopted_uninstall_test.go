package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestUninstallClaudeRemovesAdoptedRenamedEntry(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "mcpServers": {
    "deja-vu": {
      "command": "/old/bin/deja",
      "args": ["mcp"],
      "env": {
        "DEJA_INDEX_DIR": "/memory"
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installClaude(exe, false); err != nil {
		t.Fatal(err)
	}
	result, err := installClaude(exe, true)
	if err != nil {
		t.Fatal(err)
	}

	servers := issue2269JSONServers(t, path, "mcpServers")
	if _, ok := servers["deja-vu"]; ok {
		t.Fatalf("uninstall left the adopted deja-vu entry: %#v", servers)
	}
	if result.Action == "unchanged" {
		t.Fatalf("uninstall action = %q, want a changed result", result.Action)
	}
}

func TestUninstallClaudeLeavesUnadoptedRenamedEntry(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "mcpServers": {
    "deja-vu": {
      "command": "/hand/bin/deja",
      "args": ["serve"],
      "env": {
        "OWNER": "hand"
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := installClaude(filepath.Join(t.TempDir(), "deja"), true)
	if err != nil {
		t.Fatal(err)
	}

	servers := issue2269JSONServers(t, path, "mcpServers")
	if _, ok := servers["deja-vu"]; !ok {
		t.Fatalf("uninstall removed the unadopted deja-vu entry: %#v", servers)
	}
	if !strings.Contains(result.Note, `left "deja-vu"`) {
		t.Errorf("uninstall note does not name the remaining entry: %q", result.Note)
	}
}

func TestUninstallCodexRemovesAdoptedRenamedBlock(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `[mcp_servers.deja-vu]
command = "/old/bin/deja"
args = ["mcp"]
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodex(exe, true); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "[mcp_servers.deja-vu]") {
		t.Fatalf("uninstall left the adopted TOML block:\n%s", body)
	}
}

func TestUninstallClaudeRemovesOnlyAdoptedRenamedEntry(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "mcpServers": {
    "deja-vu": {
      "command": "/old/bin/deja",
      "args": ["mcp"],
      "env": {
        "OWNER": "adopted"
      }
    },
    "memory": {
      "command": "/hand/bin/deja",
      "args": ["serve"],
      "env": {
        "OWNER": "hand"
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installClaude(exe, false); err != nil {
		t.Fatal(err)
	}
	result, err := installClaude(exe, true)
	if err != nil {
		t.Fatal(err)
	}

	servers := issue2269JSONServers(t, path, "mcpServers")
	if _, ok := servers["deja-vu"]; ok {
		t.Fatalf("uninstall left the adopted deja-vu entry: %#v", servers)
	}
	if _, ok := servers["memory"]; !ok {
		t.Fatalf("uninstall removed the untouched memory entry: %#v", servers)
	}
	if !strings.Contains(result.Note, `left "memory"`) {
		t.Errorf("uninstall note does not name memory: %q", result.Note)
	}
	if strings.Contains(result.Note, "deja-vu") {
		t.Errorf("uninstall note names the removed entry: %q", result.Note)
	}
}
