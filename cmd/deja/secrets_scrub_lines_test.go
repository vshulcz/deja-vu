package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures are put together from pieces so nothing in the source reads as
// a credential to a scanner.
const (
	fakePass = "hunter2" + "pass"
	fakeDSN  = "postgres://svc:" + fakePass + "@db.example.test/app"
	pemHead  = "-----BEGIN RSA " + "PRIVATE KEY-----"
	pemTail  = "-----END RSA " + "PRIVATE KEY-----"
)

// scrubFixture writes lines as a transcript aged past the busy window.
func scrubFixture(t *testing.T, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

// jsonLine encodes v the way the clients do, without HTML escaping.
func jsonLine(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func scrubLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func assertLinesParse(t *testing.T, lines []string) {
	t.Helper()
	for i, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("line %d no longer parses: %s", i+1, l)
		}
	}
}

// On disk an escaped quote is `\"`. The command-password value class stops at
// the quote and took the backslash with it, so the line stopped parsing (#5622).
func TestScrubKeepsAnEscapedQuoteInATranscriptLine(t *testing.T) {
	call := jsonLine(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{"content": []any{map[string]any{
			"type":  "tool_use",
			"name":  "Bash",
			"input": map[string]any{"command": "echo \"$(date -u +%H:%M:%SZ)\" \\\n  > /tmp/stamp && curl -u deploy:" + fakePass + " https://ci.example.test"},
		}}},
	})
	untouched := `{"type": "user", "message": {"content": "a < b, and \"quoted\""}}`
	p := scrubFixture(t, "s.jsonl", untouched, call, untouched)
	o := scrubOne(scrubTarget{path: p, id: "s", kinds: map[string]bool{"command-password": true}}, nil, false)
	if o.skipped != "" {
		t.Fatalf("skipped: %s", o.skipped)
	}
	lines := scrubLines(t, p)
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	assertLinesParse(t, lines)
	if lines[0] != untouched || lines[2] != untouched {
		t.Errorf("a line with nothing to redact was rewritten:\n%s\n%s", lines[0], lines[2])
	}
	if strings.Contains(lines[1], fakePass) {
		t.Errorf("the password survived: %s", lines[1])
	}
	var rec struct {
		Message struct {
			Content []struct {
				Input struct {
					Command string `json:"command"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &rec); err != nil || len(rec.Message.Content) != 1 {
		t.Fatalf("decode: %v", err)
	}
	if cmd := rec.Message.Content[0].Input.Command; !strings.Contains(cmd, "deploy:[redacted:command-password]") || !strings.HasPrefix(cmd, `echo "$(date -u +%H:`) {
		t.Errorf("command after scrub: %q", cmd)
	}
}

// pemPrivateRE spans lines. On the raw file it ran from a BEGIN marker in one
// record to an END marker records later and deleted everything between:
// 1040 lines came back as 572 (#5622).
func TestScrubPrivateKeyRuleStaysInsideOneString(t *testing.T) {
	key := pemHead + "\nMIIEpAIBAAKCAQEA7XqFAKEFAKEFAKEFAKEFAKE\nFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n" + pemTail
	lines := make([]string, 0, 9)
	lines = append(lines,
		jsonLine(t, map[string]any{"type": "user", "message": map[string]any{"content": "the file opens with " + pemHead + " and nothing else here"}}),
	)
	for i := range 6 {
		lines = append(lines, jsonLine(t, map[string]any{"type": "assistant", "n": i, "message": map[string]any{"content": "talking about key rotation"}}))
	}
	lines = append(lines,
		jsonLine(t, map[string]any{"type": "user", "message": map[string]any{"content": "and it closes with " + pemTail + " at the bottom"}}),
		jsonLine(t, map[string]any{"type": "tool_result", "content": "here it is:\n" + key + "\nend of file"}),
	)
	p := scrubFixture(t, "k.jsonl", lines...)
	o := scrubOne(scrubTarget{path: p, id: "k", kinds: map[string]bool{"private-key": true}}, nil, false)
	if o.skipped != "" {
		t.Fatalf("skipped: %s", o.skipped)
	}
	got := scrubLines(t, p)
	if len(got) != len(lines) {
		t.Fatalf("%d lines, want %d", len(got), len(lines))
	}
	assertLinesParse(t, got)
	for i := range len(lines) - 1 {
		if got[i] != lines[i] {
			t.Errorf("line %d changed:\n%s\n%s", i+1, lines[i], got[i])
		}
	}
	last := got[len(got)-1]
	if strings.Contains(last, "FAKEFAKE") || !strings.Contains(last, "[redacted:private-key]") {
		t.Errorf("the key in one string was not redacted: %s", last)
	}
	if !strings.Contains(last, "here it is:") || !strings.Contains(last, "end of file") {
		t.Errorf("the text around the key went with it: %s", last)
	}
}
