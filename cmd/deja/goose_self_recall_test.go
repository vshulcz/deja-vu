package main

import (
	"os"
	"strings"
	"testing"
)

// Plain goose keeps the session-start digest in one AGENTS.md that every
// session re-reads each turn. `goose session -r` fires SessionStart under the
// resumed id, and the hook wrote that session into its own digest; a session
// still open when a later one started in its project got itself back on its
// next turn; and the prompt hook never stamped the session live, so the MCP
// tool answered it with its own transcript.
func TestAPlainGooseSessionIsNotRecalledToItself(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	shared := func() string {
		b, _ := os.ReadFile(gooseHintsPath())
		return string(b)
	}

	// Control: a different session starting in the project is shown gp1.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "other", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shared(), "`gp1`") {
		t.Fatalf("the digest does not name gp1 even for another session, so this measures nothing:\n%s", shared())
	}

	// gp1 is resumed: SessionStart names it.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "gp1", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shared(), "`gp1`") {
		t.Errorf("a resumed goose session was given itself as history:\n%s", shared())
	}

	// A later session puts gp1 back into the shared file while gp1 is open.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "other", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	before := shared()
	if !strings.Contains(before, "`gp1`") {
		t.Fatalf("the later session's digest does not name gp1:\n%s", before)
	}
	// Another session's prompt leaves the shared file as it is.
	if err := refreshGooseForPrompt(dir, []byte(`{"session_id":"third","message":"hi","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if shared() != before {
		t.Errorf("a prompt from a session the digest does not name rewrote the shared file:\n%s", shared())
	}
	// gp1's next prompt takes itself out before goose reads the file, and
	// leaves the reader's own lines where they were.
	if err := os.WriteFile(gooseHintsPath(), []byte("# my notes\n\n"+before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(dir, []byte(`{"session_id":"gp1","message":"hi","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shared(), "`gp1`") {
		t.Errorf("an open goose session was shown itself on its next turn:\n%s", shared())
	}
	if !strings.HasPrefix(shared(), "# my notes\n") {
		t.Errorf("taking gp1 out lost the reader's lines:\n%s", shared())
	}
	if !liveSessionIDs(dir)["gp1"] {
		t.Errorf("the plain goose prompt hook did not stamp gp1 live, so MCP recall answers it with itself")
	}
}

// Only the asker's entry leaves the shared block. The rest is what another
// session started with, possibly in another project, and rebuilding it for the
// asker swapped that session's digest for the asker's.
func TestDropDigestEntryKeepsTheOtherSessions(t *testing.T) {
	digest := "<deja-recall>\nlead\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_3`\n  - User: one\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_31`\n  - User: two\n  - Assistant: `x`\n" +
		"✓ recalled from claude session · today\n  - Session: **p/b** `c9`\n  - User: three\n</deja-recall>\n"
	got, ok := dropDigestEntry(digest, "s_31")
	want := "<deja-recall>\nlead\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_3`\n  - User: one\n" +
		"✓ recalled from claude session · today\n  - Session: **p/b** `c9`\n  - User: three\n</deja-recall>\n"
	if !ok || got != want {
		t.Errorf("dropDigestEntry(s_31) = %v\n%s", ok, got)
	}
	if _, ok := dropDigestEntry(digest, "s_9"); ok {
		t.Error("an id the digest does not name was reported dropped")
	}
}
