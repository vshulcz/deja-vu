package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// sessionMentions searches each message on its own; the rule it keeps is a
// search of the title and messages, lowered and joined by spaces. Enough
// messages to be split across workers, and files that only a join can match.
func TestSessionMentionsMatchesTheJoinedText(t *testing.T) {
	joined := func(s model.Session, files []string) []bool {
		var text strings.Builder
		text.WriteString(strings.ToLower(s.Title))
		for _, m := range s.Messages {
			text.WriteString(" ")
			text.WriteString(strings.ToLower(m.Text))
		}
		low := text.String()
		out := make([]bool, len(files))
		for i, f := range files {
			out[i] = strings.Contains(low, f)
		}
		return out
	}
	var msgs []model.Message
	for i := range 700 {
		msgs = append(msgs, model.Message{Role: "user", Text: fmt.Sprintf("Step %d touched Store_%d.GO and KELVINK", i, i%50)})
	}
	msgs = append(msgs, model.Message{Text: "ends with release"}, model.Message{Text: "notes.md starts here"})
	sessions := []model.Session{
		{Title: "Auth.go refresh", Messages: msgs},
		{Title: "", Messages: nil},
		{Title: "only the title names jwks.go"},
		{Title: "x", Messages: msgs[:3]},
	}
	fileSets := [][]string{
		{"auth.go", "store_7.go", "store_49.go", "store_50.go", "jwks.go"},
		{"kelvinK", "kelvink", "step 699"},
		{"release notes.md"}, // only the join between two messages holds it
		{"release notes.md", "auth.go", "missing.go"},
		{""},
	}
	for si, s := range sessions {
		for _, files := range fileSets {
			got, want := sessionMentions(s, files), joined(s, files)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("session %d, files %q: got %v, the joined text says %v", si, files, got, want)
			}
		}
	}
}
