package sources

import (
	"bytes"
	"encoding/json"
	"path"
	"strconv"
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
	// Reverted marks a later revert/deletion of Path, used by FinalChanges to
	// drop reverted edits for that path.
	Reverted bool
	// TestFailed marks edits whose subsequent test run in the session failed.
	TestFailed bool
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
	case "antigravity":
		return antigravityChanges(s.Path)
	}
	return genericChanges(s)
}

func antigravityChanges(file string) []Change {
	var out []Change
	seen := map[Change]bool{}
	cwd := ""
	_ = scanJSONLBytes(file, 0, func(line []byte) {
		hasEdit := bytes.Contains(line, []byte(`"edit_file"`)) ||
			bytes.Contains(line, []byte(`"write_file"`)) ||
			bytes.Contains(line, []byte(`"write_to_file"`))
		hasCwd := cwd == "" && (bytes.Contains(line, []byte(`"Cwd"`)) || bytes.Contains(line, []byte(`"cwd"`)))
		if !hasEdit && !hasCwd {
			return
		}
		var v struct {
			Source    string `json:"source"`
			Type      string `json:"type"`
			ToolCalls []any  `json:"tool_calls"`
		}
		if json.Unmarshal(line, &v) != nil || v.Source != "MODEL" || v.Type != "PLANNER_RESPONSE" {
			return
		}
		for _, raw := range v.ToolCalls {
			call, _ := raw.(map[string]any)
			args, _ := call["args"].(map[string]any)
			if cwd == "" {
				if c := firstNonEmpty(antigravityArg(args, "Cwd"), antigravityArg(args, "cwd")); c != "" {
					cwd = c
				}
			}
			p := decodeURIPath(strings.TrimPrefix(firstNonEmpty(antigravityArg(args, "TargetFile"), antigravityArg(args, "Path")), "file://"))
			if p == "" || strings.ContainsAny(p, "\n\r") {
				continue
			}
			var ch Change
			switch str(call["name"]) {
			case "edit_file":
				ch = Change{Path: p, Old: antigravityArg(args, "Target"), New: antigravityArg(args, "Replacement")}
			case "write_file":
				ch = Change{Path: p, New: antigravityArg(args, "Content"), Created: true}
			case "write_to_file":
				ch = Change{Path: p, New: antigravityArg(args, "CodeContent"), Created: true}
			default:
				continue
			}
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
			}
		}
	})
	if len(out) == 0 {
		return nil
	}
	if cwd == "" {
		cwd = commonDir(out)
	}
	for i := range out {
		out[i].Path = relTo(cwd, out[i].Path)
	}
	return out
}

type claudeMsgPart struct {
	inline    bool
	typ       string
	id        string
	name      string
	input     json.RawMessage
	toolUseID string
	isErr     bool
	text      string
}

func decodeClaudeMsgParts(raw json.RawMessage) []claudeMsgPart {
	trimmed := trimJSONSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var out []claudeMsgPart
	appendInline := func(s string) {
		for _, blk := range parseInlineBlocks(s) {
			switch blk.typ {
			case "tool_use":
				out = append(out, claudeMsgPart{
					inline: true,
					typ:    "tool_use",
					name:   blk.name,
					input:  blk.input,
				})
			case "tool_result":
				out = append(out, claudeMsgPart{
					inline: true,
					typ:    "tool_result",
					isErr:  blk.isErr,
					text:   blk.text,
				})
			}
		}
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			appendInline(s)
		}
		return out
	}
	var elems []json.RawMessage
	if json.Unmarshal(trimmed, &elems) != nil {
		return nil
	}
	for _, el := range elems {
		et := trimJSONSpace(el)
		if len(et) == 0 {
			continue
		}
		if et[0] == '"' {
			var s string
			if json.Unmarshal(et, &s) == nil {
				appendInline(s)
			}
			continue
		}
		var obj struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			Text      string          `json:"text"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(et, &obj) != nil {
			continue
		}
		switch obj.Type {
		case "tool_use":
			out = append(out, claudeMsgPart{
				typ:   "tool_use",
				id:    obj.ID,
				name:  obj.Name,
				input: obj.Input,
			})
		case "tool_result":
			txt := claudeResultText(obj.Content)
			if txt == "" {
				txt = obj.Text
			}
			out = append(out, claudeMsgPart{
				typ:       "tool_result",
				toolUseID: obj.ToolUseID,
				isErr:     obj.IsError || strings.HasPrefix(strings.TrimSpace(txt), "<tool_use_error>"),
				text:      txt,
			})
		case "", "text":
			txt := obj.Text
			if txt == "" {
				txt = claudeResultText(obj.Content)
			}
			if txt != "" {
				appendInline(txt)
			}
		}
	}
	return out
}

func claudeResultText(raw json.RawMessage) string {
	trimmed := trimJSONSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s
		}
		return ""
	}
	var arr []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &arr) == nil {
		var parts []string
		for _, a := range arr {
			if a.Text != "" {
				parts = append(parts, a.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

type pendingInlineKind uint8

const (
	pendingOther pendingInlineKind = iota
	pendingEdit
	pendingBash
)

type pendingInlineCall struct {
	kind        pendingInlineKind
	callID      string
	revertPaths []string
	isTest      bool
	editEnd     int
}

func claudeChanges(file, id string) []Change {
	var out []Change
	cwd := ""
	failed := map[string]bool{}
	structRevert := map[string][]string{}
	structTest := map[string]int{}
	var inlinePending []pendingInlineCall
	inlineSeq := 0

	applyTestResult := func(endIdx int, failedTest bool) {
		limit := min(endIdx, len(out))
		for i := range limit {
			out[i].TestFailed = failedTest
		}
	}

	_ = scanJSONLBytes(file, 0, func(line []byte) {
		hasFilePath := bytes.Contains(line, []byte("file_path"))
		hasToolUse := bytes.Contains(line, []byte("tool_use"))
		hasCommand := len(out) > 0 && bytes.Contains(line, []byte("command"))
		hasResult := (len(out) > 0 || len(inlinePending) > 0) &&
			(bytes.Contains(line, []byte("tool_result")) || bytes.Contains(line, []byte("is_error")))
		if !hasFilePath && !hasToolUse && !hasCommand && !hasResult && (cwd != "" || !bytes.Contains(line, []byte(`"cwd"`))) {
			return
		}
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
		if v.Cwd != "" {
			cwd = v.Cwd
		}
		parts := decodeClaudeMsgParts(v.Message.Content)
		for _, p := range parts {
			switch p.typ {
			case "tool_use":
				switch p.name {
				case "Write", "Edit", "MultiEdit":
					var in struct {
						FilePath  string `json:"file_path"`
						OldString string `json:"old_string"`
						NewString string `json:"new_string"`
						Content   string `json:"content"`
						Edits     []struct {
							OldString string `json:"old_string"`
							NewString string `json:"new_string"`
						} `json:"edits"`
					}
					if json.Unmarshal(p.input, &in) != nil || in.FilePath == "" || strings.ContainsAny(in.FilePath, "\n\r") {
						if p.inline {
							inlinePending = append(inlinePending, pendingInlineCall{kind: pendingOther})
						}
						continue
					}
					callID := p.id
					if p.inline || callID == "" || callID == inlineToolID {
						inlineSeq++
						callID = "__inline_" + strconv.Itoa(inlineSeq)
					}
					rel := cleanRepoPath(relTo(cwd, in.FilePath))
					before := len(out)
					switch p.name {
					case "Write":
						out = append(out, Change{Path: rel, New: in.Content, Created: true, call: callID})
					case "Edit":
						out = append(out, Change{Path: rel, Old: in.OldString, New: in.NewString, call: callID})
					case "MultiEdit":
						for _, e := range in.Edits {
							out = append(out, Change{Path: rel, Old: e.OldString, New: e.NewString, call: callID})
						}
					}
					if p.inline {
						if len(out) > before {
							inlinePending = append(inlinePending, pendingInlineCall{kind: pendingEdit, callID: callID})
						} else {
							inlinePending = append(inlinePending, pendingInlineCall{kind: pendingOther})
						}
					}
				case "Bash":
					var in struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(p.input, &in) != nil {
						if p.inline {
							inlinePending = append(inlinePending, pendingInlineCall{kind: pendingOther})
						}
						continue
					}
					reverts := parseShellRevert(in.Command)
					isTest := len(out) > 0 && isTestCommand(in.Command)
					if p.inline || p.id == "" || p.id == inlineToolID {
						kind := pendingOther
						if len(reverts) > 0 || isTest {
							kind = pendingBash
						}
						inlinePending = append(inlinePending, pendingInlineCall{
							kind:        kind,
							revertPaths: reverts,
							isTest:      isTest,
							editEnd:     len(out),
						})
					} else {
						if len(reverts) > 0 {
							structRevert[p.id] = reverts
						}
						if isTest {
							structTest[p.id] = len(out)
						}
					}
				default:
					if p.inline {
						inlinePending = append(inlinePending, pendingInlineCall{kind: pendingOther})
					}
				}
			case "tool_result":
				if !p.inline && p.toolUseID != "" && p.toolUseID != inlineToolID {
					if p.isErr {
						failed[p.toolUseID] = true
					}
					if rps, ok := structRevert[p.toolUseID]; ok {
						delete(structRevert, p.toolUseID)
						if !p.isErr {
							for _, rp := range rps {
								out = append(out, Change{Path: cleanRepoPath(relTo(cwd, rp)), Reverted: true})
							}
						}
					}
					if endIdx, ok := structTest[p.toolUseID]; ok {
						delete(structTest, p.toolUseID)
						applyTestResult(endIdx, p.isErr || testOutputFailed(p.text))
					}
					continue
				}
				if len(inlinePending) > 0 {
					pend := inlinePending[0]
					inlinePending = inlinePending[1:]
					switch pend.kind {
					case pendingEdit:
						if p.isErr && pend.callID != "" {
							failed[pend.callID] = true
						}
					case pendingBash:
						if len(pend.revertPaths) > 0 && !p.isErr {
							for _, rp := range pend.revertPaths {
								out = append(out, Change{Path: cleanRepoPath(relTo(cwd, rp)), Reverted: true})
							}
						}
						if pend.isTest {
							applyTestResult(pend.editEnd, p.isErr || testOutputFailed(p.text))
						}
					}
				}
			}
		}
	})
	return finalizeSessionPaths(withoutFailed(out, failed))
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
		if c.Reverted {
			n := 0
			for _, k := range kept {
				if k.Path != c.Path {
					kept[n] = k
					n++
				}
			}
			kept = kept[:n]
			continue
		}
		if scratchFile(c, cs) {
			continue
		}
		folded := false
		for i := len(kept) - 1; i >= 0 && c.Old != ""; i-- {
			if kept[i].Path == c.Path && strings.Contains(kept[i].New, c.Old) {
				kept[i].New = strings.Replace(kept[i].New, c.Old, c.New, 1)
				if c.TestFailed {
					kept[i].TestFailed = true
				}
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
	if c.Reverted {
		return true
	}
	p := strings.TrimPrefix(strings.ReplaceAll(c.Path, `\`, "/"), "./")
	low := strings.ToLower(p)
	if strings.Contains(low, "site-packages/") || strings.Contains(low, "swebench_") {
		return true
	}
	if absPath(p) || strings.HasPrefix(p, "..") || strings.Contains(p, "/tmp/") || strings.HasPrefix(p, "tmp/") {
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
	return (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) && !testDir(p)
}

// cleanRepoPath normalizes transcript paths and strips container /testbed/ prefixes.
func cleanRepoPath(p string) string {
	s := strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	s = strings.TrimPrefix(s, "./")
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "/testbed/"); i >= 0 {
		s = s[i+len("/testbed/"):]
	} else if rest, ok := strings.CutPrefix(s, "testbed/"); ok {
		s = rest
	}
	s = path.Clean(s)
	if s == "." {
		return ""
	}
	return s
}

func finalizeSessionPaths(cs []Change) []Change {
	if len(cs) == 0 {
		return cs
	}
	var absCandidates []Change
	for i := range cs {
		cs[i].Path = cleanRepoPath(cs[i].Path)
		low := strings.ToLower(cs[i].Path)
		if !absPath(cs[i].Path) || strings.Contains(low, "site-packages/") || strings.Contains(low, "swebench_") || strings.Contains(low, "/tmp/") {
			continue
		}
		absCandidates = append(absCandidates, cs[i])
	}
	if dir := commonDir(absCandidates); dir != "" && strings.Count(strings.Trim(dir, "/"), "/") >= 1 {
		prefix := strings.TrimSuffix(dir, "/") + "/"
		for i := range cs {
			if rest, ok := strings.CutPrefix(cs[i].Path, prefix); ok && rest != "" {
				cs[i].Path = rest
			}
		}
	}
	return cs
}

func splitShellSegments(cmd string) [][]string {
	var segs [][]string
	var cur []string
	var tok strings.Builder
	inToken := false
	var quote byte
	flushTok := func() {
		if inToken {
			cur = append(cur, tok.String())
			tok.Reset()
			inToken = false
		}
	}
	flushSeg := func() {
		flushTok()
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(cmd) {
				i++
				tok.WriteByte(cmd[i])
			} else {
				tok.WriteByte(c)
			}
			inToken = true
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			inToken = true
		case '\\':
			if i+1 < len(cmd) {
				i++
				tok.WriteByte(cmd[i])
				inToken = true
			}
		case ' ', '\t', '\r':
			flushTok()
		case '\n', ';':
			flushSeg()
		case '|', '&':
			if i+1 < len(cmd) && cmd[i+1] == c {
				i++
				flushSeg()
			} else {
				flushSeg()
			}
		default:
			tok.WriteByte(c)
			inToken = true
		}
	}
	flushSeg()
	return segs
}

func trimShellPrefixes(args []string) []string {
	for len(args) > 0 {
		a := args[0]
		if a == "env" || a == "command" {
			args = args[1:]
			continue
		}
		if a == "timeout" && len(args) >= 2 {
			args = args[2:]
			continue
		}
		if eq := strings.IndexByte(a, '='); eq > 0 && !strings.HasPrefix(a, "-") && !strings.Contains(a[:eq], "/") {
			args = args[1:]
			continue
		}
		break
	}
	return args
}

func stripRedirections(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == ">" || a == ">>" || a == "2>" || a == "&>" || a == "<" {
			i++
			continue
		}
		if strings.HasPrefix(a, "2>") || strings.HasPrefix(a, ">") || strings.HasPrefix(a, "1>") || strings.HasPrefix(a, "&>") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// parseShellRevert extracts repo file paths explicitly reverted or deleted by
// shell commands such as `git checkout -- ...`, `git restore ...`, or `rm ...`.
func parseShellRevert(cmd string) []string {
	var out []string
	seen := map[string]bool{}
	addPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." || strings.HasPrefix(p, "-") {
			return
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, seg := range splitShellSegments(cmd) {
		args := stripRedirections(trimShellPrefixes(seg))
		if len(args) < 2 {
			continue
		}
		bin := path.Base(strings.ReplaceAll(args[0], `\`, "/"))
		switch bin {
		case "rm":
			pastFlags := false
			for _, a := range args[1:] {
				if a == "--" {
					pastFlags = true
					continue
				}
				if !pastFlags && strings.HasPrefix(a, "-") {
					continue
				}
				addPath(a)
			}
		case "git":
			i := 1
			for i < len(args) {
				a := args[i]
				if a == "-C" || a == "--git-dir" || a == "--work-tree" {
					i += 2
					continue
				}
				if strings.HasPrefix(a, "-") {
					i++
					continue
				}
				break
			}
			if i >= len(args) {
				continue
			}
			sub := args[i]
			rest := args[i+1:]
			switch sub {
			case "checkout":
				dash := slicesIndex(rest, "--")
				if dash >= 0 {
					for _, a := range rest[dash+1:] {
						addPath(a)
					}
					continue
				}
				hasBranchFlag := false
				var candidates []string
				for _, a := range rest {
					if a == "-b" || a == "-B" || a == "--orphan" || a == "--track" {
						hasBranchFlag = true
						break
					}
					if !strings.HasPrefix(a, "-") && strings.Contains(a, ".") {
						candidates = append(candidates, a)
					}
				}
				if !hasBranchFlag {
					for _, c := range candidates {
						addPath(c)
					}
				}
			case "restore":
				stagedOnly := false
				worktree := false
				dash := -1
				for idx, a := range rest {
					if a == "--" {
						dash = idx
						break
					}
					if a == "-S" || a == "--staged" {
						stagedOnly = true
					}
					if a == "-W" || a == "--worktree" {
						worktree = true
					}
				}
				if stagedOnly && !worktree {
					continue
				}
				start := 0
				if dash >= 0 {
					start = dash + 1
				}
				for j := start; j < len(rest); j++ {
					a := rest[j]
					if dash < 0 {
						if a == "-s" || a == "--source" {
							j++
							continue
						}
						if strings.HasPrefix(a, "-") {
							continue
						}
					}
					addPath(a)
				}
			}
		}
	}
	return out
}

func slicesIndex(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

// isTestCommand reports whether a shell command invokes a test runner.
func isTestCommand(cmd string) bool {
	for _, seg := range splitShellSegments(cmd) {
		args := stripRedirections(trimShellPrefixes(seg))
		if len(args) == 0 {
			continue
		}
		rawBin := strings.ReplaceAll(args[0], `\`, "/")
		bin := path.Base(rawBin)
		switch bin {
		case "pytest", "py.test", "unittest", "tox", "nox", "jest", "vitest", "mocha", "rspec", "phpunit", "ctest":
			return true
		case "runtests.py":
			return true
		case "go", "cargo":
			if len(args) >= 2 && args[1] == "test" {
				return true
			}
		case "npm", "pnpm", "yarn", "bun", "mvn", "gradle", "gradlew":
			if len(args) >= 2 && args[1] == "test" {
				return true
			}
		case "npx", "uv", "poetry", "pipenv":
			for _, a := range args[1:min(len(args), 4)] {
				ab := path.Base(a)
				if ab == "pytest" || ab == "unittest" || ab == "jest" || ab == "vitest" {
					return true
				}
			}
		case "manage.py":
			if len(args) >= 2 && args[1] == "test" {
				return true
			}
		case "test":
			if strings.HasSuffix(rawBin, "bin/test") {
				return true
			}
		}
		if bin == "python" || strings.HasPrefix(bin, "python3") || bin == "python2" {
			for i := 1; i < len(args); i++ {
				a := args[i]
				if a == "-c" {
					break
				}
				if a == "-m" && i+1 < len(args) {
					mod := args[i+1]
					if mod == "pytest" || mod == "unittest" {
						return true
					}
					i++
					continue
				}
				if strings.HasPrefix(a, "-") {
					continue
				}
				sb := path.Base(strings.ReplaceAll(a, `\`, "/"))
				if sb == "runtests.py" || strings.HasPrefix(sb, "test_") || strings.HasSuffix(sb, "_test.py") {
					return true
				}
				if sb == "manage.py" && i+1 < len(args) && args[i+1] == "test" {
					return true
				}
				break
			}
		}
	}
	return false
}

// testOutputFailed reports whether stdout/stderr from a test command indicates
// a failure, ignoring passing zero-failure summaries and benign Python site.py
// .pth startup warnings.
func testOutputFailed(text string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, "<tool_use_error>") {
		return true
	}
	inSitePthWarning := false
	for line := range strings.SplitSeq(text, "\n") {
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}
		if strings.HasPrefix(tr, "Error processing line ") && strings.Contains(tr, ".pth:") {
			inSitePthWarning = true
			continue
		}
		if inSitePthWarning {
			if tr == "Remainder of file ignored" {
				inSitePthWarning = false
			}
			continue
		}
		if code, ok := statusCode(tr, "Exit code ", ""); ok && code != 0 {
			return true
		}
		if code, ok := statusCode(tr, "Exit code: ", ""); ok && code != 0 {
			return true
		}
		if strings.HasPrefix(tr, "FAILED ") || tr == "FAILED" || strings.HasPrefix(tr, "FAILED(") ||
			strings.HasPrefix(tr, "--- FAIL:") || strings.HasPrefix(tr, "FAIL\t") ||
			strings.HasPrefix(tr, "test result: FAILED") || strings.HasPrefix(tr, "FAIL (") ||
			strings.HasPrefix(tr, "Traceback (most recent call last):") ||
			strings.Contains(tr, "=== FAILURES ===") || strings.Contains(tr, "=== ERRORS ===") ||
			strings.Contains(tr, "FAILURES!") {
			return true
		}
		if hasNonzeroCountBefore(tr, " failed") || hasNonzeroFailureKV(tr) {
			return true
		}
	}
	return false
}

func hasNonzeroCountBefore(s, suffix string) bool {
	idx := 0
	for {
		pos := strings.Index(s[idx:], suffix)
		if pos < 0 {
			return false
		}
		end := idx + pos
		start := end
		for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
			start--
		}
		if start < end {
			n, err := strconv.Atoi(s[start:end])
			if err == nil && n > 0 {
				return true
			}
		}
		idx = end + len(suffix)
	}
}

func hasNonzeroFailureKV(s string) bool {
	for _, key := range []string{"failures=", "errors="} {
		idx := strings.Index(s, key)
		if idx < 0 {
			continue
		}
		start := idx + len(key)
		end := start
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end > start {
			if n, err := strconv.Atoi(s[start:end]); err == nil && n > 0 {
				return true
			}
		}
	}
	return false
}

func testPath(p string) bool {
	base := path.Base(p)
	return testDir(p) || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func testDir(p string) bool {
	return strings.HasPrefix(p, "tests/") || strings.HasPrefix(p, "test/") || strings.Contains(p, "/tests/") || strings.Contains(p, "/test/") || strings.Contains(p, "__tests__/")
}
