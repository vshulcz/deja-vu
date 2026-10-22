package sources

import (
	"path/filepath"
	"testing"
)

// CodeWhale keeps write_file {path, content} registered for sessions saved
// before 0.9.6 (tools/file.rs WriteFileTool), and fim_edit {path,
// prefix_anchor, suffix_anchor} returns the text it generated between them as
// FimEditResult in the result (tools/fim.rs). Both had a files record and
// nothing blame could match (#595).
func TestCodeWhaleWriteFileAndFimEditRecordTheWrittenSide(t *testing.T) {
	const generated = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {\n\t\tif err := fn(); err == nil {"
	fim := vocabJSON(map[string]any{"success": true, "path": "/tmp/proj/fim.go", "generated_text": generated,
		"prefix_end": 42, "suffix_start": 97, "message": "FIM edit applied to `/tmp/proj/fim.go`."})

	ss := cwSaved(t, "Successfully wrote 61 bytes to /tmp/proj/jitter.go", false,
		map[string]any{"name": "write_file", "input": map[string]any{"path": "/tmp/proj/jitter.go", "content": jitter}})
	vocabCheck(t, ss, []string{vocabFiles("/tmp/proj/jitter.go"), vocabWrote("/tmp/proj/jitter.go", jitter)}, nil)

	ss = cwSaved(t, fim, false,
		map[string]any{"name": "fim_edit", "input": map[string]any{"path": "/tmp/proj/fim.go", "prefix_anchor": "func retry(fn func() error) error {", "suffix_anchor": "\treturn errGiveUp"}})
	vocabCheck(t, ss, []string{vocabFiles("/tmp/proj/fim.go"), vocabWrote("/tmp/proj/fim.go", generated)}, nil)

	// A fim_edit that came back as an error wrote nothing.
	ss = cwSaved(t, fim, true,
		map[string]any{"name": "fim_edit", "input": map[string]any{"path": "/tmp/proj/fim.go", "prefix_anchor": "a", "suffix_anchor": "b"}})
	vocabCheck(t, ss, nil, []string{vocabWrote("/tmp/proj/fim.go", generated)})

	// A provider that numbers calls per turn reuses an id: each result is
	// filed under the call it answered, not the last call with that id.
	second := "\t\treturn fmt.Errorf(\"retry: gave up after %d attempts\", maxAttempts)"
	call := func(path string) any {
		return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "call_0", "name": "fim_edit",
			"input": map[string]any{"path": path, "prefix_anchor": "a", "suffix_anchor": "b"}}}}
	}
	res := func(text string) any {
		return map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_0",
			"content": vocabJSON(map[string]any{"success": true, "generated_text": text})}}}
	}
	doc := map[string]any{
		"schema_version": 1,
		"metadata":       map[string]any{"id": "cw-2", "created_at": "2026-10-01T10:00:00Z", "workspace": "/tmp/proj"},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			call("/tmp/proj/first.go"), res(generated), call("/tmp/proj/second.go"), res(second),
		},
	}
	ss = vocabParse(t, ParseCodeWhaleFile, vocabWrite(t, filepath.Join(t.TempDir(), "cw-2.json"), vocabJSON(doc)))
	vocabCheck(t, ss, []string{vocabWrote("/tmp/proj/first.go", generated), vocabWrote("/tmp/proj/second.go", second)},
		[]string{vocabWrote("/tmp/proj/second.go", generated)})
}
