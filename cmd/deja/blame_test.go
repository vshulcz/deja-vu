package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The line answer as an object, which is what an agent asking about a line has
// to read out of the prose today (#3723). Three properties matter and each has
// cost something elsewhere: it is one line, it names itself on that line, and
// it says which rule answered.
func TestTheLineAnswerAsJSONNamesItsRuleAndItsSession(t *testing.T) {
	target := search.BlameTarget{Base: "pool.go", Line: 42, FullPath: "/w/pool.go"}
	commit := lineCommit{
		SHA:     "abcdef1234567890",
		Subject: "fix: one pool per process\n\nthe body is not the subject",
		Author:  "someone",
		When:    time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	author := lineAuthor{
		Session: model.Session{Harness: "claude", ID: "2915986c9f79", Project: "deja-vu"},
		Matched: "cfg.MaxConns = int32(size)",
		Asked:   "make the pool size configurable",
		Wrote:   true,
	}
	var buf bytes.Buffer
	if err := writeLineAnswerJSON(&buf, buildLineAnswer(target, commit, author, true, "")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n"); n != 0 {
		t.Errorf("the answer is %d lines; the recogniser drops one", n+1)
	}
	if !strings.HasPrefix(buf.String(), `{"kind":"deja.blame-line"`) {
		t.Errorf("the object does not name itself first: %s", buf.String())
	}
	var got lineAnswerJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != jsonout.Version || got.File != "pool.go" || got.Line != 42 {
		t.Errorf("answer = %+v", got)
	}
	if !got.Attributed || got.Rule != lineRuleWrote {
		t.Errorf("rule = %q attributed = %v, want the written rule", got.Rule, got.Attributed)
	}
	if got.Commit == nil || got.Commit.SHA != commit.SHA || got.Commit.Subject != "fix: one pool per process" {
		t.Errorf("commit = %+v, want the subject's first line", got.Commit)
	}
	if got.Commit.When != "2026-09-01T12:00:00Z" {
		t.Errorf("when = %q, want RFC 3339 in UTC", got.Commit.When)
	}
	if got.Session == nil || got.Session.Harness != "claude" || got.Session.Project != "deja-vu" ||
		got.Session.Asked != "make the pool size configurable" || !strings.HasPrefix(got.Session.Ctx, "deja ctx ") {
		t.Errorf("session = %+v", got.Session)
	}

	// Nothing attributed is an answer too, and the two silences are different:
	// git named a commit and no session wrote the line, or git could not say
	// anything at all.
	buf.Reset()
	if err := writeLineAnswerJSON(&buf, buildLineAnswer(target, commit, lineAuthor{}, false, "")); err != nil {
		t.Fatal(err)
	}
	got = lineAnswerJSON{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Attributed || got.Rule != "" || got.Session != nil || got.Commit == nil {
		t.Errorf("unattributed answer = %+v", got)
	}
	buf.Reset()
	if err := writeLineAnswerJSON(&buf, buildLineAnswer(target, lineCommit{}, lineAuthor{}, false, "this line is not committed yet")); err != nil {
		t.Fatal(err)
	}
	got = lineAnswerJSON{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Why != "this line is not committed yet" || got.Commit != nil || got.Attributed {
		t.Errorf("answer with no commit = %+v", got)
	}
}

// `--attribution` and `--git-note` answer about a line. Given a path with no
// line they used to be free to answer about the whole file, which is a
// different question, so they refuse by name (#3726 is the same lesson).
func TestTheLineFlagsRefuseAPathWithNoLine(t *testing.T) {
	for _, flag := range []string{"--attribution", "--git-note"} {
		_, _, mode, err := parseBlame([]string{flag, "main.go"})
		if err != nil {
			t.Fatalf("parseBlame %s: %v", flag, err)
		}
		if flag == "--attribution" && !mode.Attribution {
			t.Error("--attribution was dropped")
		}
		if flag == "--git-note" && !mode.GitNote {
			t.Error("--git-note was dropped")
		}
		err = runBlame(t.TempDir(), []string{flag, "main.go"})
		if err == nil || !strings.Contains(err.Error(), "one line") {
			t.Errorf("blame %s main.go: err = %v, want a refusal naming the line", flag, err)
		}
	}
}

// The opt-in note: a teammate's clone sees the attribution through
// `git log --notes=deja`, and running blame twice does not write it twice.
func TestTheGitNoteRecordsTheAttributionOnceOnItsOwnRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	path := filepath.Join(repo, "pool.go")
	if err := os.WriteFile(path, []byte("package pool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	// A note is a git object, so writing one needs a committer identity. A CI
	// runner has none configured, which is how the first run of this test found
	// out that the failure said only "exit status 128".
	git("config", "user.name", "t")
	git("config", "user.email", "t@example.com")
	git("add", "pool.go")
	git("commit", "-qm", "first")
	sha := strings.TrimSpace(git("rev-parse", "HEAD"))

	target := search.BlameTarget{Base: "pool.go", Line: 1, FullPath: path}
	commit := lineCommit{SHA: sha, When: time.Now().Add(-time.Hour)}
	author := lineAuthor{
		Session: model.Session{Harness: "claude", ID: "2915986c9f79", Project: "deja-vu"},
		Matched: "package pool",
	}

	var out bytes.Buffer
	if err := writeGitNote(&out, target, commit, author, true); err != nil {
		t.Fatal(err)
	}
	note := git("notes", "--ref=deja", "show", sha)
	if !strings.Contains(note, "written in claude") || !strings.Contains(note, "rule: replaced") {
		t.Errorf("note = %q, want the session and the rule", note)
	}
	if !strings.Contains(note, "deja ctx ") {
		t.Errorf("note = %q, want the command that opens the session", note)
	}
	// Nothing lands in the ref git shows by default, which is shared with
	// whatever else a repository keeps notes for.
	if out := git("notes", "list"); strings.TrimSpace(out) != "" {
		t.Errorf("refs/notes/commits was written: %q", out)
	}

	out.Reset()
	if err := writeGitNote(&out, target, commit, author, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already records") {
		t.Errorf("the second write said %q", out.String())
	}
	if again := git("notes", "--ref=deja", "show", sha); again != note {
		t.Errorf("the note grew on a second run:\n%s\nwas\n%s", again, note)
	}

	// A guess is never published: with no session attributed there is nothing
	// to record, and the refusal says so rather than writing an empty note.
	if err := writeGitNote(&out, target, commit, lineAuthor{}, false); err == nil {
		t.Error("a note was written for a line nothing is attributed to")
	}
}

// "exit status 128" names nothing. A note is a git object, so a machine with no
// committer identity configured cannot write one — which is what CI hit on the
// first run of the test above, and what a reader has to be told to fix.
func TestTheGitNotePassesOnGitsOwnRefusal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	ident := []string{
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	path := filepath.Join(repo, "pool.go")
	if err := os.WriteFile(path, []byte("package pool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(nil, "init", "-q")
	run(ident, "add", "pool.go")
	run(ident, "commit", "-qm", "first")
	sha := strings.TrimSpace(run(nil, "rev-parse", "HEAD"))

	// No identity anywhere this git can read: not in the repository, not in a
	// global or system file, not in the environment.
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(k, "")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	err := writeGitNote(io.Discard, search.BlameTarget{Base: "pool.go", Line: 1, FullPath: path},
		lineCommit{SHA: sha}, lineAuthor{Session: model.Session{Harness: "claude", ID: "abc123"}}, true)
	if err == nil {
		t.Skip("this git wrote a note with no identity configured")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("the failure says only the exit code: %v", err)
	}
}

// The missing-path check only looked at path == "" after the loop, so a blank
// path got through (`deja blame "   "`, also after `--`), and an empty first
// argument was quietly replaced by the next one (`deja blame "" main.go`) (#5938).
func TestBlameRefusesABlankPath(t *testing.T) {
	for _, args := range [][]string{
		{"   "},
		{"\t"},
		{"--", "   "},
		{"--json", "--", " "},
		{""},
		{"--", ""},
		{"", "main.go"},
		{"  ", "main.go"},
		{"--", "", "main.go"},
	} {
		path, _, _, err := parseBlame(args)
		if err == nil {
			t.Errorf("parseBlame(%q) accepted path %q", args, path)
			continue
		}
		if !strings.Contains(err.Error(), "blame needs a path") {
			t.Errorf("parseBlame(%q): got %v, want the missing-path error", args, err)
		}
	}
}

// A real path is unchanged, including one that only starts or ends with a
// space and a dash-led name after `--`.
func TestBlameKeepsARealPath(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"main.go"}, "main.go"},
		{[]string{"--", "-report.md"}, "-report.md"},
		{[]string{" main.go"}, " main.go"},
		{[]string{"--json", "main.go:12"}, "main.go:12"},
	} {
		path, _, _, err := parseBlame(tc.args)
		if err != nil || path != tc.want {
			t.Errorf("parseBlame(%q) = %q, %v; want %q", tc.args, path, err, tc.want)
		}
	}
}

// blame answers "who touched this file" with the ten best and said nothing
// about the rest, so ten of forty read as the whole answer — the sentence
// `search` prints two commands away (#2299).
func TestBlameSaysHowManyItHeldBack(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	for i := range 25 {
		at := time.Now().Add(-time.Duration(2+i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%03d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"touching api/upload.go, take %d"}}`, i, at, i)
		name := fmt.Sprintf("s%03d.jsonl", i)
		if err := os.WriteFile(filepath.Join(claude, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the cap really is holding sessions back.
	out, err := captureRun(t, "blame", "api/upload.go")
	if err != nil {
		t.Fatal(err)
	}
	capped := strings.Count(out, "touching api/upload.go")
	all, err := captureRun(t, "blame", "api/upload.go", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if whole := strings.Count(all, "touching api/upload.go"); whole <= capped {
		t.Fatalf("--all shows %d and the default %d, so nothing is being held back", whole, capped)
	}

	said, err := captureRunStderr(t, "blame", "api/upload.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "--all") {
		t.Errorf("blame held sessions back without saying so: %q", said)
	}
	if !strings.Contains(said, "25") {
		t.Errorf("the note does not say how many there are: %q", said)
	}

	// With --all there is nothing to announce.
	said, err = captureRunStderr(t, "blame", "api/upload.go", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "add --all") {
		t.Errorf("--all still suggests --all: %q", said)
	}
}

// Copilot CLI 1.0.98: a parent whose `task` sub-agent writes notes.txt and
// which writes parent.txt itself, then a /fork of it that only says hi. The
// fork holds copies of every record at the source's times. Blame gave both
// lines to the fork, and the sub-agent's line to the parent before that
// (#5216, #5217).
func TestBlameCopilotCreditsTheSubagentAndTheSourceNotTheFork(t *testing.T) {
	const (
		parentID = "f67048ad-042c-42f4-ac33-5db51c4d48b0"
		childID  = "717532a2-bf69-4164-be09-03daacd1762f"
		forkID   = "2f07ff7f-aa0e-4b85-93fb-2096b6e9833f"
		notes    = "release notes drafted by the helper sub-agent"
		mine     = "the parent session wrote this whole line itself"
	)
	work := t.TempDir()
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	q := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	notesPath, parentPath := filepath.Join(work, "notes.txt"), filepath.Join(work, "parent.txt")
	copied := []string{
		`{"type":"user.message","data":{"content":"write the release notes with a helper agent"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_task","toolName":"task","arguments":{"prompt":"draft the notes","agent_type":"general-purpose"}},"timestamp":"2026-10-18T05:31:47.355Z"}`,
		`{"type":"subagent.started","agentId":"` + childID + `","data":{"toolCallId":"call_task","agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"` + childID + `","data":{"content":"draft the notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"tool.execution_start","agentId":"` + childID + `","data":{"toolCallId":"call_create","toolName":"create","arguments":{"path":"` + q(notesPath) + `","file_text":"` + notes + `\n"}},"timestamp":"2026-10-18T05:31:47.407Z"}`,
		`{"type":"assistant.message","agentId":"` + childID + `","data":{"content":"Created notes.txt."},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"assistant.message","data":{"content":"The helper wrote notes.txt."},"timestamp":"2026-10-18T05:31:51.660Z"}`,
		`{"type":"user.message","data":{"content":"the parent writes its own file"},"timestamp":"2026-10-18T05:33:10.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_parent","toolName":"create","arguments":{"path":"` + q(parentPath) + `","file_text":"` + mine + `\n"}},"timestamp":"2026-10-18T05:33:10.100Z"}`,
	}
	start := func(id, at string) string {
		return `{"type":"session.start","data":{"sessionId":"` + id + `","startTime":"` + at + `","context":{"cwd":"` + q(work) + `"}},"timestamp":"` + at + `"}`
	}
	write := func(id string, lines []string) string {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "events.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src := append([]string{start(parentID, "2026-10-18T05:31:46.707Z")}, copied...)
	fork := append([]string{start(forkID, "2026-10-18T05:34:00.000Z")}, copied...)
	fork = append(fork,
		`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"write the release notes with a helper agent\" (`+parentID+`) as \"write the release notes with a helper agent (fork)\" (`+forkID+`)."},"timestamp":"2026-10-18T05:34:00.002Z"}`,
		`{"type":"user.message","data":{"content":"hi"},"timestamp":"2026-10-18T05:34:10.000Z"}`,
	)
	var all []model.Session
	for _, p := range []string{write(forkID, fork), write(parentID, src)} {
		ss, err := sources.ParseCopilotFile(p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ss...)
	}
	commit := lineCommit{SHA: "7b969b0e", When: time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)}
	for _, tc := range []struct{ path, text, want string }{
		{notesPath, notes, childID},
		{parentPath, mine, parentID},
	} {
		if err := os.WriteFile(tc.path, []byte(tc.text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := search.BlameTarget{FullPath: tc.path, Base: filepath.Base(tc.path), Line: 1}
		// Either order the candidates arrive in.
		for _, ss := range [][]model.Session{all, reversed(all)} {
			got, ok := attributeLine(ss, target, commit, nil)
			if !ok || got.Session.ID != tc.want {
				t.Errorf("%s:1 credited %q, want %s", filepath.Base(tc.path), got.Session.ID, tc.want)
			}
		}
	}

	// The sub-agent hangs under its parent; resume sends the reader there.
	for _, s := range all {
		if s.ID != childID {
			continue
		}
		if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "deja resume "+parentID) {
			t.Errorf("resume of the sub-agent = %v, want a pointer at its parent", err)
		}
	}
}

func reversed(ss []model.Session) []model.Session {
	out := make([]model.Session, len(ss))
	for i, s := range ss {
		out[len(ss)-1-i] = s
	}
	return out
}

// blame answers under the name you type, so a rename drops everything said
// under the old name out of the file's history — and the name a reader has is
// the one in their editor. The history is in the index and reachable, just not
// from there (#1627).
func TestBlameNamesTheFileTheHistoryStopsAt(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(store, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(id, day, path string) string {
		return `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
			path + `","old_string":"a","new_string":"b"}}]}}`
	}
	edit := func(id, day, path, old string) string {
		return `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
			path + `","old_string":"` + old + `","new_string":"// nothing to see"}}]}}`
	}
	say := func(id, day, text string) string {
		return `{"type":"user","timestamp":"2026-07-` + day + `T10:01:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"user","content":"` + text + `"}}`
	}
	write("one.jsonl",
		say("aaaa0001", "10", "the retry loop in internal/search/old_name.go keeps firing"),
		touch("aaaa0001", "10", "/proj/internal/search/old_name.go"))
	write("two.jsonl",
		say("bbbb0002", "12", "the backoff in internal/search/old_name.go is wrong"),
		touch("bbbb0002", "12", "/proj/internal/search/old_name.go"))
	write("three.jsonl",
		say("cccc0003", "14", "rename internal/search/old_name.go to internal/search/new_name.go"),
		touch("cccc0003", "14", "/proj/internal/search/old_name.go"),
		touch("cccc0003", "14", "/proj/internal/search/new_name.go"))
	write("four.jsonl",
		say("dddd0004", "16", "the backoff in internal/search/new_name.go is still wrong"),
		touch("dddd0004", "16", "/proj/internal/search/new_name.go"))
	// Two files that merely coincided once, a test file beside its subject, a
	// same-named file in another package, and a file from another project:
	// none of them is a name this file's history continues from, and each of
	// them fired before the rules were narrowed.
	write("five.jsonl",
		say("eeee0005", "11", "touch both while wiring the cache"),
		touch("eeee0005", "11", "/proj/internal/other/parser.go"),
		touch("eeee0005", "11", "/proj/internal/other/cache.go"))
	write("six.jsonl",
		say("ffff0006", "13", "the cache still misses"),
		touch("ffff0006", "13", "/proj/internal/other/cache.go"))
	write("seven.jsonl",
		say("gggg0007", "11", "write the table test"),
		touch("gggg0007", "11", "/proj/internal/tbl/foo.go"),
		touch("gggg0007", "11", "/proj/internal/tbl/foo_test.go"))
	write("eight.jsonl",
		say("hhhh0008", "13", "the table test needs another case"),
		touch("hhhh0008", "13", "/proj/internal/tbl/foo_test.go"))
	// The word came from a file, not from anybody: an edit carries its payload
	// in the message text, so a file whose own contents read "renamed from"
	// used to vote for a rename nobody performed.
	write("eleven.jsonl",
		say("kkkk0011", "11", "update the docs"),
		edit("kkkk0011", "11", "/proj/internal/edge/quiet.go", "// renamed from loud.go in v2"),
		touch("kkkk0011", "11", "/proj/internal/edge/loud.go"))
	write("twelve.jsonl",
		say("llll0012", "13", "the edge case again"),
		touch("llll0012", "13", "/proj/internal/edge/quiet.go"))
	// A move out of another directory, worded as a rename: a name that moved
	// house is not the name this file's history continues under, and pointing
	// at it is how the note would start naming vendored copies.
	write("nine.jsonl",
		say("iiii0009", "15", "rename internal/deep/gone.go to internal/search/new_name.go"),
		touch("iiii0009", "15", "/proj/internal/deep/gone.go"),
		touch("iiii0009", "15", "/proj/internal/search/new_name.go"))
	// And a refactor that swept several files along while saying the word:
	// whichever of them stopped last would be named on the strength of the
	// sweep rather than of anything about it.
	write("ten.jsonl",
		say("jjjj0010", "15", "rename internal/search/swept.go to internal/search/new_name.go while we are here"),
		touch("jjjj0010", "15", "/proj/internal/search/swept.go"),
		touch("jjjj0010", "15", "/proj/internal/search/a.go"),
		touch("jjjj0010", "15", "/proj/internal/search/b.go"),
		touch("jjjj0010", "15", "/proj/internal/search/c.go"),
		touch("jjjj0010", "15", "/proj/internal/search/new_name.go"))

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "blame", "internal/search/new_name.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "old_name.go") {
		t.Errorf("nothing points at the name the history stops at:\n%s", note)
	}
	if !strings.Contains(note, "deja blame") {
		t.Errorf("the line does not say how to read what came before:\n%s", note)
	}
	// What was seen, not what it means: deja cannot tell a rename from a file
	// that was deleted while this one took over its work.
	if strings.Contains(note, "renamed") {
		t.Errorf("the line claims a rename it cannot know about:\n%s", note)
	}
	// One name, and it is the one in the same directory, from the session that
	// was about the move rather than one that swept it along.
	for _, other := range []string{"gone.go", "swept.go", "a.go"} {
		if strings.Contains(note, other) {
			t.Errorf("the note named %s:\n%s", other, note)
		}
	}

	// The other direction says nothing: nothing came before the old name, and
	// a file whose history is all under its own name needs no hint.
	note, err = captureRunStderr(t, "blame", "internal/search/old_name.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "continues from") {
		t.Errorf("the file the history starts at was given a hint:\n%s", note)
	}

	// And the cases that are not a rename by any reading: a pair that met
	// once, a test file beside its subject, and a file whose own contents
	// carried the word.
	for _, file := range []string{"internal/other/cache.go", "internal/tbl/foo_test.go", "internal/edge/quiet.go"} {
		note, err = captureRunStderr(t, "blame", file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(note, "continues from") {
			t.Errorf("%s was told its history continues from a file it merely shared a session with:\n%s", file, note)
		}
	}
}

// The verb has to be about these two names. A turn that renames two other
// files and mentions ours in passing said nothing about ours; "remove" is not
// "move" — matching the substring made the deletion case, the one thing this
// note cannot tell from a rename, vote for itself; and a file whose own
// contents read "renamed from x" is not somebody saying so.
func TestOnlyASentenceAboutTheseTwoNamesCounts(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"rename old_name.go to new_name.go", true},
		{"git mv internal/search/old_name.go internal/search/new_name.go", true},
		{"moving old_name.go to new_name.go now", true},
		{"rename helper.go to helpers.go, and while you are there old_name.go and new_name.go both need the retry fix", false},
		{"remove old_name.go, new_name.go covers everything it did", false},
		{"old_name.go removed, new_name.go takes over", false},
		{"old_name.go and new_name.go both call the same helper", false},
		// Far apart, with somebody else's rename in between.
		{"old_name.go still leaks; also rename helper.go to helpers.go; and new_name.go needs the same guard", false},
	} {
		// Through the same span the reader of a session goes through, so the
		// table guards what production does rather than its own arithmetic.
		if got := sessionTextSaysItMoved(c.text, "old_name.go", "new_name.go"); got != c.want {
			t.Errorf("movedBetween(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// blame is the tool an agent calls before it edits a file, and on a machine
// with no history it answered a bare `[]` — no note, nothing. The empty array
// is the whole answer, so an agent has no way to tell "nobody ever touched this
// file" from "deja has nothing indexed at all", which is the distinction #2862
// drew for recall.
func TestBlameSaysWhyItFoundNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "no indexed history") {
		t.Errorf("an empty store answers with nothing at all:\n%s", text)
	}
}

// A store that holds sessions, asked about a file none of them touched, keeps
// its own answer: nothing was indexed is not true there.
func TestBlameOnAStoreWithHistoryDoesNotCallItEmpty(t *testing.T) {
	dir := manySessionStore(t, 3)
	text, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"nowhere-near-this-store.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "no indexed history") {
		t.Errorf("a store with sessions in it was called empty:\n%s", text)
	}
}

func TestBlameErrorAndEmptyBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// Index dir squatted by a file: ensure/search must surface the error.
	squat := filepath.Join(tmp, "squatted")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "index.db"))
	if err := runBlame(index.DefaultDir(), []string{"parser.go"}); err == nil {
		t.Fatal("expected blame error for squatted index dir")
	}
	if _, _, err := blameTextResult(index.DefaultDir(), search.BlameOptions{}, "parser.go", 5); err == nil {
		t.Fatal("expected MCP blame error for squatted index dir")
	}
	// Healthy empty index: the no-mentions message path.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "empty-index"))
	out, err := captureRun(t, "blame", "never-mentioned.go")
	if err != nil || out != "" {
		t.Fatalf("blame on empty index: %v (out=%q)", err, out)
	}
	if s, _, err := blameTextResult(index.DefaultDir(), search.BlameOptions{}, "never-mentioned.go", 5); err != nil || s == "" {
		t.Fatalf("mcp empty = %q err=%v", s, err)
	}
}

func TestEmbedCommandBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"embed me"}}`,
	})
	if err := runEmbed(index.DefaultDir(), []string{"--bogus"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	// Dead endpoint with real records: the embed call must surface the error.
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/api/embed")
	if err := runEmbed(index.DefaultDir(), nil); err == nil {
		t.Fatal("expected embed endpoint error")
	}
	// Squatted index dir: Ensure error path.
	squat := filepath.Join(tmp, "sq")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "i"))
	if err := runEmbed(index.DefaultDir(), nil); err == nil {
		t.Fatal("expected ensure error")
	}
}

func TestMaybeSemanticGuards(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	hit := []search.Hit{{}}
	// Existing hits pass through untouched.
	if out, used := maybeSemantic(index.DefaultDir(), hit, search.Options{}, os.Stderr); used || len(out) != 1 {
		t.Fatal("hits must pass through")
	}
	// NoEmbed and env opt-outs.
	if _, used := maybeSemantic(index.DefaultDir(), nil, search.Options{NoEmbed: true}, os.Stderr); used {
		t.Fatal("NoEmbed must skip")
	}
	t.Setenv("DEJA_EMBED", "off")
	if _, used := maybeSemantic(index.DefaultDir(), nil, search.Options{}, os.Stderr); used {
		t.Fatal("env off must skip")
	}
	t.Setenv("DEJA_EMBED", "")
	// No sidecar at all.
	if _, used := maybeSemantic(index.DefaultDir(), nil, search.Options{}, os.Stderr); used {
		t.Fatal("missing sidecar must skip")
	}
}

func TestMaybeSemanticSidecarBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"connection pool exhausted"}}`,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := 1
		if list, ok := req.Input.([]any); ok {
			n = len(list)
		}
		vecs := make([]string, n)
		for i := range vecs {
			vecs[i] = "[1,0]"
		}
		_, _ = fmt.Fprintf(w, `{"embeddings":[%s]}`, strings.Join(vecs, ","))
	}))
	defer srv.Close()
	t.Setenv("DEJA_EMBED_URL", srv.URL+"/api/embed")
	if err := runEmbed(index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}
	// Sidecar current: fallback fires.
	out, used := maybeSemantic(index.DefaultDir(), nil, search.Options{Query: "totally different words"}, os.Stderr)
	if !used || len(out) == 0 {
		t.Fatalf("semantic fallback did not fire: used=%v out=%d", used, len(out))
	}
	// Dead endpoint with a valid sidecar: query embed fails, silent skip.
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/api/embed")
	if _, used := maybeSemantic(index.DefaultDir(), nil, search.Options{Query: "anything"}, os.Stderr); used {
		t.Fatal("dead endpoint must skip")
	}
	t.Setenv("DEJA_EMBED_URL", srv.URL+"/api/embed")
	// A version 2 sidecar follows its records through a rebuild and is still
	// used; one written before identities is stale once the generation moves.
	asVersion1Sidecar(t, index.DefaultDir())
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s2.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"another session"}}`,
	})
	if err := run([]string{"index", "--rebuild"}); err != nil {
		t.Fatal(err)
	}
	if _, used := maybeSemantic(index.DefaultDir(), nil, search.Options{Query: "anything"}, os.Stderr); used {
		t.Fatal("stale sidecar generation must skip")
	}
}

// A fork carries its source's edit records with the source's timestamps, so
// the record that made a line sits in both at the same instant. Candidates
// arrive newest first and the first one kept the tie, so a Claude /branch, or
// a fork of it, was credited with the edit its source made.
func TestBlameLineCreditsTheSourceNotItsFork(t *testing.T) {
	const line = "package main // WOMBAT-9 parent"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 1}
	commit := lineCommit{SHA: "f90b6e0e", When: time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-20 * time.Minute)
	replaced := "package main"
	session := func(id, kind, parent string) model.Session {
		return model.Session{Harness: "claude", ID: id, Kind: kind, Parent: parent, Messages: []model.Message{
			{Role: "user", Text: "edit main.go", Time: at.Add(-time.Second)},
			{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: at},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at},
		}}
	}
	src := session("6f40857d", "", "")
	branch := session("84f06bb6", "fork", "6f40857d")
	twig := session("46345f36", "fork", "84f06bb6")
	removed := map[string]bool{blameSpanKey(replaced): true}

	orders := [][]model.Session{
		{branch, src},
		{twig, src, branch},
		{twig, branch, src},
		{src, twig, branch},
	}
	for _, ss := range orders {
		for _, rm := range []map[string]bool{removed, nil} {
			got, ok := attributeLine(ss, target, commit, rm)
			if !ok || got.Session.ID != "6f40857d" {
				t.Errorf("order %s, replaced=%v: credited %q, want the source 6f40857d", forkOrder(ss), rm != nil, got.Session.ID)
			}
		}
	}

	// Control: a fork that wrote the line itself, later than the source, keeps it.
	own := session("84f06bb6", "fork", "6f40857d")
	own.Messages[1].Time, own.Messages[2].Time = at.Add(time.Minute), at.Add(time.Minute)
	if got, _ := attributeLine([]model.Session{src, own}, target, commit, removed); got.Session.ID != "84f06bb6" {
		t.Errorf("a fork's own later edit went to %q", got.Session.ID)
	}
	// And two unrelated sessions tied at one instant keep the first, as before.
	other := session("aaaa", "", "")
	if got, _ := attributeLine([]model.Session{other, src}, target, commit, removed); got.Session.ID != "aaaa" {
		t.Errorf("an unrelated tie moved to %q", got.Session.ID)
	}
}

func forkOrder(ss []model.Session) string {
	out := ""
	var outSb71 strings.Builder
	for _, s := range ss {
		outSb71.WriteString(s.ID + " ")
	}
	out += outSb71.String()
	return out
}

// claude --resume <id> --fork-session writes a copy that names no source and
// keeps every copied record's time, so the copy starts on the same turn as its
// source and ties with it on the edit. The copy holds the source's last turn;
// the source holds none of the copy's own (#5316).
func TestBlameGivesAForkSessionCopysLineToItsSource(t *testing.T) {
	const line = "	zebracount := countRowsPastLimit(rows, 4096)"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 2}
	commit := lineCommit{SHA: "271b28da", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-time.Hour)
	copied := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "the marmotquill exporter drops rows, add a zebracount", Time: at},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at.Add(time.Second)},
			{Role: "assistant", Text: "added zebracount, paging fixes the exporter", Time: at.Add(2 * time.Second)},
		}
	}
	src := model.Session{Harness: "claude", ID: "aaaaaaaa", Started: at, Messages: copied()}
	fork := model.Session{Harness: "claude", ID: "ffffffff", Started: at, Messages: append(copied(),
		model.Message{Role: "user", Text: "hi", Time: at.Add(10 * time.Minute)},
		model.Message{Role: "assistant", Text: "hello", Time: at.Add(10*time.Minute + time.Second)})}

	for _, order := range [][]model.Session{{fork, src}, {src, fork}} {
		if got, ok := attributeLine(order, target, commit, nil); !ok || got.Session.ID != "aaaaaaaa" {
			t.Errorf("order %s: credited %q, want the source aaaaaaaa", forkOrder(order), got.Session.ID)
		}
	}

	// Control: the source went on after the copy too, so neither holds the
	// other's last turn and nothing says which is the copy; the tie keeps the
	// first, as before.
	went := src
	went.Messages = append(copied(), model.Message{Role: "user", Text: "source again", Time: at.Add(20 * time.Minute)})
	if got, _ := attributeLine([]model.Session{fork, went}, target, commit, nil); got.Session.ID != "ffffffff" {
		t.Errorf("siblings that both went on: credited %q, want the first", got.Session.ID)
	}
	// And a sub-agent whose parent holds its turns is not a copy of it.
	child := src
	child.ID, child.Kind, child.Parent = "agent-x1", "subagent", "ffffffff"
	if got, _ := attributeLine([]model.Session{fork, child}, target, commit, nil); got.Session.ID != "ffffffff" {
		t.Errorf("a sub-agent tie moved to %q", got.Session.ID)
	}
}

// Grok 1.0.41 `--fork-session` copies the source's records into the fork's
// updates.jsonl under the fork's sessionId and outer timestamp, keeping the
// source's eventId (shape read off a live fork). The fork read them as its own
// turns, stamped later than the source, so blame gave the source's line to a
// fork that only said hi, and search listed the source's prompt twice.
func TestGrokForkKeepsOnlyItsOwnRecords(t *testing.T) {
	const (
		src  = "01a15000-0000-7000-8000-00000000000a"
		fork = "01a15000-0000-7000-8000-00000000000f"
		line = "func roundHalfEven(cents int64) int64 { return bankers(cents) }"
	)
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	proj := filepath.Join(root, "work", "ledger")
	file := filepath.Join(proj, "ledger.go")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package ledger\n\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 18, 2, 20, 0, 0, time.UTC)
	tf := t0.Add(100 * time.Second)
	event := func(sid, eid string, at time.Time, update map[string]any) string {
		b := mustMarshal(t, map[string]any{"timestamp": at.Unix(), "method": "session/update", "params": map[string]any{
			"sessionId": sid, "update": update,
			"_meta": map[string]any{"eventId": eid, "agentTimestampMs": at.UnixMilli(), "promptId": eid}}})
		return string(b) + "\n"
	}
	turn := func(sid, owner string, at time.Time, user string, write bool) []string {
		out := []string{event(sid, owner+"-3", at, map[string]any{"sessionUpdate": "user_message_chunk",
			"content": map[string]any{"type": "text", "text": user}, "_meta": map[string]any{"promptIndex": 0}})}
		if write {
			out = append(out, event(sid, owner+"-8", at, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call_w1", "title": "write",
				"rawInput": map[string]any{"file_path": file, "content": "package ledger\n\n" + line + "\n"},
				"_meta":    map[string]any{"x.ai/tool": map[string]any{"version": 1, "name": "write", "kind": "edit"}}}))
		}
		return append(out, event(sid, owner+"-14", at.Add(2*time.Second), map[string]any{"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{"type": "text", "text": "done"}}))
	}
	// A drive colon is no use in a directory name on Windows.
	group := filepath.Join(root, "sessions", strings.ReplaceAll(url.PathEscape(proj), ":", "%3A"))
	write := func(id string, summary map[string]any, lines []string) string {
		dir := filepath.Join(group, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		b := mustMarshal(t, summary)
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "updates.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	source := turn(src, src, t0, "Add banker rounding, codeword quillfen.", true)
	var copied []string
	for _, l := range source {
		var m map[string]any
		_ = json.Unmarshal([]byte(l), &m)
		m["timestamp"] = tf.Unix()
		params, ok := m["params"].(map[string]any)
		if !ok {
			t.Fatalf("no params in %s", l)
		}
		params["sessionId"] = fork
		b := mustMarshal(t, m)
		copied = append(copied, string(b)+"\n")
	}
	srcPath := write(src, map[string]any{"info": map[string]any{"id": src, "cwd": proj}, "session_kind": "headless",
		"created_at": t0.Format(time.RFC3339Nano)}, source)
	forkPath := write(fork, map[string]any{"info": map[string]any{"id": fork, "cwd": proj}, "session_kind": "headless",
		"created_at": tf.Format(time.RFC3339Nano), "parent_session_id": src, "forked_at": tf.Format(time.RFC3339Nano)},
		append(copied, turn(fork, fork, tf.Add(time.Second), "hi", false)...))

	parse := func(p string) model.Session {
		ss, err := sources.ParseGrokFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %v %d", p, err, len(ss))
		}
		return ss[0]
	}
	s, f := parse(srcPath), parse(forkPath)
	for _, m := range f.Messages {
		if strings.Contains(m.Text, "quillfen") || m.Role == sources.RoleWrote {
			t.Errorf("fork kept its source's record: %s %q", m.Role, m.Text)
		}
	}
	if len(f.Messages) == 0 || f.Messages[0].Text != "hi" {
		t.Errorf("fork lost its own turn: %+v", f.Messages)
	}

	target := search.BlameTarget{FullPath: file, Base: "ledger.go", Line: 3}
	commit := lineCommit{SHA: "d65793c9", When: tf.Add(time.Minute)}
	for _, ss := range [][]model.Session{{f, s}, {s, f}} {
		if got, ok := attributeLine(ss, target, commit, nil); !ok || got.Session.ID != src {
			t.Errorf("blame credited %q, want the source %s", got.Session.ID, src)
		}
	}

	// Control: what the fork writes itself is the fork's.
	forkPath = write(fork, map[string]any{"info": map[string]any{"id": fork, "cwd": proj}, "session_kind": "headless",
		"created_at": tf.Format(time.RFC3339Nano), "parent_session_id": src, "forked_at": tf.Format(time.RFC3339Nano)},
		append(copied, turn(fork, fork, tf.Add(time.Second), "write it again", true)...))
	if got, _ := attributeLine([]model.Session{s, parse(forkPath)}, target, commit, nil); got.Session.ID != fork {
		t.Errorf("the fork's own later write went to %q", got.Session.ID)
	}
	// Control: a session that is not a fork keeps a record under another
	// eventId; only a fork's copies are dropped.
	plain := write("01a15000-0000-7000-8000-0000000000b0", map[string]any{"info": map[string]any{"id": "01a15000-0000-7000-8000-0000000000b0", "cwd": proj},
		"session_kind": "headless"}, source)
	if got := parse(plain); len(got.Messages) != len(s.Messages) {
		t.Errorf("a non-fork lost records: %d, want %d", len(got.Messages), len(s.Messages))
	}
}

// blame answers "who decided this", and it answered with the accepted line of
// a decision that had been taken back: attachLifecycles ran on search hits
// only, and blame carries its own hit type (#1017).
func TestBlameCarriesTheDecisionThatHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"we raised the pool cap in pool.go to 200"},"timestamp":"2026-08-04T10:00:00Z","sessionId":"w25","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "w25", "--state", "accepted", "--note", "the pool cap is 200"); err != nil {
		t.Fatal(err)
	}

	// While the decision holds, blame says nothing extra.
	out, err := captureRun(t, "blame", "pool.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "tried and rejected") {
		t.Errorf("a decision that holds was reported as withdrawn:\n%s", out)
	}

	if _, err = captureRun(t, "promote", "w25", "--state", "rejected", "--note", "rolled back, the cap stays at 20"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "blame", "pool.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tried and rejected") {
		t.Errorf("blame reports a decision that was taken back as current:\n%s", out)
	}
	if !strings.Contains(out, "rolled back, the cap stays at 20") {
		t.Errorf("blame does not name the correction that won:\n%s", out)
	}
}

// A line asked about and then not mentioned at all leaves the reader unable to
// tell "this line has no history" from "your `:120` was ignored". Found by
// running the edges: a line past the end of the file, a directory, a file
// outside a repository and a file that is not there all printed the ordinary
// listing and nothing about the line (#3726).
func TestLineBlameSaysWhyItCannotAnswer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "app")
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tracked := filepath.Join(repo, "internal", "pool.go")
	if err := os.WriteFile(tracked, []byte("package app\n\nvar conns = 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "internal/pool.go")
	git("commit", "-qm", "first")
	untracked := filepath.Join(repo, "internal", "new.go")
	if err := os.WriteFile(untracked, []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "notes.md")
	if err := os.WriteFile(outside, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		line int
		want string
	}{
		{"past the end of the file", tracked, 900, "the file has 3 lines"},
		{"a directory", filepath.Join(repo, "internal"), 12, "that is a directory"},
		{"a file that is not there", filepath.Join(repo, "internal", "nope.go"), 4, "there is no such file here"},
		{"outside a repository", outside, 1, "not in a repository"},
		{"not tracked", untracked, 1, "not tracked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, why := gitLineCommit(tc.path, tc.line)
			if why == "" {
				t.Fatalf("no reason given for %s", tc.name)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.want)
			}
		})
	}

	// And the line it can answer for carries no refusal.
	if c, why := gitLineCommit(tracked, 3); why != "" || c.SHA == "" {
		t.Fatalf("a committed line should resolve: sha=%q why=%q", c.SHA, why)
	}
}

// The attribution rule, which is the whole of this surface: the session has to
// have replaced the same text the commit deleted, which makes it the session
// that performed the change and wrote the line being read. Measured over 494 commits
// of this repository, time-and-file overlap attributed 2 of 4 dependency bumps
// to a session that had nothing to do with them and this rule attributed none
// (#1181).
func TestAttributeLineTakesTheSessionThatWroteTheRemovedLine(t *testing.T) {
	target := search.BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool", Line: 12}
	commit := lineCommit{SHA: "abc1234567", Subject: "fix: one pool per shard", When: time.Now()}
	removed := map[string]bool{
		"conns := pool.New(shards * connsPerShard)": true,
	}
	wrote := model.Session{
		Harness: "claude", ID: "wrote", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "make the pool per shard", Time: commit.When.Add(-2 * time.Hour)},
			{Role: sources.RoleEdit, Text: "/work/app/internal/pool.go\nconns := pool.New(shards * connsPerShard)", Time: commit.When.Add(-time.Hour)},
		},
	}
	// The session that only read the file: a `files` record is not an edit, and
	// reading a line is not writing it.
	read := model.Session{
		Harness: "claude", ID: "read", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleFiles, Text: "/work/app/internal/pool.go", Time: commit.When.Add(-30 * time.Minute)},
		},
	}
	// And one that edited the same file after the commit, which cannot be what
	// the commit replaced.
	later := model.Session{
		Harness: "claude", ID: "later", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleEdit, Text: "/work/app/internal/pool.go\nconns := pool.New(shards * connsPerShard)", Time: commit.When.Add(time.Hour)},
		},
	}

	got, ok := attributeLine([]model.Session{read, later, wrote}, target, commit, removed)
	if !ok || got.Session.ID != "wrote" {
		t.Fatalf("attributed to %q (ok=%v), want the session that wrote the line", got.Session.ID, ok)
	}
	if !strings.Contains(got.Matched, "connsPerShard") {
		t.Errorf("the matched line is not reported: %q", got.Matched)
	}
	if got.Asked == "" {
		t.Error("the session's own task is what orients a reader; it is missing")
	}

	// The control: a commit whose removed lines nobody wrote — a dependency
	// bump, a merge, a hand edit deja never saw — attributes to nothing.
	if _, ok := attributeLine([]model.Session{read, later, wrote}, target, commit, map[string]bool{
		"uses: actions/checkout@v7.0.1": true,
	}); ok {
		t.Error("a commit deja never saw must attribute to nothing")
	}
	// And a worktree checkout of the same repository is the same file.
	wt := search.BlameTarget{FullPath: "/work/app/.claude/worktrees/x/internal/pool.go", Base: "pool.go", Stem: "pool", Line: 12}
	if _, ok := attributeLine([]model.Session{wrote}, wt, commit, removed); !ok {
		t.Error("the same file in a worktree checkout should still match")
	}
}

// A line is evidence only if it could have been written on purpose. Braces and
// `return nil` are in every diff and in every session.
func TestBlameSpanKeyDropsLinesThatMatchEverything(t *testing.T) {
	for _, s := range []string{"}", "\t}", "return nil", "  return err", "})", "import ("} {
		if blameSpanKey(s) != "" {
			t.Errorf("%q matches every commit and must not count", s)
		}
	}
	if got := blameSpanKey("   conns := pool.New(shards * connsPerShard)  "); got != "conns := pool.New(shards * connsPerShard)" {
		t.Errorf("got %q", got)
	}
}

// End to end against a real repository: git for the line's commit, the store
// for the session that wrote what it replaced.
func TestBlameLineNamesTheSessionBehindTheLine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	file := filepath.Join(repo, "pool.go")
	const before = "package app\n\nvar conns = newPool(shardCount * connsPerShard)\n"
	const after = "package app\n\nvar conns = newPool(shardCount)\n"
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "pool.go")
	git("commit", "-qm", "first")
	if err := os.WriteFile(file, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-aqm", "fix: one connection per shard is enough")

	// The session that wrote the line the second commit replaced.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "aaaa1111-2222-4000-8000-c3d4e5f6a7b8"
	stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	// Encoded rather than pasted: a Windows path carries backslashes, and a
	// path glued into a JSON fixture by hand is this repository's most frequent
	// windows-only failure.
	var b strings.Builder
	for _, rec := range []map[string]any{
		{"type": "user", "sessionId": id, "cwd": repo, "timestamp": stamp,
			"message": map[string]any{"role": "user", "content": "size the pool by shard"}},
		{"type": "assistant", "sessionId": id, "cwd": repo, "timestamp": stamp,
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "name": "Edit", "input": map[string]any{
					"file_path":  file,
					"old_string": "var conns = newPool(shardCount * connsPerShard)",
					"new_string": "var conns = newPool(shardCount)",
				}},
			}}},
	} {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "blame", file+":3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pool.go:3 last changed in") {
		t.Fatalf("the line's own commit is not named:\n%s", out)
	}
	if !strings.Contains(out, "written in claude") || !strings.Contains(out, search.ShortID(id)) {
		t.Fatalf("the session that wrote the replaced line is not named:\n%s", out)
	}
	if !strings.Contains(out, "connsPerShard") {
		t.Fatalf("the replaced line is not shown:\n%s", out)
	}

	// The control, in the same repository: a commit whose removed lines no
	// session wrote must say so rather than name the nearest session.
	readme := filepath.Join(repo, "README.md")
	if err = os.WriteFile(readme, []byte("# app\n\nrun it with make, and read docs/pool.md first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-qm", "docs: a readme")
	if err = os.WriteFile(readme, []byte("# app\n\nrun it with make, and read docs/shards.md first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-aqm", "chore(deps): bump the docs link")
	out, err = captureRun(t, "blame", readme+":3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no indexed session wrote this line or the lines this commit replaced") {
		t.Fatalf("a commit deja never saw must attribute to nothing:\n%s", out)
	}
}

// `deja blame internal/pool/pool.go` in one checkout listed other repos'
// pool.go sessions in among its own. A path names a file in this checkout;
// the others are counted and left to --all-projects.
func TestBlameAnswersFromThisProjectAndCountsTheRest(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	checkout := filepath.Join(tmp, "work", "checkout")
	payments := filepath.Join(tmp, "work", "payments")
	for _, d := range []string{filepath.Join(checkout, "internal", "pool"), payments} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	write := func(id, cwd string) {
		// The project comes from the record's cwd; the directory name only has
		// to be one Windows accepts, which an encoded C:\ path is not.
		dir := filepath.Join(root, "-work-"+filepath.Base(cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":"retire idle connections in internal/pool/pool.go before the proxy drops them"}}`, id, at, cwd)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("here0001", checkout)
	write("there001", payments)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)

	var runErr error
	out := captureStdout(t, func() { runErr = runBlame(dir, []string{"internal/pool/pool.go"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "here0001") || strings.Contains(out, "there001") {
		t.Fatalf("blame in the checkout listed:\n%s", out)
	}

	out = captureStdout(t, func() { runErr = runBlame(dir, []string{"internal/pool/pool.go", "--all-projects"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "here0001") || !strings.Contains(out, "there001") {
		t.Fatalf("--all-projects listed:\n%s", out)
	}
}

// A Qwen /branch copies its source's records at their original times, each
// marked forkedFrom, so the write_file that made a line sits in both files at
// one instant and the branch took the credit (#5224).
func TestBlameLineGivesAQwenBranchCopyToItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	repo := filepath.Join(root, "my-app")
	const line = "WALLABY3 parent line written by the parent session itself"
	file := filepath.Join(repo, "parent.txt")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jq := func(v string) string { b := mustMarshal(t, v); return string(b) }
	const src, branch = "8bde8fee-08e8-4950-b83b-2e23c486eb7d", "42355bb7-67a9-47b4-8436-796a7075a970"
	records := func(id, forked string) string {
		ff := ""
		if forked != "" {
			ff = `,"forkedFrom":{"sessionId":"` + forked + `"}`
		}
		r := func(typ, at, msg string) string {
			return `{"sessionId":"` + id + `","timestamp":"2026-10-18T06:03:` + at + `Z","type":"` + typ + `","cwd":` + jq(repo) + ff + `,` + msg + "}\n"
		}
		return r("user", "05.759", `"message":{"role":"user","parts":[{"text":"write parent.txt yourself"}]}`) +
			r("assistant", "05.848", `"message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"write_file","args":{"file_path":`+jq(file)+`,"content":"`+line+`\n"}}}]}`) +
			r("tool_result", "05.974", `"message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"write_file","response":{"output":"ok"}}}]}`)
	}
	parse := func(id, body string) model.Session {
		t.Helper()
		p := filepath.Join(root, "qwen", "projects", "-w-my-app", "chats", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := sources.ParseQwenFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %d sessions, %v", id, len(ss), err)
		}
		return ss[0]
	}
	source := parse(src, records(src, ""))
	copied := parse(branch, records(branch, src))
	if copied.Kind != "fork" || copied.Parent != src {
		t.Fatalf("branch read as kind=%q parent=%q", copied.Kind, copied.Parent)
	}
	target := search.BlameTarget{FullPath: file, Base: "parent.txt", Line: 1}
	commit := lineCommit{SHA: "e6ec5986", When: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)}
	for _, ss := range [][]model.Session{{copied, source}, {source, copied}} {
		got, ok := attributeLine(ss, target, commit, nil)
		if !ok || got.Session.ID != src {
			t.Errorf("order %s,%s: credited %q, want the source %s", ss[0].ID[:8], ss[1].ID[:8], got.Session.ID, src)
		}
	}
	// Control: the branch's own later write stays the branch's.
	own := parse(branch, strings.ReplaceAll(records(branch, src), "06:03:05", "06:09:05"))
	own.Kind, own.Parent = "fork", src
	if got, _ := attributeLine([]model.Session{source, own}, target, commit, nil); got.Session.ID != branch {
		t.Errorf("a branch's own later write went to %q", got.Session.ID)
	}
}

const aReason = "the saving is not where I claimed it was: most of the time goes on the candidate walk, which runs before the shortcut, so the check has to move earlier"

func sessionOf(msgs ...model.Message) model.Session {
	return model.Session{Harness: "claude", ID: "s1", Project: "app", Messages: msgs}
}

// The session's conclusion says nothing about the change — 0 of 81 lines in
// #3722. The turn the edit sits in does, about half the time, and that is what
// the line answer quotes (#3723).
func TestTheTurnAnEditSitsInIsQuoted(t *testing.T) {
	s := sessionOf(
		model.Message{Role: "user", Text: "make the fuzzy tier cheaper"},
		model.Message{Role: "assistant", Text: aReason},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nmatches := closeTokens(term, idx)"},
	)
	if got := saidBefore(s, 2); got != aReason {
		t.Fatalf("said = %q", got)
	}
	// The user's own instruction is not it: the question is what the session
	// decided, and "make it cheaper" is what it was asked.
	if got := saidBefore(sessionOf(
		model.Message{Role: "user", Text: aReason},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("a user turn was quoted as the session's own words: %q", got)
	}
}

// Half of these turns are handovers — "now the shared writer" — and they carry
// no reason at all. The floor is what keeps them out; it is a length, because
// the lexical test #3722 used caught 1 reason in 40.
func TestAHandoverLineIsNotQuotedAsAReason(t *testing.T) {
	short := "Now the shared writer."
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: short},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("a handover line was quoted as a reason: %q", got)
	}
	// And the search keeps going past it: the reason is often one turn above
	// the announcement of the edit.
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: aReason},
		model.Message{Role: "assistant", Text: short},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 2); got != aReason {
		t.Fatalf("said = %q, want the turn above the handover", got)
	}
}

// A session that ran deja keeps what deja printed, and this reads the messages
// around an edit rather than the ones blame already filters. Its own answer
// must not come back as the reason for the line it was about (#1330, #3723).
func TestDejaOwnAnswerIsNotQuotedAsAReason(t *testing.T) {
	own := "pool.go:3 last changed in abcdef12 · 2026-09-01 · fix: one pool per process\n" +
		"written in claude · 2915986c-9f7 · goprojects/deja-vu\n" +
		"why, in full: deja ctx 2915986c-9f7"
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: own},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("deja quoted its own answer back as the reason: %q", got)
	}
	// Including the shape this very line prints, which is the loop one turn
	// further out.
	quoted := saidBeforePrefix + aReason
	if got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: quoted},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1); got != "" {
		t.Fatalf("the quoted turn came back as a reason of its own: %q", got)
	}
}

func TestTheQuotedTurnIsBoundedAndFarTurnsAreLeftAlone(t *testing.T) {
	long := strings.Repeat("a reason spelled out at length. ", 40)
	got := saidBefore(sessionOf(
		model.Message{Role: "assistant", Text: long},
		model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"},
	), 1)
	if r := []rune(got); len(r) != saidBeforeMax+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("quoted %d runes, want %d and a marker", len([]rune(got)), saidBeforeMax+1)
	}
	msgs := make([]model.Message, 0, saidBeforeLookback+7)
	msgs = append(msgs, model.Message{Role: "assistant", Text: aReason})
	for range saidBeforeLookback + 5 {
		msgs = append(msgs, model.Message{Role: "user", Text: "go on"})
	}
	msgs = append(msgs, model.Message{Role: sources.RoleEdit, Text: "/w/pool.go\nx := 1"})
	if got := saidBefore(sessionOf(msgs...), len(msgs)-1); got != "" {
		t.Fatalf("a turn from earlier work was quoted: %q", got)
	}
}

// Both renderings say what the quote literally is, because it is a reason only
// about half the time.
func TestBothRenderingsLabelTheQuoteForWhatItIs(t *testing.T) {
	target := search.BlameTarget{Base: "pool.go", Line: 42, FullPath: "/w/pool.go"}
	commit := lineCommit{SHA: "abcdef1234567890", Subject: "fix: one pool per process", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	author := lineAuthor{
		Session: model.Session{Harness: "claude", ID: "2915986c9f79", Project: "deja-vu"},
		Matched: "cfg.MaxConns = int32(size)",
		Said:    aReason,
		Wrote:   true,
	}
	var prose bytes.Buffer
	printLineAuthor(&prose, target, commit, author, true)
	if !strings.Contains(prose.String(), saidBeforePrefix+aReason) {
		t.Fatalf("prose:\n%s", prose.String())
	}
	var js bytes.Buffer
	if err := writeLineAnswerJSON(&js, buildLineAnswer(target, commit, author, true, "")); err != nil {
		t.Fatal(err)
	}
	var got lineAnswerJSON
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SaidBefore != aReason {
		t.Fatalf("said_before = %q", got.SaidBefore)
	}
	// Still one line: the recogniser drops one, and this field carries
	// transcript text that can be anything.
	if n := strings.Count(strings.TrimRight(js.String(), "\n"), "\n"); n != 0 {
		t.Fatalf("the answer is %d lines", n+1)
	}
}

// The reading tools answer from the snapshot while a rebuild runs (#1733).
// blame was left out because its path takes the blocking EnsureForSearch — and
// blame is the tool an agent calls before editing a file, so "ask again then"
// means the edit happens without the history (#1784).
func TestBlameReadsWithoutWaitingForARebuild(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"b1","cwd":"/w","timestamp":"2026-08-24T01:00:00Z","message":{"role":"user","content":"zapfizzle editing parser.go here"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The agent-facing path must not be the blocking one.
	hits, _, _, _, err := findBlameHitsStale(dir, search.BlameTarget{Stem: "parser.go", Base: "parser.go"}, search.BlameOptions{All: true}, policy.ActivationMCP, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("blame found nothing on a store that mentions the file")
	}
	_ = tmp
	// And the tool no longer declines while a rebuild is in flight: that is
	// what buildingNowForBlockingTool is for, and blame is not one of those
	// any more. The case has to be there for this to mean anything.
	src, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for block := range strings.SplitSeq(string(src), "case \"") {
		if !strings.HasPrefix(block, "blame\"") {
			continue
		}
		found = true
		if strings.Contains(block, "buildingNowForBlockingTool") {
			t.Error("blame still declines while a rebuild runs")
		}
	}
	if !found {
		t.Fatal("no blame case in mcp.go, so this proved nothing")
	}
}

// And the answer says so, in the line recall uses for the same state.
func TestBlameSaysWhenItServedASnapshot(t *testing.T) {
	hits := []search.BlameHit{{Session: model.Session{ID: "s", Harness: "claude", Project: "proj"}, Count: 1}}
	body := renderMCPBlame("a.go", "a.go", hits, 0, coverageLine(index.Coverage{Stale: true}))
	if !strings.Contains(body, "refreshing in the background") {
		t.Errorf("a snapshot answer says nothing about the refresh: %s", body)
	}
	if quiet := renderMCPBlame("a.go", "a.go", hits, 0, ""); strings.Contains(quiet, "refresh") {
		t.Errorf("an ordinary answer carries the note: %s", quiet)
	}
}

// The note must not cost a session: the payload is trimmed to its budget on the
// hits alone, and the note is added afterwards.
func TestTheRefreshNoteDoesNotCostASession(t *testing.T) {
	hits := make([]search.BlameHit, 0, 40)
	for i := range 40 {
		hits = append(hits, search.BlameHit{
			Session:  model.Session{ID: "s", Harness: "claude", Project: "proj", Title: strings.Repeat("x", 300)},
			Count:    i,
			Snippets: []string{strings.Repeat("y", 300)},
		})
	}
	quiet := countBlameSessions(blameBodyFor(hits, false))
	noisy := countBlameSessions(blameBodyFor(hits, true))
	if quiet < 2 || noisy != quiet {
		t.Errorf("the note cost %d session(s): %d against %d", quiet-noisy, noisy, quiet)
	}
	// What it does cost is its own length, and no more.
	over := len(blameBodyFor(hits, true)) - blameMCPBudget
	if note := len("(" + coverageLine(index.Coverage{Stale: true}) + ")\n"); over > note {
		t.Errorf("the payload is %d bytes over the budget, more than the note's %d", over, note)
	}
}

// blameBodyFor runs the same trim-then-note sequence blameTextResult does.
func blameBodyFor(hits []search.BlameHit, refreshing bool) string {
	body := renderMCPBlame("a.go", "a.go", hits, 0, "")
	for len(body) > blameMCPBudget && len(hits) > 1 {
		hits = hits[:max(len(hits)*3/4, 1)]
		body = renderMCPBlame("a.go", "a.go", hits, 0, "")
	}
	if refreshing {
		body = renderMCPBlame("a.go", "a.go", hits, 0, coverageLine(index.Coverage{Stale: true}))
	}
	return body
}

// countBlameSessions counts the numbered rows of a blame page.
func countBlameSessions(body string) int {
	n := 0
	for line := range strings.SplitSeq(body, "\n") {
		if blameRowRE.MatchString(line) {
			n++
		}
	}
	return n
}

var blameRowRE = regexp.MustCompile(`^\d+\. \[`)

// blameStore: one session about retry.go that was promoted, several that only
// edited it.
func blameStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now().UTC()
	write := func(sid string, minutes int, lines []string) {
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = minutes
	}
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	edit := func(sid, old, when string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go","old_string":"` + old + `","new_string":"y"}}]}}`
	}
	write("dec", 400, []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"should the retry budget go up to 10 for the payments client?"}}`,
		edit("dec", "return 3", at(400)),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"no: the pool change fixed the timeouts, the retry budget stays at 5"}}`,
	})
	for k := range 3 {
		sid := "f" + string(rune('0'+k))
		write(sid, 100-10*k, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(100-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"more work on retry.go"}}`,
			edit(sid, "x", at(99-10*k)),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "accepted",
		"--note", "the retry budget stays at 5; the pool change is what fixed the timeouts"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// blame answers "who decided this". It marks a decision that was taken back
// (#1017) and said nothing about the one that stands, so the row carrying the
// answer read as the oldest and least relevant (#2514).
func TestBlameMarksTheDecisionThatStands(t *testing.T) {
	dir := blameStore(t)
	target, err := search.ResolveBlamePath("retry.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{}, "search", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the fixture found no history for retry.go")
	}
	var found bool
	for _, h := range hits {
		if h.Session.ID != "dec" {
			continue
		}
		found = true
		line := search.BlameLifecycleLine(h)
		if line == "" {
			t.Errorf("the session carrying the standing decision is marked with nothing")
		}
		if !strings.Contains(line, "retry budget stays at 5") {
			t.Errorf("the decision itself is not in the line: %q", line)
		}
	}
	if !found {
		t.Fatal("the promoted session is not among the blame rows at all")
	}
}

// A decision that was taken back still reads as a warning, not as one that
// stands.
func TestBlameStillWarnsAboutAWithdrawnDecision(t *testing.T) {
	dir := blameStore(t)
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "rejected", "--note", "we went to 10 after all"); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("retry.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{}, "search", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID != "dec" {
			continue
		}
		line := search.BlameLifecycleLine(h)
		if !strings.Contains(line, "tried and rejected") {
			t.Errorf("a withdrawn decision no longer reads as one: %q", line)
		}
		if strings.Contains(line, "standing") {
			t.Errorf("a withdrawn decision reads as standing: %q", line)
		}
	}
}

// goose and Hermes stamp records to the whole second, so a parent and the
// sub-agent it spawned can write one line at the same instant. The tie-break
// meant for forks took the older session and gave the sub-agent's line to
// its parent.
func TestBlameKeepsASubagentsLineOnAWholeSecondTie(t *testing.T) {
	const line = "retries := 5 // NUMBAT-4 written by the delegate"
	dir := t.TempDir()
	path := filepath.Join(dir, "retry.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "retry.go", Line: 1}
	commit := lineCommit{SHA: "c0ffee12", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-10 * time.Minute)
	replaced := "retries := 3"
	for _, harness := range []string{"goose", "hermes"} {
		session := func(id, kind, parent string, started time.Time) model.Session {
			return model.Session{Harness: harness, ID: id, Kind: kind, Parent: parent, Started: started, Messages: []model.Message{
				{Role: "user", Text: "raise the retries", Time: started},
				{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: at},
				{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at},
			}}
		}
		parent := session("parent-1", "", "", at.Add(-time.Hour))
		child := session("child-1", "subagent", "parent-1", at.Add(-time.Minute))
		for _, rm := range []map[string]bool{{blameSpanKey(replaced): true}, nil} {
			got, ok := attributeLine([]model.Session{child, parent}, target, commit, rm)
			if !ok || got.Session.ID != "child-1" {
				t.Errorf("%s, replaced=%v: credited %q, want the sub-agent child-1", harness, rm != nil, got.Session.ID)
			}
		}
	}
}

func TestParseBlame(t *testing.T) {
	path, options, jsonOutput, err := parseBlame([]string{"--json", "--all", "--harness", "claude", "--project", "api", "--since", "30d", "main.go"})
	if err != nil || path != "main.go" || !jsonOutput.JSON || !options.All || options.Harness != "claude" || options.Project != "api" || options.Since <= 0 {
		t.Fatalf("parse path=%q options=%#v json=%v err=%v", path, options, jsonOutput, err)
	}
	for _, args := range [][]string{{}, {"--harness"}, {"--bad", "main.go"}, {"a.go", "b.go"}} {
		if _, _, _, err := parseBlame(args); err == nil {
			t.Fatalf("parse accepted %#v", args)
		}
	}
	if _, _, _, err := parseBlame([]string{"--since", "bad", "main.go"}); err == nil {
		t.Fatal("bad since accepted")
	}
	if _, _, _, err := parseBlame([]string{"--json", "main.go"}); err != nil {
		t.Fatalf("json parse err=%v", err)
	}
}

func TestBlameCLIAndMCP(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	out, err := captureRun(t, "blame", "--json", "parser.go")
	if err != nil || !strings.Contains(out, `"session"`) || !strings.Contains(out, "parser.go") {
		t.Fatalf("blame out=%q err=%v", out, err)
	}
	text, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go","harness":"claude","limit":1}`))
	if err != nil || countBlameSessions(text) != 1 {
		t.Fatalf("mcp blame=%q err=%v", text, err)
	}
	if text, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go","all":true}`)); err != nil || countBlameSessions(text) == 0 {
		t.Fatalf("mcp all blame=%q err=%v", text, err)
	}
	if err := runBlame(index.DefaultDir(), []string{"parser.go"}); err != nil {
		t.Fatal(err)
	}
	if err := runBlame(index.DefaultDir(), []string{"missing.go"}); err != nil {
		t.Fatal(err)
	}
	if err := runBlame(index.DefaultDir(), nil); err == nil {
		t.Fatal("missing blame path accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":`)); err == nil {
		t.Fatal("malformed mcp blame accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"x.go","since":"bad"}`)); err == nil {
		t.Fatal("bad mcp blame since accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"   "}`)); err == nil {
		t.Fatal("empty mcp blame path accepted")
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	if err := runBlame(index.DefaultDir(), []string{"parser.go"}); err == nil {
		t.Fatal("blame accepted blocked index")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go"}`)); err == nil {
		t.Fatal("mcp blame accepted blocked index")
	}
}

// The file was edited, but the only session that touched it is one a rule
// withholds. blame said "no sessions mention it" — looked-and-absent, when the
// rule hid it. search, last and files name the rule (#686, #680).
func TestBlameNamesTheTrustPolicyOnAnEmptyResult(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	// An imported session edited token.go; no local session touches it.
	var batch []byte
	for _, r := range []index.SyncRecord{
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "user", Text: "the token rotation work"},
		{Harness: "claude", SessionID: "peer", Project: "svc", Role: "files", Text: filepath.Join(tmp, "repo", "token.go")},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)
	out, _ := captureRunStderr(t, "blame", "token.go")
	if !strings.Contains(out, "trust policy") {
		t.Errorf("blame did not name the rule that emptied the result:\n%s", out)
	}
	if strings.Contains(out, "no sessions mention") {
		t.Errorf("blame still reads as looked-and-absent when a rule hid the match:\n%s", out)
	}
}

// A blame row carried every file its session touched — up to the forty the
// manifest holds. In real-world stores, that was 3186 of an 8044-byte
// answer, about files the question did not ask about, and the answer is trimmed
// by dropping whole sessions to fit its budget: six real paths came back with 20
// sessions and 9.9 KB of quoted history, and with the list bounded the same
// budget carried 38 sessions and 15.7 KB.
func TestABlameRowNamesAFewTouchedFilesNotForty(t *testing.T) {
	touched := make([]string, 0, 30)
	for i := range 30 {
		touched = append(touched, fmt.Sprintf("/work/app/internal/thing%02d.go", i))
	}
	hit := search.BlameHit{
		Session: model.Session{
			Harness: "claude", ID: "s1", Project: "app",
			Title: "touched a great many files", Touched: touched,
		},
		Count: 1, Snippets: []string{"… internal/thing00.go …"},
	}
	body := renderMCPBlame("internal/thing00.go", "thing00.go", []search.BlameHit{hit}, 0, "")
	var line string
	for l := range strings.SplitSeq(body, "\n") {
		if after0, ok := strings.CutPrefix(l, "also worked on: "); ok {
			line = after0
		}
	}
	if line == "" {
		t.Fatalf("the row names no other file at all:\n%s", body)
	}
	got := strings.Split(line, ", ")
	if len(got) > blameTouchedCap {
		t.Errorf("the row names %d touched files, which is the manifest's list rather than a few", len(got))
	}
	// The head of the list is the most-touched end, so that is what survives,
	// past the file that was asked about.
	if got[0] != "thing01.go" {
		t.Errorf("the first other file is %q, not the one the session touched most", got[0])
	}
	if strings.Contains(line, "/work/") {
		t.Errorf("the row spends its budget on full paths: %s", line)
	}
}

// blame picks candidates with an ordinary search for the file's stem, and the
// paths a tool call recorded do not answer one — so the session that actually
// changed the file was invisible while an older one that only mentioned it in
// passing was the answer (#688).
func TestBlameFindsSessionsThatOnlyTouchedTheFile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(name string, lines ...string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(path string) string {
		b := mustMarshal(t, map[string]any{"file_path": path, "old_string": "a", "new_string": "b"})
		return `{"type":"assistant","sessionId":"edited","cwd":"/w/p","timestamp":"2026-07-25T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` + string(b) + `}]}}`
	}
	// Says the name, never touches it.
	write("named.jsonl",
		`{"type":"user","sessionId":"named","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"we rewrote pool.go to fix the starvation"}}`)
	// Touches it, never says the name.
	write("edited.jsonl",
		`{"type":"user","sessionId":"edited","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"the starvation is back under burst load"}}`,
		edit(filepath.ToSlash(filepath.Join(tmp, "repo", "pool.go"))))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("pool.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{All: true}, "search", nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.Session.ID] = true
	}
	if !ids["edited"] {
		t.Errorf("the session that edited the file is missing: %v", ids)
	}
	if !ids["named"] {
		t.Errorf("the session that named the file is missing: %v", ids)
	}
	// Newer work on the same file comes first — that is the question blame answers.
	if len(hits) > 0 && hits[0].Session.ID != "edited" {
		t.Errorf("top hit = %q, want the session that actually changed it", hits[0].Session.ID)
	}
}

// A blame for a file nobody touched must not drag in unrelated sessions.
func TestBlameTouchersMatchTheWholeName(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"unrelated work"}}` + "\n" +
		`{"type":"assistant","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/poolmanager.go","old_string":"a","new_string":"b"}}]}}` + "\n" +
		// A path that *contains* the name is not the file: old-pool.go and
		// pool.go are two files, and substring matching answers for both.
		`{"type":"assistant","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/old-pool.go","old_string":"a","new_string":"b"}}]}}`
	if err := os.WriteFile(filepath.Join(root, "e1.jsonl"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("pool.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{All: true}, "search", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("poolmanager.go answered a blame for pool.go: %#v", hits)
	}
}

// Search returns sessions with only their matching messages attached, so one
// that matched on speech arrives with its `files` record stripped — and #688
// skipped it as "already present". Mentioning the subject in the same session
// therefore removed it from that file's blame (#723).
func TestBlameFindsASessionThatBothMentionsAndTouchesTheFile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	edited := filepath.ToSlash(filepath.Join(tmp, "repo", "internal", "parser.go"))
	b := mustMarshal(t, map[string]any{"file_path": edited, "old_string": "a", "new_string": "b"})
	body := `{"type":"user","sessionId":"s2","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the parser choked on a nested block"}}` + "\n" +
		`{"type":"assistant","sessionId":"s2","cwd":"/w/p","timestamp":"2026-07-21T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` + string(b) + `}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s2.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("parser.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{All: true}, "search", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "s2" {
		t.Fatalf("hits = %#v — the session that edited the file is missing", hits)
	}
	// One session, one row: replacing the trimmed copy must not duplicate it.
	seen := map[string]int{}
	for _, h := range hits {
		seen[h.Session.ID]++
	}
	if seen["s2"] != 1 {
		t.Errorf("session appears %d times", seen["s2"])
	}
}

// opencode's --fork copies the source's messages and parts with their original
// times and names no source in the store. The edit record then ties to the
// millisecond in both sessions, and whichever came first in the hit list took
// the line: the fork, for an edit it only copied. The source started no later
// than any copy of it, so an exact tie goes to the earlier session.
func TestBlameGivesATiedLineToTheSessionACopyCameFrom(t *testing.T) {
	const (
		line     = `	println("the parent session replaced this println")`
		replaced = `	println("original line kept here for testing")`
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n"+line+"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 4}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	start := commit.When.Add(-time.Hour)
	editAt := start.Add(15 * time.Second)
	records := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "replace the println argument", Time: start.Add(60 * time.Millisecond)},
			{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: editAt},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: editAt},
		}
	}
	source := model.Session{Harness: "opencode", ID: "ses_source", Started: start, Messages: records()}
	fork := model.Session{Harness: "opencode", ID: "ses_fork", Title: "replace println (fork #1)",
		Started: start.Add(60 * time.Millisecond), Messages: append(records(),
			model.Message{Role: "user", Text: "now write fork.txt", Time: start.Add(time.Minute)})}

	for _, order := range [][]model.Session{{fork, source}, {source, fork}} {
		got, ok := attributeLine(order, target, commit, map[string]bool{blameSpanKey(replaced): true})
		if !ok || got.Session.ID != "ses_source" {
			t.Errorf("replaced rule, order %s,%s: credited %q, want the source", order[0].ID, order[1].ID, got.Session.ID)
		}
		got, ok = attributeLine(order, target, commit, nil)
		if !ok || got.Session.ID != "ses_source" {
			t.Errorf("written rule, order %s,%s: credited %q, want the source", order[0].ID, order[1].ID, got.Session.ID)
		}
	}

	// Control: the fork's own later edit of the same line is newer, and is the fork's.
	own := fork
	own.Messages = append(records(), model.Message{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: start.Add(2 * time.Minute)})
	if got, _ := attributeLine([]model.Session{source, own}, target, commit, nil); got.Session.ID != "ses_fork" {
		t.Errorf("a fork that wrote the line again later is credited %q, want the fork", got.Session.ID)
	}
}

// A commit that only adds lines — 71% of them — leaves the replaced-span rule
// with nothing to match, and that silence is what #3773 measured: 7.3% of
// committed lines answered. The written side answers the same question from the
// other direction.
func TestALineNobodyReplacedIsAttributedToTheSessionThatWroteIt(t *testing.T) {
	const line = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte("package pool\n\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 3}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	wrote := func(id string, at time.Time, written string) model.Session {
		return model.Session{
			Harness: "claude", ID: id,
			Messages: []model.Message{{
				Role: sources.RoleWrote,
				Text: sources.WroteRecord(path, written),
				Time: at,
			}},
		}
	}

	// Nothing was deleted, so the strong rule has nothing to say.
	author, found := attributeLine([]model.Session{
		wrote("s1", commit.When.Add(-2*time.Hour), line),
	}, target, commit, nil)
	if !found {
		t.Fatal("a line a session wrote and a commit added is attributed to nobody")
	}
	if author.Session.ID != "s1" || !author.Wrote {
		t.Fatalf("attributed to %q, wrote=%v", author.Session.ID, author.Wrote)
	}

	// The strong rule still wins where it has evidence: replacing the text a
	// commit deleted proves the session made that very change.
	replaced := "cfg.MaxConns = 10 // the number from before this change"
	strong := model.Session{Harness: "claude", ID: "s2", Messages: []model.Message{{
		Role: sources.RoleEdit,
		Text: path + "\n" + replaced,
		Time: commit.When.Add(-time.Hour),
	}}}
	author, found = attributeLine([]model.Session{
		wrote("s1", commit.When.Add(-2*time.Hour), line), strong,
	}, target, commit, map[string]bool{blameSpanKey(replaced): true})
	if !found || author.Session.ID != "s2" || author.Wrote {
		t.Fatalf("the weaker rule won: %q wrote=%v", author.Session.ID, author.Wrote)
	}
}

func TestTheWrittenRuleTakesTheLastSessionBeforeTheCommit(t *testing.T) {
	const line = "pool, err := pgxpool.NewWithConfig(ctx, cfg) // one pool per process"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	at := func(id string, d time.Duration) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{{
			Role: sources.RoleWrote,
			Text: sources.WroteRecord(path, line),
			Time: commit.When.Add(d),
		}}}
	}
	// Three sessions wrote the same line; the commit carried the last one
	// before it, and the one after it wrote something else.
	author, found := attributeLine([]model.Session{
		at("older", -48*time.Hour), at("newer", -time.Hour), at("after", time.Hour),
	}, target, commit, nil)
	if !found {
		t.Fatal("nothing was attributed")
	}
	if author.Session.ID != "newer" {
		t.Errorf("attributed to %q, want the last write before the commit", author.Session.ID)
	}
}

func TestTheWrittenRuleNeedsTheSameFile(t *testing.T) {
	const line = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	// The same line, written into a different file: a boilerplate line moves
	// between files and the rule has to stay about this one.
	elsewhere := model.Session{Harness: "claude", ID: "elsewhere", Messages: []model.Message{{
		Role: sources.RoleWrote,
		Text: sources.WroteRecord(filepath.Join(dir, "other", "cache.go"), line),
		Time: commit.When.Add(-time.Hour),
	}}}
	if _, found := attributeLine([]model.Session{elsewhere}, target, commit, nil); found {
		t.Error("a line written into another file was attributed to this one")
	}
}

func TestAShortLineIsNeverAttributedByTheWrittenRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	// Below the 24-rune floor: every session in the store wrote this.
	if err := os.WriteFile(path, []byte("\treturn nil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	s := model.Session{Harness: "claude", ID: "s1", Messages: []model.Message{{
		Role: sources.RoleWrote,
		// The record holds a real line, so the session is a candidate; the
		// question is whether the short line asked about matches anything.
		Text: sources.WroteRecord(path, "\treturn nil\nsomething long enough to be evidence here"),
		Time: commit.When.Add(-time.Hour),
	}}}
	if _, found := attributeLine([]model.Session{s}, target, commit, nil); found {
		t.Error("`return nil` was attributed to a session")
	}
}
