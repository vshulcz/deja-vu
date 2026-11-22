package sources

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Approving a plan writes a USER_INPUT step whose <USER_REQUEST> is empty: the
// IDE's own comment about the document, with the person contributing nothing.
// deja indexed the comment as their words, opening tag and absolute path
// included — two of the 67 real user records on this machine (#3326).
func TestAntigravitySkipsAPlanApprovalWithNoRequestInIt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "brain", "f017fad5-095f-47ee-abf7-c13b7ce6844a", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`,
		`{"step_index":13,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:45:28Z","content":"Comments on artifact URI: file:///w/api/implementation_plan.md\n\nThe user has approved this document.\n\n\n<USER_REQUEST>\n\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-07-08T17:45:28+03:00.\n</ADDITIONAL_METADATA>"}`,
		`{"step_index":14,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:47:00Z","content":"Comments on artifact URI: file:///w/api/implementation_plan.md\n\n<USER_REQUEST>\ncap the retries at three\n</USER_REQUEST>"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseAntigravityFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got))
	}
	var users []string
	for _, m := range got[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 2 {
		t.Fatalf("user turns = %d, want 2 — the approval is the IDE's own record: %q", len(users), users)
	}
	for _, u := range users {
		if strings.Contains(u, "has approved this document") || strings.Contains(u, "USER_REQUEST") ||
			strings.Contains(u, "artifact URI") {
			t.Errorf("a user turn carries the IDE's own text: %q", u)
		}
	}
	if users[1] != "cap the retries at three" {
		t.Errorf("second turn = %q, want the words inside the request tag", users[1])
	}
}

// Whatever the person wrote around the tag stays: a line above it, and a turn
// that carries two request blocks with words between them (review of #3326).
func TestAntigravityKeepsWhatIsWrittenAroundTheRequestTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"also note: <USER_REQUEST>\nplease fix the bug\n</USER_REQUEST>", "also note:\nplease fix the bug"},
		{"<USER_REQUEST>first</USER_REQUEST> and also <USER_REQUEST>second</USER_REQUEST>", "first\nand also\nsecond"},
		{"Comments on artifact URI: file:///w/api/implementation_plan.md\n\nThe user has approved this document.\n\n\n<USER_REQUEST>\n\n</USER_REQUEST>", ""},
	}
	for _, c := range cases {
		if got := cleanAntigravityUserContent(c.in); got != c.want {
			t.Errorf("cleanAntigravityUserContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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

// An edit through replace_file_content names its file in a sentence, not on
// a "File Path:" line, and carries the change as a diff block; the step gave
// only tool output, so blame and files never saw the edit (#3279). Shape from
// a real IDE transcript.
func TestAntigravityEditStepNamesTheFileAndTheChange(t *testing.T) {
	content := "Created At: 2026-06-29T12:03:31Z\nCompleted At: 2026-06-29T12:03:37Z\n" +
		"The following changes were made by the replace_file_content tool to: /Users/me/coding/app/internal/refund/application.go. If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.\n" +
		"[diff_block_start]\n@@ -18,14 +18,17 @@\n \tRunInTx(ctx context.Context) error\n }\n \n-// Application handles order refund operations\n+// OrderService defines the interface for order operations\n+type OrderService interface {\n+}\n[diff_block_end]\n"
	at := time.Date(2026, 6, 29, 12, 3, 31, 0, time.UTC)
	var files, edits []string
	for _, m := range antigravityStep("CODE_ACTION", content, at) {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	if len(files) != 1 || files[0] != "/Users/me/coding/app/internal/refund/application.go" {
		t.Errorf("files = %q, want the edited path", files)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/Users/me/coding/app/internal/refund/application.go\n") ||
		!strings.Contains(edits[0], "// Application handles order refund operations") {
		t.Errorf("edits = %q, want the path and the removed lines", edits)
	}
	// write_to_file says the same sentence with its own tool name.
	created := "Created At: 2026-06-29T12:05:00Z\nThe following changes were made by the write_to_file tool to: /Users/me/coding/app/new.go. If relevant, run it.\n"
	var got []string
	for _, m := range antigravityStep("CODE_ACTION", created, at) {
		if m.Role == RoleFiles {
			got = append(got, m.Text)
		}
	}
	if len(got) != 1 || got[0] != "/Users/me/coding/app/new.go" {
		t.Errorf("write_to_file files = %q", got)
	}
}

// Antigravity writes the file a step touched as a `file://` URI, and deja
// stripped the scheme textually — which leaves the percent-encoding where it
// was. That is the same shape that put `/c%3A/Users/me/my%20app/main.go` into a
// Copilot Chat files record until #3498, and it is invisible here: of 329
// `file://` URIs in this machine's Antigravity transcripts, none is encoded,
// because no path on it has a space (#3505).
func TestAntigravityDecodesAnEncodedFilePath(t *testing.T) {
	for name, tc := range map[string]struct{ line, want string }{
		"a space": {
			line: "File Path: file:///Users/me/my%20app/main.go",
			want: "/Users/me/my app/main.go",
		},
		"a drive letter and a space": {
			line: "File Path: file:///c%3A/Users/me/my%20app/main.go",
			want: "/c:/Users/me/my app/main.go",
		},
		"nothing to decode": {
			line: "File Path: file:///Users/me/app/main.go",
			want: "/Users/me/app/main.go",
		},
		// A per-cent that is not an escape stays as it is: there the text was
		// never encoded, and decoding it would invent a path.
		"a literal per-cent": {
			line: "File Path: file:///Users/me/app/report%.md",
			want: "/Users/me/app/report%.md",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", filepath.Join(root, "home"))
			ag := filepath.Join(root, "antigravity")
			t.Setenv("DEJA_ANTIGRAVITY_ROOT", ag)
			dir := filepath.Join(ag, "brain", "traj", ".system_generated", "logs")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "transcript.jsonl")
			// A CODE_ACTION step is the one that names a file, and its kind is
			// what decides that — not its source (#3331).
			rec := `{"source":"MODEL","type":"CODE_ACTION","created_at":"2026-01-02T03:04:05Z","content":` +
				jsonString("Edited file\n"+tc.line+"\nthe retry budget is three now") + "}\n"
			if err := os.WriteFile(path, []byte(rec), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseAntigravityFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range ss {
				for _, m := range s.Messages {
					if m.Role == RoleFiles {
						got = append(got, m.Text)
					}
				}
			}
			if len(got) == 0 {
				t.Fatalf("no files record at all: %#v", ss)
			}
			for _, p := range got {
				if p != tc.want {
					t.Errorf("files record has %q, want %q", p, tc.want)
				}
			}
		})
	}
}

// What an Antigravity store keeps beside the transcript deja reads, and what
// it does not: the row that reports an unread transcript is only as good as
// this list, and a first cut that exempted everything outside a conversation
// swallowed a transcript restored beside one (#3377).
func TestAntigravitySidecarFilesClaimsOnlyWhatItKnows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	brain := filepath.Join(root, "brain", "f017fad5", ".system_generated")
	for _, d := range []string{
		filepath.Join(brain, "logs", "chunks", "transcript"),
		filepath.Join(brain, "messages"),
		filepath.Join(brain, "subagents"),
		filepath.Join(root, "brain", "f017fad5", "scratch"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "restored_backup"),
		filepath.Join(root, "brain", "sessB", ".system_generated", "logs"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claimed := map[string]bool{
		filepath.Join(brain, "logs", "transcript_full.jsonl"):                                       true,
		filepath.Join(brain, "logs", "chunks", "transcript", "0.jsonl"):                             true,
		filepath.Join(brain, "messages", "8f1c2a3e.json"):                                           true,
		filepath.Join(brain, "subagents", "child-1.json"):                                           true,
		filepath.Join(root, "brain", "f017fad5", "scratch", "notes.json"):                           true,
		filepath.Join(root, "brain", "f017fad5", "task.md.metadata.json"):                           true,
		filepath.Join(root, "cache", "last_conversations.json"):                                     true,
		filepath.Join(root, "settings.json"):                                                        true,
		filepath.Join(brain, "logs", "transcript.jsonl"):                                            false,
		filepath.Join(root, "restored_backup", "transcript.jsonl"):                                  false,
		filepath.Join(root, "brain", "sessB", ".system_generated", "logs", "transcript_full.jsonl"): false,
	}
	for p := range claimed {
		write(p)
	}

	got := AntigravitySidecarFiles()
	for p, want := range claimed {
		has := slices.Contains(got, p)
		if has != want {
			verb := "claimed"
			if want {
				verb = "left out"
			}
			t.Errorf("%s: %s by the sidecar list", filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), verb)
		}
	}
}

// Gemini's own scratch under `tmp`: the per-project log the CLI writes beside
// the chats. Everything else under that tree is a chat deja either read or did
// not (#3397).
func TestGeminiSidecarFilesClaimsOnlyTheLog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats := filepath.Join(root, "tmp", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "tmp", "proj", "logs.json"),
		filepath.Join(chats, "session-2026-08-22T06-32-6b36be8d.jsonl"),
	} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := GeminiSidecarFiles()
	if len(got) != 1 || filepath.Base(got[0]) != "logs.json" {
		t.Errorf("GeminiSidecarFiles() = %v, want the project log alone", got)
	}
}

func antigravityTree(t *testing.T) (root, transcript string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	transcript = filepath.Join(root, "brain", "traj-123", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, transcript
}

func TestParseAntigravityFile(t *testing.T) {
	_, p := antigravityTree(t)
	long := strings.Repeat("x", 70*1024)
	lines := `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nBuild this\n<USER_SETTINGS_CHANGE>{\"theme\":\"dark\"}</USER_SETTINGS_CHANGE>\n<ADDITIONAL_METADATA>{\"cwd\":\"/tmp\"}</ADDITIONAL_METADATA>\n</USER_REQUEST>"}
{"step_index":2,"source":"SYSTEM","type":"SYSTEM","created_at":"2026-07-08T14:18:28Z","content":"system noise"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:29Z","thinking":"secret reasoning","content":"I can help."}
{"step_index":4,"source":"MODEL","type":"CODE_ACTION","created_at":"2026-07-08T14:18:30Z","content":""}
not-json
{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:31Z","content":"` + long + `"}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1: %#v", len(ss), ss)
	}
	s := ss[0]
	if s.Harness != "antigravity" || s.ID != "traj-123" || s.Project != "-" {
		t.Fatalf("bad meta: %#v", s)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "Build this" {
		t.Fatalf("user unwrap wrong: %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "I can help." || strings.Contains(s.Messages[1].Text, "secret") {
		t.Fatalf("assistant wrong: %#v", s.Messages[1])
	}
	if s.Messages[0].Time.Format("2006-01-02T15:04:05Z") != "2026-07-08T14:18:27Z" {
		t.Fatalf("timestamp wrong: %v", s.Messages[0].Time)
	}
	if s.Started != s.Messages[0].Time || s.Updated != s.Messages[2].Time {
		t.Fatalf("session times wrong: started=%v updated=%v", s.Started, s.Updated)
	}
	// The parser passes the full 70 KiB body through; ingest redacts and caps it
	// to 64 KiB, so the truncation the index needs happens after redaction, not here.
	if len(s.Messages[2].Text) != 70*1024 {
		t.Fatalf("message len = %d, want %d", len(s.Messages[2].Text), 70*1024)
	}
}

func TestAntigravityTranscriptsEnvOverride(t *testing.T) {
	root, p := antigravityTree(t)
	if err := os.WriteFile(p, []byte(`{"source":"USER_EXPLICIT","created_at":"2026-07-08T14:18:27Z","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := AntigravityTranscripts()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("transcripts = %v, want %s", files, p)
	}
	roots := AntigravityRoots()
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots = %v, want %s", roots, root)
	}
}

func TestAntigravityRootsGlob(t *testing.T) {
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	want := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "antigravity-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	roots := AntigravityRoots()
	if len(roots) != 1 || roots[0] != want {
		t.Fatalf("roots = %v, want %s", roots, want)
	}
}

// Antigravity puts prose and tool transcripts in the same MODEL stream, and
// reading only the source made shell dumps into assistant speech — 333 of 369
// rows on a real store, 90%, ranked as things the agent said. The step's kind
// is what separates them, and the same header that identifies a tool step
// carries the work: a command on "Task Description:", a file on "File Path:".
func TestAntigravityStepSeparatesProseFromTools(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "brain", "conv-1", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	// The workspace the session belongs to, which the parser used to ignore.
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"),
		[]byte(`{"conversations":{"conv-1":{"summary":{"WorkspaceURIs":["file:///Users/me/coding/api-gateway"]}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", dir)
	rows := []string{
		`{"source":"USER_EXPLICIT","type":"USER_REQUEST","created_at":"2026-07-20T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-20T10:01:00Z","content":"I will start by reading the file."}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-07-20T10:02:00Z","content":"Created At: 2026-07-20T10:02:00Z\nTask Description: go test ./...\nOutput:\nFAIL\tgithub.com/x/y"}`,
		`{"source":"MODEL","type":"VIEW_FILE","created_at":"2026-07-20T10:03:00Z","content":"Created At: 2026-07-20T10:03:00Z\nCompleted At: 2026-07-20T10:03:01Z\nFile Path: ` + "`" + `file:///w/app/retry.go` + "`" + `\nTotal Lines: 40"}`,
		`{"source":"MODEL","type":"GREP_SEARCH","created_at":"2026-07-20T10:04:00Z","content":"Created At: 2026-07-20T10:04:00Z\nCompleted At: 2026-07-20T10:04:00Z"}`,
	}
	p := filepath.Join(logs, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	// Reachable by project: every session used to report "-".
	if ss[0].Project != "coding/api-gateway" {
		t.Errorf("project = %q, want the workspace from the metadata", ss[0].Project)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// Prose is the only thing that counts as speech.
	if len(byRole["assistant"]) != 1 || !strings.Contains(byRole["assistant"][0], "reading the file") {
		t.Errorf("assistant = %q, want only the planner prose", byRole["assistant"])
	}
	for _, blob := range byRole["assistant"] {
		if strings.Contains(blob, "Task Description") || strings.Contains(blob, "File Path") {
			t.Errorf("a tool transcript is still speech: %q", blob)
		}
	}
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./..." {
		t.Errorf("commands = %q", byRole[RoleCommand])
	}
	if len(byRole[RoleFiles]) != 1 || byRole[RoleFiles][0] != "/w/app/retry.go" {
		t.Errorf("files = %q, want the plain path without the file:// scheme or backticks", byRole[RoleFiles])
	}
	// A step whose whole body is its own timestamps has nothing to index.
	for _, out := range byRole[RoleToolOutput] {
		if strings.HasPrefix(out, "Created At:") || out == "" {
			t.Errorf("indexed a step with no content but its header: %q", out)
		}
	}
}

func TestAntigravityBodyDropsTheHeader(t *testing.T) {
	got := antigravityBody("Created At: 2026-07-20T10:02:00Z\nCompleted At: 2026-07-20T10:02:01Z\nthe real output")
	if got != "the real output" {
		t.Fatalf("got %q", got)
	}
	if got := antigravityBody("Created At: x\nCompleted At: y"); got != "" {
		t.Fatalf("a header-only step should strip to empty, got %q", got)
	}
}

func TestAntigravityPathForms(t *testing.T) {
	for in, want := range map[string]string{
		"File Path: `file:///w/a.go`":              "/w/a.go",
		"File Path: file:///w/b.go":                "/w/b.go",
		"Created file file:///w/c.md with content": "/w/c.md",
		"File Path: not-a-uri":                     "",
	} {
		if got := antigravityPath(in); got != want {
			t.Errorf("antigravityPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every antigravity session reported project "-", so the harness was
// unreachable from any --project query — while the workspace sat in plain
// JSON in a directory deja already walks.
func TestAntigravityProjectComesFromTheMetadata(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"conversations":{"conv-1-abc":{"summary":{"WorkspaceURIs":["file:///Users/me/coding/api-gateway"]}}}}`
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	if got := antigravityProject("conv-1-abc"); got != "coding/api-gateway" {
		t.Fatalf("project = %q, want the workspace from the metadata", got)
	}
	// A conversation the metadata does not know still has to parse.
	if got := antigravityProject("never-seen"); got != "-" {
		t.Fatalf("unknown conversation = %q, want the placeholder", got)
	}
}

func TestAntigravitySubagentLineageAndWorkspaceFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	parentSub := filepath.Join(root, "brain", "parent-conv", ".system_generated", "subagents")
	childLogs := filepath.Join(root, "brain", "child-conv", ".system_generated", "logs")
	for _, d := range []string{parentSub, childLogs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	desc := `{"conversationId":"child-conv","subagentDescriptor":{"typeName":"self","role":"Index Auditor"},"state":"SUBAGENT_STATE_ALIVE","spawnStepIndex":10,"workspaceUris":["file:///Users/me/coding/deja-vu"]}`
	if err := os.WriteFile(filepath.Join(parentSub, "child-conv.json"), []byte(desc), 0o644); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(childLogs, "transcript.jsonl")
	row := `{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-31T12:00:00Z","content":"<USER_REQUEST>\naudit the index\n</USER_REQUEST>"}` + "\n" +
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-31T12:01:00Z","content":"Done."}` + "\n"
	if err := os.WriteFile(childPath, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	if !AntigravitySubagentFile(childPath) {
		t.Fatalf("AntigravitySubagentFile(%s) = false, want true", childPath)
	}
	ss, err := ParseAntigravityFile(childPath)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseAntigravityFile = %v, %#v", err, ss)
	}
	if ss[0].Kind != "subagent" || ss[0].Parent != "parent-conv" || ss[0].Agent != "Index Auditor" {
		t.Fatalf("subagent meta = kind=%q parent=%q agent=%q", ss[0].Kind, ss[0].Parent, ss[0].Agent)
	}
	if ss[0].Project != "coding/deja-vu" {
		t.Fatalf("project = %q, want coding/deja-vu from workspaceUris fallback", ss[0].Project)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := AntigravityTranscripts(); len(got) != 0 {
		t.Fatalf("DEJA_INCLUDE_SUBAGENTS=0 kept subagent transcript: %v", got)
	}
	newest, err := ParseAntigravityNewest(childPath)
	if err != nil || len(newest) != 1 {
		t.Fatalf("ParseAntigravityNewest = %v, %#v", err, newest)
	}
	toolOnly := filepath.Join(childLogs, "tool.jsonl")
	toolRow := `{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-31T12:01:00Z","content":" Done. ","tool_calls":[{"name":"run_command","arguments":{"CommandLine":"ls"}}]}` + "\n"
	if err := os.WriteFile(toolOnly, []byte(toolRow), 0o644); err != nil {
		t.Fatal(err)
	}
	if newest, err := ParseAntigravityNewest(toolOnly); err != nil || len(newest) != 1 {
		t.Fatalf("ParseAntigravityNewest(toolOnly) = %v, %#v", err, newest)
	}
	proseOnly := filepath.Join(childLogs, "prose.jsonl")
	proseRow := `{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-31T12:01:00Z","content":"Only prose"}` + "\n"
	if err := os.WriteFile(proseOnly, []byte(proseRow), 0o644); err != nil {
		t.Fatal(err)
	}
	if newest, err := ParseAntigravityNewest(proseOnly); err != nil || len(newest) != 1 {
		t.Fatalf("ParseAntigravityNewest(proseOnly) = %v, %#v", err, newest)
	}
}

func parseAntigravityLines(t *testing.T, lines string) map[string][]string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	dir := filepath.Join(root, "brain", "7d1e0c3a-5b6f-4a2e-9c1d-0e2f3a4b5c6d", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	got["project"] = []string{ss[0].Project}
	return got
}

// The planner row carries the structured call; the RUN_COMMAND step after it
// need not name the command at all, and then the command was lost (#4358).
func TestAntigravityReadsTheCommandAndFilesFromToolCalls(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop in client.go\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj","WaitMsBeforeAsync":500}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"--- FAIL: TestRetry (0.00s)\nFAIL"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"/tmp/proj/client.go"}},{"name":"replace_file_content","args":{"TargetFile":"/tmp/proj/client.go"}},{"name":"write_to_file","args":{"TargetFile":"/tmp/proj/retry_test.go"}}]}
{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:20Z","content":"Fixed the off-by-one in the retry loop."}
`)
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the run_command call", c)
	}
	f := got[RoleFiles]
	if len(f) != 3 || f[0] != "/tmp/proj/client.go" || f[2] != "/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want the viewed, edited and written paths", f)
	}
	// No files outside the root directory: the command's Cwd names the project.
	if p := got["project"]; p[0] != "tmp/proj" {
		t.Errorf("project = %q, want the command's Cwd", p)
	}
}

// When the step does carry the header too, the command is the same one run
// once, not two.
func TestAntigravityToolCallAndStepHeaderAreOneCommand(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj"}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"Task Description: go test ./...\n--- FAIL: TestRetry (0.00s)"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"Again.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj"}}]}
{"step_index":4,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:15Z","content":"Task Description: go test ./...\nok"}
{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:20Z","content":"Edited.","tool_calls":[{"name":"replace_file_content","args":{"TargetFile":"/tmp/proj/client.go"}}]}
{"step_index":6,"source":"MODEL","type":"CODE_ACTION","status":"DONE","created_at":"2026-09-30T10:00:21Z","content":"The following changes were made by the replace_file_content tool to: /tmp/proj/client.go. If relevant, run it.\n"}
`)
	if c := got[RoleCommand]; len(c) != 2 {
		t.Errorf("commands = %q, want one per run", c)
	}
	if f := got[RoleFiles]; len(f) != 1 {
		t.Errorf("files = %q, want the edit once", f)
	}
}

// On disk every arg is itself JSON: CommandLine is `"go test ./..."`, quotes
// and all, and so are Cwd, AbsolutePath and TargetFile. Read as they stand,
// the command was indexed with its quotes and a second time from the step
// header without them, and no path was absolute (#4358).
func TestAntigravityToolCallArgsAreJSONEncoded(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\"","Cwd":"\"/tmp/proj\"","WaitMsBeforeAsync":500}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"Task Description: go test ./...\n--- FAIL: TestRetry (0.00s)"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"\"/tmp/proj/client.go\""}},{"name":"write_to_file","args":{"TargetFile":"\"/tmp/proj/retry_test.go\""}}]}
`)
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the one run, unquoted", c)
	}
	if f := got[RoleFiles]; len(f) != 2 || f[0] != "/tmp/proj/client.go" || f[1] != "/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want the paths unquoted", f)
	}
	if p := got["project"]; p[0] != "tmp/proj" {
		t.Errorf("project = %q, want tmp/proj", p)
	}
}

// The same diff block carries the written side, and some blocks on a real
// store carry unified headers — a "+++ b/<path>" line is long enough to be
// hashed as a written line, and would then answer blame with a file header
// nobody wrote (#3773).
func TestAntigravityRecordsTheWrittenSideOfADiffBlock(t *testing.T) {
	const added = "// OrderService defines the interface for order operations"
	const removed = "// Application handles order refund operations"
	const header = "+++ b/internal/refund/application.go"
	content := "Created At: 2026-06-29T12:03:31Z\n" +
		"The following changes were made by the replace_file_content tool to: /Users/me/coding/app/internal/refund/application.go. If relevant, run it.\n" +
		"[diff_block_start]\n--- a/internal/refund/application.go\n" + header + "\n@@ -18,14 +18,17 @@\n" +
		" \tRunInTx(ctx context.Context) error\n-" + removed + "\n+" + added +
		"\n+type OrderService interface {\n[diff_block_end]\n"
	at := time.Date(2026, 6, 29, 12, 3, 31, 0, time.UTC)

	var wrote []string
	for _, m := range antigravityStep("CODE_ACTION", content, at) {
		if m.Role == RoleWrote {
			wrote = append(wrote, m.Text)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	h, ok := HashWrittenLine(added)
	if !ok {
		t.Fatal("the added line is not evidence")
	}
	path, has := WroteRecordHas(wrote[0], h)
	if !has {
		t.Errorf("the record does not hold the added line: %q", wrote[0])
	}
	if path != "/Users/me/coding/app/internal/refund/application.go" {
		t.Errorf("the record is filed under %q", path)
	}
	// Nothing else in the block is a written line: not the removed side, not
	// the context line, and not the headers in either of the forms a missing
	// guard would leave them ("+++ b/x" or "++ b/x").
	want := map[string]bool{}
	for _, line := range []string{added, "type OrderService interface {"} {
		if h, ok := HashWrittenLine(line); ok {
			want[strconv.FormatUint(h, 16)] = true
		}
	}
	_, hashes, _ := strings.Cut(wrote[0], "\n")
	for got := range strings.FieldsSeq(hashes) {
		if !want[got] {
			t.Errorf("the record holds a hash of something the block did not write: %q", wrote[0])
		}
	}
	if len(strings.Fields(hashes)) != len(want) {
		t.Errorf("hashes = %d, want %d", len(strings.Fields(hashes)), len(want))
	}
}

func TestAntigravityRecordsAgyRunCommandExits(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "brain", "conv-agy-exits", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-01T10:00:00Z","content":"run tests"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-01T10:00:01Z","content":"running ok","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./..."}}]}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-07-01T10:00:02Z","content":"Command exited with code 0.\nCommand output:\nok"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-01T10:00:03Z","content":"running fail","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./fail"}}]}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-07-01T10:00:04Z","content":"Command exited with code 2.\nCommand output:\nFAIL"}`,
	}, "\n") + "\n"
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	ss, err := ParseAntigravityFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{
		"$ go test ./...  → exit 0",
		"$ go test ./fail  → exit 2",
	}
	if !slices.Equal(cmds, want) {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

func TestAntigravityDeniedToolCallDoesNotCountAsToolError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "brain", "conv-denied", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-01T10:00:00Z","content":"run command"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-01T10:00:01Z","content":"trying","tool_calls":[{"name":"run_command","args":{"CommandLine":"rm -rf /tmp/x"}},{"name":"run_command","args":{"CommandLine":"go test ./..."}}]}`,
		`{"source":"MODEL","type":"RUN_COMMAND","status":"ERROR","created_at":"2026-07-01T10:00:02Z","content":"Encountered error in step execution: tool call denied"}`,
		`{"source":"MODEL","type":"RUN_COMMAND","status":"ERROR","created_at":"2026-07-01T10:00:03Z","content":"Command exited with code 1.\nCommand output:\nboom"}`,
	}, "\n") + "\n"
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	ss, err := ParseAntigravityFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	if ss[0].ToolErrors != 1 {
		t.Fatalf("ToolErrors = %d, want 1 (denied step must not count as error)", ss[0].ToolErrors)
	}
}

func TestAntigravityProjectDeterministicAcrossWorkspaces(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Users", "alex")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	last, err := json.Marshal(map[string]string{
		home:               "conv-multi",
		"/tmp/work/repo-b": "conv-multi",
		"/tmp/work/repo-a": "conv-multi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), last, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	for range 5 {
		if got := antigravityProject("conv-multi"); got != "work/repo-a" {
			t.Fatalf("antigravityProject = %q, want %q", got, "work/repo-a")
		}
	}
}

func TestAntigravitySkipsSchedulerTaskDescriptionsButKeepsGenericCommand(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "brain", "conv-timer", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-20T10:00:00Z","content":"check status"}`,
		`{"source":"MODEL","type":"GENERIC","created_at":"2026-07-20T10:01:00Z","content":"Created At: 2026-07-20T10:01:00Z\nTask Description: Timer: 60s (check status)"}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-07-20T10:01:30Z","content":"Created At: 2026-07-20T10:01:30Z\nTask Description: Cron: */5 * * * * cleanup"}`,
		`{"source":"MODEL","type":"GENERIC","created_at":"2026-07-20T10:02:00Z","content":"Created At: 2026-07-20T10:02:00Z\nTask Description: golangci-lint run"}`,
	}
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	ss, err := ParseAntigravityFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ golangci-lint run" {
		t.Fatalf("commands = %q, want only golangci-lint run", cmds)
	}
}

func TestAntigravityEditCallsExtractsMCPPathsAndSkipsScratch(t *testing.T) {
	toolCalls := []any{
		map[string]any{
			"name": "mcp_fs_read",
			"args": map[string]any{
				"paths": []any{
					"/Users/alex/.gemini/antigravity/brain/conv-1/scratch/plan.md",
					"/work/app/pkg/billing/invoice.go",
				},
			},
		},
		map[string]any{
			"name": "mcp_fs_write",
			"args": map[string]any{
				"AbsolutePath": "file:///work/app/pkg/billing/ledger.go",
				"Target":       "oldTotal",
				"Replacement":  "newTotal",
			},
		},
	}

	got := AntigravityEditCalls(toolCalls)
	want := []EditCall{
		{
			ToolName: "mcp_fs_read",
			FilePath: "/work/app/pkg/billing/invoice.go",
		},
		{
			ToolName:  "mcp_fs_write",
			FilePath:  "/work/app/pkg/billing/ledger.go",
			OldString: "oldTotal",
			NewString: "newTotal",
		},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("AntigravityEditCalls() = %+v, want %+v", got, want)
	}
}
