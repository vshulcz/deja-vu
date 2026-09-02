package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseAmpThread(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "threads"))

	path := filepath.Join(root, "threads", "thread-abc.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const created int64 = 1767337445000
	data := `{"id":"thread-abc","title":"Fix the parser","created":1767337445000,"env":{"initial":{"trees":[{"uri":"file:///tmp/amp-project"}]}},"messages":[{"role":"user","content":[{"type":"text","text":"Please fix the parser."},{"type":"image","url":"file:///tmp/ignored.png"}]},{"role":"assistant","content":[{"type":"text","text":"I found the issue."},{"type":"text","text":"The parser now handles it."}]},{"role":"system","content":[{"type":"text","text":"ignored system message"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseAmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "amp" || s.ID != "thread-abc" || s.Title != "Fix the parser" {
		t.Fatalf("identity = %#v", s)
	}
	if s.Project != "amp-project" {
		t.Fatalf("project = %q, want amp-project", s.Project)
	}
	if s.Path != path {
		t.Fatalf("path = %q, want %q", s.Path, path)
	}
	stamp := time.UnixMilli(created)
	if !s.Started.Equal(stamp) || !s.Updated.Equal(stamp) {
		t.Fatalf("session timestamps = %v / %v, want %v", s.Started, s.Updated, stamp)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("got %d messages, want user and assistant only: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "Please fix the parser." {
		t.Fatalf("user message = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "I found the issue.\nThe parser now handles it." {
		t.Fatalf("assistant message = %#v", s.Messages[1])
	}
	for i, message := range s.Messages {
		if !message.Time.Equal(stamp) {
			t.Errorf("message %d timestamp = %v, want thread created %v", i, message.Time, stamp)
		}
	}
}

func TestParseAmpFallsBackToTitleWithoutWorkingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.json")
	data := `{"id":"fallback-1","title":"Untitled work","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseAmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Project != "Untitled work" {
		t.Fatalf("fallback project = %#v, want title", sessions)
	}
}

func TestParseAmpRejectsMalformedOrTruncatedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"id":"bad",`},
		{name: "truncated message", body: `{"id":"bad","title":"broken","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"cut`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseAmpFile(path); err == nil {
				t.Fatal("malformed Amp file parsed without an error")
			}
		})
	}
}

func TestLoadAmpSkipsMalformedThread(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "threads"))
	if err := os.MkdirAll(AmpRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	valid := `{"id":"valid","title":"Valid","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"valid text"}]}]}`
	for name, body := range map[string]string{"valid.json": valid, "truncated.json": `{"id":"truncated",`} {
		if err := os.WriteFile(filepath.Join(AmpRoot(), name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded := LoadAmp()
	if len(loaded) != 1 || loaded[0].ID != "valid" {
		t.Fatalf("LoadAmp = %#v, want only valid thread", loaded)
	}
}

func TestAmpDiscoveryAndRegistry(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg"))
	t.Setenv("AMP_DATA_HOME", "")
	t.Setenv("DEJA_AMP_ROOT", "")
	wantDefault := filepath.Join(root, "xdg", "amp", "threads")
	if got := AmpRoot(); got != wantDefault {
		t.Fatalf("default AmpRoot = %q, want %q", got, wantDefault)
	}
	native := filepath.Join(root, "native-amp")
	t.Setenv("AMP_DATA_HOME", native)
	if got := AmpRoot(); got != filepath.Join(native, "threads") {
		t.Fatalf("AMP_DATA_HOME AmpRoot = %q", got)
	}
	t.Setenv("AMP_DATA_HOME", "")

	override := filepath.Join(root, "custom-amp")
	t.Setenv("DEJA_AMP_ROOT", override)
	path := filepath.Join(override, "thread-1.json")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"thread-1","title":"Discovery","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"find me"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := AmpThreadFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("AmpThreadFiles = %v, want [%s]", files, path)
	}
	loaded := LoadAmp()
	if len(loaded) != 1 || loaded[0].ID != "thread-1" {
		t.Fatalf("LoadAmp = %#v", loaded)
	}
	if !IsKnownHarness("amp") || KindForPath(path) != "amp" {
		t.Fatalf("Amp was not registered: known=%v kind=%q", IsKnownHarness("amp"), KindForPath(path))
	}
}
