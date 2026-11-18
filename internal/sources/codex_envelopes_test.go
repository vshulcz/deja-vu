package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)


// Codex VS Code wraps every real input in an IDE-context envelope
// ('# Context from my IDE setup:' … '## My request:'). Only the request body
// is the person's words; the envelope itself must never become the prompt.
// The shapes below are the ones measured on a real store of 19 rollouts /
// 716 wrapped messages (Nwflower/dsh-chat-import#114).

func codexParseLines(t *testing.T, lines []string) model.Session {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-19T10-29-00-abc.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodexRollout: err=%v len=%d", err, len(ss))
	}
	return ss[0]
}

func codexCollectUserTexts(s model.Session) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == "user" {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestCodexIdeSetupRequestKept(t *testing.T) {
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: src/main.cpp\n## Open tabs:\n- a.cpp: src/a.cpp\n\n## My request:\n先看一下入口函数\n"}]}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"入口在 src/main.cpp。"}]}}`,
		`{"timestamp":"2026-10-19T10:31:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Open tabs:\n- a.cpp: src/a.cpp\n\n## My request:\n没有活动文件时的包装\n"}]}}`,
	})
	got := codexCollectUserTexts(s)
	want := []string{"先看一下入口函数", "没有活动文件时的包装"}
	if len(got) != len(want) {
		t.Fatalf("user texts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("user texts = %#v, want %#v", got, want)
		}
	}
	var gotAssistant bool
	for _, m := range s.Messages {
		if m.Role == "assistant" && m.Text == "入口在 src/main.cpp。" {
			gotAssistant = true
		}
	}
	if !gotAssistant {
		t.Fatalf("assistant message lost: %#v", s.Messages)
	}
}

func TestCodexIdeSetupContextOnlyDropped(t *testing.T) {
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Open tabs:\n- a.cpp: src/a.cpp"}]}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"收到。"}]}}`,
	})
	if got := codexCollectUserTexts(s); len(got) != 0 {
		t.Fatalf("context-only wrapper stored as user text: %#v", got)
	}
	var gotAssistant bool
	for _, m := range s.Messages {
		if m.Role == "assistant" && m.Text == "收到。" {
			gotAssistant = true
		}
	}
	if !gotAssistant {
		t.Fatalf("assistant message lost: %#v", s.Messages)
	}
}

func TestCodexIdeSetupVariants(t *testing.T) {
	const meta = `{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`
	cases := []struct{ name, line, want string }{
		{
			// CRLF rollout: the marker line ends '\r\n' and the body keeps it.
			name: "crlf",
			line: `{"timestamp":"2026-10-19T10:32:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\r\n\r\n## Active file: src/b.cpp\r\n## Open tabs:\r\n- b.cpp: src/b.cpp\r\n\r\n## My request:\r\nCRLF 换行也要认\r\n"}]}}`,
			want: "CRLF 换行也要认",
		},
		{
			// A pasted PR template: the body carries its own '##' headings and
			// only the line-start '## My request:' marker may split it.
			name: "template body",
			line: `{"timestamp":"2026-10-19T10:33:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: .github/pull_request_template.md\n## Open tabs:\n- pr.md: .github/pull_request_template.md\n\n## My request:\n帮我把这段填一下\n\n## Description\n某个改动\n\n### Validation\n跑过测试\n"}]}}`,
			want: "帮我把这段填一下\n\n## Description\n某个改动\n\n### Validation\n跑过测试",
		},
		{
			// The prefix match is case-insensitive; the marker stays line-start.
			name: "uppercase prefix",
			line: `{"timestamp":"2026-10-19T10:34:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# CONTEXT FROM MY IDE SETUP:\n## My request:\n大小写不敏感"}]}}`,
			want: "大小写不敏感",
		},
		{
			// Plain text passes through untouched.
			name: "plain text",
			line: `{"timestamp":"2026-10-19T10:35:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"普通提问"}]}}`,
			want: "普通提问",
		},
		{
			// An envelope that does not open the text is ordinary content.
			name: "mid-text prefix",
			line: `{"timestamp":"2026-10-19T10:36:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"先看这个：# Context from my IDE setup:\n## My request:\n不在开头"}]}}`,
			want: "先看这个：# Context from my IDE setup:\n## My request:\n不在开头",
		},
	}
	for _, tc := range cases {
		s := codexParseLines(t, []string{meta, tc.line})
		got := codexCollectUserTexts(s)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s: user texts = %#v, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCodexIdeSetupMixedBlocks(t *testing.T) {
	// An annotation block beside a wrapped request: blocks unwrap one by one,
	// so both survive. Joined first, the annotation header would hide the
	// envelope from the prefix check and the whole wrapper would be stored.
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Response annotations:\nEach item contains text selected from an earlier Codex response.\n<response-annotations>[{\"text\":\"这段要改\"}]</response-annotations>"},{"type":"input_text","text":"# Context from my IDE setup:\n\n## My request:\n按批注改"}]}}`,
	})
	got := codexCollectUserTexts(s)
	want := "# Response annotations:\nEach item contains text selected from an earlier Codex response.\n<response-annotations>[{\"text\":\"这段要改\"}]</response-annotations>\n按批注改"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("user texts = %#v, want %q", got, want)
	}
}
