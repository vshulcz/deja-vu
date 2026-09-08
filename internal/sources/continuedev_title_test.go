package sources

import (
	"testing"
)

// Continue's placeholders — "New Session" (NEW_SESSION_TITLE) and "Untitled
// Session" (the CLI's DEFAULT_SESSION_TITLE) — and a title that is only the
// first user line are no titles: left empty, the index derives one and looks
// past a greeting (#3274).
func TestContinuePlaceholderTitleIsLeftToTheIndex(t *testing.T) {
	for _, tc := range []struct {
		title, first, want string
	}{
		{"New Session", "fix the flaky retry test", ""},
		{"Untitled Session", "fix the flaky retry test", ""},
		{"hi", "hi", ""},
		{"Explaining how ContinueRoot picks its directory", "where does ContinueRoot look", "Explaining how ContinueRoot picks its directory"},
	} {
		root := t.TempDir()
		sid := "8f1c2a3e-0000-4000-8000-000000000000"
		path := writeContinueStore(t, root, map[string]any{
			"sessionId": sid, "title": tc.title, "workspaceDirectory": "/w/api",
			"history": []map[string]any{
				{"message": map[string]any{"role": "user", "content": tc.first}},
				{"message": map[string]any{"role": "assistant", "content": "on it"}},
			},
		}, nil)
		ss, err := ParseContinueFile(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%q: parse: %v, %d sessions", tc.title, err, len(ss))
		}
		if ss[0].Title != tc.want {
			t.Errorf("title %q with first line %q → %q, want %q", tc.title, tc.first, ss[0].Title, tc.want)
		}
	}
}
