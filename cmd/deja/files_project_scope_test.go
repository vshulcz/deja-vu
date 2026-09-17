package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Asked in one repository, `deja files` used to answer with the busiest
// project's files: on a real store a generic topic named eight paths and not
// one of them was in the repository the question came from (#3713).
func TestFilesAnswersAboutTheProjectItWasAskedIn(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	here := filepath.Join(tmp, "work", "hereproj")
	if err := os.MkdirAll(filepath.Join(here, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(id, cwd, file string, touches int) {
		if err := os.MkdirAll(filepath.Join(cwd, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(tmp, "claude", "proj-"+id)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the timeout on the worker"}}`+"\n", id, cwd)
		for i := 0; i < touches; i++ {
			fmt.Fprintf(&b, `{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-20T10:0%d:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`+"\n",
				id, cwd, i+1, file)
		}
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The other project is busier, which is what put it in front of the answer.
	write("hereproj", here, filepath.Join(here, "internal", "worker.go"), 2)
	write("otherproj", filepath.Join(tmp, "work", "otherproj"), filepath.Join(tmp, "work", "otherproj", "queue", "handler.go"), 9)

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)

	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"timeout"}, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "handler.go") {
		t.Errorf("another project's file answered a question asked here:\n%s", got)
	}
	if !strings.Contains(got, "worker.go") {
		t.Errorf("this project's own file is missing:\n%s", got)
	}
	// And the answer says which project it is about, so an agent reading the
	// paths knows whether they are in the tree it is standing in.
	if !strings.Contains(got, "hereproj") {
		t.Errorf("the scope is not named:\n%s", got)
	}

	// The machine is still one flag away, and it has the busier project in it.
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"timeout", "--all-projects"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "handler.go") {
		t.Errorf("--all-projects should reach the whole machine:\n%s", buf.String())
	}

	// A project with nothing to say gets the machine's answer rather than
	// "nobody mentioned it": an agent told nothing exists invents something.
	empty := filepath.Join(tmp, "work", "emptyproj")
	if err := os.MkdirAll(filepath.Join(empty, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(empty)
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"timeout"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "handler.go") || !strings.Contains(buf.String(), "nothing in this project") {
		t.Errorf("a project with nothing to say should widen and say so:\n%s", buf.String())
	}
}
