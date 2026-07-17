package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The audit issue #46 asks for proof that no path from parser to disk skips
// redaction: message text, tool-result payloads and metadata like titles.
// Secrets are planted per harness (including a grok generated_title and a
// claude tool_result block), then every byte of the built index is scanned.
func TestNoPlaintextSecretReachesIndex(t *testing.T) {
	root, dir := allHarnessEnv(t)
	secrets := map[string]string{
		"claude-text":  "AKIAIOSFODNN7EXAMPL1",
		"claude-tool":  "ghp_toolresultsecret0123456789abcdef",
		"grok-title":   "sk-ant-api03-titlesecret0123456789",
		"aider-text":   "xoxb-1234567890-aidersecret",
		"codex-text":   "glpat-codexsecret0123456789",
		"gemini-text":  "hf_geminisecret0123456789abcd",
		"claude-title": "sk-proj-firsttitle0123456789abcdef",
	}
	write(t, filepath.Join(root, "claude", "-p-app", "s1.jsonl"),
		`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"leaked key `+secrets["claude-text"]+` in text"}}`+"\n"+
			`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"tool output with `+secrets["claude-tool"]+`"}]}]}}`+"\n")
	write(t, filepath.Join(root, "claude", "-p-app", "s2.jsonl"),
		`{"type":"user","sessionId":"s2","timestamp":"2026-02-01T11:00:00Z","message":{"role":"user","content":"please use `+secrets["claude-title"]+` to auth"}}`+"\n")
	write(t, filepath.Join(root, "codex", "sessions", "2026", "02", "01", "rollout-2026-02-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-02-01T10:00:00Z","payload":{"session_id":"x1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-02-01T10:00:01Z","payload":{"role":"user","content":"token `+secrets["codex-text"]+`"}}`+"\n")
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.json"),
		`{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"secret `+secrets["gemini-text"]+`"}]}`)
	write(t, filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
		"# aider chat started at 2026-02-01 10:00:00\n\n#### paste "+secrets["aider-text"]+" here\n\nok\n")
	grokDir := filepath.Join(root, "grok", "sessions", "%2Fp%2Fapp")
	write(t, filepath.Join(grokDir, "gr1", "summary.json"),
		`{"info":{"id":"gr1","cwd":"/p/app"},"generated_title":"session about `+secrets["grok-title"]+`","created_at":"2026-02-01T10:00:00Z","updated_at":"2026-02-01T10:05:00Z"}`)
	write(t, filepath.Join(grokDir, "gr1", "updates.jsonl"),
		`{"timestamp":1769940000000,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello grok"},"_meta":{"promptIndex":0}},"_meta":{"promptId":"p0"}}}`+"\n")

	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}

	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("index looks empty: %v", files)
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for name, secret := range secrets {
			if strings.Contains(string(b), secret) {
				t.Errorf("plaintext %s secret survived in %s", name, filepath.Base(p))
			}
		}
	}

	// Titles specifically: the grok generated_title and the first-message
	// title must come back scrubbed, not dropped.
	ss, err := Search(dir, search.Options{Query: "hello grok", All: true})
	if err != nil || len(ss) == 0 {
		t.Fatalf("grok session lost: %v %v", ss, err)
	}
	if !strings.Contains(ss[0].Title, "[redacted:") || strings.Contains(ss[0].Title, secrets["grok-title"]) {
		t.Fatalf("grok title not redacted: %q", ss[0].Title)
	}
}
