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
	body, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
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
		var m struct {
			Timestamp time.Time `json:"timestamp"`
		}
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

func claudeLine(id, role, cwd, text string, at time.Time) string {
	body, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"type":%q,"sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":%q,"content":%s}}`,
		role, id, cwd, at.UTC().Format(time.RFC3339), role, string(body))
}

func appendClaudeLine(t *testing.T, path, line string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// An MCP-only install (`deja install claude-code`, without `-auto`) writes no
// hook entries, so nothing marks the live session, and recall answered with
// the conversation asking (#6027) — even when the prompt line was appended to
// JSONL after the initial index build.
func TestMCPOnlyClaudeRecallAndContextLeaveOutUnstampedAsker(t *testing.T) {
	tmp := hermeticEnv(t)
	hostsNoWarmup(t)
	t.Cleanup(func() { setMCPClient("") })
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "billing")
	otherProj := filepath.Join(tmp, "work", "analytics")
	for _, p := range []string{proj, otherProj} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(proj)

	now := time.Now()
	old := now.Add(-2 * time.Hour)
	priorPath := filepath.Join(root, "billing", "prior-cc.jsonl")
	appendClaudeLine(t, priorPath, claudeLine("prior-cc", "user", proj, "decide the retry ceiling for HALCYON-9 webhooks", old), old)
	appendClaudeLine(t, priorPath, claudeLine("prior-cc", "assistant", proj, "HALCYON-9 webhook retries cap at 4 attempts with exponential jitter.", old.Add(time.Minute)), old.Add(time.Minute))

	// A session finished 2 minutes ago in the same project must still be
	// eligible when a newer session asks right after it.
	recentDone := now.Add(-2 * time.Minute)
	donePath := filepath.Join(root, "billing", "done-cc.jsonl")
	appendClaudeLine(t, donePath, claudeLine("done-cc", "user", proj, "verify HALCYON-9 dead-letter routing", recentDone), recentDone)
	appendClaudeLine(t, donePath, claudeLine("done-cc", "assistant", proj, "HALCYON-9 dead-letter routing goes to billing-dlq.", recentDone.Add(10*time.Second)), recentDone.Add(10*time.Second))

	// A brand-new session in ANOTHER project must not be mistaken for the asker
	// in `proj`, nor hide `done-cc` if `done-cc` were the asker.
	otherNow := now.Add(-2 * time.Second)
	otherPath := filepath.Join(root, "analytics", "other-cc.jsonl")
	appendClaudeLine(t, otherPath, claudeLine("other-cc", "user", otherProj, "unrelated analytics note", otherNow), otherNow)

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Simulate the asking session created and appended AFTER initial index
	// build, right before the MCP tool call arrives (so EnsureForSearch picks
	// up the new turn first, and liveLineageCut must run after it).
	const asker = "88a1bc23-4d5e-4f60-9a11-77bc88de9900"
	askAt := now.Add(-5 * time.Second)
	askerPath := filepath.Join(root, "billing", asker+".jsonl")
	appendClaudeLine(t, askerPath, claudeLine(asker, "user", proj, "check HALCYON-9 retry ceiling and dead-letter routing", askAt), askAt)

	for _, client := range []string{"claude-code", "Claude Code"} {
		setMCPClient(client)
		out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"HALCYON-9 retry ceiling"}`))
		if err != nil {
			t.Fatalf("recall (%s): %v", client, err)
		}
		if strings.Contains(out, asker[:8]) {
			t.Errorf("recall (%s) answered with the unstamped asking session:\n%s", client, out)
		}
		if !strings.Contains(out, "4 attempts") {
			t.Errorf("recall (%s) missed the prior session holding the answer:\n%s", client, out)
		}

		// Two recent sessions in the same project: only the newest asking
		// session is dropped; the 2-minute-old finished session still answers.
		outDLQ, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"HALCYON-9 dead-letter routing"}`))
		if err != nil {
			t.Fatalf("recall dlq (%s): %v", client, err)
		}
		if strings.Contains(outDLQ, asker[:8]) {
			t.Errorf("recall dlq (%s) returned the asking session:\n%s", client, outDLQ)
		}
		if !strings.Contains(outDLQ, "billing-dlq") {
			t.Errorf("recall dlq (%s) dropped the 2-minute-old finished session too:\n%s", client, outDLQ)
		}

		ctxOut, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"context","query":"HALCYON-9 retry ceiling"}`))
		if err != nil {
			t.Fatalf("context (%s): %v", client, err)
		}
		if strings.Contains(ctxOut, asker[:8]) {
			t.Errorf("context (%s) answered with the unstamped asking session:\n%s", client, ctxOut)
		}
		if !strings.Contains(ctxOut, "4 attempts") {
			t.Errorf("context (%s) missed the prior session holding the answer:\n%s", client, ctxOut)
		}
	}

	// When the asking session is the ONLY session mentioning a brand-new token,
	// recall must report no match instead of echoing the prompt back.
	soleAt := now.Add(-1 * time.Second)
	appendClaudeLine(t, askerPath, claudeLine(asker, "user", proj, "investigate ZIRCON-7741 handshake timeout", soleAt), soleAt)
	setMCPClient("claude-code")
	soleOut, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"ZIRCON-7741"}`))
	if err != nil {
		t.Fatalf("sole recall: %v", err)
	}
	if strings.Contains(soleOut, asker[:8]) || strings.Contains(soleOut, "handshake timeout") {
		t.Errorf("sole-token recall echoed the asking session back:\n%s", soleOut)
	}
}

func TestMCPClientHarnessesMapping(t *testing.T) {
	cases := []struct {
		client string
		want   string
	}{
		{"claude-code", "claude"},
		{"Claude Code", "claude"},
		{"codex-mcp-client", "codex"},
		{"cursor-vscode", "cursor"},
		{"gemini-cli", "gemini"},
		{"qwen-code", "qwen"},
		{"opencode", "opencode"},
		{"roo-cline", "roo"},
		{"zoo-code", "roo"},
		{"kilo-code", "kilocode"},
		{"openclaw-bundle-mcp", "openclaw"},
		{"", ""},
		{"deja-shell-verify", ""},
		{"unknown-custom-client", ""},
	}
	for _, tc := range cases {
		got := strings.Join(mcpClientHarnesses(tc.client), ",")
		if got != tc.want {
			t.Errorf("mcpClientHarnesses(%q) = %q, want %q", tc.client, got, tc.want)
		}
	}
}
