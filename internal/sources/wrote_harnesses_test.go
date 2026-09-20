package sources

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The written side goes wherever the replaced side already is, and it has to
// arrive under the key each harness actually uses: Copilot calls it new_str,
// and reading new_string there records nothing while looking like it works.
func TestEveryDialectReadsItsOwnWrittenKey(t *testing.T) {
	const written = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	want, ok := HashWrittenLine(written)
	if !ok {
		t.Fatal("the line is not evidence")
	}
	for _, c := range []struct {
		name    string
		dialect toolDialect
		call    map[string]any
	}{
		{"claude", claudeDialect, map[string]any{
			"type": "tool_use", "name": "Edit",
			"input": map[string]any{"file_path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"cursor", cursorDialect, map[string]any{
			"type": "tool_use", "name": "StrReplace",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"copilot", copilotDialect, map[string]any{
			"type": "tool_use", "name": "edit",
			"input": map[string]any{"path": "/w/pool.go", "old_str": "x", "new_str": written},
		}},
		{"qwen", qwenDialect, map[string]any{
			"type": "tool_use", "name": "replace",
			"input": map[string]any{"file_path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"kimi", kimiDialect, map[string]any{
			"type": "tool_use", "name": "Edit",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"codewhale", codeWhaleDialect, map[string]any{
			"type": "tool_use", "name": "str_replace",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
	} {
		got := wroteRecordsIn([]any{c.call}, c.dialect)
		if len(got) == 0 {
			t.Errorf("%s: the call wrote a line and nothing was recorded", c.name)
			continue
		}
		if _, has := WroteRecordHas(got[0], want); !has {
			t.Errorf("%s: the record does not hold the written line: %q", c.name, got[0])
		}
		if strings.Contains(got[0], "default_pool_size") {
			t.Errorf("%s: the record carries the text rather than a hash", c.name)
		}
	}
}

// Codex has no Edit tool: every change it makes is an apply_patch, and the
// payload carries both sides. On the store this was read against there was
// exactly one such call, which is why the reader is pinned by a fixture rather
// than by that store.
func TestCodexRecordsBothSidesOfAPatch(t *testing.T) {
	const removed = "cfg.MaxConns = 10 // the number from before this change"
	const added = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	payload := map[string]any{
		"name": "apply_patch",
		"input": "*** Begin Patch\n*** Update File: internal/pool/pool.go\n@@\n-" + removed +
			"\n+" + added + "\n*** End Patch",
	}
	var s model.Session
	codexPatch(&s, payload, "/w", time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC))

	var gotEdit, gotWrote string
	for _, m := range s.Messages {
		switch m.Role {
		case RoleEdit:
			gotEdit = m.Text
		case RoleWrote:
			gotWrote = m.Text
		}
	}
	if !strings.Contains(gotEdit, removed) {
		t.Errorf("the replaced span is not what the patch removed: %q", gotEdit)
	}
	h, _ := HashWrittenLine(added)
	path, has := WroteRecordHas(gotWrote, h)
	if !has {
		t.Errorf("the written record does not hold the added line: %q", gotWrote)
	}
	// Paths in a Codex patch are relative to the session's cwd, and blame
	// compares the tail of the path it is given. The record keeps the host's
	// separator, and recordNamesFile folds them before comparing, so this
	// folds them too rather than pinning the reader to one platform.
	if filepath.ToSlash(path) != "/w/internal/pool/pool.go" {
		t.Errorf("the record is filed under %q", path)
	}
	if old, _ := HashWrittenLine(removed); func() bool { _, ok := WroteRecordHas(gotWrote, old); return ok }() {
		t.Error("a removed line was hashed as written")
	}
}
