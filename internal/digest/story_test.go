package digest

import (
	"reflect"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A reply is the last thing the agent said before the next real ask; a nudge
// does not cut it, and a probe file under /tmp is not an edit.
func TestStoryOfKeepsAsksRepliesRunsAndEdits(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the scheduler times out under load"},
		{Role: "assistant", Text: "Let me look at the pool first."},
		{Role: sources.RoleCommand, Text: "$ go test ./...  → exit 1"},
		{Role: sources.RoleToolOutput, Text: "--- FAIL: TestLoad"},
		{Role: sources.RoleEdit, Text: "/work/app/pool.go\nold span"},
		{Role: sources.RoleEdit, Text: "/tmp/probe/x.go\nold span"},
		{Role: "user", Text: "go on"},
		{Role: "assistant", Text: "The proxy closes idle connections at five minutes, so the pool must retire them sooner."},
		{Role: "user", Text: "now add a gauge for it on the dashboard"},
	}}
	st := StoryOf(s)
	want := []Turn{
		{User: true, Text: "the scheduler times out under load"},
		{Text: "The proxy closes idle connections at five minutes, so the pool must retire them sooner."},
		{User: true, Text: "now add a gauge for it on the dashboard"},
	}
	if !reflect.DeepEqual(st.Turns, want) {
		t.Errorf("turns = %+v", st.Turns)
	}
	if len(st.Runs) != 1 || st.Runs[0].Output != "--- FAIL: TestLoad" {
		t.Errorf("runs = %+v", st.Runs)
	}
	if !reflect.DeepEqual(st.Edited, []string{"/work/app/pool.go"}) {
		t.Errorf("edited = %q", st.Edited)
	}
}
