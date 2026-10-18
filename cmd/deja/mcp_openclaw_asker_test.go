package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// openclawLine is one pi-shaped OpenClaw transcript message.
func openclawLine(id, role, text string, at time.Time) string {
	body, _ := json.Marshal(text)
	content := string(body)
	if role == "assistant" {
		content = `[{"type":"text","text":` + string(body) + `}]`
	}
	return fmt.Sprintf(`{"type":"message","id":%q,"timestamp":%q,"message":{"role":%q,"content":%s}}`, id, at.UTC().Format(time.RFC3339), role, content)
}

func writeOpenClawSession(t *testing.T, id string, started time.Time, lines ...string) {
	t.Helper()
	path := filepath.Join(sources.OpenClawRoot(), "main", "sessions", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":%q,"cwd":"/w/p"}`, id, started.UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(head+"\n"+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Written when its last line was, as OpenClaw appends them.
	last := started
	for _, l := range lines {
		var m struct{ Timestamp time.Time }
		if json.Unmarshal([]byte(l), &m) == nil && m.Timestamp.After(last) {
			last = m.Timestamp
		}
	}
	if err := os.Chtimes(path, last, last); err != nil {
		t.Fatal(err)
	}
}

// `openclaw chat` and `openclaw tui --local` run no plugin or hook, so nothing
// stamps the session live, and an MCP recall from inside it answered with that
// session first (#5271). With no OpenClaw session stamped, the newest one,
// written moments ago, is the one asking.
func TestOpenClawRecallLeavesOutAnUnstampedAsker(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "openclaw"))
	t.Cleanup(func() { setMCPClient("") })
	now := time.Now()
	old := now.Add(-3 * time.Hour)
	writeOpenClawSession(t, "prior-oc", old,
		openclawLine("a", "user", "pick the cache eviction policy for KESTREL-7", old),
		openclawLine("b", "assistant", "KESTREL-7 eviction is LRU with a 512 MB cap.", old.Add(time.Minute)))
	const asker = "416c6e1c-0938-435e-8c12-a3e0a84095db"
	writeOpenClawSession(t, asker, now.Add(-30*time.Second),
		openclawLine("c", "user", "what was the KESTREL-7 eviction policy we picked?", now.Add(-20*time.Second)))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	q := json.RawMessage(`{"query":"KESTREL-7 eviction"}`)
	recall := func(client string) string {
		t.Helper()
		setMCPClient(client)
		out, err := callMCPTool(dir, "recall", q)
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		return out
	}

	// Control: another host's recall, which has hooks to stamp its session,
	// is served the asker; this fixture can produce the bug.
	if out := recall("claude-code"); !strings.Contains(out, asker[:8]) {
		t.Fatalf("the fixture never served the asking session:\n%s", out)
	}
	out := recall("openclaw-bundle-mcp")
	if strings.Contains(out, asker[:8]) {
		t.Errorf("OpenClaw's recall answered with the session asking:\n%s", out)
	}
	if !strings.Contains(out, "512 MB") {
		t.Errorf("the session holding the answer is not on the page:\n%s", out)
	}

	// A stamp from before the asker's turn, a hooked `openclaw agent --local`
	// run a while back, says nothing about who is asking now.
	writeLiveSessions(dir, map[string]time.Time{"prior-oc": now.Add(-3 * time.Minute)})
	if out := recall("openclaw-bundle-mcp"); strings.Contains(out, asker[:8]) {
		t.Errorf("an older stamp turned the fallback off:\n%s", out)
	}
	// A session stamped as recently as the newest write means the hooks run,
	// and they name the asker.
	markSessionLive(dir, "prior-oc")
	if out := recall("openclaw-bundle-mcp"); !strings.Contains(out, asker[:8]) {
		t.Errorf("with the hooks stamping, the newest session was taken for the asker anyway:\n%s", out)
	}
}

// The newest OpenClaw session left a while ago is not the one asking: it is
// the one most likely to hold the answer.
func TestOpenClawRecallKeepsAnOlderNewestSession(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "openclaw"))
	t.Cleanup(func() { setMCPClient("") })
	at := time.Now().Add(-40 * time.Minute)
	writeOpenClawSession(t, "left-oc", at,
		openclawLine("a", "user", "pick the cache eviction policy for KESTREL-7", at),
		openclawLine("b", "assistant", "KESTREL-7 eviction is LRU with a 512 MB cap.", at.Add(time.Minute)))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	setMCPClient("openclaw-bundle-mcp")
	out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"KESTREL-7 eviction"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "512 MB") {
		t.Errorf("a session left 40 minutes ago was dropped as the asker:\n%s", out)
	}
}
