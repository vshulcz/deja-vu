package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestResumeCommandPerHarness(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "projects", "my-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(real, string(filepath.Separator), "-")
	claudePath := filepath.Join("/claude/projects", encoded, "abc.jsonl")
	grokPath := filepath.Join(tmp, "grok-sessions", url.PathEscape(real), "019f-grok", "updates.jsonl")

	aFile := filepath.Join(tmp, "ses_3.json")
	if err := os.WriteFile(aFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		s       model.Session
		wantDir string
		wantCmd string
		wantErr string
	}{
		{"claude with resolvable dir", model.Session{Harness: "claude", ID: "abc-123", Project: "projects/my-app", Path: claudePath}, real, "claude --resume abc-123", ""},
		{"codex rollout", model.Session{Harness: "codex", ID: "uuid-1", Project: "my-app"}, "", "codex resume uuid-1", ""},
		{"codex history entry", model.Session{Harness: "codex", ID: "uuid-2", Project: "history"}, "", "", "nothing to resume"},
		{"opencode with dir", model.Session{Harness: "opencode", ID: "ses_1", Project: "my-app", Path: real}, real, "opencode -s ses_1", ""},
		// opencode reopens a session from any directory; a cd into a deleted one
		// stopped the command before it started (#4201).
		{"opencode with its dir gone", model.Session{Harness: "opencode", ID: "ses_2", Project: "gone", Path: filepath.Join(tmp, "projects", "gone")}, "", "opencode -s ses_2", ""},
		{"opencode path that is a file", model.Session{Harness: "opencode", ID: "ses_3", Project: "f", Path: aFile}, "", "opencode -s ses_3", ""},
		{"kilo with its dir gone", model.Session{Harness: "kilocode", ID: "ses_4", Project: "gone", Path: filepath.Join(tmp, "projects", "gone")}, "", "kilo -s ses_4", ""},
		{"grok build session", model.Session{Harness: "grok", ID: "019f-grok", Project: "my-app", Path: grokPath}, real, "grok --resume 019f-grok", ""},
		{"grok-dev row", model.Session{Harness: "grok", ID: "019f-dev", Project: "my-app", Path: filepath.Join(tmp, "grok.db")}, "", "", "grok-dev store"},
		{"imported", model.Session{Harness: "claude", ID: "imported-9f5", Project: "imported:my-app"}, "", "", "another machine"},
		{"unknown harness", model.Session{Harness: "mystery", ID: "x"}, "", "", "don't know how"},
	}
	for _, c := range cases {
		if runtime.GOOS == "windows" && c.name == "claude with resolvable dir" {
			continue // claude encodes unix-style absolute paths; resolution is a no-op on windows
		}
		dir, cmd, err := resumeCommand(c.s)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if dir != c.wantDir || cmd != c.wantCmd {
			t.Fatalf("%s: got (%q, %q), want (%q, %q)", c.name, dir, cmd, c.wantDir, c.wantCmd)
		}
	}
}

func TestFormatResumeCommand(t *testing.T) {
	dir := filepath.Join("tmp", "project's dir")
	got := formatResumeCommand(dir, "claude --resume 019f")
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(got, "powershell.exe -NoProfile") || !strings.Contains(got, "project''s dir") || !strings.Contains(got, "-ErrorAction Stop") || !strings.Contains(got, "claude --resume 019f") {
			t.Fatalf("Windows resume command = %q", got)
		}
		return
	}
	want := "cd " + shellQuote(dir) + " && claude --resume 019f"
	if got != want {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
}

func TestRunResumePrintAndErrors(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-resume")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"resume123","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"resume needle"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "resume123.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	var out bytes.Buffer
	if err := runResume(index.DefaultDir(), []string{"resume"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "claude --resume resume123") {
		t.Fatalf("resume output = %q", got)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing", nil, "resume needs id-prefix"},
		{"exec without prefix", []string{"--exec"}, "resume needs id-prefix"},
		{"not found", []string{"nope"}, `no session matches "nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var discard bytes.Buffer
			err := runResume(index.DefaultDir(), tc.args, &discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// qwen scopes `qwen sessions list` to the current project, so the id alone is
// not enough: run the command anywhere else and it reopens nothing.
func TestResumeQwenRunsInTheProjectDirectory(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "projects", "my-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	encoded := strings.ReplaceAll(real, string(filepath.Separator), "-")
	path := filepath.Join(tmp, "qwen", "projects", encoded, "chats", "a2d5a292.jsonl")

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Project: "my-app", Path: path})
	if err != nil {
		t.Fatalf("qwen resume: %v", err)
	}
	if cmd != "qwen -r a2d5a292" {
		t.Fatalf("cmd = %q", cmd)
	}
	if runtime.GOOS != "windows" && dir != real {
		t.Fatalf("dir = %q, want the project directory %q", dir, real)
	}
}

// Crush looks for a session in the store under the current directory and
// nowhere else, so the same id resolves to nothing when the command is run
// anywhere but the project the store sits under.
func TestCrushResumeRunsInTheProject(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "my-app")
	path := filepath.Join(project, ".crush", "crush.db")
	id := "942cbc1e-78c7-41cb-aa8a-78c3baab018c"

	dir, cmd, err := resumeCommand(model.Session{Harness: "crush", ID: id, Project: "my-app", Path: path})
	if err != nil {
		t.Fatalf("crush resume: %v", err)
	}
	if cmd != "crush --session "+id {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != project {
		t.Fatalf("dir = %q, want the project directory %q", dir, project)
	}

	// An id that is not a uuid never reaches a command line.
	if _, _, err := resumeCommand(model.Session{Harness: "crush", ID: "x; rm -rf /", Project: "p", Path: path}); err == nil {
		t.Fatal("a non-uuid id was accepted")
	}
}

// Cursor's CLI transcripts are named after the chat id `--resume` takes, while
// IDE chats come out of a different store and reopen only in the editor.
func TestResumeCursorSplitsCLIFromIDE(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	// Cursor encodes without the leading separator: Users-x-app, not -Users-x-app.
	encoded := strings.TrimPrefix(strings.ReplaceAll(real, string(filepath.Separator), "-"), "-")
	id := "de875c53-88ae-4e73-8953-9813479364d8"
	path := filepath.Join(tmp, "projects", encoded, "agent-transcripts", id, id+".jsonl")
	// cursor-agent opens the chat from chats/<md5 of the directory>/<id>.
	t.Setenv("CURSOR_CONFIG_DIR", tmp)
	t.Setenv("DEJA_CURSOR_CLI_ROOT", tmp)
	store := filepath.Join(tmp, "chats", sources.CursorChatBucket(real), id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The folder name does not decode back to a Windows path; meta.json names it.
	meta, _ := json.Marshal(map[string]string{"cwd": real})
	if err := os.WriteFile(filepath.Join(filepath.Dir(store), "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Project: "app", Path: path})
	if err != nil {
		t.Fatalf("cursor resume: %v", err)
	}
	if cmd != "cursor-agent --resume "+id {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != real {
		t.Fatalf("dir = %q, want the project directory %q", dir, real)
	}

	// An IDE chat carries a composer id from state.vscdb, which cursor-agent
	// does not take: printing the command anyway sends someone to a new chat.
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: "composer-1", Path: filepath.Join(tmp, "state.vscdb")}); err == nil {
		t.Fatal("an IDE chat produced a terminal command")
	}
}

// gemini finds a session only from the directory it ran in (#4211); the
// store records it in projects.json and in the project folder's .project_root.
func TestResumeGeminiRunsInTheProjectDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	work := filepath.Join(t.TempDir(), "проект app")
	chats := filepath.Join(root, "tmp", "app", "chats")
	for _, d := range []string{work, chats} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(chats, "session-2026-10-01T12-50-a5bc80ac.jsonl")
	s := model.Session{Harness: "gemini", ID: "a5bc80ac-786c", Project: "app", Path: path}

	// .project_root alone, as the folder writes it.
	if err := os.WriteFile(filepath.Join(root, "tmp", "app", ".project_root"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err := resumeCommand(s)
	if err != nil || cmd != "gemini --resume a5bc80ac-786c" || dir != work {
		t.Fatalf("resume = (%q, %q, %v), want the command run in %q", dir, cmd, err, work)
	}
	// projects.json is the fallback when the folder keeps no .project_root.
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"projects":{` + jsonString(other) + `:"app"}}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(s); dir != work {
		t.Fatalf("dir = %q, want .project_root's %q over the registry", dir, work)
	}
	if err := os.Remove(filepath.Join(root, "tmp", "app", ".project_root")); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(s); dir != other {
		t.Fatalf("dir = %q, want the registry's %q", dir, other)
	}
	// A directory that is gone gets no cd.
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(s); dir != "" {
		t.Fatalf("dir = %q for a directory that is gone", dir)
	}
}

// An older store keys the project folder by a hash of the path and records
// nothing to invert — the command carries no cd.
func TestResumeGeminiPrintsNoDirectory(t *testing.T) {
	t.Setenv("DEJA_GEMINI_ROOT", t.TempDir())
	dir, cmd, err := resumeCommand(model.Session{Harness: "gemini", ID: "a5bc80ac-786c", Project: "app", Path: "/g/tmp/app/chats/s.jsonl"})
	if err != nil {
		t.Fatalf("gemini resume: %v", err)
	}
	if cmd != "gemini --resume a5bc80ac-786c" {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want none — the store keeps a hash, not a path", dir)
	}
}

// OpenClaw addresses a conversation by key, and the uuid its file is named
// after opens nothing. The key lives in the store beside the transcript.
func TestResumeOpenClawUsesTheSessionKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents", "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "7de16196.jsonl")
	store := `{"agent:main:other":{"sessionId":"0000"},"agent:main:main":{"sessionId":"7de16196"}}`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(store), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cmd, err := resumeCommand(model.Session{Harness: "openclaw", ID: "7de16196", Project: "openclaw-main", Path: path})
	if err != nil {
		t.Fatalf("openclaw resume: %v", err)
	}
	if cmd != "openclaw chat --session agent:main:main" {
		t.Fatalf("cmd = %q", cmd)
	}

	// A transcript the store does not know about has no key, and a command
	// built from the uuid would open a new conversation instead of that one.
	orphan := filepath.Join(dir, "deadbeef.jsonl")
	if _, _, err := resumeCommand(model.Session{Harness: "openclaw", ID: "deadbeef", Path: orphan}); err == nil {
		t.Fatal("a session missing from the store resumed anyway")
	}
}

func TestResumeOpenClawSQLiteUsesTheCurrentSessionKey(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agent", "openclaw-agent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "agents", "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	create := exec.Command("sqlite3", db)
	create.Stdin = bytes.NewReader(sql)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create OpenClaw store: %v: %s", err, out)
	}
	// The transcript fixture omits the current-session table; resume must not
	// guess which window the key opens when that mapping is absent.
	if _, _, err := resumeCommand(model.Session{Harness: "openclaw", ID: "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d", Path: db}); err == nil {
		t.Fatal("resume accepted a store without a current-session mapping")
	}
	// A reset leaves both windows searchable, but only the pointed-to one
	// may be reopened under their shared key.
	check := func(current, old string) {
		t.Helper()
		sessions, err := sources.ParseOpenClawDB(db)
		if err != nil || len(sessions) != 2 {
			t.Fatalf("parse OpenClaw store: sessions=%v err=%v", sessions, err)
		}
		for _, s := range sessions {
			_, cmd, err := resumeCommand(s)
			switch s.ID {
			case current:
				if err != nil || cmd != "openclaw chat --session agent:main:main" {
					t.Errorf("current session %s: cmd=%q err=%v", s.ID, cmd, err)
				}
			case old:
				if err == nil {
					t.Errorf("old window %s reopened the current session with %q", s.ID, cmd)
				} else if !strings.Contains(err.Error(), "not the current session") {
					// It is in the store; saying otherwise sends the user looking for it.
					t.Errorf("old window %s: %v", s.ID, err)
				}
			default:
				t.Errorf("unexpected session %s", s.ID)
			}
		}
	}
	first := "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	second := "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"
	stmt := "CREATE TABLE session_nodes (session_key TEXT PRIMARY KEY, current_session_id TEXT NOT NULL); " +
		"INSERT INTO session_nodes VALUES ('agent:main:main', '" + second + "');"
	if out, err := exec.Command("sqlite3", db, stmt).CombinedOutput(); err != nil {
		t.Fatalf("add current-session pointer: %v: %s", err, out)
	}
	check(second, first)
	// Changing the authoritative pointer must change which window is safe to resume.
	stmt = "UPDATE session_nodes SET current_session_id = '" + first + "' WHERE session_key = 'agent:main:main'"
	if out, err := exec.Command("sqlite3", db, stmt).CombinedOutput(); err != nil {
		t.Fatalf("update current-session pointer: %v: %s", err, out)
	}
	check(first, second)
}

// Hermes was the one harness deja gave up on, while its own CLI takes the
// exact session ID deja indexes.
func TestResumeHermes(t *testing.T) {
	dir, cmd, err := resumeCommand(model.Session{ID: "20260727_135519_a55c71", Harness: "hermes", Project: "p"})
	if err != nil {
		t.Fatalf("hermes resume: %v", err)
	}
	if dir != "" {
		t.Fatalf("hermes resume should not need a directory, got %q", dir)
	}
	if cmd != "hermes --resume 20260727_135519_a55c71" {
		t.Fatalf("cmd = %q", cmd)
	}
}
