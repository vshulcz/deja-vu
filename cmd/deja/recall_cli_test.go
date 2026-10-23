package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A skill drives deja through the shell, and the shell had no answer an agent
// could read: `deja search --json` ran to megabytes. `deja recall` prints the
// page the MCP tool serves, word for word (#4781).
func TestRecallOnTheCommandLineIsTheMCPAnswer(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	writeClaudeFixture(t, filepath.Join(proj, "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"why did the zonkomatic export break"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the zonkomatic export needs CRLF line endings for the finance importer"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecall(dir, []string{"zonkomatic", "export"}, &out); err != nil {
		t.Fatal(err)
	}
	mcp, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"recall","q":"zonkomatic export"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CRLF") {
		t.Fatalf("the command served nothing from the fixture:\n%s", out.String())
	}
	// The MCP answer may carry the once-per-session environment block after
	// the frame; the page itself has to be the same.
	if !strings.HasPrefix(mcp, strings.TrimRight(out.String(), "\n")) {
		t.Errorf("the command and the tool disagree.\ncli:\n%s\nmcp:\n%s", out.String(), mcp)
	}
	if len(out.String()) > recallMCPBudget+512 {
		t.Errorf("answered %d bytes, past the tool's budget", out.Len())
	}

	// The same answer whichever way the flag is written, and a query may come
	// after -- even when it starts with a dash.
	for _, args := range [][]string{{"zonkomatic", "--limit=1", "export"}, {"--limit", "1", "--", "zonkomatic", "export"}} {
		var b bytes.Buffer
		if err := runRecall(dir, args, &b); err != nil || !strings.Contains(b.String(), "CRLF") {
			t.Errorf("recall %q: %v\n%s", args, err, b.String())
		}
	}
	if err := runRecall(dir, []string{"--", "--zonkomatic"}, &bytes.Buffer{}); err != nil {
		t.Errorf("a query after -- that starts with a dash was refused: %v", err)
	}

	for _, bad := range [][]string{nil, {"--limit"}, {"--limit", "0", "x"}, {"--nope", "x"}, {"--project", "", "x"}} {
		if err := runRecall(dir, bad, &bytes.Buffer{}); err == nil {
			t.Errorf("recall %q was accepted", bad)
		}
	}
}

// The page ends with "call recall again with offset=N". A plugin tool that
// serves this page through the shell had no way to ask for that offset, so the
// paging line pointed at nothing; --offset is the MCP tool's offset.
func TestRecallOnTheCommandLinePagesWithOffset(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	for _, s := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(proj, s+".jsonl"), s, []string{
			`{"type":"user","sessionId":"` + s + `","cwd":"/work/app","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the quorbleflax cache in ` + s + ` keeps evicting"}}`,
			`{"type":"assistant","sessionId":"` + s + `","cwd":"/work/app","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"quorbleflax eviction comes from the ttl in ` + s + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A plugin's child inherits COLUMNS; what it reads is still the page.
	t.Setenv("COLUMNS", "40")
	var first bytes.Buffer
	if err := runRecall(dir, []string{"--limit", "1", "quorbleflax"}, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "offset=1") {
		t.Fatalf("the first page names no offset to ask for:\n%s", first.String())
	}
	var second bytes.Buffer
	if err := runRecall(dir, []string{"--limit", "1", "--offset", "1", "quorbleflax"}, &second); err != nil {
		t.Fatalf("recall --offset 1: %v", err)
	}
	if second.String() == first.String() || !strings.Contains(second.String(), "offset=2") {
		t.Errorf("--offset 1 did not serve the next page:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
	mcp, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"recall","q":"quorbleflax","limit":1,"offset":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mcp, strings.TrimRight(second.String(), "\n")) {
		t.Errorf("the command and the tool disagree at offset 1.\ncli:\n%s\nmcp:\n%s", second.String(), mcp)
	}
	for _, bad := range [][]string{{"--offset", "-1", "x"}, {"--offset", "two", "x"}, {"x", "--offset"}} {
		if err := runRecall(dir, bad, &bytes.Buffer{}); err == nil {
			t.Errorf("recall %q was accepted", bad)
		}
	}
}

// A session that matched more often than the page quotes is named with an
// instruction to read the rest. It named recall_context, which the OpenClaw
// and Hermes plugins and the shell do not have; recall resolves the id to the
// same digest, and every caller has recall (#5579).
func TestRecallSendsADeepReadBackToRecall(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	lines := make([]string, 0, 2*deepReadFrom)
	for i := range 2 * deepReadFrom {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		lines = append(lines, fmt.Sprintf(`{"type":%q,"sessionId":"deep-session-01","cwd":"/work/app","timestamp":"2026-09-01T10:%02d:00Z","message":{"role":%q,"content":"the frobnicator stalls again, attempt %d"}}`, role, i, role, i))
	}
	writeClaudeFixture(t, filepath.Join(proj, "deep-session-01.jsonl"), "deep-session-01", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	if err := runRecall(dir, []string{"frobnicator"}, &page); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.String(), "call recall again with that id as the query") {
		t.Fatalf("the page names no way to read the rest of the session:\n%s", page.String())
	}
	if strings.Contains(page.String(), "recall_context") {
		t.Errorf("the page sends the caller to recall_context:\n%s", page.String())
	}
	var deep bytes.Buffer
	if err := runRecall(dir, []string{"deep-session-01"}, &deep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deep.String(), "# deja context") || !strings.Contains(deep.String(), "attempt 13") {
		t.Errorf("recall with the id did not read the session:\n%s", deep.String())
	}
}

// recall resolved an id only in the shape looksLikeSessionID accepts, so a
// session it names by an id outside that shape (a Devin subagent's carries a
// colon, this one has no digit at all) answered "nothing matched" where
// recall_context opened it (#5579).
func TestRecallOpensAnIDOutsideTheUsualShape(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	writeClaudeFixture(t, filepath.Join(proj, "qwertyzxcvbnm.jsonl"), "qwertyzxcvbnm", []string{
		`{"type":"user","sessionId":"qwertyzxcvbnm","cwd":"/work/app","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the gizmotron needs a warm cache"}}`,
		`{"type":"assistant","sessionId":"qwertyzxcvbnm","cwd":"/work/app","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"warmed the gizmotron cache before the deploy"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if looksLikeSessionID("qwertyzxcvbnm") {
		t.Fatal("the fixture id passes looksLikeSessionID, so this proves nothing")
	}
	var out bytes.Buffer
	if err := runRecall(dir, []string{"qwertyzxcvbnm"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# deja context") || !strings.Contains(out.String(), "gizmotron") {
		t.Errorf("recall with the id did not open the session:\n%s", out.String())
	}
}
