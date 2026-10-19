package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeIDLine(sid, text string) string {
	return `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// A tail that changes the file's sessionId is read whole, since a rebuild
// files the whole transcript under the last id (#5379). The rest still append.
func TestClaudeTailThatChangesTheSessionIsNotAppended(t *testing.T) {
	big := strings.Repeat("x", 200<<10) // longer than a backward read's chunk
	meta := `{"type":"user","isMeta":true,"sessionId":"s-z","message":{"role":"user","content":"caveat"}}` + "\n"
	side := func(agent, parent string) string {
		return `{"type":"assistant","isSidechain":true,"agentId":"` + agent + `","sessionId":"` + parent + `","message":{"role":"assistant","content":"done"}}` + "\n"
	}
	noID := `{"type":"user","message":{"role":"user","content":"no id"}}` + "\n"
	cases := []struct {
		name, head, tail string
		want             bool
	}{
		{"same id", claudeIDLine("s-a", "one"), claudeIDLine("s-a", "two"), true},
		{"new id", claudeIDLine("s-a", "one"), claudeIDLine("s-b", "two"), false},
		{"new id after a long line", claudeIDLine("s-a", big), claudeIDLine("s-b", "two"), false},
		{"same id behind a long line", claudeIDLine("s-a", "one") + claudeIDLine("s-a", big), claudeIDLine("s-a", "two"), true},
		{"new id back to the old", claudeIDLine("s-a", "one"), claudeIDLine("s-b", "two") + claudeIDLine("s-a", "three"), true},
		{"a meta record names nothing", claudeIDLine("s-a", "one"), meta, true},
		{"a summary line names nothing", claudeIDLine("s-a", "one"), `{"type":"summary","summary":"x","leafUuid":"u"}` + "\n", true},
		{"nothing filed before", `{"type":"summary","summary":"x"}` + "\n", claudeIDLine("s-b", "two"), true},
		{"no id, then one", noID, claudeIDLine("s-b", "two"), false},
		{"named by its file both times", noID, noID, true},
		{"named by its file, then its own id", noID, claudeIDLine("f", "two"), true},
		{"same sidechain", side("ag1", "p"), side("ag1", "p"), true},
		{"another sidechain", side("ag1", "p"), side("ag2", "p"), false},
		{"new id after a BOM", "\ufeff" + claudeIDLine("s-a", "one"), claudeIDLine("s-b", "two"), false},
		{"a line the parser rejects names nothing", claudeIDLine("s-a", "one") + `{"type":"user","sessionId":"s-b","message":"str"}` + "\n", claudeIDLine("s-b", "two"), false},
		{"new id after a line of many chunks", claudeIDLine("s-a", strings.Repeat("y", 1<<20)), claudeIDLine("s-b", "two"), false},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "f.jsonl")
			if err := os.WriteFile(p, []byte(tc.head+tc.tail), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := claudeSameSession(p, int64(len(tc.head))); got != tc.want {
				t.Errorf("claudeSameSession = %v, want %v", got, tc.want)
			}
			if got := claudeResumes(p, int64(len(tc.head))); got != tc.want {
				t.Errorf("claudeResumes = %v, want %v", got, tc.want)
			}
			// Cherry Studio writes the same format and asks the same question.
			if got := CherryStudioResumes(p, int64(len(tc.head))); got != tc.want {
				t.Errorf("CherryStudioResumes = %v, want %v", got, tc.want)
			}
		})
	}
}
