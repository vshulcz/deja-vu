package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bar generalised one rule to all of memory: an `auto` rule read as
// "activates nothing" while search and MCP still answered, and a `search` rule
// — the reader's own queries — produced no line at all (#1102).
func TestStatuslineNamesWhichPathsARuleSwitchesOff(t *testing.T) {
	tmp := hermeticEnv(t)
	pol := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	write := func(body string) {
		if err := os.WriteFile(pol, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(a string) string {
		return `{"activations":{"` + a + `":{"local":false,"imported":false}}}`
	}

	write(deny("auto"))
	if got := policyStatusLine(); got != "deja · auto off · the trust policy (`deja doctor`)" {
		t.Errorf("auto-only rule: %q", got)
	}
	write(deny("search"))
	if got := policyStatusLine(); !strings.Contains(got, "search off") {
		t.Errorf("a rule on the reader's own searches said: %q", got)
	}
	write(`{"activations":{"auto":{"local":false,"imported":false},"search":{"local":false,"imported":false},"mcp":{"local":false,"imported":false}}}`)
	if got := policyStatusLine(); !strings.Contains(got, "activates nothing") {
		t.Errorf("a rule that really does switch everything off: %q", got)
	}
	if err := os.Remove(pol); err != nil {
		t.Fatal(err)
	}
	if got := policyStatusLine(); got != "" {
		t.Errorf("no rule at all should leave the ordinary line: %q", got)
	}
}
