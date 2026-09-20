package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
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
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
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
