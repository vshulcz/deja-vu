package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Roo Code and Cline append an <environment_details> block to every user turn —
// visible files, open tabs, the clock, the running cost, the mode, a workspace
// listing — and deja indexed it inside the person's message. So a query on
// "files", "current" or any open tab's path hit every Roo and Cline session,
// and ctx printed the listing where the reader's own question goes (#3255).
func TestClineDropsTheHostsBlockFromAUserTurn(t *testing.T) {
	const question = "fix the flaky vantrell retry: the second attempt reuses the first timeout"
	env := "<environment_details>\n# VSCode Visible Files\ninternal/retry/retry.go\n" +
		"# VSCode Open Tabs\ninternal/retry/retry.go\n" +
		"# Current Time\nCurrent time in ISO 8601 UTC format: 2026-09-08T01:33:20.000Z\n" +
		"# Current Cost\n$0.12\n# Current Mode\n<slug>code</slug>\n</environment_details>"

	for _, tc := range []struct{ name, in string }{
		// Roo appends the block after the task text, in the same turn.
		{"after the text", question + "\n" + env},
		// And inside the <task> envelope, which is where Cline puts it.
		{"inside the task envelope", "<task>\n" + question + "\n" + env + "\n</task>"},
		{"before the text", env + "\n" + question},
	} {
		got := unwrapClineTask(tc.in)
		if got != question {
			t.Errorf("%s: user turn =\n  %q\nwant\n  %q", tc.name, got, question)
		}
	}

	// An unterminated block — the listing cut mid-write — takes the rest with
	// it rather than leaving half a listing in the message.
	if got := unwrapClineTask(question + "\n<environment_details>\n# VSCode Visible"); got != question {
		t.Errorf("an unterminated block survived: %q", got)
	}

	// Two blocks in one turn, which a resumed task carries.
	if got := unwrapClineTask(env + "\n" + question + "\n" + env); got != question {
		t.Errorf("a second block survived: %q", got)
	}

	// What the person wrote is untouched, including a message that talks about
	// the block or opens with a path.
	for _, mine := range []string{
		"why is environment_details in my context",
		"/etc/hosts is wrong on the build box",
		question,
	} {
		if got := unwrapClineTask(mine); got != mine {
			t.Errorf("the reader's message changed: %q -> %q", mine, got)
		}
	}
}

// The same through the parser, since the block lands in the store: it has to be
// gone from the message every surface reads, not only from the unwrapper.
func TestRooTaskIndexesTheQuestionWithoutTheListing(t *testing.T) {
	const question = "fix the flaky vantrell retry"
	dir := filepath.Join(t.TempDir(), "tasks", "1767225700000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	turns := []map[string]any{
		{"role": "user", "content": question +
			"\n<environment_details>\n# Current Cost\n$0.12\n</environment_details>"},
		{"role": "assistant", "content": "the second attempt reuses the deadline, not the timeout"},
	}
	b, err := json.Marshal(turns)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseRooTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "environment_details") || strings.Contains(m.Text, "Current Cost") {
			t.Errorf("the host's block reached the index as %s: %q", m.Role, m.Text)
		}
	}
	found := false
	for _, m := range ss[0].Messages {
		if strings.TrimSpace(m.Text) == question {
			found = true
		}
	}
	if !found {
		t.Errorf("the question did not survive: %#v", ss[0].Messages)
	}
}
