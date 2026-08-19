package sources

import (
	"fmt"
	"strings"
	"testing"
)

// A thread that talks and works: two commands, several files, and the calls
// that name neither. Shaped after a real store — Zed keeps tool calls in the
// same content array as the prose, tagged ToolUse.
const zedWorkBody = `{"version":"0.3.0","title":"worked thread","updated_at":"2026-07-19T09:00:02Z","messages":[
{"User":{"id":"u1","content":[{"Text":"the retry queue stalls on staging"}]}},
{"Agent":{"content":[
 {"Text":"Let me look."},
 {"ToolUse":{"id":"c1","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c2","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}},
 {"ToolUse":{"id":"c3","name":"grep","input":{"regex":"backoff","include_pattern":"**/*.go"}}}
],"tool_results":{},"reasoning_details":null}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c4","name":"edit_file","input":{"path":"/w/app/queue/retry.go","display_description":"spread the wakeups","mode":"edit"}}},
 {"ToolUse":{"id":"c5","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c6","name":"move_path","input":{"source_path":"/w/app/old.go","destination_path":"/w/app/queue/jitter.go"}}},
 {"ToolUse":{"id":"c7","name":"terminal","input":{"command":"go build ./...","cd":"/w/app"}}},
 {"Text":"We spread the wakeups over a second."}
],"tool_results":{},"reasoning_details":null}}
]}`

func zedWorkedStore(t *testing.T) []string {
	t.Helper()
	zedHome(t)
	hex := zedZstdHex(t, zedWorkBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('w1', 'worked thread', '2026-07-19T09:00:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-07-19T08:00:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	sessions, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	var roles []string
	for _, m := range sessions[0].Messages {
		roles = append(roles, m.Role+"\t"+m.Text)
	}
	return roles
}

// Zed keeps what the agent did in the same place as what it said, and deja read
// only the saying. Measured on a real 30-thread store: 797 ToolUse blocks
// against 69 of agent prose, so `deja how`, `deja blame`, the files-touched
// line and the worked-on-most ranking were all blind on a Zed history.
func TestZedIndexesTheCommandsAThreadRan(t *testing.T) {
	rows := zedWorkedStore(t)

	var commands []string
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleCommand {
			commands = append(commands, text)
		}
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want the two terminal calls", commands)
	}
	for i, want := range []string{"$ go test ./queue/...", "$ go build ./..."} {
		if commands[i] != want {
			t.Errorf("command %d = %q, want %q", i, commands[i], want)
		}
	}
	// The prose is still there: the work is added beside it, not instead.
	joined := strings.Join(rows, "\n")
	for _, said := range []string{"the retry queue stalls on staging", "We spread the wakeups over a second."} {
		if !strings.Contains(joined, said) {
			t.Errorf("the conversation lost %q:\n%s", said, joined)
		}
	}
}

func TestZedIndexesTheFilesAThreadTouched(t *testing.T) {
	rows := zedWorkedStore(t)

	var files []string
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleFiles {
			files = append(files, strings.Split(text, "\n")...)
		}
	}
	want := []string{
		"/w/app/queue/retry.go",
		"/w/app/old.go",
		"/w/app/queue/jitter.go",
	}
	for _, w := range want {
		found := false
		for _, f := range files {
			if f == w {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is missing from %v", w, files)
		}
	}
	// A path is recorded once per message however often the thread reads it:
	// the record is which files the work touched, not how many reads it took.
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role != RoleFiles {
			continue
		}
		seen := map[string]bool{}
		for _, p := range strings.Split(text, "\n") {
			if seen[p] {
				t.Errorf("%s is listed twice in one record: %q", p, text)
			}
			seen[p] = true
		}
	}
}

// A search names a pattern and a scope, not a file the session worked on, and
// a path inside a shell command is guesswork — the same line claude's parser
// draws.
func TestZedLeavesSearchesAndShellPathsOutOfTheFileRecord(t *testing.T) {
	rows := zedWorkedStore(t)

	joined := strings.Join(rows, "\n")
	for _, absent := range []string{"backoff", "**/*.go"} {
		if strings.Contains(joined, absent) {
			t.Errorf("a grep's pattern reached the index: %q in\n%s", absent, joined)
		}
	}
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleFiles && strings.Contains(text, "/w/app\n") {
			t.Errorf("a terminal's working directory was recorded as a file: %q", text)
		}
	}
}
