package sources

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Change is one edit a session made to a file, both sides as text: what was
// there and what the session left. The index keeps the written side only as
// line hashes (RoleWrote), so the code a session came to is read back from its
// transcript when a reader asks for that session, never stored a second time.
type Change struct {
	Path string
	Old  string
	New  string
	// Created marks a whole-file write: a file the session brought into being
	// rather than one it edited.
	Created bool
	// call is the tool call that made the change, to drop it when the
	// harness reports that call failed.
	call string
}

// withoutFailed drops the changes whose tool call came back an error: an
// edit refused, denied or aimed at text that was not there changed nothing.
func withoutFailed(cs []Change, failed map[string]bool) []Change {
	if len(failed) == 0 {
		return cs
	}
	out := cs[:0]
	for _, c := range cs {
		if c.call == "" || !failed[c.call] {
			out = append(out, c)
		}
	}
	return out
}

// Changes reads the edits a session made back from its transcript, in the
// order they were made, with paths relative to the directory it ran in. Nil
// for a harness whose transcript it cannot read edits from, or when the file
// is gone.
func Changes(s model.Session) []Change {
	if s.Path == "" {
		return nil
	}
	switch s.Harness {
	case "claude":
		return claudeChanges(s.Path, s.ID)
	case "codex":
		return codexChanges(s.Path)
	}
	return genericChanges(s)
}

func claudeChanges(file, id string) []Change {
	var out []Change
	cwd := ""
	failed := map[string]bool{}
	_ = scanJSONLBytes(file, 0, func(line []byte) {
		var v struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &v) != nil || (v.Type != "assistant" && v.Type != "user") {
			return
		}
		if id != "" && v.SessionID != "" && v.SessionID != id {
			return
		}
		if v.Type == "user" {
			// The result of each call comes back on a user line.
			if !bytes.Contains(line, []byte(`"is_error"`)) {
				return
			}
			var results []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				IsError   bool   `json:"is_error"`
			}
			if json.Unmarshal(trimJSONSpace(v.Message.Content), &results) == nil {
				for _, r := range results {
					if r.Type == "tool_result" && r.IsError && r.ToolUseID != "" {
						failed[r.ToolUseID] = true
					}
				}
			}
			return
		}
		if v.Cwd != "" {
			cwd = v.Cwd
		}
		var parts []struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input struct {
				FilePath  string `json:"file_path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
				Content   string `json:"content"`
				Edits     []struct {
					OldString string `json:"old_string"`
					NewString string `json:"new_string"`
				} `json:"edits"`
			} `json:"input"`
		}
		if json.Unmarshal(trimJSONSpace(v.Message.Content), &parts) != nil {
			return
		}
		for _, p := range parts {
			if p.Type != "tool_use" || p.Input.FilePath == "" || strings.ContainsAny(p.Input.FilePath, "\n\r") {
				continue
			}
			rel := relTo(cwd, p.Input.FilePath)
			switch p.Name {
			case "Write":
				out = append(out, Change{Path: rel, New: p.Input.Content, Created: true, call: p.ID})
			case "Edit":
				out = append(out, Change{Path: rel, Old: p.Input.OldString, New: p.Input.NewString, call: p.ID})
			case "MultiEdit":
				for _, e := range p.Input.Edits {
					out = append(out, Change{Path: rel, Old: e.OldString, New: e.NewString, call: p.ID})
				}
			}
		}
	})
	return withoutFailed(out, failed)
}

// relTo is p relative to the directory the session ran in, when it is under
// it; otherwise p as written. Paths are compared in slash form, so a
// transcript reads the same whichever OS wrote it and whichever reads it.
func relTo(cwd, p string) string {
	if cwd == "" {
		return p
	}
	base := path.Clean(strings.ReplaceAll(cwd, `\`, "/"))
	full := strings.ReplaceAll(p, `\`, "/")
	if !absPath(full) {
		full = base + "/" + full
	}
	full = path.Clean(full)
	if full == base {
		return p
	}
	if r, ok := strings.CutPrefix(full, strings.TrimSuffix(base, "/")+"/"); ok {
		return r
	}
	return p
}

// absPath reports whether p, in slash form, is absolute on any OS: rooted, or
// with a drive letter.
func absPath(p string) bool {
	return strings.HasPrefix(p, "/") || len(p) >= 3 && p[1] == ':' && p[2] == '/'
}

// FinalChanges is what a session's edits came to: an edit that rewrites text
// an earlier edit put in is folded into that earlier one, so each change reads
// from the code as it was to the code as the session left it, and files the
// agent wrote for itself — a reproduction script, a debug print, its own
// notes — are left out. Changes to tests come after the change they test.
func FinalChanges(cs []Change) []Change {
	var kept []Change
	for _, c := range cs {
		if scratchFile(c, cs) {
			continue
		}
		folded := false
		for i := len(kept) - 1; i >= 0 && c.Old != ""; i-- {
			if kept[i].Path == c.Path && strings.Contains(kept[i].New, c.Old) {
				kept[i].New = strings.Replace(kept[i].New, c.Old, c.New, 1)
				folded = true
				break
			}
		}
		if !folded {
			kept = append(kept, c)
		}
	}
	var code, tests []Change
	for _, c := range kept {
		if testPath(c.Path) {
			tests = append(tests, c)
		} else {
			code = append(code, c)
		}
	}
	return append(code, tests...)
}

// scratchFile is a file the session created to try something out rather than
// a change to the project: outside the directory it ran in, under a temporary
// directory, or a new file whose name says it is a reproduction, a debug aid
// or the agent's notes.
func scratchFile(c Change, all []Change) bool {
	p := c.Path
	if absPath(strings.ReplaceAll(p, `\`, "/")) || strings.HasPrefix(p, "..") || strings.Contains(p, "/tmp/") || strings.HasPrefix(p, "tmp/") {
		return true
	}
	created := c.Created
	for _, o := range all {
		if o.Path == p && o.Created {
			created = true
		}
	}
	if !created {
		return false
	}
	base := strings.ToLower(path.Base(p))
	for _, pre := range []string{"reproduce", "repro_", "debug", "scratch", "verify_", "check_"} {
		if strings.HasPrefix(base, pre) {
			return true
		}
	}
	if strings.HasSuffix(base, ".md") {
		switch base {
		case "readme.md", "changelog.md", "contributing.md":
			return false
		}
		return !strings.Contains(p, "docs/")
	}
	// A test file the agent made beside the code rather than in the
	// project's tests is a check it ran, not one it kept.
	return strings.HasPrefix(base, "test_") && !testDir(p)
}

func testPath(p string) bool {
	base := path.Base(p)
	return testDir(p) || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func testDir(p string) bool {
	return strings.HasPrefix(p, "tests/") || strings.HasPrefix(p, "test/") || strings.Contains(p, "/tests/") || strings.Contains(p, "/test/") || strings.Contains(p, "__tests__/")
}
