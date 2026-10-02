package sources

import (
	"strings"
	"testing"
)

// dsh writes every tool call twice: as a tool-call block inside the
// assistant/message and as a tool/call event. Shapes from dsh 0.1.1-rc.2: bash,
// edit and write from the fs tools, and str_replace_editor, which names its
// file `path` and its spans old_str/new_str/file_text (#4291).
const deepSeekToolLog = `{"type":"session","version":0,"id":"session-11111111-2222-3333-4444-555555555555","createdAt":1790874400000,"cwd":"/tmp/proj","delegationDepth":0}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"fix the retry loop"}],"source":{"kind":"user"},"role":"user","id":"u1"}}
{"type":"assistant/message","seq":18,"time":1790874400200,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_1","name":"bash","arguments":"{\"command\": \"go test ./retry/...\", \"description\": \"run the tests\"}"}],"source":{"kind":"model"},"id":"a1"}}}
{"type":"tool/call","seq":19,"time":1790874400200,"data":{"turn":1,"step":1,"callId":"call_1","name":"bash","arguments":"{\"command\": \"go test ./retry/...\", \"description\": \"run the tests\"}"}}
{"type":"tool/result","seq":20,"time":1790874400300,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_1"},"content":[{"type":"tool-result","toolCallId":"call_1","content":[{"type":"text","text":"ok  \texample.com/proj/retry\t0.01s"}],"isError":false}],"role":"user","id":"t1"}}}
{"type":"assistant/message","seq":21,"time":1790874400400,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_2","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}],"source":{"kind":"model"},"id":"a2"}}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"turn":1,"step":2,"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"turn":1,"step":2,"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file /tmp/proj/retry.go has been updated successfully."}],"isError":false}],"role":"user","id":"t2"}}}
{"type":"assistant/message","seq":24,"time":1790874400600,"data":{"turn":1,"step":3,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_3","name":"write","arguments":"{\"file_path\": \"/tmp/proj/PLAN.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"},{"type":"tool-call","id":"call_4","name":"str_replace_editor","arguments":"{\"command\": \"str_replace\", \"path\": \"/tmp/proj/backoff.go\", \"old_str\": \"delay *= 2\", \"new_str\": \"delay = min(delay*2, maxDelay) // capped backoff\"}"}],"source":{"kind":"model"},"id":"a3"}}}
{"type":"tool/call","seq":25,"time":1790874400600,"data":{"turn":1,"step":3,"callId":"call_3","name":"write","arguments":"{\"file_path\": \"/tmp/proj/PLAN.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"}}
{"type":"tool/call","seq":26,"time":1790874400600,"data":{"turn":1,"step":3,"callId":"call_4","name":"str_replace_editor","arguments":"{\"command\": \"str_replace\", \"path\": \"/tmp/proj/backoff.go\", \"old_str\": \"delay *= 2\", \"new_str\": \"delay = min(delay*2, maxDelay) // capped backoff\"}"}}
{"type":"tool/result","seq":27,"time":1790874400650,"data":{"turn":1,"step":3,"message":{"source":{"kind":"tool","callId":"call_3"},"content":[{"type":"tool-result","toolCallId":"call_3","content":[{"type":"text","text":"<path>/tmp/proj/PLAN.md</path>\n<type>file</type>\n<content>\nCreated file\n</content>"}],"isError":false}],"role":"user","id":"t3"}}}
{"type":"tool/result","seq":28,"time":1790874400660,"data":{"turn":1,"step":3,"message":{"source":{"kind":"tool","callId":"call_4"},"content":[{"type":"tool-result","toolCallId":"call_4","content":[{"type":"text","text":"The file /tmp/proj/backoff.go has been edited."}],"isError":false}],"role":"user","id":"t4"}}}
{"type":"assistant/message","seq":27,"time":1790874400700,"data":{"turn":1,"step":4,"message":{"role":"assistant","content":[{"type":"text","text":"Bounded the retry loop."}],"source":{"kind":"model"},"id":"a4"}}}
`

func TestParseDeepSeekFileKeepsToolCalls(t *testing.T) {
	path := writeDeepSeekSession(t, t.TempDir(), "tools", deepSeekToolLog, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Errorf("%s record has no time: %q", m.Role, m.Text)
		}
	}
	// Each call is written twice; it must be counted once.
	if got := byRole[RoleCommand]; strings.Join(got, "|") != "$ go test ./retry/...  → exit 0" {
		t.Errorf("commands = %q, want the one bash call", got)
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, p := range []string{"/tmp/proj/retry.go", "/tmp/proj/PLAN.md", "/tmp/proj/backoff.go"} {
		if strings.Count(files, p) != 1 {
			t.Errorf("files = %q, want %s once", files, p)
		}
	}
	edits := byRole[RoleEdit]
	want := []string{"/tmp/proj/retry.go\nfor {", "/tmp/proj/backoff.go\ndelay *= 2"}
	if strings.Join(edits, "|") != strings.Join(want, "|") {
		t.Errorf("edits = %q, want %q", edits, want)
	}
	wrote := strings.Join(byRole[RoleWrote], "\n")
	for _, p := range []string{"/tmp/proj/retry.go", "/tmp/proj/PLAN.md", "/tmp/proj/backoff.go"} {
		if strings.Count(wrote, p+"\n") != 1 {
			t.Errorf("wrote = %q, want one record for %s", wrote, p)
		}
	}
	if len(byRole["assistant"]) != 1 || len(byRole["tool-output"]) != 4 {
		t.Errorf("prose changed: %+v", byRole)
	}
}

// A dsh tool takes a path relative to the session's directory as readily as an
// absolute one. Stored as "retry.go", the edit was out of reach of
// `deja restore /tmp/proj/retry.go` and of blame.
func TestParseDeepSeekFileResolvesARelativePathAgainstTheSessionCwd(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-rel","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"bound the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file retry.go has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "rel", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := strings.Join(byRole[RoleFiles], "|"); got != "/tmp/proj/retry.go" {
		t.Errorf("files = %q, want the path resolved against the session cwd", got)
	}
	if got := strings.Join(byRole[RoleEdit], "|"); got != "/tmp/proj/retry.go\nfor {" {
		t.Errorf("edits = %q", got)
	}
}

// What dsh's result says decides what the call is credited with: an edit that
// failed or was denied changed nothing, a call with no result may not have run,
// and a bash result carries its exit status as a trailing marker rather than as
// an error, so the status comes from the marker (#4291).
func TestParseDeepSeekFileCreditsWhatTheResultConfirms(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-res","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"fix the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":10,"time":1790874400200,"data":{"callId":"ok","name":"bash","arguments":"{\"command\": \"go test ./retry/...\"}"}}
{"type":"tool/result","seq":11,"time":1790874400210,"data":{"message":{"source":{"kind":"tool","callId":"ok"},"content":[{"type":"tool-result","toolCallId":"ok","content":[{"type":"text","text":"ok  \texample.com/proj/retry\t0.01s"}],"isError":false}]}}}
{"type":"tool/call","seq":12,"time":1790874400220,"data":{"callId":"bad","name":"bash","arguments":"{\"command\": \"go vet ./retry/...\"}"}}
{"type":"tool/result","seq":13,"time":1790874400230,"data":{"message":{"source":{"kind":"tool","callId":"bad"},"content":[{"type":"tool-result","toolCallId":"bad","content":[{"type":"text","text":"retry.go:3: unreachable code\n[exit code: 1]"}],"isError":false}]}}}
{"type":"tool/call","seq":14,"time":1790874400240,"data":{"callId":"denied","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/denied.go\", \"old_string\": \"return nil\", \"new_string\": \"return fmt.Errorf(\\\"retry budget exhausted after %d attempts\\\", n)\"}"}}
{"type":"tool/result","seq":15,"time":1790874400250,"data":{"message":{"source":{"kind":"tool","callId":"denied"},"content":[{"type":"tool-result","toolCallId":"denied","content":[{"type":"text","text":"Error: rejected"}],"isError":true}]}}}
{"type":"tool/call","seq":16,"time":1790874400260,"data":{"callId":"lost","name":"write","arguments":"{\"file_path\": \"/tmp/proj/lost.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"}}
{"type":"tool/call","seq":18,"time":1790874400280,"data":{"callId":"good","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":19,"time":1790874400290,"data":{"message":{"source":{"kind":"tool","callId":"good"},"content":[{"type":"tool-result","toolCallId":"good","content":[{"type":"text","text":"The file /tmp/proj/retry.go has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "res", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	wantCmds := "$ go test ./retry/...  → exit 0|$ go vet ./retry/...  → exit 1"
	if got := strings.Join(byRole[RoleCommand], "|"); got != wantCmds {
		t.Errorf("commands = %q, want %q", got, wantCmds)
	}
	edits := strings.Join(byRole[RoleEdit], "|")
	wrote := strings.Join(byRole[RoleWrote], "|")
	if edits != "/tmp/proj/retry.go\nfor {" {
		t.Errorf("edits = %q, want only the edit whose result came back clean", edits)
	}
	if strings.Contains(wrote, "denied.go") || strings.Contains(wrote, "lost.md") || !strings.Contains(wrote, "/tmp/proj/retry.go\n") {
		t.Errorf("wrote = %q, want only the confirmed edit", wrote)
	}
}

// str_replace_editor's view takes a directory as well as a file, and the call
// cannot tell which; the result can. A listing is not a file the session read.
func TestParseDeepSeekFileKeepsADirectoryViewOutOfFiles(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-view","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"look around"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":10,"time":1790874400200,"data":{"callId":"dir","name":"str_replace_editor","arguments":"{\"command\": \"view\", \"path\": \"/tmp/proj\"}"}}
{"type":"tool/result","seq":11,"time":1790874400210,"data":{"message":{"source":{"kind":"tool","callId":"dir"},"content":[{"type":"tool-result","toolCallId":"dir","content":[{"type":"text","text":"Here're the files and directories up to 2 levels deep in /tmp/proj, excluding hidden items:\nd\t/tmp/proj\nf\t/tmp/proj/retry.go\n"}],"isError":false}]}}}
{"type":"tool/call","seq":12,"time":1790874400220,"data":{"callId":"file","name":"str_replace_editor","arguments":"{\"command\": \"view\", \"path\": \"/tmp/proj/retry.go\"}"}}
{"type":"tool/result","seq":13,"time":1790874400230,"data":{"message":{"source":{"kind":"tool","callId":"file"},"content":[{"type":"tool-result","toolCallId":"file","content":[{"type":"text","text":"Here's the content of /tmp/proj/retry.go with line numbers (which has a total of 1 lines):\n     1  package retry\n"}],"isError":false}]}}}
{"type":"tool/call","seq":14,"time":1790874400240,"data":{"callId":"img","name":"read_image","arguments":"{\"file_path\": \"/tmp/proj/backoff.png\"}"}}
{"type":"tool/result","seq":15,"time":1790874400250,"data":{"message":{"source":{"kind":"tool","callId":"img"},"content":[{"type":"tool-result","toolCallId":"img","content":[{"type":"text","text":"image"}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "view", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "|"); got != "/tmp/proj/retry.go|/tmp/proj/backoff.png" {
		t.Errorf("files = %q, want the viewed file and the image, not the directory", got)
	}
}

// The exit status is read off the end of dsh's own result text. A long result
// goes through dsh's spill policy (on at 50 KB by default), which appends a
// "(Omitted N bytes. …)" notice after the marker, and a clean run's output can
// end in a bracketed line of its own; the persistent-shell bash ends a timeout
// or a dead shell with a reset notice instead of a marker (#4291).
func TestDeepSeekExitCodeReadsDshsMarkers(t *testing.T) {
	spill := "\n\n(Omitted 81234 bytes. Full formatted result stored at: /home/u/.dsh/spill/session-1/call_9-bash.txt. Use read with offset/limit, or grep this path to search within it.)"
	reset := "The persistent bash shell was reset; the next bash call starts from the workspace with a fresh current directory and environment."
	cases := []struct {
		name, text, code string
		ok               bool
	}{
		{"clean", "ok  \tretry\t0.01s", "0", true},
		{"nonzero", "FAIL retry\n[exit code: 1]", "1", true},
		{"marker mid-output", "[exit code: 3]\nthen it went on\n", "0", true},
		{"spilled failure", "--- FAIL: TestRetry\n[exit code: 1]" + spill, "1", true},
		{"spilled clean", "PASS\nok retry" + spill, "0", true},
		{"spill notice alone", strings.TrimLeft(spill, "\n"), "", false},
		{"truncated clean", "PASS\n[output truncated; full output: /tmp/x]", "0", true},
		{"json array", "[]", "0", true},
		{"ninja", "[2/2] Linking CXX executable app", "0", true},
		{"timed out", "partial\n[timed out after 120000ms]", "", false},
		{"killed", "partial\n[killed by signal: SIGKILL]", "", false},
		{"sandbox", "touch: /etc/x: Operation not permitted\n[sandbox: file access denied under workspace-write mode]", "", false},
		{"persistent timeout", "Your command timed out after 300 seconds or experienced an OOM error. Below is partial output:\npartial\n" + reset, "", false},
		{"persistent shell died", "partial\n[shell exited: code 1]\n" + reset, "", false},
	}
	for _, c := range cases {
		code, ok := deepSeekExitCode(c.text)
		if code != c.code || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, code, ok, c.code, c.ok)
		}
	}
}

// A path with a leading separator or a drive letter is rooted on every host.
// Go's filepath.IsAbs is false for /tmp/proj/retry.go on Windows and for
// C:\proj\retry.go everywhere else, so the reader joined such a path onto the
// session's cwd and stored one that does not exist (#4438).
func TestResolveToolPathKeepsARootedPath(t *testing.T) {
	for _, c := range []struct{ p, cwd, want string }{
		{"/tmp/proj/retry.go", "/tmp/proj", "/tmp/proj/retry.go"},
		{`\tmp\proj\retry.go`, "/tmp/proj", `\tmp\proj\retry.go`},
		{`C:\proj\retry.go`, `C:\proj`, `C:\proj\retry.go`},
		{"C:/proj/retry.go", `C:\proj`, "C:/proj/retry.go"},
		{`\\srv\share\retry.go`, `C:\proj`, `\\srv\share\retry.go`},
		{"retry.go", "/tmp/proj", "/tmp/proj/retry.go"},
		{"./sub/retry.go", "/tmp/proj", "/tmp/proj/sub/retry.go"},
		{"retry.go", "", "retry.go"},
		{"", "/tmp/proj", ""},
	} {
		if got := resolveToolPath(c.p, c.cwd); got != c.want {
			t.Errorf("resolveToolPath(%q, %q) = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}

// The same rule end to end: a session recorded on Windows and read anywhere
// keeps its drive-letter paths as written (#4438).
func TestParseDeepSeekFileKeepsADriveLetterPath(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-win","createdAt":1790874400000,"cwd":"C:\\proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"bound the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"C:\\\\proj\\\\retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "win", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "|"); got != `C:\proj\retry.go` {
		t.Errorf("files = %q, want C:\\proj\\retry.go as written", got)
	}
}
