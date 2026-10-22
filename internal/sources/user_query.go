package sources

import "strings"

// UserQuery returns what a person typed when a harness recorded the turn as
// its own context blocks followed by the prompt in a closing
// <user_query>…</user_query>. WorkBuddy's desktop app writes every prompt that
// way, behind two <system-reminder> blocks, so the whole record opened like a
// reminder and was dropped as plumbing (#4734).
//
// Only a text that opens with a tag and ends on the closing tag counts: a
// person's sentence that mentions <user_query> is left alone. With more than
// one pair the last is the prompt, the earlier ones being quoted context.
func UserQuery(text string) (string, bool) {
	const open, closeTag = "<user_query>", "</user_query>"
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "<") || !strings.HasSuffix(t, closeTag) {
		return "", false
	}
	body := t[:len(t)-len(closeTag)]
	start := strings.LastIndex(body, open)
	if start < 0 {
		return "", false
	}
	q := strings.TrimSpace(body[start+len(open):])
	if q == "" {
		return "", false
	}
	return q, true
}
