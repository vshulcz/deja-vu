package index

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

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
