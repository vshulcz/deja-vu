package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The trust policy keys on which channel is asking: a machine may allow
// imported memory in its owner's own searches and deny it to an agent over MCP.
// `how` served both from one function with the search activation compiled in,
// so the MCP rule was never consulted — recall, asked the same question on the
// same machine, correctly said it was withholding something while how handed
// the imported command over.
func TestHowHonoursTheChannelsOwnPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written out rather than seeded: `how` answers from command records, and
	// only a Bash tool_use produces one.
	body := `{"type":"user","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"how do we ship the zonkomatic"}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "sess-one.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	cfg := filepath.Join(tmp, "config", "deja")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	// Denied over MCP, allowed for the owner's own searches. Keyed on "local"
	// because the seeded session is this machine's own; the split is what
	// matters, not which origin carries it.
	if err := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"mcp":{"local":false},"search":{"local":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	if policy.Load().Allows(policy.ActivationMCP, "app") {
		t.Fatal("the policy did not take effect, so this test would pass for the wrong reason")
	}
	if !policy.Load().Allows(policy.ActivationSearch, "app") {
		t.Fatal("the search side is denied too, so this cannot tell the two apart")
	}

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	entries, _, err := howEntries(dir, []string{"zonkomatic"}, "", policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the owner's own search returned nothing, so the fixture never had a command to withhold")
	}

	out, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replicas=9") {
		t.Errorf("how handed the command to the agent although the mcp policy denies it: %q", out)
	}
	// Filtering alone would turn the leak into a confident negative over records
	// that do exist, which is the failure emptyRecallAnswerPolicy and blame's
	// omission note were both written for.
	if strings.Contains(out, "No command on this machine") {
		t.Errorf("how told the agent nothing exists while the policy was hiding something: %q", out)
	}

	// The other direction, which absence alone cannot prove: with the mcp rule
	// allowing, the command must come back. Without this the test still passes
	// if how simply stops answering anyone.
	if err := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"mcp":{"local":true},"search":{"local":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "replicas=9") {
		t.Errorf("how withheld the command although the mcp policy allows it: %q", out)
	}
}
