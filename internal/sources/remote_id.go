package sources

import "strings"

// RemoteSessionID returns the session_… id at the end of a Claude Code remote
// control URL (https://claude.ai/code/session_01…), or "" when s is not one.
// A bare id passes through, so a selector can go through the same call.
func RemoteSessionID(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		if !strings.Contains(s[:i], "claude.ai/code") {
			return ""
		}
		s = s[i+1:]
	}
	if !strings.HasPrefix(s, "session_") || len(s) <= len("session_") {
		return ""
	}
	return s
}
