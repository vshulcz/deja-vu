package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestInstallClaudeUpdatesDejaUnderItsExistingName(t *testing.T) {
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

	servers := issue2269JSONServers(t, path, "mcpServers")
	if len(servers) != 1 {
		t.Fatalf("install left %d entries, want one: %#v", len(servers), servers)
	}
	entry, ok := servers["deja-vu"].(map[string]any)
	if !ok {
		t.Fatalf("deja-vu entry missing: %#v", servers)
	}
	if _, ok := servers["deja"]; ok {
		t.Fatal("install added a second entry named deja")
	}
	want := mcpServerEntry(exe)
	for _, key := range []string{"type", "command", "args"} {
		if !sameEntry(entry[key], want[key]) {
			t.Errorf("%s = %#v, want %#v", key, entry[key], want[key])
		}
	}
	wantEnv := map[string]any{"DEJA_INDEX_DIR": "/memory"}
	if !sameEntry(entry["env"], wantEnv) {
		t.Errorf("env was not preserved: %#v", entry["env"])
	}
}

func TestInstallClaudePrefersTheLiteralDejaEntry(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "mcpServers": {
    "deja": {
      "command": "/old/bin/deja",
      "args": ["mcp"],
      "env": {"OWNER": "canonical"}
    },
    "deja-vu": {
      "command": "/hand/bin/deja",
      "args": ["serve"],
      "env": {"OWNER": "hand"}
    }
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeServers := issue2269JSONServers(t, path, "mcpServers")
	foreignBefore := beforeServers["deja-vu"]

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installClaude(exe, false); err != nil {
		t.Fatal(err)
	}

	servers := issue2269JSONServers(t, path, "mcpServers")
	if len(servers) != 2 {
		t.Fatalf("install left %d entries, want two: %#v", len(servers), servers)
	}
	if !sameEntry(servers["deja-vu"], foreignBefore) {
		t.Errorf("foreign entry changed:\ngot  %#v\nwant %#v", servers["deja-vu"], foreignBefore)
	}
	entry, ok := servers["deja"].(map[string]any)
	if !ok {
		t.Fatalf("literal deja entry missing: %#v", servers)
	}
	want := mcpServerEntry(exe)
	for _, key := range []string{"type", "command", "args"} {
		if !sameEntry(entry[key], want[key]) {
			t.Errorf("%s = %#v, want %#v", key, entry[key], want[key])
		}
	}
}

func TestInstallCodexUpdatesRenamedTOMLBlockInPlace(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `[mcp_servers.deja-vu]
command = "/old/bin/deja"
args = ["mcp"]
startup_timeout_ms = 20000
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "deja")
	result, err := installCodex(exe, false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if strings.Contains(got, "[mcp_servers.deja]\n") {
		t.Fatalf("install appended a literal deja block:\n%s", got)
	}
	if strings.Count(got, "[mcp_servers.") != 1 {
		t.Fatalf("install left more than one MCP block:\n%s", got)
	}
	for _, want := range []string{
		"[mcp_servers.deja-vu]",
		"startup_timeout_ms = 20000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("updated block lost %q:\n%s", want, got)
		}
	}
	command, args := mcpCommandArgs(exe)
	for _, want := range []string{
		fmt.Sprintf("command = %q", command),
		"args = " + tomlStringArray(args),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("updated block does not contain %q:\n%s", want, got)
		}
	}
	if !strings.Contains(result.Note, `"deja-vu"`) {
		t.Errorf("install note does not name the updated entry: %q", result.Note)
	}
}

func TestATOMLHeaderMayCarryAComment(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `[mcp_servers.deja-vu] # hand-wired
command = "/old/bin/deja"
args = ["mcp"]
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := doctorTOMLDejaKeys(path), []string{"deja-vu"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commented header keys = %#v, want %#v", got, want)
	}

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); strings.Contains(got, "[mcp_servers.deja]\n") ||
		strings.Count(got, "[mcp_servers.") != 1 {
		t.Fatalf("install did not update the commented block in place:\n%s", got)
	}
}

func TestDoctorDejaKeyProbesCountEntries(t *testing.T) {
	dir := t.TempDir()
	jsonProbe := doctorJSONDejaKeys("mcpServers")

	jsonTwo := filepath.Join(dir, "two.json")
	if err := os.WriteFile(jsonTwo, []byte(`{
  "mcpServers": {
    "deja-vu": {"command": "/opt/deja", "args": ["mcp"]},
    "deja": {"command": "/usr/local/bin/deja", "args": ["mcp"]}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := jsonProbe(jsonTwo), []string{"deja", "deja-vu"}; !reflect.DeepEqual(got, want) {
		t.Errorf("JSON two-entry keys = %#v, want %#v", got, want)
	}

	jsonOne := filepath.Join(dir, "one.json")
	if err := os.WriteFile(jsonOne, []byte(`{"mcpServers":{"memory":{"command":"deja","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := jsonProbe(jsonOne), []string{"memory"}; !reflect.DeepEqual(got, want) {
		t.Errorf("JSON one-entry keys = %#v, want %#v", got, want)
	}

	tomlTwo := filepath.Join(dir, "two.toml")
	if err := os.WriteFile(tomlTwo, []byte(`[mcp_servers.deja-vu]
command = "/opt/deja"
args = ["mcp"]

[mcp_servers.deja]
type = "stdio"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := doctorTOMLDejaKeys(tomlTwo), []string{"deja", "deja-vu"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TOML two-entry keys = %#v, want %#v", got, want)
	}

	tomlOne := filepath.Join(dir, "one.toml")
	if err := os.WriteFile(tomlOne, []byte(`[mcp_servers.memory]
command = "deja"
args = ["mcp"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := doctorTOMLDejaKeys(tomlOne), []string{"memory"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TOML one-entry keys = %#v, want %#v", got, want)
	}
}

func TestDoctorReportsTwoClaudeEntriesRunningDeja(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const config = `{
  "mcpServers": {
    "deja": {"command": "/usr/local/bin/deja", "args": ["mcp"]},
    "deja-vu": {"command": "/opt/deja", "args": ["mcp"]}
  }
}
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorMCP(&out)
	const want = "two entries in this config run deja (`deja`, `deja-vu`) — every session starts the server twice"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("doctor output does not contain the duplicate warning:\n%s", out.String())
	}
}

func TestUninstallClaudeLeavesAndNamesTheForeignEntry(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "mcpServers": {
    "deja": {
      "command": "/usr/local/bin/deja",
      "args": ["mcp"]
    },
    "deja-vu": {
      "command": "/hand/bin/deja",
      "args": ["serve"],
      "env": {"OWNER": "hand"}
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
	if _, ok := servers["deja"]; ok {
		t.Fatal("uninstall left the literal deja entry")
	}
	if _, ok := servers["deja-vu"]; !ok {
		t.Fatal("uninstall removed the hand-written deja-vu entry")
	}
	if !strings.Contains(result.Note, `left "deja-vu"`) ||
		!strings.Contains(result.Note, "uninstall does not remove it") {
		t.Errorf("uninstall note does not explain the remaining entry: %q", result.Note)
	}
}

func issue2269JSONServers(t *testing.T, path, key string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, body)
	}
	servers, ok := root[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object in %s: %#v", key, path, root[key])
	}
	return servers
}
