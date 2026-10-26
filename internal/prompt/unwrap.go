package prompt

import "strings"

// Unwrap takes off the double quotes a host put around a whole prompt. opencode
// 1.18 builds the message of `opencode run "<question>"` by quoting every
// argument that holds a space and escaping the quotes inside it, so the hook
// got `"how did we …?"` where the person typed the question. Inside one pair of
// quotes the prompt read as a single clause: the question no longer led the
// terms, an instruction in front of it spent the budget, and the recall block
// came back empty where the same words on stdin found the session.
//
// Only a prompt that is one quoted span is unwrapped: an unescaped quote inside
// means two spans, `"foo" and "bar"`, and that is the person's own quoting.
func Unwrap(p string) string {
	t := strings.TrimSpace(p)
	if len(t) < 2 || t[0] != '"' || t[len(t)-1] != '"' {
		return p
	}
	inner := t[1 : len(t)-1]
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '\\':
			if i == len(inner)-1 {
				// The last quote is escaped, so it closes nothing.
				return p
			}
			i++
		case '"':
			return p
		}
	}
	inner = strings.ReplaceAll(inner, `\"`, `"`)
	if strings.TrimSpace(inner) == "" {
		return p
	}
	return inner
}
