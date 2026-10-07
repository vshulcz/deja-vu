package sources

import "fmt"

// CommandCodeLatestPrompt is the newest turn the person typed in a Command
// Code transcript, and a key that names that turn: Command Code fires no
// per-prompt hook, so its PreToolUse answers the question from here.
func CommandCodeLatestPrompt(path string) (text, key string) {
	if !commandCodeIsTranscript(path) {
		return "", ""
	}
	n := 0
	_ = scanJSONLFromOffset(path, 0, func(m map[string]any) {
		n++
		if typ, _ := m["type"].(string); typ != "message" {
			return
		}
		msg, _ := m["message"].(map[string]any)
		if role, _ := msg["role"].(string); role != "user" {
			return
		}
		if txt, toolOut := textFromContentKind(msg["content"]); txt != "" && !toolOut {
			text, key = txt, fmt.Sprintf("%d:%v", n, m["timestamp"])
		}
	})
	return text, key
}
