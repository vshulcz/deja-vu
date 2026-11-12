package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A Qoder CLI transcript is Claude Code-shaped JSONL under
// <root>/projects/<flattened cwd>/<uuid>.jsonl. The store must attribute it
// to the "qoder" harness — never to "claude" — and discovery, single-file
// parsing and the offset variant must all agree.
func TestParseQoderFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(root, "qoder"))
	// A suffix nobody can have on disk, for the same reason the Qwen fixture
	// uses one: the decoder resolves an encoded project against the
	// filesystem (#3512).
	unique := encodedFixtureSuffix(t)
	project := filepath.Join(root, "qoder", "projects", "-tmp-deja-vu-"+unique)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "3f2b7c1e-0a5d-4b62-9e84-1c2d3e4f5a6b.jsonl")
	data := `{"type":"user","sessionId":"qo-1","cwd":"/tmp/deja-vu","timestamp":"2026-01-02T03:04:05Z","uuid":"m1","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"hello qoder"}]}}
{"type":"assistant","sessionId":"qo-1","cwd":"/tmp/deja-vu","timestamp":"2026-01-02T03:05:05Z","uuid":"m2","parentUuid":"m1","message":{"role":"assistant","content":[{"type":"text","text":"hi from qoder"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseQoderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1: %#v", len(ss), ss)
	}
	if ss[0].Harness != "qoder" {
		t.Fatalf("harness = %q, want qoder", ss[0].Harness)
	}
	if ss[0].ID != "qo-1" {
		t.Fatalf("session id = %q, want qo-1", ss[0].ID)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %#v, want 2", ss[0].Messages)
	}
	if ss[0].Messages[0].Text != "hello qoder" || ss[0].Messages[1].Text != "hi from qoder" {
		t.Fatalf("message text = %#v", ss[0].Messages)
	}

	// The offset variant reads the same file from the beginning…
	from, err := ParseQoderFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(from) != 1 || from[0].Harness != "qoder" || len(from[0].Messages) != 2 {
		t.Fatalf("offset parse = %#v", from)
	}
	// …and from the end yields no new messages.
	tail, err := ParseQoderFileFromOffset(path, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		if len(s.Messages) != 0 {
			t.Fatalf("offset %d returned messages: %#v", len(data), s.Messages)
		}
	}

	// Discovery: the store lists exactly this transcript, and the registry
	// matcher claims it.
	files := QoderSessionFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("QoderSessionFiles = %v, want [%s]", files, path)
	}
	if !QoderUnderRoot(path) {
		t.Fatalf("QoderUnderRoot(%s) = false", path)
	}

	// End-to-end through the store loader.
	all := LoadQoder()
	if len(all) != 1 || all[0].Harness != "qoder" || all[0].ID != "qo-1" {
		t.Fatalf("LoadQoder = %#v", all)
	}
}

// The Qoder store must not reach outside its own root: a stock Claude
// transcript under ~/.claude stays with the claude harness.
func TestQoderUnderRootRejectsForeignPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(root, "qoder"))
	if QoderUnderRoot(filepath.Join(root, "claude", "projects", "x", "s.jsonl")) {
		t.Fatal("claimed a path outside the Qoder root")
	}
	if !QoderUnderRoot(filepath.Join(root, "qoder", "projects", "x", "s.jsonl")) {
		t.Fatal("rejected a path inside the Qoder root")
	}
}
