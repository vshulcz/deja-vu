package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Kimi Code 2.2 fires SessionEnd only when a session is closed, and `kimi -p`
// exits without closing one, so a finished print run kept its live stamp for
// twenty minutes and the next session's MCP recall left it out (#5145). Its
// Stop does fire, with this payload; it clears the stamp now, and deja's MCP
// tool call stamps the session again before recall reads the stamps.
func TestKimiStopClearsTheStampAndMCPCallRestampsIt(t *testing.T) {
	hermeticEnv(t)
	if _, err := installKimiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(sources.KimiConfigDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][2]string{
		{"event = \"Stop\"\ncommand = ", " hook-session-end\"\n"},
		{"event = \"PreToolUse\"\nmatcher = \"^mcp__deja__\"\ncommand = ", " hook-mcp-call\"\n"},
	} {
		found := false
		for _, table := range strings.Split(string(b), "[[hooks]]") {
			found = found || strings.Contains(table, want[0]) && strings.Contains(table, want[1])
		}
		if !found {
			t.Errorf("kimi-auto wrote no %q table running%s in:\n%s", want[0], want[1], b)
		}
	}

	dir := t.TempDir()
	const sid = "session_8ebaf6dc-4f8c-4c97-b1a4-6107e0d4b51d"
	markSessionLive(dir, sid)
	runHookSessionEnd(dir, strings.NewReader(`{"hook_event_name":"Stop","session_id":"`+sid+`","cwd":"/w/kite-app","client_type":"kimi_code_cli","stop_hook_active":false}`))
	if liveSessionIDs(dir)[sid] {
		t.Fatalf("kimi's Stop payload left the session stamped: %v", readLiveSessions(dir))
	}
	runHookMCPCall(dir, strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"`+sid+`","cwd":"/w/kite-app","tool_name":"mcp__deja__deja","tool_input":{"query":"HERON-B1"}}`), nil)
	if !liveSessionIDs(dir)[sid] {
		t.Errorf("deja's MCP call did not stamp the asking session: %v", readLiveSessions(dir))
	}
}
