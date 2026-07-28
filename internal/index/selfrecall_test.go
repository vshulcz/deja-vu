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
	t.Setenv("HOME", home)
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
