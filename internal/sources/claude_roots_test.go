package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func writeClaudeTranscript(t *testing.T, dir, name, said string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"` + name + `","cwd":"/work/app","timestamp":"2026-08-02T10:00:00Z",` +
		`"message":{"role":"user","content":"` + said + `"}}` + "\n"
	p := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// cc-mirror runs isolated Claude Code variants, each with its own home under
// ~/.cc-mirror/<variant>/.claude. A session run through a variant reached
// nothing: deja read one directory and that was not it (#2996).
func TestClaudeReadsXcodeCCMirrorVariantsAndTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	main := writeClaudeTranscript(t, filepath.Join(home, ".claude", "projects", "-work-app"), "main-1", "the ordinary store")
	// A sibling of projects/, where headless and SDK-driven clients write.
	headless := writeClaudeTranscript(t, filepath.Join(home, ".claude", "transcripts"), "headless-1", "an SDK run")
	xcode := writeClaudeTranscript(t, filepath.Join(home, "Library", "Developer", "Xcode", "CodingAssistant", "ClaudeAgentConfig", "projects", "-work-app"), "xcode-1", "an Xcode run")
	variant := writeClaudeTranscript(t, filepath.Join(home, ".cc-mirror", "work", ".claude", "projects", "-work-app"), "variant-1", "a cc-mirror variant")

	files := ClaudeFiles()
	found := map[string]bool{}
	for _, f := range files {
		found[f] = true
	}
	for _, want := range []string{main, headless, xcode, variant} {
		if !found[want] {
			t.Fatalf("%s is not offered to the index:\n%#v", want, files)
		}
	}
	// And the registry has to call them Claude's, or the ingester holds a file
	// no parser claims: the match was a prefix test against the single root.
	for _, want := range []string{main, headless, xcode, variant} {
		if !UnderClaudeRoot(want) {
			t.Fatalf("%s is not under a claude root", want)
		}
		if kind := KindForPath(want); kind != "claude" {
			t.Fatalf("%s is attributed to %q, not claude", want, kind)
		}
	}
}

func TestClaudeDoesNotReadAnXcodeRootTwice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	xcodeConfig := filepath.Join(home, "Library", "Developer", "Xcode", "CodingAssistant", "ClaudeAgentConfig")
	t.Setenv("CLAUDE_CONFIG_DIR", xcodeConfig)
	transcript := writeClaudeTranscript(t, filepath.Join(xcodeConfig, "projects", "-work-app"), "xcode-1", "one Xcode run")

	files := ClaudeFiles()
	if len(files) != 1 || files[0] != transcript {
		t.Fatalf("Xcode config was read more than once: %#v", files)
	}
}

// The stand's override is the whole answer: a run that pins the store must not
// pick up the machine's own history through the new roots.
func TestTheClaudeRootOverrideStaysTheWholeAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeClaudeTranscript(t, filepath.Join(home, ".claude", "projects", "-work-app"), "main-1", "the real machine")
	writeClaudeTranscript(t, filepath.Join(home, ".cc-mirror", "work", ".claude", "projects", "-work-app"), "variant-1", "a variant")
	stand := filepath.Join(home, "stand")
	seeded := writeClaudeTranscript(t, filepath.Join(stand, "-work-app"), "stand-1", "the seeded store")
	t.Setenv("DEJA_CLAUDE_ROOT", stand)

	files := ClaudeFiles()
	if len(files) != 1 || files[0] != seeded {
		t.Fatalf("the override let other roots in: %#v", files)
	}
}

// CLAUDE_CODE_PROJECT_DIR_NAME is documented and renames the directory. deja
// hard-coded "projects" and read nothing on a machine that set it.
func TestClaudeHonoursTheProjectDirName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "chats")

	renamed := writeClaudeTranscript(t, filepath.Join(home, ".claude", "chats", "-work-app"), "renamed-1", "under the renamed directory")
	if got := ClaudeRoot(); got != filepath.Join(home, ".claude", "chats") {
		t.Fatalf("ClaudeRoot = %q, want the renamed directory", got)
	}
	files := ClaudeFiles()
	if len(files) != 1 || files[0] != renamed {
		t.Fatalf("the renamed store was not read: %#v", files)
	}
}
