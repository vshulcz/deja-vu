package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session the agent opened itself, or one whose prompts are all harness
// plumbing, printed a blank line in `deja last` and on the first screen — three
// of four rows carrying no information at all (#692).
func TestSessionTitleFallsBackToTheAssistant(t *testing.T) {
	at := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, min int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(min) * time.Minute)}
	}
	cases := []struct {
		name string
		msgs []model.Message
		want string
	}{
		{"user turn wins", []model.Message{
			msg("assistant", "capped the pool at 200", 0),
			msg("user", "the pool starves under load", 1),
		}, "the pool starves under load"},
		{"no user turn at all", []model.Message{
			msg("assistant", "only assistant speech here", 0),
		}, "only assistant speech here"},
		{"user turns are all plumbing", []model.Message{
			msg("user", "<local-command-stdout>ok</local-command-stdout>", 0),
			msg("assistant", "done", 1),
		}, "done"},
		{"assistant plumbing is skipped too", []model.Message{
			msg("assistant", "<command-name>/clear</command-name>", 0),
			msg("assistant", "cleared it", 1),
		}, "cleared it"},
		// Tool output is not speech: naming a session after a stack trace is
		// how the titles in #636 happened.
		{"nothing worth naming", []model.Message{
			msg("tool-output", "panic: runtime error", 0),
			msg("files", "/repo/pool.go", 1),
		}, ""},
	}
	for _, c := range cases {
		if got := sessionTitle(model.Session{Messages: c.msgs}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
