package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Three commands taken from the tools' own sources, and the one case that has
// to refuse: a Kiro session belonging to the IDE, which reopens from the app
// and would otherwise get a CLI command that does nothing (#3651).
func TestResumeCommandsForTheNewHarnesses(t *testing.T) {
	for _, tc := range []struct {
		harness string
		id      string
		want    string
	}{
		{"kiro", "f2946a26-3735-4b08-8d05-c928010302d5", "kiro-cli chat --resume-id f2946a26-3735-4b08-8d05-c928010302d5"},
		{"kimchi", "reg-kimchi-001", "kimchi --session reg-kimchi-001"},
		{"gjc", "reg-gjc-001", "gjc --resume reg-gjc-001"},
	} {
		_, cmd, err := resumeCommand(model.Session{Harness: tc.harness, ID: tc.id})
		if err != nil {
			t.Errorf("%s: %v", tc.harness, err)
			continue
		}
		if cmd != tc.want {
			t.Errorf("%s = %q, want %q", tc.harness, cmd, tc.want)
		}
	}

	// Continue's flag forks rather than continues, so the command is offered
	// with the caveat beside it. Silence there would promise the wrong thing.
	if _, cmd, err := resumeCommand(model.Session{Harness: "continue", ID: "abc123"}); err != nil {
		t.Errorf("continue: %v", err)
	} else if cmd != "cn --fork abc123" {
		t.Errorf("continue = %q", cmd)
	}
	if note := resumeCaveats["continue"]; !strings.Contains(note, "fork") {
		t.Errorf("continue's caveat does not say it forks: %q", note)
	}

	_, _, err := resumeCommand(model.Session{Harness: "kiro", ID: "sess_00000000-0000-4000-8000-000000000000"})
	if err == nil {
		t.Fatal("an IDE session got a CLI command that would not find it")
	}
	if !strings.Contains(err.Error(), "IDE") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}
