package main

// Grok Build sends its hook payload in camelCase throughout, where every other
// harness deja wires sends snake_case — sessionId, workspaceRoot,
// transcriptPath, toolName, toolInput, measured on 1.0.5. Only cwd and prompt
// happen to be spelled the same, which is why grok looked wired: the recall it
// produced was for the right project and the right question, under no session
// at all.
//
// A session with no name of its own shares the empty key in the dedup ledger
// with every other session on the machine, so the second grok session looks
// like it has already been shown everything the first one was, and a compaction
// forgets nothing because there is nothing filed under its name.
//
// The structs below embed this one, so a payload decodes into both spellings at
// once, and each adopts grok's where the common spelling arrived empty.
type grokEnvelope struct {
	SessionID      string `json:"sessionId"`
	WorkspaceRoot  string `json:"workspaceRoot"`
	TranscriptPath string `json:"transcriptPath"`
	ToolName       string `json:"toolName"`
	ToolInput      struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"toolInput"`
}

// adoptGrok returns the value every other harness sends, or grok's when that
// one is absent.
func adoptGrok(common, grok string) string {
	if common != "" {
		return common
	}
	return grok
}

// adoptGrokRoots is the same for the project path, which grok names once where
// cursor sends a list.
func adoptGrokRoots(common []string, grok string) []string {
	if len(common) > 0 || grok == "" {
		return common
	}
	return []string{grok}
}
