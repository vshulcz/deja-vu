package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A full build ends a pending run on any turn that is not a command or its
// output, but the update walked only those two roles, so nothing ended it: a
// clean `make build`, a prompt, then a Read of a log holding a test failure
// was on file as make build ending in that failure after the first update
// (#4309).
func TestACommandFailureUpdateEndsARunWhereAFullBuildDoes(t *testing.T) {
	c, tmp := newCFStore(t)
	read := func(i int) string {
		sid, id := c.sid(i), fmt.Sprintf("read_%d", i)
		ts := fmt.Sprintf("2026-09-%02dT10:30:00Z", 1+i%28)
		a := map[string]any{"type": "assistant", "sessionId": sid, "timestamp": ts, "cwd": "/tmp/app", "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Read", "input": map[string]any{"file_path": "/tmp/app/log.txt"}}}}}
		r := map[string]any{"type": "user", "sessionId": sid, "timestamp": ts, "cwd": "/tmp/app", "message": map[string]any{
			"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "--- FAIL: TestStoreRejectsStaleWrites (0.01s)\nFAIL"}}}}
		ab := mustJSON(a)
		rb := mustJSON(r)
		return string(ab) + "\n" + string(rb) + "\n"
	}
	write := func(i int, body string) {
		if err := os.MkdirAll(filepath.Dir(c.path("app", i)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path("app", i), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, i := range []int{0, 1} {
		write(i, c.prompt("app", i, "build it")+c.turn("app", i, 0, "make build", "ok", false)+
			c.prompt("app", i, "now read the log")+read(i))
	}
	for _, i := range []int{2, 3} {
		write(i, c.prompt("app", i, "run the tests")+
			c.turn("app", i, 0, "go test ./...", "--- FAIL: TestStoreRejectsStaleWrites (0.01s)\nFAIL", true))
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	full := ReadCommandFails(dir)
	if len(full) != 1 || full[0].Head != "go test ./..." {
		t.Fatalf("the full build's table is not the one go test row: %+v", full)
	}
	c.appendTurn("app", 2, 5, "go test ./...", "--- FAIL: TestStoreRejectsStaleWrites (0.01s)\nFAIL")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := ReadCommandFails(dir)
	fresh := filepath.Join(t.TempDir(), "index.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if want := ReadCommandFails(fresh); !reflect.DeepEqual(got, want) {
		t.Errorf("the update left\n%+v\na full build of the same transcripts gives\n%+v", got, want)
	}
}
