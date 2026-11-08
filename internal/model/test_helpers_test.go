package model

// IsSpokenRole reports whether a message role represents conversational speech
// rather than a tool or file-activity work record.
func IsSpokenRole(role string) bool {
	return !IsWorkRole(role)
}
