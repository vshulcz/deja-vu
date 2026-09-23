package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// The command as it was run carries the way one machine reached the directory,
// which is the longest part of the line and the only part the reader cannot
// use. A list of six of them was mostly the same home directory six times.
func TestOrientShowsTheCommandWithoutTheWayItGotThere(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"$ cd /Users/x/code/svc; go test ./...", "go test ./..."},
		{"cd /Users/x/code/svc && make test", "make test"},
		{"cd a; cd b; go vet ./...", "go vet ./..."},
		{"go test ./...", "go test ./..."},
		{"cdk deploy --all", "cdk deploy --all"}, // not a cd
		{"cd /Users/x/code/svc", "cd /Users/x/code/svc"},
	} {
		if got := orientCommand(tc.in); got != tc.want {
			t.Errorf("orientCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := "go test ./... -run 'TestSomethingWithAVeryLongNameIndeed|TestAnotherOne' -count=1 -v"
	if got := orientCommand(long); len([]rune(got)) > orientCommandMax+1 {
		t.Errorf("a long command was not cut: %q", got)
	}
}

// A path under the directory the question came from reads as the editor names
// it. One outside it keeps its full name, because a bare "MEMORY.md" the
// reader cannot find is worse than a long path.
func TestOrientNamesPathsFromWhereTheQuestionWasAsked(t *testing.T) {
	if got := orientPath("/w/svc/internal/store/store.go", "/w/svc"); got != "internal/store/store.go" {
		t.Errorf("path under the cwd = %q", got)
	}
	if got := orientPath("/other/place/notes.md", "/w/svc"); got != "/other/place/notes.md" {
		t.Errorf("path outside the cwd was rewritten to %q", got)
	}
	if got := orientPath("/w/svc/main.go", ""); got != "/w/svc/main.go" {
		t.Errorf("with no cwd the path changed: %q", got)
	}
}

// orient takes no subject, so an empty store has to say the store is empty
// rather than answer with nothing — an agent told nothing exists invents
// something, which is the failure every empty answer here is written against.
func TestOrientOnAStoreWithNothingInItSaysSo(t *testing.T) {
	hermeticEnv(t)
	out, err := callMCPTool(t.TempDir(), "orient", json.RawMessage(`{"mode":"orient"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No past session") {
		t.Errorf("orient on an empty store answered %q", out)
	}
}

// The mode is declared, reachable through the one tool, and needs no payload —
// q is what every other mode reads, and orient asking for one would be asking
// the agent a question it called orient to avoid.
func TestOrientIsReachableWithoutAnArgument(t *testing.T) {
	if _, ok := dispatcherModes["orient"]; !ok {
		t.Fatal("orient is not one of the modes the tool dispatches")
	}
	raw := json.RawMessage(`{"mode":"orient","q":"ignored"}`)
	if got := string(spreadQ("orient", raw)); got != string(raw) {
		t.Errorf("q was spread into orient's arguments: %s", got)
	}
	schema, _ := dejaTool()["inputSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	mode, _ := props["mode"].(map[string]any)
	modes, _ := mode["enum"].([]string)
	found := false
	for _, m := range modes {
		found = found || m == "orient"
	}
	if !found {
		t.Errorf("the declared modes are %v, without orient", modes)
	}
}

// The map arrives unasked in the session-start digest, so the bar it applies
// has to be outright rather than by ranking: a project with nothing recurring
// gets nothing, because an empty map still costs every prompt of the session.
func TestTheDigestMapSaysNothingWhenThereIsNoPractice(t *testing.T) {
	hermeticEnv(t)
	if got := orientDigestBlock(t.TempDir(), t.TempDir(), []string{"nowhere"}, policy.ActivationAuto); got != "" {
		t.Errorf("an empty store produced a map block:\n%s", got)
	}
}
