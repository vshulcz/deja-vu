package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The question the tool hook asks of a promoted session: did it run this
// command. Compared the way CommandHistory compares (#2516).
func TestSessionRanCommand(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "run the orders migration"},
		{Role: roleCommand, Text: "$ Make Migrate-Orders"},
		{Role: roleCommand, Text: "git status --short"},
		// An assistant sentence that names the command is not a run of it.
		{Role: "assistant", Text: "make something-else"},
	}}
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{cmd: "make migrate-orders", want: true},
		{cmd: "  make migrate-orders  ", want: true},
		{cmd: "git status --short", want: true},
		{cmd: "make migrate", want: false},
		{cmd: "make something-else", want: false},
		{cmd: "", want: false},
		// A multi-line command is never the stored single-line one, for the
		// reason CommandHistory gives: the harmless first line would carry the
		// rest.
		{cmd: "make migrate-orders\nrm -rf /", want: false},
	} {
		if got := SessionRanCommand(s, tc.cmd); got != tc.want {
			t.Errorf("SessionRanCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
	if SessionRanCommand(model.Session{Updated: time.Now()}, "make migrate-orders") {
		t.Error("a session with no records ran nothing")
	}
}
