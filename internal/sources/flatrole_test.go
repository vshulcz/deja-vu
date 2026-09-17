package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Command Code's transcript has a sibling with the same extension — the
// checkpoint stream — and reading it as a conversation adds a session with no
// words in it, which then competes for a recall slot (#3647).
func TestCommandCodeSkipsTheCheckpointStream(t *testing.T) {
	root := fixtureRoot(t, "commandcode", "DEJA_COMMANDCODE_ROOT")
	files := CommandCodeSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want the transcript alone", files)
	}
	if strings.Contains(files[0], ".checkpoints.") {
		t.Fatalf("the checkpoint stream was listed as a transcript: %s", files[0])
	}

	ss, err := ParseCommandCodeFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "reg-commandcode-001" {
		t.Errorf("id = %q, want the sessionId the lines carry", s.ID)
	}
	if s.Project != "commandcode/demo" {
		t.Errorf("project = %q, want it decoded from the directory", s.Project)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %d, want prompt, tool output and answer: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[1].Role != RoleToolOutput {
		t.Errorf("the tool line came back as %q; an error a user hit has to be searchable", s.Messages[1].Role)
	}
	if !strings.Contains(s.Messages[1].Text, "connection refused") {
		t.Errorf("the tool output lost its text: %+v", s.Messages[1])
	}
	if s.Messages[2].Role != "assistant" || !strings.Contains(s.Messages[2].Text, "host index") {
		t.Errorf("the answer is wrong: %+v", s.Messages[2])
	}

	// The skip has to be nameable, or doctor counts the checkpoint stream as a
	// file deja failed to understand and the row reports drift on a store it is
	// reading correctly.
	cps := CommandCodeCheckpointFiles()
	if len(cps) != 1 || !strings.HasSuffix(cps[0], ".checkpoints.jsonl") {
		t.Errorf("checkpoint files = %v, want the one beside the transcript", cps)
	}
	_ = root
}

// ZCode writes the same lines, and content can be a plain string there rather
// than the block array.
func TestZCodeReadsAStringContent(t *testing.T) {
	fixtureRoot(t, "zcode", "DEJA_ZCODE_ROOT")
	files := ZCodeSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	ss, err := ParseZCodeFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("sessions = %+v", ss)
	}
	if !strings.Contains(ss[0].Messages[1].Text, "empty frame") {
		t.Errorf("the answer is missing: %+v", ss[0].Messages)
	}
	if ss[0].Messages[1].Time.IsZero() {
		t.Error("the answer has no time, so recency cannot rank it")
	}
}

// Senpi and Kimchi are pi's envelope, and what has to hold is that the harness
// name and the project come back as theirs rather than pi's.
func TestSenpiAndKimchiReadThePiEnvelope(t *testing.T) {
	root := fixtureRoot(t, "senpi", "DEJA_SENPI_ROOT")
	ss, err := ParseSenpiFile(filepath.Join(root, "-workspace-senpi-demo", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("senpi sessions = %d", len(ss))
	}
	if ss[0].Harness != "senpi" {
		t.Errorf("harness = %q, want senpi", ss[0].Harness)
	}
	if ss[0].Project != "senpi/demo" {
		t.Errorf("project = %q, want it from the encoded directory", ss[0].Project)
	}
	if len(ss[0].Messages) != 2 || !strings.Contains(ss[0].Messages[1].Text, "acknowledged") {
		t.Errorf("senpi messages = %+v", ss[0].Messages)
	}

	// Kimchi keeps a directory per project, the way pi does: its own binary
	// builds `<agent>/sessions/--<encoded cwd>--` in getDefaultSessionDirPath.
	// The fixture is laid out that way, and the walk has to find it — a flat
	// listing would read nothing on a real machine (#3678).
	kroot := fixtureRoot(t, "kimchi", "DEJA_KIMCHI_ROOT")
	nested := filepath.Join(kroot, "--workspace-kimchi-demo--", "session.jsonl")
	files := KimchiSessionFiles()
	found := false
	for _, f := range files {
		if f == nested {
			found = true
		}
	}
	if !found {
		t.Fatalf("the per-project transcript is not in the file list: %v", files)
	}
	ks, err := ParseKimchiFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks) != 1 {
		t.Fatalf("kimchi sessions = %d", len(ks))
	}
	if ks[0].Harness != "kimchi" {
		t.Errorf("harness = %q, want kimchi", ks[0].Harness)
	}
	// The header's cwd is what names the project — authoritative, and on a real
	// machine the encoded directory is built from the same value, so the layout
	// must not change the attribution. The flat case below asserts that pair.
	if ks[0].Project != "kimchi/demo" {
		t.Errorf("project = %q, want it from the header's cwd", ks[0].Project)
	}
	if ks[0].ID != "reg-kimchi-001" {
		t.Errorf("id = %q, want the header's id", ks[0].ID)
	}

	// And a session file sitting directly under the root is still read, with
	// the header's cwd naming the project.
	flat := filepath.Join(t.TempDir(), "session.jsonl")
	body, err := os.ReadFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIMCHI_ROOT", filepath.Dir(flat))
	fs, err := ParseKimchiFile(flat)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Project != ks[0].Project {
		t.Errorf("flat file: %+v, want the same project the nested one got (%q)", fs, ks[0].Project)
	}
}

// gjc keeps a sub-agent's passes one directory deeper than the session they
// belong to. Indexed as sessions of their own they repeat the parent's work and
// compete with it for the same recall slot, so they are skipped unless asked
// for — the switch Claude Code's and Cursor's sub-agents already use (#3647).
func TestGjcSkipsSubagentPassesUnlessAskedFor(t *testing.T) {
	root := fixtureRoot(t, "gjc", "DEJA_GJC_ROOT")
	files := GjcSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want the session alone", files)
	}
	if strings.Contains(filepath.ToSlash(files[0]), "/session/") {
		t.Fatalf("a sub-agent pass was listed as a session: %s", files[0])
	}

	ss, err := ParseGjcFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Harness != "gjc" {
		t.Fatalf("sessions = %+v", ss)
	}
	if ss[0].ID != "reg-gjc-001" {
		t.Errorf("id = %q, want the header's id", ss[0].ID)
	}
	if len(ss[0].Messages) != 2 || !strings.Contains(ss[0].Messages[1].Text, "twice") {
		t.Errorf("messages = %+v", ss[0].Messages)
	}
	// The service_tier_change line is not a turn, and read as one it is an
	// empty message in the middle of the session.
	for _, m := range ss[0].Messages {
		if m.Text == "" {
			t.Errorf("a bookkeeping line became a message: %+v", ss[0].Messages)
		}
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := GjcSessionFiles(); len(got) != 2 {
		t.Errorf("with sub-agents asked for, files = %v, want both", got)
	}
	_ = root
}

// fixtureRoot points one reader at its committed fixture store.
func fixtureRoot(t *testing.T, name, env string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(env, root)
	return root
}
