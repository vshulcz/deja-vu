package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The quotes under a blame hit are the evidence a reader judges it by, so they
// have to be the messages that say something about the file. Taking the first
// two mentions led with whichever line named it earliest.
func TestBlameQuotesTheMessageThatExplainsTheFile(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "also worth a look at auth.go some day"},
			{Role: "assistant", Text: "in internal/auth/auth.go we moved the token check out of auth.go because auth.go ran it twice"},
		},
	}
	hits := Blame([]model.Session{s}, BlameTarget{Base: "auth.go", FullPath: "internal/auth/auth.go"}, BlameOptions{})
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no blame hit, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "moved the token check") {
		t.Errorf("first quote is %q, not the message that explains the file", hits[0].Snippets[0])
	}
}

// A message naming the file by path is more specific evidence than one repeating
// the bare name, however often it repeats it — otherwise a fix could rank on the
// count alone and quote whichever line says "auth.go" the most.
func TestBlamePrefersThePathOverTheRepeatedBareName(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "auth.go auth.go auth.go auth.go, anyway"},
			{Role: "assistant", Text: "the change is in internal/auth/auth.go"},
		},
	}
	hits := Blame([]model.Session{s}, BlameTarget{Base: "auth.go", FullPath: "internal/auth/auth.go"}, BlameOptions{})
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no blame hit, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "internal/auth/auth.go") {
		t.Errorf("first quote is %q, not the one naming the path", hits[0].Snippets[0])
	}
}
