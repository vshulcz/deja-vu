package sources

import (
	"encoding/json"
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

	if got := antigravityProject("cli-conv-42"); got != "coding/api-gateway" {
		t.Errorf("project = %q, want the workspace the CLI recorded", got)
	}
	// The IDE's own mapping keeps working.
	if got := antigravityProject("ide-conv"); got != "coding/other" {
		t.Errorf("IDE conversation = %q, want its own workspace", got)
	}
	if got := antigravityProject("neither"); got != "-" {
		t.Errorf("unknown conversation = %q, want the placeholder", got)
	}
	if !isHomeOrEmptyProject("Users/me") || !isHomeOrEmptyProject("home/me") || !isHomeOrEmptyProject("-") || isHomeOrEmptyProject("coding/other") {
		t.Error("isHomeOrEmptyProject should match only empty, placeholder, or home-directory project names")
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
			want:  "coding/api-gateway",
		},
		{
			name: "agent skill and brain artifact paths do not collapse checkout to home",
			paths: []string{
				"/Users/me/.agents/skills/deja-dev/SKILL.md",
				"/Users/me/.gemini/antigravity-cli/brain/1234/roadmap.md",
				"/Users/me/coding/api-gateway/cmd/main.go\n/Users/me/coding/api-gateway/internal/db/pool.go",
			},
			want: "coding/api-gateway",
		},
		{
			name:  "a single file still names its directory",
			paths: []string{"/Users/me/coding/api-gateway/main.go"},
			want:  "coding/api-gateway",
		},
		{
			name:  "two checkouts share only the parent, which is no project",
			paths: []string{"/Users/me/coding/api-gateway/main.go", "/Users/me/coding/billing/main.go"},
			want:  "me/coding",
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
			want:  "coding/api-gateway",
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
	if got, want := antigravityProjectFromFiles(messages), filepath.Base(filepath.Dir(checkout))+"/api-gateway"; got != want {
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
	// Marshalled rather than pasted: the path goes inside a JSON string, and a
	// path can hold a quote as well as a backslash (#2096).
	viewed, err := json.Marshal(map[string]any{
		"source": "MODEL", "type": "VIEW_FILE",
		"content":    "Created At: 2026-08-14T09:00:01Z\nFile Path: file://" + filepath.Join(deep, "pool.go") + "\n\nthe pool is capped at 4",
		"created_at": "2026-08-14T09:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"source":"USER_EXPLICIT","content":"why is the pool exhausted?","created_at":"2026-08-14T09:00:00Z","type":""}
` + string(viewed) + `
{"source":"MODEL","type":"PLANNER_RESPONSE","content":"the pool is capped at 4","created_at":"2026-08-14T09:00:02Z"}
`
	if err = os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseAntigravityFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	if got := sessions[0].Project; got != filepath.Base(filepath.Dir(checkout))+"/api-gateway" {
		t.Fatalf("project = %q; a CLI session with no cache entry is invisible "+
			"to recall without one", got)
	}
}

func TestParseAntigravityCLIToolCallsAndHomeProject(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "api-gateway")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, transcript := antigravityTree(t)
	convID := antigravitySessionID(transcript)
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Session launched in /Users/me so last_conversations.json maps /Users/me -> convID.
	last := `{"/Users/me":"` + convID + `"}`
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), []byte(last), 0o644); err != nil {
		t.Fatal(err)
	}

	poolPath := filepath.Join(checkout, "internal", "db", "pool.go")
	cfgPath := filepath.Join(checkout, "internal", "db", "config.go")
	quoteJSON := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	plannerStep, err := json.Marshal(map[string]any{
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"content":    "Updating pool cap.",
		"created_at": "2026-08-14T09:00:01Z",
		"tool_calls": []map[string]any{
			{
				"id":   "1",
				"name": "read_file",
				"args": map[string]any{"Path": quoteJSON(poolPath)},
			},
			{
				"id":   "2",
				"name": "edit_file",
				"args": map[string]any{
					"Path":        quoteJSON(poolPath),
					"Target":      quoteJSON("const maxDatabaseConnectionPoolSize = 4"),
					"Replacement": quoteJSON("const maxDatabaseConnectionPoolSize = 64"),
				},
			},
			{
				"id":   "3",
				"name": "write_file",
				"args": map[string]any{
					"Path":    quoteJSON(cfgPath),
					"Content": quoteJSON("package db\nconst defaultTimeout = 30\n"),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	genericEditStep, err := json.Marshal(map[string]any{
		"source":     "MODEL",
		"type":       "GENERIC",
		"status":     "DONE",
		"content":    "Created At: 2026-08-14T09:00:02Z\nCompleted At: 2026-08-14T09:00:02Z\nMade 1 replacement in " + poolPath + " starting at line 10 (+1/-1 lines).",
		"created_at": "2026-08-14T09:00:02Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"source":"USER_EXPLICIT","content":"increase pool cap","created_at":"2026-08-14T09:00:00Z","type":"USER_INPUT"}` + "\n" +
		string(plannerStep) + "\n" +
		string(genericEditStep) + "\n"
	if err = os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseAntigravityFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(sessions))
	}
	wantProj := filepath.Base(filepath.Dir(checkout)) + "/api-gateway"
	if sessions[0].Project != wantProj {
		t.Errorf("project = %q, want %q (resolved from CLI tool_calls instead of /Users/me)", sessions[0].Project, wantProj)
	}
	var gotFiles, gotEdit, gotWrote int
	for _, m := range sessions[0].Messages {
		switch m.Role {
		case RoleFiles:
			gotFiles++
		case RoleEdit:
			gotEdit++
		case RoleWrote:
			gotWrote++
		}
	}
	if gotFiles == 0 || gotEdit == 0 || gotWrote < 2 {
		t.Errorf("records: files=%d edit=%d wrote=%d, want >=1 files, >=1 edit, >=2 wrote", gotFiles, gotEdit, gotWrote)
	}

	// Subagent of a home-launched parent inherits the parent's file-resolved project.
	subID := "sub-conv-0001"
	subMetaDir := filepath.Join(root, "brain", convID, ".system_generated", "subagents")
	if err = os.MkdirAll(subMetaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(subMetaDir, subID+".json"), []byte(`{"subagentDescriptor":{"typeName":"research","role":"Codebase Researcher"},"workspaceUris":["file:///Users/me"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	subLogs := filepath.Join(root, "brain", subID, ".system_generated", "logs")
	if err = os.MkdirAll(subLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	subTranscript := filepath.Join(subLogs, "transcript.jsonl")
	subBody := `{"source":"USER_EXPLICIT","content":"research the pool cap","created_at":"2026-08-14T09:01:00Z","type":"USER_INPUT"}` + "\n" +
		`{"source":"MODEL","type":"PLANNER_RESPONSE","content":"The pool cap is 64.","created_at":"2026-08-14T09:01:05Z"}` + "\n"
	if err = os.WriteFile(subTranscript, []byte(subBody), 0o644); err != nil {
		t.Fatal(err)
	}
	subSessions, err := ParseAntigravityFile(subTranscript)
	if err != nil || len(subSessions) != 1 {
		t.Fatalf("subagent parse: len=%d err=%v", len(subSessions), err)
	}
	if subSessions[0].Project != wantProj {
		t.Errorf("subagent project = %q, want parent's resolved project %q", subSessions[0].Project, wantProj)
	}
}

func TestAntigravityDropsHookDeniedToolCall(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	body := `{"source":"USER_EXPLICIT","content":"check status","created_at":"2026-08-14T09:00:00Z","type":"USER_INPUT"}` + "\n" +
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-08-14T09:00:01Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"git status -s && git log -1"}},{"name":"run_command","args":{"CommandLine":"git status -s"}}]}` + "\n" +
		`{"source":"MODEL","type":"GENERIC","status":"ERROR","content":"Encountered error in tool execution: tool call denied by pre-tool hook","created_at":"2026-08-14T09:00:02Z"}` + "\n" +
		`{"source":"MODEL","type":"GENERIC","status":"DONE","content":"The command exited with code 0.\nOutput:\nM README.md","created_at":"2026-08-14T09:00:03Z"}` + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseAntigravityFile(transcript)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseAntigravityFile: len=%d err=%v", len(sessions), err)
	}
	var cmds, outputs []string
	for _, m := range sessions[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outputs = append(outputs, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ git status -s  → exit 0" {
		t.Errorf("commands = %v, want only [\"$ git status -s  → exit 0\"] (hook-denied command dropped)", cmds)
	}
	if len(outputs) != 1 || outputs[0] != "The command exited with code 0.\nOutput:\nM README.md" {
		t.Errorf("outputs = %v, want 1 output containing M README.md", outputs)
	}
}
