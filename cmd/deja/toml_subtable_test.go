package main

import (
	"strings"
	"testing"
)

// TOML lets a server carry sub-tables, and their headers open with `[` too.
// Removing a block by walking to the next such line stopped at the sub-table
// and left it behind, naming a server that had just gone (#2716).
func TestRemovingABlockTakesItsSubTables(t *testing.T) {
	const cfg = `model = "gpt-5"

[mcp_servers.deja]
command = "/usr/local/bin/deja"
args = ["mcp"]

[mcp_servers.deja.env]
DEJA_INDEX_DIR = "/tmp/idx"

[mcp_servers.memory]
command = "/usr/local/bin/mcp-memory"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	if strings.Contains(got, "[mcp_servers.deja]") {
		t.Errorf("the block itself survived:\n%s", got)
	}
	if strings.Contains(got, "deja.env") {
		t.Errorf("a sub-table was left naming a server that is gone:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.memory]") {
		t.Errorf("another server went with it:\n%s", got)
	}
	if !strings.Contains(got, `model = "gpt-5"`) {
		t.Errorf("the reader's own settings went:\n%s", got)
	}
}

// The dot is what decides. A bare prefix test would take a differently named
// server with it, which is the failure this rule has to avoid while fixing the
// other one.
func TestASimilarlyNamedServerStays(t *testing.T) {
	const cfg = `[mcp_servers.deja]
command = "/usr/local/bin/deja"

[mcp_servers.deja-vu]
command = "/hand/bin/deja"

[mcp_servers.dejavu-notes]
command = "/hand/bin/notes"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	for _, keep := range []string{"[mcp_servers.deja-vu]", "[mcp_servers.dejavu-notes]"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s was removed with the block that merely shares its opening:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "/usr/local/bin/deja") {
		t.Errorf("the block deja owns survived:\n%s", got)
	}
}

// A sub-table of another server is not this one's to take, whichever order the
// file happens to hold them in.
func TestAnotherServersSubTableIsLeftAlone(t *testing.T) {
	const cfg = `[mcp_servers.deja]
command = "/usr/local/bin/deja"

[mcp_servers.memory.env]
KEY = "1"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	if !strings.Contains(got, "[mcp_servers.memory.env]") {
		t.Errorf("another server's sub-table was taken:\n%s", got)
	}
	if !strings.Contains(got, `KEY = "1"`) {
		t.Errorf("its body went too:\n%s", got)
	}
}
