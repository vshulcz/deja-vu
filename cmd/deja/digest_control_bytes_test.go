package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Nothing deja serves carries a control byte, and two things rest on that. The
// old one is why the stripping exists: an escape byte recolours a terminal and
// a carriage return rewinds a line. The new one is arithmetic — a control byte
// is six bytes in JSON, and the injection log's size bound assumes three at
// worst (#1982), so one arriving would make the log's own guarantee wrong.
//
// Two packages strip them — `internal/search` for what search serves and
// `internal/redact` for what a digest is built from — and each has its own
// tests. This holds the paths between them: the writers of a snapshot,
// driven over a transcript full of what an agent captures from a terminal.
func TestNothingServedCarriesAControlByte(t *testing.T) {
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(claude, "-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Colour, a bell, and a rewind — captured terminal output, as a transcript
	// picks it up.
	dirty := "the build failed: \x1b[31mERROR\x1b[0m\x07 pgbouncer pool timed out\r and retried"
	line := func(role string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "s1", "cwd": "/tmp/app",
			"timestamp": "2026-08-20T10:00:00Z",
			"message":   map[string]any{"role": role, "content": dirty},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := line("user") + "\n" + line("assistant") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	idx := index.DefaultDir()
	// Every path below answers about a project, and this is the one the
	// fixture was written into.
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/app")

	for _, c := range []struct {
		name string
		args string
		tool string
	}{
		{"recall", `{"query":"pgbouncer"}`, "recall"},
		{"recall_context", `{"query":"pgbouncer"}`, "recall_context"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := callMCPTool(idx, c.tool, json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			// An answer that carries none of the transcript proves nothing
			// about stripping it.
			if !strings.Contains(out, "pgbouncer") {
				t.Fatalf("%s answered without the session in it:\n%q", c.name, out)
			}
			if at := controlByteAt(out); at >= 0 {
				t.Errorf("%s served byte 0x%02x at %d", c.name, out[at], at)
			}
		})
	}

	t.Run("the session start", func(t *testing.T) {
		withHookStdin(t, `{"source":"startup","session_id":"ses1","cwd":"/tmp/app"}`)
		out := captureStdout(t, func() {
			if err := runHookContext(idx, true); err != nil {
				t.Error(err)
			}
		})
		if !strings.Contains(out, "pgbouncer") {
			t.Fatalf("the hook injected nothing about this project:\n%q", out)
		}
		if at := controlByteAt(out); at >= 0 {
			t.Errorf("the session start served byte 0x%02x at %d", out[at], at)
		}
	})

	t.Run("the prompt hook", func(t *testing.T) {
		var b strings.Builder
		if err := runHookPromptMode(idx, strings.NewReader(`{"prompt":"pgbouncer pool","session_id":"ses2"}`), &b, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "pgbouncer") {
			t.Fatalf("the hook injected nothing:\n%q", b.String())
		}
		if at := controlByteAt(b.String()); at >= 0 {
			t.Errorf("the prompt hook served byte 0x%02x at %d", b.String()[at], at)
		}
	})

	t.Run("a resource read", func(t *testing.T) {
		got, code, msg := mcpResourceRead(idx, "deja://session/claude:s1")
		if code != 0 {
			t.Fatalf("resource read failed: %d %s", code, msg)
		}
		text := resourceText(t, got)
		if !strings.Contains(text, "pgbouncer") {
			t.Fatalf("the resource answered without the session in it:\n%q", text)
		}
		if at := controlByteAt(text); at >= 0 {
			t.Errorf("the resource served byte 0x%02x at %d", text[at], at)
		}
	})

	t.Run("the antigravity hook", func(t *testing.T) {
		var b strings.Builder
		if err := runHookAntigravity(idx, strings.NewReader(`{"invocation_num":1,"workspace_paths":["/tmp/app"]}`), &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "pgbouncer") {
			t.Fatalf("the hook injected nothing about this project:\n%q", b.String())
		}
		if at := controlByteAt(b.String()); at >= 0 {
			t.Errorf("the antigravity hook served byte 0x%02x at %d", b.String()[at], at)
		}
	})

	// And the records those paths left behind, which is where the size bound
	// reads them.
	snaps := usage.Snapshots(idx, 0)
	if len(snaps) == 0 {
		t.Fatal("nothing was recorded, so the digests were not checked")
	}
	for _, s := range snaps {
		if at := controlByteAt(s.Digest); at >= 0 {
			t.Errorf("a %s digest carries byte 0x%02x at %d", s.Kind, s.Digest[at], at)
		}
	}
}

// controlByteAt is the offset of the first control character no served text
// should carry, or -1. Newline and tab are what a transcript is made of.
//
// Runes rather than bytes: the C1 controls at U+0080–U+009F are two bytes in
// UTF-8, and a terminal acts on some of them — U+009B is a control sequence
// introducer as surely as an escape is. A byte scan would walk past them.
func controlByteAt(s string) int {
	for i, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return i
		}
	}
	return -1
}

// resourceText digs the served text out of a resources/read answer.
func resourceText(t *testing.T, got any) string {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("resource answer is %T", got)
	}
	contents, ok := m["contents"].([]map[string]any)
	if !ok {
		t.Fatalf("resource contents are %T", m["contents"])
	}
	if len(contents) == 0 {
		t.Fatal("the resource answered with no contents")
	}
	text, _ := contents[0]["text"].(string)
	return text
}
