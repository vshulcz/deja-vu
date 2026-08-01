package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Taking whichever line sat first in the file disagreed with the import path,
// which orders by time — so one store titled locally and the same store
// imported elsewhere could disagree (#769).
func TestSessionTitleTakesTheEarliestTurn(t *testing.T) {
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, min int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(min) * time.Minute)}
	}
	cases := []struct {
		name string
		msgs []model.Message
		want string
	}{
		{"assistant turns stored out of order", []model.Message{
			msg("assistant", "and then we rolled the migration back", 5),
			msg("assistant", "started the zeppelin migration", 0),
		}, "started the zeppelin migration"},
		{"user turns stored out of order", []model.Message{
			msg("user", "and what about the rollback", 5),
			msg("user", "how do we start the migration", 0),
		}, "how do we start the migration"},
		{"a user turn still beats an earlier assistant one", []model.Message{
			msg("assistant", "capped the pool at 200", 0),
			msg("user", "the pool starves under load", 5),
		}, "the pool starves under load"},
		// No clock at all: file order is the only order there is.
		{"no timestamps", []model.Message{
			{Role: "assistant", Text: "first assistant line with no clock"},
			{Role: "assistant", Text: "second assistant line with no clock"},
		}, "first assistant line with no clock"},
		// A mix: the undated line must not displace a dated one that came
		// first, and must not be treated as time zero either.
		{"dated first, undated after", []model.Message{
			msg("assistant", "dated opening line", 0),
			{Role: "assistant", Text: "undated line that follows"},
		}, "dated opening line"},
		{"undated first, dated after", []model.Message{
			{Role: "assistant", Text: "undated opening line"},
			msg("assistant", "dated line that follows", 5),
		}, "undated opening line"},
		// Plumbing is skipped whatever its position.
		{"earliest is plumbing", []model.Message{
			msg("user", "<local-command-stdout>ok</local-command-stdout>", 0),
			msg("user", "the real question", 5),
		}, "the real question"},
		{"nothing worth naming", []model.Message{
			msg("tool-output", "panic: boom", 0),
		}, ""},
	}
	for _, c := range cases {
		if got := sessionTitle(model.Session{Messages: c.msgs}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
