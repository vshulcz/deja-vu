package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/hookengine"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	coordGitLockStaleAfter = 10 * time.Minute
	coordSpawnDedupWindow  = 5 * time.Second
)

var coordNow = time.Now

type (
	coordFileTouch = hookengine.CoordFileTouch
	coordStamp     = hookengine.CoordStamp
	coordSession   = hookengine.CoordSession
	coordState     = hookengine.CoordState
)

type coordActionKind int

const (
	coordActionNone coordActionKind = iota
	coordActionRead
	coordActionEdit
)

func coordPath(dir string) string {
	return dir + hookengine.CoordFile
}

func holdCoordLock(dir string) func() {
	_, release := hookengine.NewCoordStore(dir).Lock()
	return release
}

func loadCoordState(dir string) coordState {
	return hookengine.NewCoordStore(dir).LoadState()
}

func saveCoordState(dir string, st coordState) {
	hookengine.NewCoordStore(dir).SaveState(st)
}

func pruneCoordState(st *coordState, now time.Time) {
	hookengine.PruneCoordState(st, now)
}

func ensureCoordSession(st *coordState, id string, nowUnix int64) *coordSession {
	return st.EnsureSession(id, nowUnix)
}

// coordCanonicalPath resolves p relative to cwd, cleans it, resolves symlinks
// across existing parent directories (for files that do not exist yet), and
// uppercases any Windows drive letter so all harnesses produce identical keys.
func coordCanonicalPath(cwd, p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "-" {
		return ""
	}
	if strings.Contains(p, "://") {
		return ""
	}
	if !filepath.IsAbs(p) {
		if strings.TrimSpace(cwd) == "" {
			return ""
		}
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	resolved := evalSymlinksWithMissingTail(p)
	if runtime.GOOS == "windows" && len(resolved) >= 2 && resolved[1] == ':' && resolved[0] >= 'a' && resolved[0] <= 'z' {
		resolved = strings.ToUpper(resolved[:1]) + resolved[1:]
	}
	return resolved
}

func evalSymlinksWithMissingTail(p string) string {
	if res, symErr := filepath.EvalSymlinks(p); symErr == nil {
		return filepath.Clean(res)
	}
	cur := p
	var tail []string
	for {
		parent := filepath.Dir(cur)
		base := filepath.Base(cur)
		if parent == cur {
			break
		}
		tail = append(tail, base)
		cur = parent
		if res, symErr := filepath.EvalSymlinks(cur); symErr == nil {
			for _, seg := range slices.Backward(tail) {
				res = filepath.Join(res, seg)
			}
			return filepath.Clean(res)
		}
	}
	return filepath.Clean(p)
}

// resolveCoordGitWorktree walks upward from cwd using filesystem metadata only
// (never spawning git) and returns the canonical worktree root and git dir.
func resolveCoordGitWorktree(cwd string) (string, string) {
	cur := coordCanonicalPath("", cwd)
	if cur == "" {
		return "", ""
	}
	info, statErr := os.Stat(cur)
	if statErr == nil && !info.IsDir() {
		cur = filepath.Dir(cur)
	}
	for {
		dotGit := filepath.Join(cur, ".git")
		gitInfo, gitErr := os.Lstat(dotGit)
		if gitErr == nil {
			if gitInfo.IsDir() {
				return cur, dotGit
			}
			data, readErr := os.ReadFile(dotGit)
			if readErr == nil && len(data) <= 4096 {
				line := strings.TrimSpace(string(data))
				if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
					gd := coordCanonicalPath(cur, strings.TrimSpace(rest))
					if gd != "" {
						return cur, gd
					}
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", ""
		}
		cur = parent
	}
}

func coordShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func coordIsAncestor(st coordState, descendantID, ancestorID string) bool {
	if descendantID == "" || ancestorID == "" || descendantID == ancestorID {
		return false
	}
	seen := map[string]bool{descendantID: true}
	cur := descendantID
	for range 16 {
		sess := st.Sessions[cur]
		if sess == nil || sess.ParentID == "" {
			break
		}
		if sess.ParentID == ancestorID {
			return true
		}
		if seen[sess.ParentID] {
			break
		}
		seen[sess.ParentID] = true
		cur = sess.ParentID
	}
	return false
}

func coordSameLineage(dir, selfID, selfParent, transcript, harness, otherID string, st coordState, cachedLineage *map[string]bool) bool {
	if otherID == "" || otherID == selfID {
		return true
	}
	if selfParent != "" && otherID == selfParent {
		return true
	}
	if coordIsAncestor(st, selfID, otherID) || coordIsAncestor(st, otherID, selfID) {
		return true
	}
	if selfParent != "" && coordIsAncestor(st, selfParent, otherID) {
		return true
	}
	if *cachedLineage == nil && dir != "" && selfID != "" {
		*cachedLineage = askerLineageForHarness(dir, selfID, transcript, harness, selfParent)
	}
	return (*cachedLineage)[otherID]
}

func isPostToolEvent(eventName string) bool {
	switch strings.ToLower(strings.TrimSpace(eventName)) {
	case "posttooluse", "aftertool", "posttool", "posttoolusefailure":
		return true
	}
	return false
}

type coordPayloadView struct {
	SessionID       string
	ParentSessionID string
	TranscriptPath  string
	Harness         string
	EventName       string
	ToolName        string
	CWD             string
	Paths           []string
	Patch           string
	Command         string
	ToolInputCmd    string
	IsFailure       bool
}

func parseCoordPayloadView(raw []byte, fallbackCWD string, fallback toolHookInput) coordPayloadView {
	out := coordPayloadView{
		SessionID:      strings.TrimSpace(fallback.SessionID),
		TranscriptPath: strings.TrimSpace(fallback.TranscriptPath),
		Harness:        strings.ToLower(strings.TrimSpace(fallback.Harness)),
		ToolName:       strings.TrimSpace(fallback.ToolName),
		CWD:            strings.TrimSpace(fallbackCWD),
	}
	if len(raw) == 0 {
		return out
	}
	var doc map[string]any
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		return out
	}
	if out.SessionID == "" {
		out.SessionID = firstJSONString(doc, "session_id", "sessionId", "conversation_id", "conversationId")
	}
	out.ParentSessionID = firstJSONString(doc, "parent_session_id", "parentSessionId")
	if out.TranscriptPath == "" {
		out.TranscriptPath = firstJSONString(doc, "transcript_path", "transcriptPath")
	}
	if h := strings.ToLower(firstJSONString(doc, "harness")); h != "" {
		out.Harness = h
	}
	out.EventName = firstJSONString(doc, "hook_event_name", "hookEventName", "event")
	if out.ToolName == "" {
		out.ToolName = firstJSONString(doc, "tool_name", "toolName", "tool")
	}
	if out.CWD == "" {
		out.CWD = firstJSONString(doc, "cwd", "workspaceRoot")
		if out.CWD == "" {
			if roots, ok := doc["workspace_roots"].([]any); ok && len(roots) > 0 {
				if first, ok := roots[0].(string); ok {
					out.CWD = strings.TrimSpace(first)
				}
			}
		}
	}
	if strings.EqualFold(out.EventName, "PostToolUseFailure") ||
		jsonBool(doc["is_error"]) || jsonBool(doc["isError"]) || jsonBool(doc["tool_result_is_error"]) ||
		strings.EqualFold(firstJSONString(doc, "failure_type", "failureType"), "error") ||
		coordValueNonEmpty(doc["error"]) || coordValueNonEmpty(doc["tool_error"]) || coordValueNonEmpty(doc["error_message"]) {
		out.IsFailure = true
	}
	for _, key := range []string{"tool_response", "toolResult", "tool_output"} {
		resp := coordAsObject(doc[key])
		if resp == nil {
			continue
		}
		if jsonBool(resp["is_error"]) || jsonBool(resp["isError"]) ||
			coordValueNonEmpty(resp["error"]) || coordValueNonEmpty(resp["tool_error"]) {
			out.IsFailure = true
		}
		if rt, ok := resp["resultType"].(string); ok {
			rt = strings.TrimSpace(rt)
			if rt != "" && !strings.EqualFold(rt, "success") {
				out.IsFailure = true
			}
		}
		for _, ck := range []string{"exit_code", "exitCode", "exit_status", "exitStatus"} {
			if coordNonZeroStatus(resp[ck]) {
				out.IsFailure = true
			}
		}
	}

	ti := extractToolInputMap(doc)
	if ti != nil {
		out.Command = firstJSONString(ti, "command", "CommandLine", "command_line", "cmd", "script")
		if out.Command == "" {
			out.Command = firstJSONString(doc, "command", "CommandLine", "command_line")
		}
		out.ToolInputCmd = out.Command
		out.Patch = coordExtractPatchText(out.ToolName, ti)
		out.Paths = collectToolInputPaths(ti)
	}
	return out
}

func isEphemeralMCPHelperSession(id string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(id)), "-mcp-")
}

func coordExtractPatchText(toolName string, ti map[string]any) string {
	if patch := firstJSONString(ti, "patch", "patchText", "patch_text", "diff"); patch != "" {
		return patch
	}
	raw := firstJSONString(ti, "input")
	if raw == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(toolName), "patch") || looksLikePatchText(raw) {
		return raw
	}
	return ""
}

func looksLikePatchText(s string) bool {
	return strings.Contains(s, "*** Begin Patch") ||
		strings.Contains(s, "diff --git ") ||
		strings.Contains(s, "\n--- ") ||
		strings.HasPrefix(s, "--- ")
}

func extractToolInputMap(doc map[string]any) map[string]any {
	for _, k := range []string{"tool_input", "toolInput", "input"} {
		if m, ok := doc[k].(map[string]any); ok {
			return m
		}
	}
	return nil
}

func collectToolInputPaths(ti map[string]any) []string {
	var paths []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			paths = append(paths, v)
		}
	}
	for _, k := range []string{
		"file_path", "filePath", "path", "target_file", "targetFile",
		"TargetFile", "target_path", "targetPath", "absolute_path",
		"absolutePath", "AbsolutePath", "notebook_path", "notebookPath",
	} {
		if s, ok := ti[k].(string); ok {
			add(s)
		}
	}
	for _, k := range []string{"paths", "Paths", "files"} {
		if arr, ok := ti[k].([]any); ok {
			for _, item := range arr {
				switch v := item.(type) {
				case string:
					add(v)
				case map[string]any:
					add(firstJSONString(v, "file_path", "filePath", "path", "target_file", "targetFile", "absolute_path", "AbsolutePath"))
				}
			}
		}
	}
	return paths
}

func firstJSONString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			if tr := strings.TrimSpace(s); tr != "" {
				return tr
			}
		}
	}
	return ""
}

func jsonBool(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func coordJSONInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func coordToolAction(toolName string, tiCmd string, hasPatch bool) coordActionKind {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "apply_patch", "patch":
		return coordActionEdit
	case "str_replace_editor", "str_replace_based_edit_tool":
		if strings.EqualFold(strings.TrimSpace(tiCmd), "view") {
			return coordActionRead
		}
		return coordActionEdit
	case "edit", "write", "multiedit", "notebookedit", "edit_file", "write_file",
		"write_to_file", "replace_file_content", "multi_replace_file_content",
		"create_file", "insert_edit_into_file", "str_replace", "search_replace",
		"file_edit", "file_write", "patch_file", "update_file", "delete_file", "remove_file":
		return coordActionEdit
	case "read", "read_file", "view_file", "view_file_outline", "view_code_item",
		"read_notebook", "open_file", "cat_file", "file_read", "get_file", "read_many_files":
		return coordActionRead
	}
	if strings.HasPrefix(lower, "write_") || strings.HasPrefix(lower, "edit_") || strings.Contains(lower, "replace_file") || isExternalMCPEditTool(lower) {
		return coordActionEdit
	}
	if isExternalMCPReadTool(lower) {
		return coordActionRead
	}
	if hasPatch && !isCommandTool(toolName) {
		return coordActionEdit
	}
	return coordActionNone
}

func coordActionPaths(cwd string, view coordPayloadView) (coordActionKind, []string) {
	hasPatch := view.Patch != ""
	action := coordToolAction(view.ToolName, view.ToolInputCmd, hasPatch)
	if action == coordActionNone {
		return coordActionNone, nil
	}
	rawPaths := slices.Clone(view.Paths)
	if hasPatch {
		for _, pf := range applyPatchFiles(view.Patch) {
			rawPaths = append(rawPaths, sources.ShellPatchPath(view.Patch, cwd, pf))
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(rawPaths))
	for _, rp := range rawPaths {
		if sources.IsAgentScratchPath(filepath.ToSlash(rp)) {
			continue
		}
		cp := coordCanonicalPath(cwd, rp)
		if cp != "" && !seen[cp] && !sources.IsAgentScratchPath(filepath.ToSlash(cp)) {
			seen[cp] = true
			out = append(out, cp)
		}
	}
	return action, out
}

func coordDisplayPath(cwd, canonical string) string {
	if cleanCWD := coordCanonicalPath("", cwd); cleanCWD != "" {
		if rel, relErr := filepath.Rel(cleanCWD, canonical); relErr == nil && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return search.SafePath(canonical)
}

func coordFormatAge(sec int64) string {
	if sec < 60 {
		if sec <= 1 {
			return "1s"
		}
		return fmt.Sprintf("%ds", sec)
	}
	return fmt.Sprintf("%dm", sec/60)
}

func coordShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func coordPolicyAllows(cwd, transcriptPath string) bool {
	pol := policy.Load()
	if !pol.Allows(policy.ActivationAuto, "local") {
		return false
	}
	if (cwd != "" || transcriptPath != "") && pol.Ignored(transcriptPath, cwd) {
		return false
	}
	if cwd != "" && pol.Ignored("", strings.TrimRight(filepath.ToSlash(filepath.Clean(cwd)), "/")+"/") {
		return false
	}
	return true
}

// coordHandleToolFile checks Active File Radar collisions against other active
// non-lineage sessions and records the caller's edit/read touch in .coord.
func coordHandleToolFile(dir, cwd string, input toolHookInput, raw []byte) string {
	if dir == "" || recallIsOff() {
		return ""
	}
	view := parseCoordPayloadView(raw, cwd, input)
	if view.SessionID == "" || isEphemeralMCPHelperSession(view.SessionID) || !coordPolicyAllows(view.CWD, view.TranscriptPath) {
		return ""
	}
	action, paths := coordActionPaths(view.CWD, view)
	if action == coordActionNone && isCommandTool(view.ToolName) && view.Command != "" {
		if patchFiles := applyPatchFiles(view.Command); len(patchFiles) > 0 {
			action = coordActionEdit
			seen := map[string]bool{}
			for _, pf := range patchFiles {
				pf = sources.ShellPatchPath(view.Command, view.CWD, pf)
				if sources.IsAgentScratchPath(filepath.ToSlash(pf)) {
					continue
				}
				if cp := coordCanonicalPath(view.CWD, pf); cp != "" && !seen[cp] && !sources.IsAgentScratchPath(filepath.ToSlash(cp)) {
					seen[cp] = true
					paths = append(paths, cp)
				}
			}
		}
	}
	if action == coordActionNone || len(paths) == 0 {
		return ""
	}

	postEvent := isPostToolEvent(view.EventName)
	checkCollisions := action == coordActionEdit || (action == coordActionRead && postEvent)
	wt, _ := resolveCoordGitWorktree(view.CWD)
	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)

	selfParent := view.ParentSessionID
	if existing := st.Sessions[view.SessionID]; existing != nil && selfParent == "" {
		selfParent = existing.ParentID
	}

	var cachedLineage map[string]bool
	var warnings []string
	if checkCollisions {
		for _, p := range paths {
			var newestID string
			var newestTouch coordFileTouch
			for otherID, other := range st.Sessions {
				if coordSameLineage(dir, view.SessionID, selfParent, view.TranscriptPath, view.Harness, otherID, st, &cachedLineage) {
					continue
				}
				if touch, ok := other.Edits[p]; ok && touch.AtUnix > newestTouch.AtUnix {
					newestTouch = touch
					newestID = otherID
				}
			}
			if newestID == "" {
				continue
			}
			stampKey := "file:" + coordShortHash(view.SessionID+"\x00"+p+"\x00"+newestID)
			if stamped, seen := st.Stamps[stampKey]; seen && stamped.Val == newestTouch.AtUnix {
				continue
			}
			st.Stamps[stampKey] = coordStamp{AtUnix: nowUnix, Val: newestTouch.AtUnix}
			ageSec := max(nowUnix-newestTouch.AtUnix, 0)
			warnings = append(warnings, fmt.Sprintf(
				"parallel edit radar: %s was edited %s ago by another active session (%s)",
				coordDisplayPath(view.CWD, p),
				coordFormatAge(ageSec),
				coordShortID(newestID),
			))
		}
	}

	sess := ensureCoordSession(&st, view.SessionID, nowUnix)
	if selfParent != "" && sess.ParentID == "" {
		sess.ParentID = selfParent
	}
	if view.Harness != "" && sess.Harness == "" {
		sess.Harness = view.Harness
	}
	if wt != "" {
		sess.Worktree = wt
	}
	switch action { //nolint:exhaustive // coordActionNone returns above before recording touches
	case coordActionEdit:
		if sess.Edits == nil {
			sess.Edits = map[string]coordFileTouch{}
		}
		for _, p := range paths {
			sess.Edits[p] = coordFileTouch{AtUnix: nowUnix}
		}
	case coordActionRead:
		if sess.Reads == nil {
			sess.Reads = map[string]coordFileTouch{}
		}
		for _, p := range paths {
			sess.Reads[p] = coordFileTouch{AtUnix: nowUnix}
		}
	}
	pruneCoordState(&st, now)
	saveCoordState(dir, st)

	if len(warnings) == 0 {
		return ""
	}
	return defuseHostTriggers(strings.Join(warnings, "\n"))
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func tokenLooksWindowsPath(tok string) bool {
	if len(tok) >= 2 && isASCIIAlpha(tok[0]) && tok[1] == ':' {
		return true
	}
	if len(tok) >= 4 && strings.HasPrefix(tok, "-C") && isASCIIAlpha(tok[2]) && tok[3] == ':' {
		return true
	}
	return strings.HasPrefix(tok, `\\`) || strings.HasPrefix(tok, `.\`) || strings.HasPrefix(tok, `..\`)
}

func shellBaseName(p string) string {
	if idx := strings.LastIndexAny(p, `/\`); idx >= 0 {
		p = p[idx+1:]
	}
	return strings.ToLower(p)
}

func splitShellSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	var quote byte
	escaped := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if quote != 0 {
			switch {
			case ch == '\\' && quote == '"' && (runtime.GOOS != "windows" || (i+1 < len(cmd) && cmd[i+1] == '"')):
				escaped = true
			case ch == quote:
				quote = 0
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == '\\' {
			if runtime.GOOS != "windows" || (i+1 < len(cmd) && cmd[i+1] == '\n') {
				escaped = true
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			cur.WriteByte(ch)
			continue
		}
		if ch == ';' || ch == '\n' || ch == '|' || (ch == '&' && i+1 < len(cmd) && cmd[i+1] == '&') {
			if s := strings.TrimSpace(cur.String()); s != "" {
				segs = append(segs, s)
			}
			cur.Reset()
			if (ch == '|' && i+1 < len(cmd) && cmd[i+1] == '|') || (ch == '&' && i+1 < len(cmd) && cmd[i+1] == '&') {
				i++
			}
			continue
		}
		cur.WriteByte(ch)
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		segs = append(segs, s)
	}
	return segs
}

func splitShellFields(seg string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	escaped := false
	hasTok := false
	winTok := false
	for i := range len(seg) {
		ch := seg[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			hasTok = true
			continue
		}
		if !winTok {
			winTok = runtime.GOOS == "windows" ||
				tokenLooksWindowsPath(cur.String()) ||
				(cur.Len() == 0 && (strings.HasPrefix(seg[i:], `\\`) || strings.HasPrefix(seg[i:], `.\`) || strings.HasPrefix(seg[i:], `..\`)))
		}
		if quote != 0 {
			switch {
			case ch == '\\' && quote == '"' && (!winTok || (i+1 < len(seg) && seg[i+1] == '"')):
				escaped = true
			case ch == quote:
				quote = 0
			default:
				cur.WriteByte(ch)
			}
			hasTok = true
			continue
		}
		switch {
		case ch == '\\':
			if winTok {
				cur.WriteByte(ch)
			} else {
				escaped = true
			}
			hasTok = true
		case ch == '\'' || ch == '"':
			quote = ch
			hasTok = true
		case unicode.IsSpace(rune(ch)):
			if hasTok {
				out = append(out, cur.String())
				cur.Reset()
				hasTok = false
				winTok = false
			}
		default:
			cur.WriteByte(ch)
			hasTok = true
		}
	}
	if hasTok {
		out = append(out, cur.String())
	}
	return out
}

func isEnvAssignToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := range eq {
		c := tok[i]
		valid := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c == '_') || (i > 0 && c >= '0' && c <= '9')
		if !valid {
			return false
		}
	}
	return true
}

var gitValueFlags = map[string]bool{
	"-c":          true,
	"--git-dir":   true,
	"--work-tree": true,
	"--namespace": true,
}

func parseGitInvocation(fields []string) (dirOverride, subcmd string, rest []string, ok bool) {
	i := 0
	for i < len(fields) && isEnvAssignToken(fields[i]) {
		i++
	}
	if i < len(fields) && fields[i] == "env" {
		i++
		for i < len(fields) && (isEnvAssignToken(fields[i]) || strings.HasPrefix(fields[i], "-")) {
			i++
		}
	}
	if i >= len(fields) {
		return "", "", nil, false
	}
	bin := shellBaseName(fields[i])
	if bin != "git" && bin != "git.exe" {
		return "", "", nil, false
	}
	i++
	for i < len(fields) {
		f := fields[i]
		if f == "-C" && i+1 < len(fields) {
			dirOverride = fields[i+1]
			i += 2
			continue
		}
		if afterC, hasC := strings.CutPrefix(f, "-C"); hasC && afterC != "" {
			dirOverride = afterC
			i++
			continue
		}
		if gitValueFlags[f] && i+1 < len(fields) {
			i += 2
			continue
		}
		if strings.HasPrefix(f, "-") {
			i++
			continue
		}
		return dirOverride, strings.ToLower(f), fields[i+1:], true
	}
	return "", "", nil, false
}

func isMutatingGitSubcmd(subcmd string, args []string) bool {
	switch subcmd {
	case "add", "commit", "amend", "checkout", "switch", "restore", "reset",
		"rebase", "merge", "cherry-pick", "pull", "push", "rm", "mv",
		"am", "apply", "clean", "tag":
		return true
	case "stash":
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "list", "show":
				return false
			}
		}
		return true
	case "worktree":
		if len(args) > 0 && strings.EqualFold(args[0], "list") {
			return false
		}
		return true
	case "branch":
		for _, a := range args {
			switch a {
			case "-l", "--list", "--show-current", "-a", "--all", "-r", "--remotes", "--contains", "--no-contains", "--merged", "--no-merged", "-v", "-vv":
				return false
			}
		}
		return len(args) > 0
	}
	return false
}

// mutatingGitSubcommand inspects a shell command and returns the target CWD and
// mutating git subcommand if one is present.
func mutatingGitSubcommand(cmd, cwd string) (targetCWD, subcmd string) {
	curCWD := cwd
	for _, seg := range splitShellSegments(cmd) {
		fields := splitShellFields(seg)
		if len(fields) >= 2 && fields[0] == "cd" && !strings.HasPrefix(fields[1], "-") {
			if nextCWD := coordCanonicalPath(curCWD, fields[1]); nextCWD != "" {
				curCWD = nextCWD
			}
			continue
		}
		dirFlag, sub, args, ok := parseGitInvocation(fields)
		if !ok || !isMutatingGitSubcmd(sub, args) {
			continue
		}
		effectiveCWD := curCWD
		if dirFlag != "" {
			if resolvedFlag := coordCanonicalPath(curCWD, dirFlag); resolvedFlag != "" {
				effectiveCWD = resolvedFlag
			}
		}
		return effectiveCWD, sub
	}
	return "", ""
}

// coordCheckMutatingGit checks PreToolUse command invocations for an existing
// git index.lock or concurrent active edits in the same git worktree.
func coordCheckMutatingGit(dir, cwd string, input toolHookInput, raw []byte) string {
	if dir == "" || recallIsOff() {
		return ""
	}
	view := parseCoordPayloadView(raw, cwd, input)
	if isPostToolEvent(view.EventName) || !isCommandTool(view.ToolName) {
		return ""
	}
	if !coordPolicyAllows(view.CWD, view.TranscriptPath) {
		return ""
	}
	cmd := strings.TrimSpace(view.Command)
	if cmd == "" {
		cmd = strings.TrimSpace(input.ToolInput.Command)
	}
	if cmd == "" || view.SessionID == "" {
		return ""
	}
	targetCWD, subcmd := mutatingGitSubcommand(cmd, view.CWD)
	if subcmd == "" {
		return ""
	}
	wt, gitDir := resolveCoordGitWorktree(targetCWD)
	if gitDir == "" && wt == "" {
		return ""
	}

	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	var warnings []string

	wtLabel := ""
	if wt != "" {
		wtLabel = filepath.Base(wt)
		if wtLabel == "" || wtLabel == "." {
			wtLabel = search.SafePath(wt)
		}
	}

	if gitDir != "" {
		lockPath := filepath.Join(gitDir, "index.lock")
		if info, statErr := os.Stat(lockPath); statErr == nil {
			modUnix := info.ModTime().UTC().Unix()
			stampKey := "gitlock:" + coordShortHash(view.SessionID+"\x00"+gitDir)
			if stamped, seen := st.Stamps[stampKey]; !seen || stamped.Val != modUnix {
				st.Stamps[stampKey] = coordStamp{AtUnix: nowUnix, Val: modUnix}
				age := max(now.Sub(info.ModTime()), 0)
				ageText := coordFormatAge(int64(age.Seconds()))
				if age >= coordGitLockStaleAfter {
					warnings = append(warnings, fmt.Sprintf(
						"git radar: stale index.lock (%s old) in %s before git %s — check whether another git process crashed",
						ageText, wtLabel, subcmd,
					))
				} else {
					warnings = append(warnings, fmt.Sprintf(
						"git radar: index.lock active (%s old) in %s before git %s — another git operation may still be running",
						ageText, wtLabel, subcmd,
					))
				}
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			// Permission or IO oddity is ignored cleanly in hook paths.
			_ = statErr
		}
	}

	if wt != "" {
		selfParent := view.ParentSessionID
		if existing := st.Sessions[view.SessionID]; existing != nil && selfParent == "" {
			selfParent = existing.ParentID
		}
		var cachedLineage map[string]bool
		var colliderID string
		var latestEditAt int64
		var sampleFile string
		editCount := 0
		for otherID, other := range st.Sessions {
			if other.Worktree != wt || coordSameLineage(dir, view.SessionID, selfParent, view.TranscriptPath, view.Harness, otherID, st, &cachedLineage) {
				continue
			}
			for fPath, touch := range other.Edits {
				editCount++
				if touch.AtUnix > latestEditAt {
					latestEditAt = touch.AtUnix
					colliderID = otherID
					sampleFile = fPath
				}
			}
		}
		if colliderID != "" {
			stampKey := "gitwt:" + coordShortHash(view.SessionID+"\x00"+wt+"\x00"+colliderID)
			if stamped, seen := st.Stamps[stampKey]; !seen || stamped.Val != latestEditAt {
				st.Stamps[stampKey] = coordStamp{AtUnix: nowUnix, Val: latestEditAt}
				warnings = append(warnings, fmt.Sprintf(
					"git radar: %d file(s) in this worktree recently edited by active session %s (e.g. %s) before git %s",
					editCount, coordShortID(colliderID), coordDisplayPath(wt, sampleFile), subcmd,
				))
			}
		}
	}

	if len(warnings) > 0 {
		saveCoordState(dir, st)
	}
	if len(warnings) == 0 {
		return ""
	}
	return defuseHostTriggers(strings.Join(warnings, "\n"))
}

// coordRecordPreCommand touches the session's live entry in .coord when it runs
// a PreToolUse shell command.
func coordRecordPreCommand(dir, cwd string, input toolHookInput, raw []byte) {
	if dir == "" || recallIsOff() {
		return
	}
	view := parseCoordPayloadView(raw, cwd, input)
	if view.SessionID == "" || !isCommandTool(view.ToolName) || !coordPolicyAllows(view.CWD, view.TranscriptPath) {
		return
	}
	wt, _ := resolveCoordGitWorktree(view.CWD)
	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	sess := ensureCoordSession(&st, view.SessionID, nowUnix)
	if view.ParentSessionID != "" && sess.ParentID == "" {
		sess.ParentID = view.ParentSessionID
	}
	if view.Harness != "" && sess.Harness == "" {
		sess.Harness = view.Harness
	}
	if wt != "" {
		sess.Worktree = wt
	}
	saveCoordState(dir, st)
}

func isCoordVerifyCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || index.InspectionCommand(cmd) {
		return false
	}
	if index.VerifyCommand(cmd) {
		return true
	}
	return isTestRunnerCommand(cmd)
}

func normalizeVerifyCmdForBrief(cmd, cwd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	fields := strings.Fields(cmd)
	cmd = strings.Join(fields, " ")
	wt, _ := resolveCoordGitWorktree(cwd)
	canonicalCWD := coordCanonicalPath("", cwd)

	if strings.HasPrefix(cmd, "cd ") {
		if idx := strings.Index(cmd, " && "); idx > 3 {
			cdArg := strings.TrimSpace(cmd[3:idx])
			cdArg = strings.Trim(cdArg, `"'`)
			resolvedCD := coordCanonicalPath(cwd, cdArg)
			if resolvedCD != "" && (resolvedCD == canonicalCWD || (wt != "" && resolvedCD == wt)) {
				cmd = strings.TrimSpace(cmd[idx+4:])
			}
		}
	}
	cmd = defuseHostTriggers(cmd)
	runes := []rune(cmd)
	if len(runes) > 120 {
		cmd = string(runes[:117]) + "..."
	}
	return cmd
}

func coordAsObject(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case string:
		trimmed := strings.TrimSpace(x)
		if strings.HasPrefix(trimmed, "{") {
			var nested map[string]any
			if json.Unmarshal([]byte(trimmed), &nested) == nil {
				return nested
			}
		}
	}
	return nil
}

func coordValueNonEmpty(v any) bool {
	switch x := v.(type) {
	case string:
		t := strings.TrimSpace(x)
		return t != "" && !strings.EqualFold(t, "(none)") && !strings.EqualFold(t, "null")
	case map[string]any:
		for _, k := range []string{"message", "error", "code", "detail", "reason"} {
			if coordValueNonEmpty(x[k]) {
				return true
			}
		}
		return len(x) > 0 && x["message"] == nil && x["error"] == nil && x["code"] == nil
	}
	return false
}

func coordNonZeroStatus(v any) bool {
	if code, ok := coordJSONInt(v); ok {
		return code != 0
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		n, err := strconv.Atoi(s)
		return err == nil && n != 0
	}
	return false
}

func coordRawHeadIndicatesFailure(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	head := strings.ToLower(string(raw[:min(len(raw), 4096)]))
	for _, needle := range []string{
		"posttoolusefailure",
		`"tool_result_is_error":true`,
		`"is_error":true`,
		`"iserror":true`,
		`"failure_type":"error"`,
		`"tool_error":"`,
		`"error_message":"`,
	} {
		if strings.Contains(head, needle) {
			return true
		}
	}
	return false
}

func coordOutputHasNonZeroExit(text string) bool {
	if text == "" {
		return false
	}
	markers := []string{
		"exited with code ",
		"exit code: ",
		"exit code ",
		"exit_code: ",
		"exit: ",
		"| exit=",
		"→ exit ",
	}
	for rawLine := range strings.SplitSeq(text, "\n") {
		low := strings.ToLower(strings.TrimSpace(rawLine))
		if low == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(low, "exit "); ok {
			if code, parsed := parseLeadingExitInt(rest); parsed && code != 0 {
				return true
			}
		}
		for _, marker := range markers {
			idx := 0
			for {
				pos := strings.Index(low[idx:], marker)
				if pos < 0 {
					break
				}
				rest := low[idx+pos+len(marker):]
				if code, ok := parseLeadingExitInt(rest); ok && code != 0 {
					return true
				}
				idx += pos + len(marker)
			}
		}
	}
	return false
}

func parseLeadingExitInt(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return 0, false
	}
	end := 0
	if s[0] == '-' || s[0] == '+' {
		end++
	}
	digitsStart := end
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == digitsStart {
		return 0, false
	}
	if end < len(s) {
		switch s[end] {
		case '.', '>', ',', ')', ' ', '\t', '\r':
		default:
			return 0, false
		}
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil
}

func coordHasFriction(text string) bool {
	if text == "" {
		return false
	}
	return index.OutputReadsAsFailure(text)
}

func coordStreakStampKey(sessionID, cwd string) string {
	if sid := strings.TrimSpace(sessionID); sid != "" {
		return "fail-streak:sid:" + sid
	}
	clean := coordCanonicalPath("", cwd)
	if clean == "" {
		clean = filepath.Clean(strings.TrimSpace(cwd))
	}
	if clean == "" || clean == "." {
		return ""
	}
	return "fail-streak:cwd:" + clean
}

// coordRecordAfterCommand updates per-session command failure streaks and
// records passing verification commands in .coord. It returns the updated
// failure streak (0 on clean commands).
func coordRecordAfterCommand(dir, cwd, cmd, output string, raw []byte, fallbackSessionID string) int {
	view := parseCoordPayloadView(raw, cwd, toolHookInput{SessionID: fallbackSessionID})
	if strings.TrimSpace(cmd) == "" {
		cmd = view.Command
	}
	if view.CWD == "" {
		view.CWD = strings.TrimSpace(cwd)
	}
	failed := view.IsFailure || coordRawHeadIndicatesFailure(raw) || coordHasFriction(output) || isDetEnvFailureOutput(output) || coordOutputHasNonZeroExit(output)
	if !coordPolicyAllows(view.CWD, view.TranscriptPath) {
		if failed {
			return 1
		}
		return 0
	}
	return coordRecordCommandOutcome(dir, view.CWD, view.SessionID, cmd, failed)
}

// coordRecordShellAfter updates the command failure streak for non-JSON shell
// hooks (deja shell -c / postexec) and returns the updated streak.
func coordRecordShellAfter(dir, cwd, sessionID, cmd string, failed bool) int {
	return coordRecordCommandOutcome(dir, cwd, sessionID, cmd, failed)
}

// coordResetFailStreak clears a session's consecutive failure streak when an
// external signal (such as an Antigravity transcript tool step) shows exit 0.
func coordResetFailStreak(dir, sessionID, cwd string) {
	coordRecordCommandOutcome(dir, cwd, sessionID, "", false)
}

func coordRecordCommandOutcome(dir, cwd, sessionID, cmd string, failed bool) int {
	if dir == "" || recallIsOff() || isEphemeralMCPHelperSession(sessionID) || !coordPolicyAllows(cwd, "") {
		if failed {
			return 1
		}
		return 0
	}
	streakKey := coordStreakStampKey(sessionID, cwd)
	verifyNorm := ""
	if !failed && sessionID != "" && isCoordVerifyCommand(cmd) {
		verifyNorm = normalizeVerifyCmdForBrief(cmd, cwd)
	}
	wt := ""
	if !failed && verifyNorm != "" {
		wt, _ = resolveCoordGitWorktree(cwd)
	}
	return hookengine.NewCoordStore(dir).RecordOutcome(coordNow(), hookengine.CoordOutcome{
		SessionID: sessionID,
		StreakKey: streakKey,
		VerifyCmd: verifyNorm,
		Worktree:  wt,
		Failed:    failed,
	})
}
