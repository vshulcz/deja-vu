package index

import "testing"

func TestShellOnlySigsAndTheSameFailingCommand(t *testing.T) {
	out := "(eval):1: == not found\nzsh:1: command not found: gtimeout\nError: widget cache is locked by pid 4242"
	sigs := fixSignaturesFor(t.TempDir(), out)
	shell := shellOnlySigs(out, "make lint-planner && echo == done", sigs)
	parse, _ := FrictionLine("(eval):1: == not found")
	missing, _ := FrictionLine("zsh:1: command not found: gtimeout")
	locked, _ := FrictionLine("Error: widget cache is locked by pid 4242")
	if !shell[frictionHash(parse)] || !shell[frictionHash(missing)] || shell[frictionHash(locked)] {
		t.Errorf("shell-only set %v", shell)
	}
	// The command runs the missing program: that is its own failure.
	if s := shellOnlySigs(out, "gtimeout 30 go vet ./...", sigs); s[frictionHash(missing)] {
		t.Error("a missing program the command runs read as the shell's complaint")
	}
	if shellOnlySigs(out, "", sigs) != nil {
		t.Error("with no command there is nothing to compare")
	}
	if !shellSpoke("bash: line 3: syntax error near unexpected token `)'") || !shellSpoke("zsh: parse error near `|'") || shellSpoke("/bin/bash: line 9: gh: command not found") {
		t.Error("shellSpoke misread a line")
	}

	p := FixPair{Failed: "cd /work/orchard && make lint-planner && echo == done", Command: "make lint-planner && echo done"}
	if !sameFailingCommand(p, "make lint-planner && echo == ok") {
		t.Error("the same command corrected was not the same failure")
	}
	if sameFailingCommand(p, "go test ./internal/rows; echo == end") || sameFailingCommand(p, "") {
		t.Error("another command was the same failure")
	}
	if !sameFailingCommand(FixPair{Command: "make lint-planner"}, "make lint-planner") {
		t.Error("a pair with no failed command is compared on its remedy")
	}
	if got := without(map[uint64]bool{1: true, 2: true}, map[uint64]bool{2: true}); len(got) != 1 || !got[1] {
		t.Errorf("without = %v", got)
	}
}
