package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A resumed parse must produce the same session identity as a whole read.
//
// pi is on the incremental-append list, and pi filenames are
// `<ISO-timestamp>_<uuid>.jsonl` while the header's id is the bare uuid — so a
// tail read past the header filed itself under an id the whole read never
// produces, and the appended turn landed beside the session instead of in it.
// The existing offset test asserted only the message text, which is why this
// went unnoticed (#2870).
func TestPiKeepsItsIdentityWhenResumedPastTheHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-demo--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session","version":3,"id":"9f1c-uuid","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/demo"}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "2026-01-02T03-04-05_9f1c-uuid.jsonl")
	if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	whole, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ParsePiFileFromOffset(path, int64(len(head)+len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || len(resumed) != 1 {
		t.Fatalf("want one session from each read, got %d and %d", len(whole), len(resumed))
	}
	// Both against the header's own value, not merely against each other: an
	// equivalence assertion is blind to a change that breaks both reads alike.
	for _, got := range []struct {
		read string
		id   string
	}{{"whole", whole[0].ID}, {"resumed", resumed[0].ID}} {
		if got.id != "9f1c-uuid" {
			t.Errorf("%s read id = %q, want the header's 9f1c-uuid", got.read, got.id)
		}
	}
	if resumed[0].Project != whole[0].Project {
		t.Errorf("resumed project = %q, whole-read project = %q", resumed[0].Project, whole[0].Project)
	}
}

// A file whose first line is not a header must not have that line read as one:
// the resumed read fetches line 1 blind, and without the type check a plain
// message would hand the session its own message id.
func TestPiIgnoresAFirstLineThatIsNotAHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-headless--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "headless.jsonl")
	if err := os.WriteFile(path, []byte(first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	resumed, err := ParsePiFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("want one session, got %d", len(resumed))
	}
	if resumed[0].ID != "headless" {
		t.Errorf("id = %q, want the filename headless — a message line was read as the header", resumed[0].ID)
	}
}

// omp and OpenClaw share the reader and promote the header's cwd to the
// project, so losing the header renamed the project as well as the id. Both
// are off the append list today; this is what has to hold before either joins
// it.
func TestOmpKeepsHeaderProjectWhenResumedPastTheHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-Code-pleasure-course")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session","version":3,"id":"omp-uuid","timestamp":"2026-01-02T03:04:05Z","cwd":"/Users/x/work/serpass"}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "2026-01-02T03-04-05_omp-uuid.jsonl")
	if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	whole, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ParseOmpFileFromOffset(path, int64(len(head)+len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || len(resumed) != 1 {
		t.Fatalf("want one session from each read, got %d and %d", len(whole), len(resumed))
	}
	if resumed[0].ID != whole[0].ID {
		t.Errorf("resumed id = %q, whole-read id = %q", resumed[0].ID, whole[0].ID)
	}
	// Absolute, for the same reason: the header names /Users/x/work/serpass,
	// and the directory the file sits in says Code-pleasure-course.
	for _, got := range []struct {
		read    string
		project string
	}{{"whole", whole[0].Project}, {"resumed", resumed[0].Project}} {
		if !strings.Contains(got.project, "serpass") {
			t.Errorf("%s read project = %q, want the header cwd's serpass", got.read, got.project)
		}
	}
}
