package sources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The Antigravity CLI writes its transcripts where the IDE does, but never
// appears in conversation_metadata.json — that file is the IDE's. Every `agy`
// session therefore parsed with no project, and a session with no project is
// invisible to recall, which ranks within the project the user is in.
func TestAntigravityProjectFallsBackToTheCLICache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// What the IDE writes: another conversation entirely.
	meta := `{"conversations":{"ide-conv":{"summary":{"WorkspaceURIs":["file:///Users/me/coding/other"]}}}}`
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	// What the CLI writes: workspace to conversation, one per directory.
	last := `{"/Users/me/coding/api-gateway":"cli-conv-42"}`
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), []byte(last), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

	if got := antigravityProject("cli-conv-42"); got != "api-gateway" {
		t.Errorf("project = %q, want the workspace the CLI recorded", got)
	}
	// The IDE's own mapping keeps working.
	if got := antigravityProject("ide-conv"); got != "other" {
		t.Errorf("IDE conversation = %q, want its own workspace", got)
	}
	if got := antigravityProject("neither"); got != "-" {
		t.Errorf("unknown conversation = %q, want the placeholder", got)
	}
}

// The CLI cache holds only the newest conversation per workspace, so the
// sessions recall exists to surface — yesterday's — have no entry left by the
// time they matter. The files they opened still name where they ran.
func TestAntigravityProjectFromTheFilesASessionOpened(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{
			name:  "one checkout",
			paths: []string{"/Users/me/coding/api-gateway/cmd/main.go", "/Users/me/coding/api-gateway/internal/db/pool.go"},
			want:  "api-gateway",
		},
		{
			name:  "a single file still names its directory",
			paths: []string{"/Users/me/coding/api-gateway/main.go"},
			want:  "api-gateway",
		},
		{
			name:  "two checkouts share only the parent, which is no project",
			paths: []string{"/Users/me/coding/api-gateway/main.go", "/Users/me/coding/billing/main.go"},
			want:  "coding",
		},
		{
			name:  "a session in the home directory names nothing",
			paths: []string{"/Users/me/notes.md", "/Users/me/todo.txt"},
			want:  "",
		},
		{
			name:  "no files at all",
			paths: nil,
			want:  "",
		},
		// A store synced from Windows holds these, and on Windows itself every
		// path looks like this. Requiring a leading slash dropped all of them:
		// the CLI sessions there could never find their project, and the two
		// tests below only caught it because the Windows runner builds its
		// fixtures with the local separator.
		{
			name:  "windows paths",
			paths: []string{`C:\Users\me\coding\api-gateway\cmd\main.go`, `C:\Users\me\coding\api-gateway\internal\db\pool.go`},
			want:  "api-gateway",
		},
		{
			name:  "a windows home directory names nothing",
			paths: []string{`C:\Users\me\notes.md`, `C:\Users\me\todo.txt`},
			want:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []model.Message
			for _, p := range tc.paths {
				messages = append(messages, model.Message{Role: RoleFiles, Text: p})
			}
			// Speech must not be mistaken for a path.
			messages = append(messages, model.Message{Role: "assistant", Text: "/etc/passwd is not a file I opened"})
			if got := antigravityProjectFromFiles(messages); got != tc.want {
				t.Fatalf("project = %q, want %q", got, tc.want)
			}
		})
	}
}

// A session that opened one file deep in a tree shares only that file's own
// directory, which is a package. The checkout above it is the project.
func TestAntigravityProjectClimbsToTheCheckout(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "api-gateway")
	deep := filepath.Join(checkout, "internal", "db")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	messages := []model.Message{{Role: RoleFiles, Text: filepath.Join(deep, "pool.go")}}
	if got := antigravityProjectFromFiles(messages); got != "api-gateway" {
		t.Fatalf("project = %q, want the checkout rather than the package", got)
	}
}

// End to end: a CLI transcript that no cache knows about still lands in the
// project it was worked in.
// The path goes into the fixture slashed, because the fixture is JSON: a
// Windows path pasted in raw carries backslashes that JSON reads as escapes,
// the line fails to decode, and the session arrives with no files at all — a
// green test on one OS and an unexplained "-" on the other.
func TestParseAntigravityCLISessionGetsAProject(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "api-gateway")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(checkout, "internal", "db")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, transcript := antigravityTree(t)
	body := `{"source":"USER_EXPLICIT","content":"why is the pool exhausted?","created_at":"2026-08-14T09:00:00Z","type":""}
{"source":"MODEL","type":"VIEW_FILE","content":"Created At: 2026-08-14T09:00:01Z\nFile Path: file://` + filepath.ToSlash(filepath.Join(deep, "pool.go")) + `\n\nthe pool is capped at 4","created_at":"2026-08-14T09:00:01Z"}
{"source":"MODEL","type":"PLANNER_RESPONSE","content":"the pool is capped at 4","created_at":"2026-08-14T09:00:02Z"}
`
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseAntigravityFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if got := sessions[0].Project; got != "api-gateway" {
		t.Fatalf("project = %q; a CLI session with no cache entry is invisible "+
			"to recall without one", got)
	}
}
