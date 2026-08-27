package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A block whose end marker went missing cannot be bounded, so it is refused
// (#1705). On the way out that refusal has to hand the work back properly: name
// the file, and take out the blocks that *can* be taken out — grok's guidance
// lives in two files, and one broken block kept a well-formed one (#2210).
func TestUninstallNamesTheFileItRefusesAndClearsTheOther(t *testing.T) {
	hermeticEnv(t)
	home := sources.GrokHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	grokMD := filepath.Join(home, "GROK.md")
	agentsMD := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(grokMD, []byte("my own notes about this box\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatal(err)
	}
	// The premise: both files carry a block, so the removal below has two
	// things to do and not one.
	for _, p := range []string{grokMD, agentsMD} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), guidanceEnd) {
			t.Fatalf("%s does not carry a complete block, so this measures nothing:\n%s", p, b)
		}
	}

	// An editor eats one end marker.
	b, err := os.ReadFile(grokMD)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), guidanceEnd+"\n", "", 1) + "\nsomething the user wrote afterwards\n"
	if err := os.WriteFile(grokMD, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = installGuidance("grok", true)
	if err == nil {
		t.Fatal("the unbounded block was removed anyway")
	}
	// The refusal is the instruction to do it by hand, so it has to say where.
	if !strings.Contains(err.Error(), grokMD) {
		t.Errorf("the refusal names no file, so nobody knows what to edit: %v", err)
	}

	// And the file that was fine is clean: its block is independent, and on the
	// way out the reader wants as much gone as can go.
	after, err := os.ReadFile(agentsMD)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), guidanceStart) {
		t.Errorf("the twin still carries a complete deja block nobody mentioned:\n%s", after)
	}
	// The half-marked file is untouched, including the user's own text.
	still, err := os.ReadFile(grokMD)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(still), "my own notes about this box") ||
		!strings.Contains(string(still), "something the user wrote afterwards") {
		t.Errorf("the refused file lost the user's text:\n%s", still)
	}
}

// Both files unbounded: `grok-auto` runs `grok` first and fails on the same
// file twice, so naming only that one sends the reader to fix half of it and
// walk into the other half (#2210).
func TestUninstallNamesBothFilesWhenBothAreUnbounded(t *testing.T) {
	hermeticEnv(t)
	home := sources.GrokHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	grokMD := filepath.Join(home, "GROK.md")
	agentsMD := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(grokMD, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{grokMD, agentsMD} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		broken := strings.Replace(string(b), guidanceEnd+"\n", "", 1)
		if broken == string(b) {
			t.Fatalf("%s had no end marker to remove, so this measures nothing", p)
		}
		if err := os.WriteFile(p, []byte(broken+"\nthe user's own tail\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := installGuidance("grok", true)
	if err == nil {
		t.Fatal("two unbounded blocks were removed anyway")
	}
	for _, want := range []string{grokMD, agentsMD} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
}
