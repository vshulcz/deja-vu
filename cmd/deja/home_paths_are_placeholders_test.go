package main

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// Test data has to name home directories, and the shortest way to get a
// realistic one is to paste the path in front of you. Fifteen of them had been
// pasted that way — `-Users-<account>-deja-vu` in the index tests, a scratch
// path complete with a job id in the redaction tests — and a grep for
// `/Users/<account>` finds none of them, because the harnesses encode the
// separator away.
//
// Fictional accounts are not the problem: the tree is full of John, probe and
// Alice, and they are the right answer. What cannot be here is the account of
// whoever wrote the test, so this asks the machine it runs on for its own home
// and looks for that — no real name has to be written down for the check to
// work, on this laptop or on a runner.
func TestNoTrackedFileNamesTheAccountItWasWrittenOn(t *testing.T) {
	// Not os.UserHomeDir: TestMain points HOME at a temp directory, which is
	// the whole point of the hermetic suite and would leave this checking for
	// a path nobody could have pasted. The passwd entry is not overridable.
	me, err := user.Current()
	if err != nil {
		t.Skipf("no account to compare against: %v", err)
	}
	account := me.Username
	if i := strings.LastIndexAny(account, `\/`); i >= 0 {
		account = account[i+1:] // DOMAIN\user on Windows
	}
	if len(account) < 3 {
		t.Skipf("account name %q is too short to search for", account)
	}
	// A build machine's account is not a person's: `/home/runner/work/...` is
	// the path GitHub gives every job and appears in an ignore-rule fixture on
	// purpose. This check is about the laptop the test was written on.
	switch strings.ToLower(account) {
	case "runner", "runneradmin", "root", "ubuntu", "ec2-user", "admin",
		"user", "build", "jenkins", "circleci", "travis", "github", "vsts":
		t.Skipf("%q is a build account, not a developer's", account)
	}
	// Both spellings: the path as written, and the form a harness makes of it
	// when it turns a project path into one directory name.
	needles := []string{
		"/Users/" + account, "-Users-" + account,
		"/home/" + account, "-home-" + account,
		`\Users\` + account,
	}

	root := filepath.Join("..", "..")
	// Tracked files only: what a working tree holds beside them is the
	// developer's own — a local tool's output cache is where this first fired,
	// and it is ignored precisely because it belongs to nobody else.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	files := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	if len(files) < 100 {
		t.Fatalf("git listed %d files — this is not reading the repository", len(files))
	}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".go", ".md", ".html", ".txt", ".json", ".jsonl", ".yml", ".yaml", ".sh", ".mjs", ".js", ".ts", ".py", ".toml", ".sql", ".css":
		default:
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		lower := strings.ToLower(string(b))
		for _, n := range needles {
			if !strings.Contains(lower, strings.ToLower(n)) {
				continue
			}
			t.Errorf("%s carries a home directory from the machine it was written on (%q) — use a made-up account instead",
				f, n)
		}
	}
}
