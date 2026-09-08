package sources

import "testing"

// A Windows workspace path has no "/" to fold, so the key was the whole path
// and the session sat in a project of its own (#3217).
func TestPathToProjectKeyFoldsAWindowsPath(t *testing.T) {
	if got, want := pathToProjectKey(`C:\Users\qa\work\zorbex`), pathToProjectKey("C:/Users/qa/work/zorbex"); got != want {
		t.Fatalf("backslash form = %q, slash form = %q", got, want)
	}
	if got := claudeProjectName(pathToProjectKey(`C:\Users\qa\work\zorbex`)); got != "work/zorbex" {
		t.Fatalf("project = %q, want work/zorbex", got)
	}
}
