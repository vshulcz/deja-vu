package sources

import "testing"

func TestRemoteSessionIDTakesTheIdOffRemoteControlURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://claude.ai/code/session_01AbC":     "session_01AbC",
		"claude.ai/code/session_01AbC/":            "session_01AbC",
		"https://claude.ai/code/session_01AbC?x=1": "session_01AbC",
		" session_01AbC ":                          "session_01AbC",
		"session_":                                 "",
		"https://example.com/code/session_01AbC":   "",
		"4be0c2a1-0000-4000-8000-000000000001":     "",
		"":                                         "",
	} {
		if got := RemoteSessionID(in); got != want {
			t.Errorf("RemoteSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}
