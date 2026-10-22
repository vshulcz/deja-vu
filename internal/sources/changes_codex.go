package sources

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// codexChanges reads a Codex rollout's edits from the patches it applied.
// Codex edits files through apply_patch, a tool call whose input is the patch
// text; older builds passed the same text as an argument of a shell call, so
// the patch is taken from whichever string field of the call carries it.
func codexChanges(file string) []Change {
	var out []Change
	cwd, metaSeen := "", false
	failed := map[string]bool{}
	_ = scanJSONLBytes(file, 0, func(line []byte) {
		var v struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &v) != nil {
			return
		}
		switch v.Type {
		case "session_meta", "turn_context":
			// A fork copies the history it branched from, its parent's
			// session_meta included; the file's own is the first (#3933).
			if v.Type == "session_meta" {
				if metaSeen {
					return
				}
				metaSeen = true
			}
			var p struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(v.Payload, &p) == nil && p.Cwd != "" {
				cwd = p.Cwd
			}
		case "response_item":
			if bytes.Contains(v.Payload, []byte(`_call_output"`)) {
				var o struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Output any    `json:"output"`
				}
				if json.Unmarshal(v.Payload, &o) == nil && o.CallID != "" && codexCallFailed(o.Output) {
					failed[o.CallID] = true
				}
				return
			}
			if !bytes.Contains(v.Payload, []byte("Begin Patch")) {
				return
			}
			var p map[string]any
			if json.Unmarshal(v.Payload, &p) != nil {
				return
			}
			switch p["type"] {
			case "custom_tool_call", "function_call", "local_shell_call":
			default:
				return
			}
			call, _ := p["call_id"].(string)
			seen := map[string]bool{}
			for _, patch := range patchTexts(p, 0) {
				if seen[patch] {
					continue
				}
				seen[patch] = true
				for _, c := range parseApplyPatch(patch) {
					c.Path = relTo(cwd, c.Path)
					c.call = call
					out = append(out, c)
				}
			}
		}
	})
	return withoutFailed(out, failed)
}

// codexCallFailed reads a tool call's output for a failure: a nonzero exit
// code, in the text header a custom tool's output starts with or in a shell
// call's JSON metadata, or apply_patch saying it could not apply. An output
// it cannot read is not taken as a failure.
func codexCallFailed(out any) bool {
	text, _ := out.(string)
	if text == "" {
		if b, err := json.Marshal(out); err == nil {
			text = string(b)
		}
	}
	// The header opens the output; the same words further down are the
	// command's own output, a patch line among them.
	if rest, ok := strings.CutPrefix(strings.TrimSpace(text), "Exit code: "); ok {
		if j := strings.IndexAny(rest, "\n\r"); j >= 0 {
			rest = rest[:j]
		}
		return strings.TrimSpace(rest) != "0"
	}
	var meta struct {
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(text), &meta) == nil && meta.Metadata.ExitCode != nil {
		return *meta.Metadata.ExitCode != 0
	}
	return strings.Contains(text, "apply_patch verification failed") || strings.Contains(text, "Failed to apply patch")
}

// patchTexts finds the apply_patch texts in a tool call: a string field that
// holds one, or a JSON-encoded arguments string whose fields do.
func patchTexts(v any, depth int) []string {
	if depth > 4 {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case string:
		// An arguments string is JSON first: the patch inside it is escaped.
		if s := strings.TrimSpace(t); strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
			var inner any
			if json.Unmarshal([]byte(s), &inner) == nil {
				return patchTexts(inner, depth+1)
			}
		}
		for {
			i := strings.Index(t, "*** Begin Patch")
			if i < 0 {
				break
			}
			body := t[i+len("*** Begin Patch"):]
			j := strings.Index(body, "*** Begin Patch")
			if k := strings.Index(body, "*** End Patch"); k >= 0 && (j < 0 || k < j) {
				j = k
			}
			if j < 0 {
				out = append(out, body)
				break
			}
			out = append(out, body[:j])
			t = body[j:]
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, patchTexts(t[k], depth+1)...)
		}
	case []any:
		for _, x := range t {
			out = append(out, patchTexts(x, depth+1)...)
		}
	}
	return out
}

// parseApplyPatch reads Codex's patch format: a file added whole, or a file
// updated by hunks of context, removed and added lines. Each hunk is one
// change from the lines it replaced to the lines it left. A deleted file is
// not a change to read back.
func parseApplyPatch(patch string) []Change {
	var out []Change
	var path string
	var adding, updating bool
	var add, old, fresh []string
	hunk := false
	flushHunk := func() {
		if updating && hunk && (len(old) > 0 || len(fresh) > 0) {
			out = append(out, Change{Path: path, Old: strings.Join(old, "\n"), New: strings.Join(fresh, "\n")})
		}
		old, fresh, hunk = nil, nil, false
	}
	flushFile := func() {
		flushHunk()
		if adding {
			out = append(out, Change{Path: path, New: strings.Join(add, "\n"), Created: true})
		}
		path, adding, updating, add = "", false, false, nil
	}
	for l := range strings.SplitSeq(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "*** Add File: "):
			flushFile()
			path, adding = strings.TrimSpace(strings.TrimPrefix(l, "*** Add File: ")), true
		case strings.HasPrefix(l, "*** Update File: "):
			flushFile()
			path, updating = strings.TrimSpace(strings.TrimPrefix(l, "*** Update File: ")), true
		case strings.HasPrefix(l, "*** Delete File: "):
			flushFile()
		case strings.HasPrefix(l, "*** Move to: ") && updating:
			path = strings.TrimSpace(strings.TrimPrefix(l, "*** Move to: "))
		case strings.HasPrefix(l, "*** "):
			// *** Begin Patch, *** End of File and the like carry no text.
		case adding:
			if strings.HasPrefix(l, "+") {
				add = append(add, l[1:])
			}
		case updating && strings.HasPrefix(l, "@@"):
			flushHunk()
			hunk = true
		case updating:
			hunk = true
			switch {
			case strings.HasPrefix(l, "-"):
				old = append(old, l[1:])
			case strings.HasPrefix(l, "+"):
				fresh = append(fresh, l[1:])
			case strings.HasPrefix(l, " "):
				old, fresh = append(old, l[1:]), append(fresh, l[1:])
			case l == "":
				old, fresh = append(old, ""), append(fresh, "")
			}
		}
	}
	flushFile()
	for i := range out {
		out[i].Old = strings.TrimRight(out[i].Old, "\n")
		out[i].New = strings.TrimRight(out[i].New, "\n")
	}
	return out
}
