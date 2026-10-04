package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func msgs(roleText ...string) []model.Message {
	now := time.Now()
	var ms []model.Message
	for i := 0; i+1 < len(roleText); i += 2 {
		ms = append(ms, model.Message{Role: roleText[i], Text: roleText[i+1], Time: now.Add(time.Duration(i) * time.Second)})
	}
	return ms
}

// zsh refusing `echo ===` says nothing about the code; the next file the
// session edited and the next unrelated command it ran are not answers to it.
// On one machine this wall was answered "changed next: <unrelated file>" for
// 57 repeat failures.
func TestALineTheShellRefusedIsNotAnsweredByTheNextEdit(t *testing.T) {
	ms := msgs(
		"command", "sed -n 1,20p a.go; echo ===; sed -n 1,20p b.go",
		"tool-output", "(eval):1: == not found",
		"edit", "internal/index/retrieval.go\nfunc x() {}",
		"command", "go build ./...",
		"tool-output", "",
	)
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Fatalf("a refused line paired with what came after it: %+v", pairs)
	}
}

// The same line in a form that runs is the answer.
func TestALineTheShellRefusedIsAnsweredByItselfCorrected(t *testing.T) {
	ms := msgs(
		"command", "sed -n 1,20p a.go; echo ===; sed -n 1,20p b.go",
		"tool-output", "(eval):1: == not found",
		"command", "sed -n 1,20p a.go; echo ---; sed -n 1,20p b.go",
		"tool-output", "package a",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want the corrected line as a repaired pair, got %+v", pairs)
	}
}

// A shared `cd` in front made both sides a navigation, which the repair rule
// turns away, so `timeout` dropped from `cd wt && timeout 30 go test` was
// never recognised as the fix it is.
func TestADroppedWrapperBehindACdIsARepair(t *testing.T) {
	ms := msgs(
		"command", "cd /work/wt && timeout 30 go test ./cmd/deja -run TestX",
		"tool-output", "zsh:1: command not found: timeout",
		"command", "cd /work/wt && go test ./cmd/deja -run TestX",
		"tool-output", "ok  \texample.com/cmd/deja\t0.4s",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want a repaired pair, got %+v", pairs)
	}
	if !pairs[0].MachineFact() {
		t.Error("a missing program answered by the command without it is a fact about the machine")
	}
}

// gh missing is answered by another program doing its job on the same
// repository: what 34 of 49 Hermes sessions on one machine did.
func TestAMissingProgramIsAnsweredByOneDoingItsJob(t *testing.T) {
	ms := msgs(
		"command", "gh pr view 1463 --repo acme/widgets --json title,body",
		"tool-output", "/bin/bash: line 2: gh: command not found",
		"command", "git status --short",
		"tool-output", "",
		"command", "git ls-remote https://github.com/acme/widgets.git refs/pull/1463/head",
		"tool-output", "abc123\trefs/pull/1463/head",
	)
	pairs := fixPairsIn(ms, "hermes:s1", "p")
	if len(pairs) != 1 || !pairs[0].Substitute || pairs[0].Command != "git ls-remote https://github.com/acme/widgets.git refs/pull/1463/head" {
		t.Fatalf("want the ls-remote as the substitute, got %+v", pairs)
	}
	if pairs[0].Candidate || !selfEvidentPair(pairs[0]) || !pairs[0].MachineFact() {
		t.Errorf("a substitute stands on its own evidence: %+v", pairs[0])
	}
}

// One shared word is a coincidence: `gh auth status` and `git status`.
func TestOneSharedWordIsNotASubstitute(t *testing.T) {
	if substitutedProgram("gh: command not found", "gh auth status", "git status --short --branch") {
		t.Error("git status read as doing gh auth status's job")
	}
}

// Hermes runs a turn's calls together and stores the outputs after every
// command, so the record before the error is someone else's output. The error
// names the program, and that names the command.
func TestTheFailingLineIsFoundByTheProgramTheErrorNames(t *testing.T) {
	ms := msgs(
		"command", "gh api repos/acme/widgets/issues/1625 --jq .title",
		"command", "cat notes.md",
		"tool-output", "# notes",
		"tool-output", "/bin/bash: line 2: gh: command not found",
		"command", "git ls-remote git@github.com:acme/widgets.git refs/pull/1625/head",
		"tool-output", "abc\trefs/pull/1625/head",
	)
	pairs := fixPairsIn(ms, "hermes:s1", "p")
	if len(pairs) != 1 || !pairs[0].Substitute {
		t.Fatalf("want a substitute found through the named program, got %+v", pairs)
	}
}

// An install that names what was missing is still the answer.
func TestAnInstallStillAnswersAMissingModule(t *testing.T) {
	ms := msgs(
		"command", "python3 scan.py",
		"tool-output", "ModuleNotFoundError: No module named 'yaml'",
		"command", "python3 -m pip install pyyaml",
		"tool-output", "Successfully installed pyyaml",
	)
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 1 || pairs[0].Command != "python3 -m pip install pyyaml" {
		t.Fatalf("want the install, got %+v", pairs)
	}
}

// Only the shell's refusals change. A failing test is still answered by the
// file the session edited after it.
func TestAFailureThatRanStillPairsWithTheEdit(t *testing.T) {
	ms := msgs(
		"tool-output", "--- FAIL: TestPoolDrainsOnClose (0.09s)",
		"edit", "calc/calc.go\nreturn a + b",
		"tool-output", "ok  \tcalc\t0.4s",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Edit != "calc/calc.go" {
		t.Fatalf("want the edit pair, got %+v", pairs)
	}
}

func TestMachineFactIsOnlyAMissingProgramAnswered(t *testing.T) {
	cases := []struct {
		p    FixPair
		want bool
	}{
		{FixPair{Error: "command not found: timeout", Command: "go test ./...", Repaired: true}, true},
		{FixPair{Error: "No module named 'yaml'", Command: "pip install pyyaml"}, false},
		{FixPair{Error: "command not found: timeout", Command: "go test ./...", Repaired: true, Candidate: true}, false},
		{FixPair{Error: "--- FAIL: TestAdd", Command: "go test ./x -run TestAdd", Repaired: true}, false},
	}
	for _, c := range cases {
		if got := c.p.MachineFact(); got != c.want {
			t.Errorf("%+v: MachineFact = %v, want %v", c.p, got, c.want)
		}
	}
}
