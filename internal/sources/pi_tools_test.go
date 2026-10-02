package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The toolCall blocks pi writes, one of each kind, with the argument names a
// real pi, omp, OpenClaw, gjc and Kimchi session carries: `path` for the file
// tools, `command` for the shell, `edits[].oldText/newText` for an edit and
// `content` for a whole-file write. The reader kept the text and skipped the
// calls, so `deja files`, `how`, `restore` and `blame` had nothing for any of
// the six harnesses (#4113).
const piToolFixture = `{"type":"session","version":3,"id":"pi-tools","timestamp":"2026-10-01T20:50:00.000Z","cwd":"/work/app"}
{"type":"message","id":"u1","timestamp":"2026-10-01T20:50:01.000Z","message":{"role":"user","content":[{"type":"text","text":"raise the retry count"}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T20:50:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Reading the config."},{"type":"toolCall","id":"c-read","name":"read","arguments":{"path":"retry.cfg"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T20:50:03.000Z","message":{"role":"toolResult","toolCallId":"c-read","toolName":"read","content":[{"type":"text","text":"retries = 3"}],"isError":false}}
{"type":"message","id":"a2","timestamp":"2026-10-01T20:50:04.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-edit","name":"edit","arguments":{"path":"retry.cfg","edits":[{"oldText":"max_retry_attempts_before_giving_up = 3","newText":"max_retry_attempts_before_giving_up = 5"}]}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T20:50:05.000Z","message":{"role":"toolResult","toolCallId":"c-edit","toolName":"edit","content":[{"type":"text","text":"edited"}],"isError":false}}
{"type":"message","id":"a2b","timestamp":"2026-10-01T20:50:05.500Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-edit-bad","name":"edit","arguments":{"path":"retry.cfg","edits":[{"oldText":"a line the file never held at any point","newText":"what the agent hoped to write there instead"}]}}]}}
{"type":"message","id":"r2b","timestamp":"2026-10-01T20:50:05.600Z","message":{"role":"toolResult","toolCallId":"c-edit-bad","toolName":"edit","content":[{"type":"text","text":"Could not find the exact text"}],"isError":true}}
{"type":"message","id":"a3","timestamp":"2026-10-01T20:50:06.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-write","name":"write","arguments":{"path":"/work/app/NOTES.md","content":"backoff doubles on every failed attempt\n"}}]}}
{"type":"message","id":"r3","timestamp":"2026-10-01T20:50:07.000Z","message":{"role":"toolResult","toolCallId":"c-write","toolName":"write","content":[{"type":"text","text":"wrote"}],"isError":false}}
{"type":"message","id":"a4","timestamp":"2026-10-01T20:50:08.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-bash","name":"bash","arguments":{"command":"git status --short"}},{"type":"toolCall","id":"c-exec","name":"exec","arguments":{"command":"go test ./..."}}]}}
{"type":"message","id":"r4","timestamp":"2026-10-01T20:50:09.000Z","message":{"role":"toolResult","toolCallId":"c-bash","toolName":"bash","content":[{"type":"text","text":" M retry.cfg"}],"isError":false}}
{"type":"message","id":"r5","timestamp":"2026-10-01T20:50:10.000Z","message":{"role":"toolResult","toolCallId":"c-exec","toolName":"exec","content":[{"type":"text","text":"FAIL"}],"isError":true,"details":{"exitCode":1}}}
`

// gjc's edit takes one string in its hashline form: `§path`, then ops
// (`≔A..B` replace, `«A` insert before, `»A` insert after) each followed by the
// lines they write. The replaced text is not in the call; gjc's result carries
// it in details.diff.
const gjcHashlineFixture = `{"type":"session","version":3,"id":"gjc-tools","timestamp":"2026-10-01T20:55:00.000Z","cwd":"/work/app"}
{"type":"message","id":"u1","timestamp":"2026-10-01T20:55:01.000Z","message":{"role":"user","content":[{"type":"text","text":"raise the retry count"}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T20:55:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-bad","name":"edit","arguments":{"input":"*** Begin Patch\n*** Update File: retry.cfg\n*** End Patch\n"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T20:55:03.000Z","message":{"role":"toolResult","toolCallId":"c-bad","toolName":"edit","content":[{"type":"text","text":"input must begin with \"§PATH\""}],"isError":true}}
{"type":"message","id":"a2","timestamp":"2026-10-01T20:55:04.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-hash","name":"edit","arguments":{"input":"§retry.cfg\n≔1rq\nmax_retry_attempts_before_giving_up = 5\n"}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T20:55:05.000Z","message":{"role":"toolResult","toolCallId":"c-hash","toolName":"edit","content":[{"type":"text","text":"retry.cfg:\n-1--|max_retry_attempts_before_giving_up = 3\n+1sb|max_retry_attempts_before_giving_up = 5"}],"details":{"diff":"-1|max_retry_attempts_before_giving_up = 3\n+1|max_retry_attempts_before_giving_up = 5","firstChangedLine":1,"op":"update"},"isError":false}}
`

func writePiFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func rolesOf(s model.Session, role string) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	return out
}

func wroteHas(t *testing.T, records []string, path, line string) bool {
	t.Helper()
	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatalf("%q is too short to be evidence, so this measures nothing", line)
	}
	for _, r := range records {
		if p, has := WroteRecordHas(r, h); has && p == path {
			return true
		}
	}
	return false
}

func TestPiShapedToolCallsAreIndexed(t *testing.T) {
	ss, err := ParsePiFile(writePiFixture(t, piToolFixture))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	files := strings.Join(rolesOf(s, RoleFiles), "\n")
	cfg := filepath.Join("/work/app", "retry.cfg")
	for _, want := range []string{cfg, "/work/app/NOTES.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("files rows %q do not name %s", files, want)
		}
	}
	cmds := rolesOf(s, RoleCommand)
	if len(cmds) != 2 || cmds[0] != "$ git status --short  → exit 0" || cmds[1] != "$ go test ./...  → exit 1" {
		t.Errorf("commands = %q", cmds)
	}
	edits := rolesOf(s, RoleEdit)
	if len(edits) != 1 || edits[0] != cfg+"\nmax_retry_attempts_before_giving_up = 3" {
		t.Errorf("edit spans = %q", edits)
	}
	wrote := rolesOf(s, RoleWrote)
	// An edit the harness refused changed nothing, and restore must not hand
	// its text back as something that stopped existing.
	if wroteHas(t, wrote, cfg, "what the agent hoped to write there instead") {
		t.Errorf("a refused edit is recorded as written: %q", wrote)
	}
	if !wroteHas(t, wrote, cfg, "max_retry_attempts_before_giving_up = 5") {
		t.Errorf("the edit's written side is not recorded: %q", wrote)
	}
	if !wroteHas(t, wrote, "/work/app/NOTES.md", "backoff doubles on every failed attempt") {
		t.Errorf("the write is not recorded: %q", wrote)
	}

	// The switches every other harness honours turn these off here too.
	for _, env := range []string{"DEJA_INDEX_PATHS", "DEJA_INDEX_COMMANDS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES"} {
		t.Setenv(env, "0")
	}
	ss, _ = ParsePiFile(writePiFixture(t, piToolFixture))
	for _, role := range []string{RoleFiles, RoleCommand, RoleEdit, RoleWrote} {
		if got := rolesOf(ss[0], role); len(got) != 0 {
			t.Errorf("%s rows with indexing switched off: %q", role, got)
		}
	}
}

// The incremental read starts past the header, and the paths still resolve
// against the session's directory.
func TestPiShapedToolCallsFromOffset(t *testing.T) {
	p := writePiFixture(t, piToolFixture)
	off := int64(strings.Index(piToolFixture, `{"type":"message","id":"a2"`))
	ss, err := ParsePiFileFromOffset(p, off)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if edits := rolesOf(ss[0], RoleEdit); len(edits) != 1 || !strings.HasPrefix(edits[0], filepath.Join("/work/app", "retry.cfg")+"\n") {
		t.Errorf("edit spans from offset = %q", edits)
	}
}

func TestGjcHashlineEditsAreIndexed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GJC_ROOT", root)
	p := filepath.Join(root, "v2-x", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(gjcHashlineFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGjcFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	cfg := filepath.Join("/work/app", "retry.cfg")
	if files := rolesOf(s, RoleFiles); len(files) != 1 || files[0] != cfg {
		t.Errorf("files rows = %q, want the one file the hashline edit named", files)
	}
	edits := rolesOf(s, RoleEdit)
	if len(edits) != 1 || edits[0] != cfg+"\nmax_retry_attempts_before_giving_up = 3" {
		t.Errorf("edit spans = %q, want the replaced line from gjc's diff", edits)
	}
	if !wroteHas(t, rolesOf(s, RoleWrote), cfg, "max_retry_attempts_before_giving_up = 5") {
		t.Errorf("the hashline payload is not recorded as written: %q", rolesOf(s, RoleWrote))
	}
}

// A shell call whose result lands a pass later sends the file back for a whole
// read only when the result is a failure. A clean result is every command the
// agent runs while an index pass is going, deja's own included (#4443).
func TestPiResumesPastACleanCommandResult(t *testing.T) {
	head := `{"type":"session","version":3,"id":"s","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}
{"type":"message","id":"a1","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"deja search retry"}}]}}
`
	for _, tc := range []struct {
		name, res string
		resumes   bool
	}{
		{"clean", `"details":{"exitCode":0},"isError":false`, true},
		{"clean without a code", `"isError":false`, true},
		{"failed", `"details":{"exitCode":2},"isError":true`, false},
		{"nonzero code", `"details":{"exitCode":1},"isError":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "s.jsonl")
			body := head + `{"type":"message","id":"r1","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],` + tc.res + "}}\n"
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := piResumes(p, int64(len(head))); got != tc.resumes {
				t.Errorf("piResumes = %v, want %v", got, tc.resumes)
			}
		})
	}
}

// An edit result answers a call stored already: its refusal is what drops the
// edit, so the file goes back for a whole read. A read's result changes
// nothing, and a call made in the tail is known when its answer arrives.
func TestPiResumesOnAnEditResultForAStoredCall(t *testing.T) {
	call := func(id, name string) string {
		return `{"type":"message","id":"a-` + id + `","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":{"path":"retry.cfg"}}]}}` + "\n"
	}
	result := func(id, name string) string {
		return `{"type":"message","id":"r-` + id + `","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"` + id + `","toolName":"` + name + `","content":[{"type":"text","text":"Could not find the exact text"}],"isError":true}}` + "\n"
	}
	head := `{"type":"session","version":3,"id":"s","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}` + "\n"
	for _, c := range []struct {
		name, before, after string
		want                bool
	}{
		{"edit refused a pass later", call("e1", "edit"), result("e1", "edit"), false},
		{"result without a tool name", call("e1", "edit"), result("e1", ""), false},
		{"read result", call("r1", "read"), result("r1", "read"), true},
		{"edit and refusal in the tail", "", call("e2", "edit") + result("e2", "edit"), true},
		{"a user turn", "", `{"type":"message","id":"u","timestamp":"2026-09-01T09:03:00Z","message":{"role":"user","content":[{"type":"text","text":"toolCall"}]}}` + "\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "s.jsonl", head+c.before, c.after)
			if got := piResumes(p, off); got != c.want {
				t.Errorf("piResumes = %v, want %v", got, c.want)
			}
		})
	}
}

// A pi-family session read on the other OS keeps its paths in the convention
// it was written in. filepath.Join put a relative path under /work/app as
// \work\app\retry.cfg on Windows, and joined a drive-letter path onto the cwd
// everywhere else (#4438).
func TestPiPathsResolveTheSameOnEveryHost(t *testing.T) {
	for _, c := range []struct{ p, cwd, want string }{
		{"retry.cfg", "/work/app", "/work/app/retry.cfg"},
		{"./sub/retry.cfg", "/work/app", "/work/app/sub/retry.cfg"},
		{"/work/app/retry.cfg", "/work/app", "/work/app/retry.cfg"},
		{`C:\app\retry.cfg`, "/work/app", `C:\app\retry.cfg`},
		{`\app\retry.cfg`, "/work/app", `\app\retry.cfg`},
		{"retry.cfg", "", "retry.cfg"},
	} {
		r := &piReader{cwd: c.cwd}
		if got := r.abs(c.p); got != c.want {
			t.Errorf("abs(%q) under %q = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}
