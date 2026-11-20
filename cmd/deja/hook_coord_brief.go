package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	coordBriefMaxBytes       = 420
	coordBriefMaxEdits       = 6
	coordBriefMaxReads       = 8
	coordTranscriptTailBytes = 512 * 1024
)

type coordBriefData struct {
	Edits     []string
	Reads     []string
	VerifyCmd string
	VerifyAt  int64
}

type coordBriefTouch struct {
	path string
	seq  int64
}

type coordPendingCall struct {
	toolName string
	cmd      string
	seq      int64
}

func collectBriefFromCoord(dir, parentID string) (map[string]int64, map[string]int64, string, int64) {
	edits := map[string]int64{}
	reads := map[string]int64{}
	if dir == "" || parentID == "" {
		return edits, reads, "", 0
	}
	now := coordNow()
	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	sess := st.Sessions[parentID]
	if sess == nil {
		return edits, reads, "", 0
	}
	for p, t := range sess.Edits {
		if p != "" {
			edits[p] = t.AtUnix
		}
	}
	for p, t := range sess.Reads {
		if p != "" {
			reads[p] = t.AtUnix
		}
	}
	return edits, reads, sess.VerifyCmd, sess.VerifyAt
}

type transcriptBriefScanner struct {
	cwd       string
	edits     map[string]int64
	reads     map[string]int64
	pending   map[string]coordPendingCall
	verifyCmd string
	seq       int64
}

func (s *transcriptBriefScanner) recordToolCall(id, name string, args map[string]any) {
	s.seq++
	cmd := firstJSONString(args, "command", "cmd", "CommandLine", "Cmd")
	patch := firstJSONString(args, "patch", "patchText", "patch_text", "diff", "input")
	paths := collectToolInputPaths(args)
	if patch != "" {
		for _, pf := range applyPatchFiles(patch) {
			paths = append(paths, sources.ShellPatchPath(patch, s.cwd, pf))
		}
	}
	action := coordToolAction(name, cmd, patch != "")
	if action == coordActionNone && isCommandTool(name) && cmd != "" {
		if pf := applyPatchFiles(cmd); len(pf) > 0 {
			action = coordActionEdit
			for _, p := range pf {
				paths = append(paths, sources.ShellPatchPath(cmd, s.cwd, p))
			}
		}
	}
	if action != coordActionNone {
		for _, rawP := range paths {
			cp := coordCanonicalPath(s.cwd, rawP)
			if cp == "" {
				continue
			}
			switch action { //nolint:exhaustive // coordActionNone is excluded above
			case coordActionEdit:
				s.edits[cp] = s.seq
			case coordActionRead:
				s.reads[cp] = s.seq
			}
		}
	}
	if id != "" && isCommandTool(name) && isCoordVerifyCommand(cmd) {
		s.pending[id] = coordPendingCall{toolName: name, cmd: cmd, seq: s.seq}
	}
}

func (s *transcriptBriefScanner) recordVerifyResult(id string, failed bool, outText string) {
	pc, okPending := s.pending[id]
	if !okPending {
		return
	}
	delete(s.pending, id)
	if failed || coordHasFriction(outText) {
		return
	}
	if norm := normalizeVerifyCmdForBrief(pc.cmd, s.cwd); norm != "" {
		s.verifyCmd = norm
	}
}

func (s *transcriptBriefScanner) inspectContentList(items []any, recErr bool) {
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch strings.ToLower(firstJSONString(block, "type")) {
		case "tool_use", "function_call":
			id := firstJSONString(block, "id", "call_id")
			name := firstJSONString(block, "name")
			args, _ := block["input"].(map[string]any)
			if args == nil {
				args, _ = block["arguments"].(map[string]any)
			}
			s.recordToolCall(id, name, args)
		case "tool_result", "function_call_output":
			id := firstJSONString(block, "tool_use_id", "call_id", "id")
			failed := recErr || jsonBool(block["is_error"]) || jsonBool(block["isError"])
			s.recordVerifyResult(id, failed, extractTranscriptResultText(block))
		}
	}
}

func (s *transcriptBriefScanner) inspectPayload(payload map[string]any, recErr bool) {
	switch strings.ToLower(firstJSONString(payload, "type")) {
	case "function_call":
		var args map[string]any
		switch v := payload["arguments"].(type) {
		case string:
			_ = json.Unmarshal([]byte(v), &args)
		case map[string]any:
			args = v
		}
		s.recordToolCall(firstJSONString(payload, "call_id", "id"), firstJSONString(payload, "name"), args)
	case "function_call_output":
		id := firstJSONString(payload, "call_id", "id")
		s.recordVerifyResult(id, recErr, firstJSONString(payload, "output"))
	}
}

func (s *transcriptBriefScanner) inspectLine(rawLine []byte) {
	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var doc map[string]any
	if unmarshalErr := json.Unmarshal(line, &doc); unmarshalErr != nil {
		return
	}
	recErr := jsonBool(doc["is_error"]) || jsonBool(doc["isError"])
	if msg, ok := doc["message"].(map[string]any); ok {
		if arr, isArr := msg["content"].([]any); isArr {
			s.inspectContentList(arr, recErr)
		}
	}
	if arr, isArr := doc["content"].([]any); isArr {
		s.inspectContentList(arr, recErr)
	}
	if payload, ok := doc["payload"].(map[string]any); ok {
		s.inspectPayload(payload, recErr)
	}
}

func scanTranscriptBriefTail(parentTranscript, cwd string) (map[string]int64, map[string]int64, string) {
	edits := map[string]int64{}
	reads := map[string]int64{}
	if strings.TrimSpace(parentTranscript) == "" {
		return edits, reads, ""
	}
	info, statErr := os.Stat(parentTranscript)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return edits, reads, ""
	}
	f, openErr := os.Open(parentTranscript)
	if openErr != nil {
		return edits, reads, ""
	}
	defer func() { _ = f.Close() }()

	offset := int64(0)
	if info.Size() > coordTranscriptTailBytes {
		offset = info.Size() - coordTranscriptTailBytes
		if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
			return edits, reads, ""
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
	if offset > 0 && !sc.Scan() {
		return edits, reads, ""
	}

	scanner := transcriptBriefScanner{
		cwd:     cwd,
		edits:   edits,
		reads:   reads,
		pending: map[string]coordPendingCall{},
	}
	for sc.Scan() {
		scanner.inspectLine(sc.Bytes())
	}
	return scanner.edits, scanner.reads, scanner.verifyCmd
}

func extractTranscriptResultText(block map[string]any) string {
	if s, ok := block["content"].(string); ok {
		return s
	}
	if s, ok := block["output"].(string); ok {
		return s
	}
	if arr, ok := block["content"].([]any); ok {
		var parts []string
		for _, item := range arr {
			if m, isMap := item.(map[string]any); isMap {
				if txt := firstJSONString(m, "text"); txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func sortBriefPaths(scores map[string]int64, exclude map[string]int64, limit int) []string {
	if len(scores) == 0 || limit <= 0 {
		return nil
	}
	list := make([]coordBriefTouch, 0, len(scores))
	for p, seq := range scores {
		if p == "" {
			continue
		}
		if exclude != nil {
			if _, excluded := exclude[p]; excluded {
				continue
			}
		}
		list = append(list, coordBriefTouch{path: p, seq: seq})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].seq != list[j].seq {
			return list[i].seq > list[j].seq
		}
		return list[i].path < list[j].path
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]string, len(list))
	for i, item := range list {
		out[i] = item.path
	}
	return out
}

// gatherParentBriefData collects the parent's recent edits, non-edited reads,
// and latest passing verify command from .coord and a bounded tail of its transcript.
func gatherParentBriefData(dir, parentID, parentTranscript, cwd string) coordBriefData {
	cEdits, cReads, cVerify, cVerifyAt := collectBriefFromCoord(dir, parentID)
	tEdits, tReads, tVerify := scanTranscriptBriefTail(parentTranscript, cwd)

	mergedEdits := make(map[string]int64, len(cEdits)+len(tEdits))
	// Bias .coord timestamps above raw 1-based transcript sequence offsets when both exist.
	maps.Copy(mergedEdits, tEdits)
	for p, ts := range cEdits {
		mergedEdits[p] = ts + 1000000
	}

	mergedReads := make(map[string]int64, len(cReads)+len(tReads))
	maps.Copy(mergedReads, tReads)
	for p, ts := range cReads {
		mergedReads[p] = ts + 1000000
	}

	verifyCmd := cVerify
	verifyAt := cVerifyAt
	if verifyCmd == "" && tVerify != "" {
		verifyCmd = tVerify
	}

	return coordBriefData{
		Edits:     sortBriefPaths(mergedEdits, nil, coordBriefMaxEdits),
		Reads:     sortBriefPaths(mergedReads, mergedEdits, coordBriefMaxReads),
		VerifyCmd: verifyCmd,
		VerifyAt:  verifyAt,
	}
}

func renderBriefLines(cwd string, edits, reads []string, verifyCmd string) string {
	var lines []string
	lines = append(lines, "Parent session context:")
	if len(edits) > 0 {
		disp := make([]string, len(edits))
		for i, p := range edits {
			disp[i] = defuseHostTriggers(coordDisplayPath(cwd, p))
		}
		lines = append(lines, "- edited: "+strings.Join(disp, ", "))
	}
	if len(reads) > 0 {
		disp := make([]string, len(reads))
		for i, p := range reads {
			disp[i] = defuseHostTriggers(coordDisplayPath(cwd, p))
		}
		lines = append(lines, "- read: "+strings.Join(disp, ", "))
	}
	if verifyCmd != "" {
		lines = append(lines, "- verified: "+defuseHostTriggers(verifyCmd))
	}
	if len(lines) == 1 {
		return ""
	}
	return defuseHostTriggers(strings.Join(lines, "\n"))
}

func formatParentSubagentBrief(cwd string, data coordBriefData) string {
	if len(data.Edits) == 0 && len(data.Reads) == 0 && data.VerifyCmd == "" {
		return ""
	}
	edits := append([]string(nil), data.Edits...)
	reads := append([]string(nil), data.Reads...)
	verifyCmd := data.VerifyCmd

	out := renderBriefLines(cwd, edits, reads, verifyCmd)
	for len(out) > coordBriefMaxBytes && len(reads) > 0 {
		reads = reads[:len(reads)-1]
		out = renderBriefLines(cwd, edits, reads, verifyCmd)
	}
	for len(out) > coordBriefMaxBytes && len(edits) > 1 {
		edits = edits[:len(edits)-1]
		out = renderBriefLines(cwd, edits, reads, verifyCmd)
	}
	if len(out) > coordBriefMaxBytes && verifyCmd != "" {
		overhead := len(renderBriefLines(cwd, edits, reads, "x")) - 1
		budget := coordBriefMaxBytes - overhead
		if budget >= 16 {
			verifyCmd = truncateUTF8Bytes(verifyCmd, budget)
			out = renderBriefLines(cwd, edits, reads, verifyCmd)
		} else if len(edits) > 0 {
			out = renderBriefLines(cwd, edits, reads, "")
		}
	}
	if len(out) > coordBriefMaxBytes {
		out = truncateUTF8Bytes(out, coordBriefMaxBytes)
	}
	return out
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 3 {
		return ""
	}
	limit := maxBytes - 3
	n := 0
	for i, r := range s {
		w := utf8.RuneLen(r)
		if i+w > limit {
			break
		}
		n = i + w
	}
	return strings.TrimRight(s[:n], ", ") + "..."
}

func coordSubagentBriefKey(data coordBriefData) string {
	return coordShortHash(strings.Join(data.Edits, "\n") + "\x00" + strings.Join(data.Reads, "\n") + "\x00" + data.VerifyCmd)
}

// coordSubagentBriefForSpawn builds a concise live parent brief during a spawn
// PreToolUse call and records the brief fingerprint on the parent session so
// immediate start hooks for the spawned child can avoid duplicating it.
func coordSubagentBriefForSpawn(dir string, input toolHookInput, raw []byte) string {
	if dir == "" || recallIsOff() {
		return ""
	}
	view := parseCoordPayloadView(raw, hookProjectPath(input.CWD, input.WorkspaceRoots), input)
	if view.SessionID == "" || !coordPolicyAllows(view.CWD, view.TranscriptPath) {
		return ""
	}
	cwd := hookCWD(view.CWD)
	data := gatherParentBriefData(dir, view.SessionID, view.TranscriptPath, cwd)
	brief := formatParentSubagentBrief(cwd, data)
	if brief == "" {
		return ""
	}
	now := coordNow()
	nowUnix := now.UTC().Unix()
	key := coordSubagentBriefKey(data)

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	sess := ensureCoordSession(&st, view.SessionID, nowUnix)
	if wt, _ := resolveCoordGitWorktree(cwd); wt != "" {
		sess.Worktree = wt
	}
	sess.LastSpawnBriefKey = key
	sess.LastSpawnBriefAt = nowUnix
	saveCoordState(dir, st)

	return brief
}

// coordLinkParent records parent-child lineage in .coord so Active File Radar
// excludes parent-child handoffs without waiting for an index rebuild.
func coordLinkParent(dir, childID, parentID, cwd string) {
	childID = strings.TrimSpace(childID)
	parentID = strings.TrimSpace(parentID)
	if dir == "" || recallIsOff() || childID == "" || parentID == "" || childID == parentID || !coordPolicyAllows(cwd, "") {
		return
	}
	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	child := ensureCoordSession(&st, childID, nowUnix)
	child.ParentID = parentID
	if wt, _ := resolveCoordGitWorktree(cwd); wt != "" {
		child.Worktree = wt
	}
	saveCoordState(dir, st)
}

func coordParentWorkedSince(parent *coordSession, sinceUnix int64) bool {
	if parent == nil {
		return false
	}
	if parent.VerifyAt > sinceUnix {
		return true
	}
	for _, t := range parent.Edits {
		if t.AtUnix > sinceUnix {
			return true
		}
	}
	for _, t := range parent.Reads {
		if t.AtUnix > sinceUnix {
			return true
		}
	}
	return false
}

// coordSubagentBriefForStart returns the parent session's live context brief
// when a child session starts (SessionStart parent_session_id or SubagentStart),
// deduplicating against both prior delivery to childID and an immediate spawn prompt brief.
func coordSubagentBriefForStart(dir, childID, parentID, parentTranscript, cwd string) string {
	childID = strings.TrimSpace(childID)
	parentID = strings.TrimSpace(parentID)
	if dir == "" || recallIsOff() || childID == "" || parentID == "" || childID == parentID || !coordPolicyAllows(cwd, parentTranscript) {
		return ""
	}
	data := gatherParentBriefData(dir, parentID, parentTranscript, cwd)
	brief := formatParentSubagentBrief(cwd, data)
	if brief == "" {
		return ""
	}
	key := coordSubagentBriefKey(data)
	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)

	child := st.Sessions[childID]
	if child != nil && child.BriefedAt > 0 {
		return ""
	}
	if parent := st.Sessions[parentID]; parent != nil && parent.LastSpawnBriefAt > 0 {
		delta := nowUnix - parent.LastSpawnBriefAt
		sameOrUnchanged := parent.LastSpawnBriefKey == key || !coordParentWorkedSince(parent, parent.LastSpawnBriefAt)
		if delta >= 0 && delta <= int64(coordSpawnDedupWindow.Seconds()) && sameOrUnchanged {
			childSess := ensureCoordSession(&st, childID, nowUnix)
			childSess.ParentID = parentID
			childSess.BriefedAt = nowUnix
			saveCoordState(dir, st)
			return ""
		}
	}

	childSess := ensureCoordSession(&st, childID, nowUnix)
	childSess.ParentID = parentID
	if wt, _ := resolveCoordGitWorktree(cwd); wt != "" {
		childSess.Worktree = wt
	}
	childSess.BriefedAt = nowUnix
	saveCoordState(dir, st)

	return brief
}

// coordRecordAntigravitySteps records file reads/edits from earlier steps in an
// Antigravity transcript tail plus passing verify commands without pre-recording
// the latest step's EditCalls (which editFileCallLines sends through runHookTool).
func coordRecordAntigravitySteps(dir, workspace, conversationID string, steps []transcriptStep) {
	if dir == "" || recallIsOff() || strings.TrimSpace(conversationID) == "" || len(steps) == 0 || !coordPolicyAllows(workspace, "") {
		return
	}
	latestCallStepIdx := -1
	for i, step := range steps {
		if step.Type == "USER_INPUT" {
			break
		}
		if calls, ok := step.ToolCalls.([]any); ok && len(calls) > 0 {
			latestCallStepIdx = i
			break
		}
	}

	var edits []string
	var reads []string
	var verifyCmd string
	// steps are ordered newest-first; scan oldest-to-newest after skipping the latest tool-call step.
	for i, step := range slices.Backward(steps) {
		if i == latestCallStepIdx {
			continue
		}
		calls, ok := step.ToolCalls.([]any)
		if !ok || len(calls) == 0 {
			continue
		}
		for _, ec := range sources.AntigravityEditCalls(calls) {
			cp := coordCanonicalPath(workspace, ec.FilePath)
			if cp == "" {
				continue
			}
			if sources.IsCompactionEditTool(ec.ToolName) {
				edits = append(edits, cp)
			} else {
				reads = append(reads, cp)
			}
		}
		for _, rawCall := range calls {
			call, isMap := rawCall.(map[string]any)
			if !isMap {
				continue
			}
			name, _ := call["name"].(string)
			args, _ := call["args"].(map[string]any)
			if name == "run_command" && args != nil {
				cmd := firstJSONString(args, "CommandLine", "Cmd", "command", "cmd")
				if cmd != "" && isCoordVerifyCommand(cmd) && !coordHasFriction(step.Content) {
					if norm := normalizeVerifyCmdForBrief(cmd, workspace); norm != "" {
						verifyCmd = norm
					}
				}
			}
		}
	}

	if len(edits) == 0 && len(reads) == 0 && verifyCmd == "" {
		return
	}

	wt, _ := resolveCoordGitWorktree(workspace)
	now := coordNow()
	nowUnix := now.UTC().Unix()

	release := holdCoordLock(dir)
	defer release()

	st := loadCoordState(dir)
	pruneCoordState(&st, now)
	sess := ensureCoordSession(&st, conversationID, nowUnix)
	if wt != "" {
		sess.Worktree = wt
	}
	if len(edits) > 0 {
		if sess.Edits == nil {
			sess.Edits = map[string]coordFileTouch{}
		}
		for _, p := range edits {
			sess.Edits[p] = coordFileTouch{AtUnix: nowUnix}
		}
	}
	if len(reads) > 0 {
		if sess.Reads == nil {
			sess.Reads = map[string]coordFileTouch{}
		}
		for _, p := range reads {
			sess.Reads[p] = coordFileTouch{AtUnix: nowUnix}
		}
	}
	if verifyCmd != "" {
		sess.VerifyCmd = verifyCmd
		sess.VerifyAt = nowUnix
	}
	pruneCoordState(&st, now)
	saveCoordState(dir, st)
}
