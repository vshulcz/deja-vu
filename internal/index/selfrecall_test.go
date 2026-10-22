package index

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// Most harnesses prepend the recall block to a real user turn. Dropping the
// whole message would therefore lose the question that started the session —
// the block goes, the question stays.
func TestStripSelfRecallKeepsWhatTheUserWrote(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"no block": {"just a question", "just a question"},
		"block then question": {
			"<deja-recall>\nRecalled history…\n- Session: **x** `1`\n</deja-recall>\nwhy is the pool exhausted?",
			"why is the pool exhausted?",
		},
		"question then block": {
			"why is the pool exhausted?\n<deja-recall>\nstuff\n</deja-recall>",
			"why is the pool exhausted?",
		},
		"block in the middle": {
			"before\n\n<deja-recall>\nstuff\n</deja-recall>\n\nafter",
			"before\n\nafter",
		},
		"two blocks": {
			"<deja-recall>a</deja-recall>keep<deja-recall>b</deja-recall>",
			"keep",
		},
		// gemini stores the markers HTML-escaped inside its hook_context
		// wrapper. A filter that only knew the raw form left every gemini
		// session polluted while appearing to work everywhere else.
		"html-escaped block": {
			"&lt;deja-recall&gt;\nRecalled history…\n&lt;/deja-recall&gt;\nthe real question",
			"the real question",
		},
		"nothing but the block": {
			"<deja-recall>\nRecalled history…\n</deja-recall>",
			"",
		},
		// A truncated block means a context cap or a crash cut it off.
		// Everything from the marker onward is ours either way.
		"unclosed block": {
			"real question\n<deja-recall>\nrecalled and then cut off",
			"real question",
		},
	} {
		if got := stripSelfRecall(tc.in); got != tc.want {
			t.Fatalf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

// The whole point: what deja injects must not come back as what deja knows.
func TestIndexDoesNotSwallowItsOwnRecall(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	// A transcript exactly as a harness writes it: deja's block, then the real
	// user turn.
	// The newlines are escaped because the fixture interpolates straight into
	// a JSON line.
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-02-01T00:01:00Z",
			`<deja-recall>\nRecalled history from prior sessions.\n- Session: **old** `+"`abc`"+` INJECTEDTOKEN\n</deja-recall>\nGENUINEQUESTION about the pool`))

	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	// The user's own words are still findable.
	got, err := Search(dir, query.Options{Query: "GENUINEQUESTION", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("stripping the recall block took the user's question with it")
	}

	// deja's injection is not.
	for _, term := range []string{"INJECTEDTOKEN", "deja-recall"} {
		hits, err := Search(dir, query.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) > 0 {
			t.Fatalf("%q was indexed from deja's own recall block: %d hits", term, len(hits))
		}
	}

	// And the stored text carries no trace, so `deja show` and share do not
	// leak it either.
	s, ok, err := FindByPrefix(dir, "s1")
	if err != nil || !ok {
		t.Fatalf("session missing: ok=%v err=%v", ok, err)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "deja-recall") || strings.Contains(m.Text, "INJECTEDTOKEN") {
			t.Fatalf("recall block survived into the stored message: %q", m.Text)
		}
	}
}

// Codex injects its preamble as a user turn, so it survives any role filter.
// It is stripped only at the head of a message: the tags are ordinary enough
// that people quote them, and nine Claude sessions on the store this was
// measured against do exactly that (#636).
func TestStripsCodexPreambleOnlyAtTheHead(t *testing.T) {
	preamble := "<environment_context>\n  <cwd>/w</cwd>\n</environment_context>\n" +
		"<recommended_plugins>a long list</recommended_plugins>\n" +
		"# AGENTS.md instructions\n\n<INSTRUCTIONS>project rules</INSTRUCTIONS>\n"
	got := stripSelfRecall(preamble + "the actual question")
	if strings.TrimSpace(got) != "the actual question" {
		t.Fatalf("got %q, want only the question", got)
	}
	if got := strings.TrimSpace(stripSelfRecall(preamble)); got != "" {
		t.Fatalf("a message that is nothing but preamble should strip to empty, got %q", got)
	}
	// A person discussing the tag keeps their sentence.
	for _, real := range []string{
		"why does <environment_context> outrank everything?",
		"the parser should drop <user_instructions> blocks",
		"see the note about # AGENTS.md instructions in the issue",
		// A complete pair quoted mid-sentence — the shape that a
		// strip-anywhere rule eats and a strip-at-the-head rule keeps. Real
		// messages on this store look exactly like this.
		"the injected block is <environment_context><cwd>/w</cwd></environment_context> and it wins on match count",
		"codex sends <recommended_plugins>a list</recommended_plugins> before the first turn",
	} {
		if got := stripSelfRecall(real); got != real {
			t.Errorf("ate a real message:\n  in  %q\n  out %q", real, got)
		}
	}
}

// The AGENTS.md heading only introduces the injected block when the block
// follows it immediately. As a plain open/close pair it spanned to the next
// </INSTRUCTIONS> anywhere in the message, so a person asking how to stop
// Codex injecting these lost their entire question — the exact message someone
// writes when reporting this.
func TestAgentsHeadingNeedsItsBlockToFollow(t *testing.T) {
	real := []string{
		"# AGENTS.md instructions\n\nHow do I stop Codex injecting these? Mine is:\n<INSTRUCTIONS>be terse</INSTRUCTIONS>\n",
		"# AGENTS.md instructions are confusing.\n\nWhy does the block end with </INSTRUCTIONS>?",
		"# AGENTS.md instructions\n\nno block at all here",
	}
	for _, in := range real {
		if got := stripSelfRecall(in); got != in {
			t.Errorf("ate a real message:\n  in  %q\n  out %q", in, got)
		}
	}
	injected := "# AGENTS.md instructions\n\n<INSTRUCTIONS>project rules</INSTRUCTIONS>\nthe real question"
	if got := strings.TrimSpace(stripSelfRecall(injected)); got != "the real question" {
		t.Fatalf("got %q", got)
	}
}

// Nested copies of the same tag must be counted, or the outer closer is left
// behind in the index.
func TestPrefixStripCountsNesting(t *testing.T) {
	got := stripSelfRecall("<environment_context>a<environment_context>b</environment_context>c</environment_context>tail")
	if got != "tail" {
		t.Fatalf("got %q, want the whole nested block gone", got)
	}
}

// A truncated rollout, or a Codex release that renames a closing tag, leaves
// an unclosed block. Stripping to the end of the message would delete real
// text that follows, so the block stays — deliberately, and stated here so a
// future change to that behaviour is a decision rather than a slip.
func TestUnclosedPrefixBlockIsLeftAlone(t *testing.T) {
	in := "<environment_context>\n<cwd>/w</cwd>\nthe file was cut here"
	if got := stripSelfRecall(in); got != in {
		t.Fatalf("got %q", got)
	}
}

// The preamble class is not Codex-specific. Looking only where a contributor
// pointed left Cursor and Gemini titling every session with plumbing: 13 of 13
// and 9 of 9 on this machine.
func TestStripsCursorAndGeminiWrappers(t *testing.T) {
	// Cursor wraps the real question; the tags go, the question stays.
	cursor := "<timestamp>Friday, Jul 31, 2026, 2:07 PM (UTC+3)</timestamp>\n<user_query>\nCreate notes.txt containing alpha\n</user_query>"
	if got := strings.TrimSpace(stripSelfRecall(cursor)); got != "Create notes.txt containing alpha" {
		t.Fatalf("cursor: got %q", got)
	}
	// Gemini opens with its own context block.
	gemini := "<session_context>\nThis is the Gemini CLI. We are setting up the context for our chat.\nMy operating system is: darwin\n</session_context>\nwhy is the pool sized like this"
	if got := strings.TrimSpace(stripSelfRecall(gemini)); got != "why is the pool sized like this" {
		t.Fatalf("gemini: got %q", got)
	}
	// A Gemini sub-agent's first turn opens with the project and extension
	// memory, deja's own MCP instructions among it, as a part of its own
	// ahead of the task; it titled every child session (#5214).
	child := "\n<loaded_context>\n<project_context>\nThe following are instructions provided by the tool server 'deja':\n---[start of server instructions]---\ndeja indexes this user's past sessions\n---[end of server instructions]---\n</project_context>\n</loaded_context>\nCreate the child file."
	if got := strings.TrimSpace(stripSelfRecall(child)); got != "Create the child file." {
		t.Fatalf("gemini sub-agent: got %q", got)
	}
	// A person quoting the tag keeps the sentence.
	quoted := "why does every child start with <loaded_context>? </loaded_context> is the end"
	if got := stripSelfRecall(quoted); got != quoted {
		t.Fatalf("a quoted <loaded_context> was stripped: %q", got)
	}
	// The compaction preamble titled real sessions.
	compacted := "This session is being continued from a previous conversation that ran out of context.\nfix the retry loop"
	if got := strings.TrimSpace(stripSelfRecall(compacted)); got != "fix the retry loop" {
		t.Fatalf("compaction preamble: got %q", got)
	}
}

// deja's own injected recall came back through the front door: Gemini stores
// it wrapped in <hook_context> and HTML-escaped, so the marker filter never
// saw it and deja indexed its own output as if a person had written it.
func TestUnwrapsHookContextAroundOwnRecall(t *testing.T) {
	in := "hi <hook_context>&lt;deja-recall&gt;\nRecalled history from prior sessions.\n1. [claude] proj · abc\n&lt;/deja-recall&gt;</hook_context>"
	got := strings.TrimSpace(stripSelfRecall(in))
	if got != "hi" {
		t.Fatalf("got %q, want only what the person typed", got)
	}
	for _, leak := range []string{"deja-recall", "Recalled history", "hook_context"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived into the index", leak)
		}
	}
}

// Unwrapping keeps text; stripping removes it. A wrapper whose contents are
// the person's words must never be dropped whole.
func TestUnwrapBlockKeepsContents(t *testing.T) {
	if got := unwrapBlock("a <user_query>the question</user_query> b", "<user_query>", "</user_query>"); got != "a the question b" {
		t.Fatalf("got %q", got)
	}
	// Unclosed: leave it alone rather than eating the rest of the message.
	in := "a <user_query>the question"
	if got := unwrapBlock(in, "<user_query>", "</user_query>"); got != in {
		t.Fatalf("got %q", got)
	}
	// Repeated wrappers all unwrap.
	got := unwrapBlock("<user_query>one</user_query> and <user_query>two</user_query>", "<user_query>", "</user_query>")
	if got != "one and two" {
		t.Fatalf("got %q", got)
	}
}

// A tool result that is nothing but deja's own recall block strips to empty,
// and a rebuild writes no record for it (#551). The pass that re-reads a
// changed transcript wrote it anyway, an empty tool-output between the turns
// (#5555). Each case rewrites its file in place, so the pass is the
// replacement one, not an append.
func TestAReReadDropsAToolResultThatIsOnlyRecall(t *testing.T) {
	const sid = "11111111-2222-4333-8444-555555555555"
	recall := "<deja-recall>\nRecalled history from prior sessions\n- example hit\n</deja-recall>"
	at := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		harness, id string
		path        func() string
		body, extra string
	}{
		{
			harness: "claude", id: sid,
			path: func() string { return filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "demo", sid+".jsonl") },
			body: hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u1", "timestamp": "2026-10-15T01:00:00.000Z", "cwd": "/tmp/demo",
				"message": map[string]any{"role": "user", "content": "what did we decide about the cache?"}}) +
				hjson(map[string]any{"type": "assistant", "sessionId": sid, "uuid": "a1", "parentUuid": "u1", "timestamp": "2026-10-15T01:00:01.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "mcp__deja__deja", "input": map[string]any{"mode": "recall", "q": "cache"}}}}}) +
				hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u2", "parentUuid": "a1", "timestamp": "2026-10-15T01:00:02.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": "toolu_1", "type": "tool_result", "content": []any{map[string]any{"type": "text", "text": recall}}}}}}) +
				hjson(map[string]any{"type": "assistant", "sessionId": sid, "uuid": "a2", "parentUuid": "u2", "timestamp": "2026-10-15T01:00:03.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "We decided to keep the cache."}}}}),
			extra: hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u3", "parentUuid": "a2", "timestamp": "2026-10-15T01:00:04.000Z", "cwd": "/tmp/demo",
				"message": map[string]any{"role": "user", "content": "more"}}),
		},
		{
			harness: "codex", id: sid,
			path: func() string {
				return filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "10", "15", "rollout-2026-10-15T01-00-00-"+sid+".jsonl")
			},
			body: codexLine(at, "session_meta", map[string]any{"id": sid, "cwd": "/tmp/demo"}) +
				codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": "what did we decide about the cache?"}}}) +
				codexLine(at.Add(time.Second), "response_item", map[string]any{"type": "function_call", "name": "mcp__deja__deja", "call_id": "c1", "arguments": `{"mode":"recall","q":"cache"}`}) +
				codexLine(at.Add(2*time.Second), "response_item", map[string]any{"type": "function_call_output", "call_id": "c1", "output": recall}) +
				codexLine(at.Add(3*time.Second), "response_item", map[string]any{"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "We decided to keep the cache."}}}),
			extra: codexLine(at.Add(4*time.Second), "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "more"}}}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			dir := filepath.Join(tmp, "idx")
			p := tc.path()
			write(t, p, tc.body)
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			appendTo(t, p, tc.extra)
			indexPass(t, dir)
			write(t, p, tc.body)
			bumpMTime(t, p)
			var progress strings.Builder
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), replacementPassMarker) {
				t.Fatalf("the restore did not take the re-read pass, so this measures nothing: %q", progress.String())
			}
			s, ok, err := FindByIdentity(dir, tc.harness, tc.id)
			if err != nil || !ok {
				t.Fatalf("session gone after the re-read (%v)", err)
			}
			for _, m := range s.Messages {
				if strings.TrimSpace(m.Text) == "" {
					t.Errorf("the re-read kept an empty %s record", m.Role)
				}
			}
			sessionMatchesRebuild(t, dir, tc.harness, tc.id)
		})
	}
}

// A transcript with nothing to index has no row after a rebuild (#868). The
// append pass and the re-read pass gave it one with no records under it, an
// untitled line in `deja last` (#5563).
func TestAPassGivesNoRowToATranscriptWithNothingToIndex(t *testing.T) {
	recallOnly := func(sid string) string {
		return hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u2", "timestamp": "2026-10-15T01:00:01.000Z", "cwd": "/tmp/demo",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": "toolu_1", "type": "tool_result",
				"content": []any{map[string]any{"type": "text", "text": "<deja-recall>\nRecalled history from prior sessions\n- example hit\n</deja-recall>"}}}}}})
	}
	hasRow := func(t *testing.T, dir, id string) bool {
		t.Helper()
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range metas {
			if m.ID == id {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name, id, marker string
		change           func(t *testing.T, root string)
	}{
		{"a new file, appended", "s-c", "updated 1 file", func(t *testing.T, root string) {
			write(t, filepath.Join(root, "p", "c.jsonl"), recallOnly("s-c"))
		}},
		{"a file that held text, read again", "s-b", replacementPassMarker, func(t *testing.T, root string) {
			p := filepath.Join(root, "p", "b.jsonl")
			write(t, p, recallOnly("s-b"))
			bumpMTime(t, p)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			dir := filepath.Join(tmp, "idx")
			root := os.Getenv("DEJA_CLAUDE_ROOT")
			write(t, filepath.Join(root, "p", "a.jsonl"), claudeLine("s-a", "2026-10-15T01:00:00Z", "alpha one"))
			write(t, filepath.Join(root, "p", "b.jsonl"), claudeLine("s-b", "2026-10-15T01:00:00Z", "beta one"))
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			tc.change(t, root)
			var progress strings.Builder
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), tc.marker) {
				t.Fatalf("the pass was not the one this case is about: %q", progress.String())
			}
			if hasRow(t, dir, tc.id) {
				t.Errorf("%s has a row with nothing under it", tc.id)
			}
			// The control: the session that holds text keeps its row.
			if !hasRow(t, dir, "s-a") {
				t.Error("the pass dropped s-a, which holds text")
			}
			sessionMatchesRebuild(t, dir, "claude", tc.id)
			sessionMatchesRebuild(t, dir, "claude", "s-a")
		})
	}
}
