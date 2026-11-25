package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var _ Spec = (*JSONHooksSpec)(nil)

// JSONHooksSpec modes.
const (
	JSONHooksEventMap = "event-map"
	JSONHooksFlatList = "flat-list"
)

// JSONHookRule defines one hook wiring entry managed by JSONHooksSpec.
type JSONHookRule struct {
	Name          string
	Event         string
	Matcher       string
	Command       string
	Timeout       int
	MaxTimeout    int
	AdoptTimeout  bool
	StatusMessage string
}

// JSONHooksSpec manages hook registrations in either an event-keyed object
// ("event-map", e.g. Codex/Qwen/Gemini) or a flat typed list ("flat-list", e.g. Kiro).
type JSONHooksSpec struct {
	HarnessName        string
	Path               string
	Mode               string
	Rules              []JSONHookRule
	Retired            map[string]string
	Version            string
	RefuseJSONC        bool
	KeepHooksContainer bool
	RewriteMatcher     bool
	OwnedMarker        string
	ForeignNote        string
	RemoveWhenEmpty    bool
	PruneEmptyParents  int
	Ops                *Ops

	IsCommandMatch func(actual, wantCmd string) (matched, isDirect bool)
	IsAnyDejaHook  func(entry map[string]any) bool
	AfterRemove    func(path string) error

	mem *memoryState
}

// Name returns the harness name.
func (s *JSONHooksSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the JSON hooks file path.
func (s *JSONHooksSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the JSON hooks file exists on disk.
func (s *JSONHooksSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

func (s *JSONHooksSpec) mode() string {
	if s.Mode == JSONHooksFlatList {
		return JSONHooksFlatList
	}
	return JSONHooksEventMap
}

func (s *JSONHooksSpec) version() string {
	if s.Version == "" {
		return "v1"
	}
	return s.Version
}

func (s *JSONHooksSpec) matchCmd(actual, want string) (bool, bool) {
	if s.IsCommandMatch != nil {
		return s.IsCommandMatch(actual, want)
	}
	return defaultMatchHookCommand(actual, want)
}

func (s *JSONHooksSpec) entryHasCmd(entry map[string]any, want string) bool {
	if entry == nil {
		return false
	}
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h == nil || h["type"] != "command" {
			continue
		}
		cmd, _ := h["command"].(string)
		if matched, _ := s.matchCmd(cmd, want); matched {
			return true
		}
	}
	return false
}

func (s *JSONHooksSpec) entryIsAnyDeja(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	if s.IsAnyDejaHook != nil {
		return s.IsAnyDejaHook(entry)
	}
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h == nil {
			continue
		}
		cmd, _ := h["command"].(string)
		if defaultMatchHookAnyDeja(cmd) {
			return true
		}
	}
	return false
}

// Probe parses the JSON hooks file and reports wired rule keys.
func (s *JSONHooksSpec) Probe() (ProbeResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	det, err := detectPath(s.Path)
	if err != nil {
		return ProbeResult{Path: s.Path}, err
	}
	res := ProbeResult{Path: s.Path, Present: det.Present}
	if !det.Present {
		return res, nil
	}
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, ops.formatErr(s.Path, err)
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return res, nil
	}
	if s.mode() == JSONHooksFlatList && s.OwnedMarker != "" && !strings.Contains(string(old), s.OwnedMarker) {
		return res, nil
	}
	root, _, err := s.parseRoot(old)
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, ops.formatErr(s.Path, err)
	}
	if s.mode() == JSONHooksFlatList {
		items, _ := root["hooks"].([]any)
		for _, itemAny := range items {
			item, _ := itemAny.(map[string]any)
			if item == nil {
				continue
			}
			if rule, ok := s.matchFlatItem(item); ok {
				label := rule.Name
				if label == "" {
					label = rule.Event
				}
				res.DejaKeys = appendUnique(res.DejaKeys, label)
			}
		}
		res.Wired = len(res.DejaKeys) > 0
		return res, nil
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, r := range s.Rules {
		entries, _ := hooks[r.Event].([]any)
		for _, eAny := range entries {
			if e, _ := eAny.(map[string]any); e != nil && s.entryHasCmd(e, r.Command) {
				res.DejaKeys = appendUnique(res.DejaKeys, r.Event)
				break
			}
		}
	}
	res.Wired = len(res.DejaKeys) > 0
	return res, nil
}

// Wire inserts or adopts deja hook rules in the target JSON file.
func (s *JSONHooksSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	if s.mode() == JSONHooksFlatList {
		return s.wireFlatList(ops, old)
	}
	return s.wireEventMap(ops, old)
}

// Unwire removes deja hook rules from the target JSON file.
func (s *JSONHooksSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	if s.mode() == JSONHooksFlatList {
		return s.unwireFlatList(ops, old)
	}
	return s.unwireEventMap(ops, old)
}

func (s *JSONHooksSpec) parseRoot(old []byte) (map[string]any, string, error) {
	if len(bytes.TrimSpace(old)) == 0 {
		return map[string]any{}, "", nil
	}
	if s.RefuseJSONC && isJSONC(old) {
		strict, err := toStrictJSON(old)
		if err != nil {
			return nil, "", err
		}
		var root map[string]any
		if uerr := json.Unmarshal(strict, &root); uerr != nil {
			return nil, "", uerr
		}
		if root == nil {
			root = map[string]any{}
		}
		canon, merr := json.Marshal(root)
		if merr != nil {
			return nil, "", merr
		}
		return root, string(canon), nil
	}
	var root map[string]any
	if uerr := json.Unmarshal(old, &root); uerr != nil {
		return nil, "", uerr
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, "", nil
}

func cloneBeforeRoot(old []byte, strictJSONC bool) map[string]any {
	if len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	src := old
	if strictJSONC && isJSONC(old) {
		if s, err := toStrictJSON(old); err == nil {
			src = s
		}
	}
	var before map[string]any
	_ = json.Unmarshal(src, &before)
	return before
}

func (s *JSONHooksSpec) wireEventMap(ops resolvedOps, old []byte) (WireResult, error) {
	hasComments := s.RefuseJSONC && len(bytes.TrimSpace(old)) > 0 && isJSONC(old)
	root, beforeCanon, err := s.parseRoot(old)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	beforeRoot := cloneBeforeRoot(old, s.RefuseJSONC)
	s.applyEventMap(root, false)

	if hasComments {
		afterCanon, merr := json.Marshal(root)
		if merr != nil {
			return WireResult{}, ops.formatErr(s.Path, merr)
		}
		if string(afterCanon) == beforeCanon {
			return WireResult{Path: s.Path, Action: ActionUnchanged, BeforeRoot: beforeRoot, AfterRoot: root}, nil
		}
		return WireResult{}, fmt.Errorf("%s: deja cannot edit hooks in a file that carries comments — add or remove the hook by hand, or take the comments out", s.Path)
	}
	next, err := marshalHookJSON(ops, old, root)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	ops.noteCreatedDirs(filepath.Dir(s.Path))
	action, err := ops.writeConfig(s.Path, old, next)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	return WireResult{
		Path:       s.Path,
		Action:     action,
		BeforeRoot: beforeRoot,
		AfterRoot:  root,
	}, nil
}

func (s *JSONHooksSpec) unwireEventMap(ops resolvedOps, old []byte) (UnwireResult, error) {
	if len(bytes.TrimSpace(old)) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	hasComments := s.RefuseJSONC && isJSONC(old)
	root, beforeCanon, err := s.parseRoot(old)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	beforeRoot := cloneBeforeRoot(old, s.RefuseJSONC)
	removed := s.applyEventMap(root, true)

	if hasComments {
		afterCanon, merr := json.Marshal(root)
		if merr != nil {
			return UnwireResult{}, ops.formatErr(s.Path, merr)
		}
		if string(afterCanon) == beforeCanon {
			return UnwireResult{Path: s.Path, Action: ActionUnchanged, BeforeRoot: beforeRoot, AfterRoot: root}, nil
		}
		return UnwireResult{}, fmt.Errorf("%s: deja cannot edit hooks in a file that carries comments — add or remove the hook by hand, or take the comments out", s.Path)
	}
	next, err := marshalHookJSON(ops, old, root)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	action, err := ops.writeConfig(s.Path, old, next)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	res := UnwireResult{
		Path:       s.Path,
		Action:     action,
		BeforeRoot: beforeRoot,
		AfterRoot:  root,
	}
	if action != ActionUnchanged {
		res.RemovedKeys = removed
	}
	return res, nil
}

func (s *JSONHooksSpec) applyEventMap(root map[string]any, uninstall bool) []string {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		if uninstall {
			return nil
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	var touched []string
	for _, r := range s.retireEventsSorted() {
		sub := s.Retired[r]
		oldList, _ := hooks[r].([]any)
		var survivors []any
		dropped := false
		for _, entryAny := range oldList {
			entry, _ := entryAny.(map[string]any)
			if entry != nil && sub == "" && s.entryIsAnyDeja(entry) {
				dropped = true
				continue
			}
			if entry != nil && sub != "" && s.entryHasCmd(entry, "deja "+sub) {
				dropped = true
				continue
			}
			survivors = append(survivors, entryAny)
		}
		if dropped {
			touched = appendUnique(touched, r)
		}
		if len(survivors) == 0 {
			delete(hooks, r)
		} else {
			hooks[r] = survivors
		}
	}
	for _, rule := range s.Rules {
		entries, _ := hooks[rule.Event].([]any)
		var kept []any
		found := false
		for _, entryAny := range entries {
			entry, _ := entryAny.(map[string]any)
			if entry != nil && s.entryHasCmd(entry, rule.Command) {
				if uninstall {
					touched = appendUnique(touched, rule.Event)
					continue
				}
				if found {
					continue
				}
				found = true
				s.adoptEntry(entry, rule)
			}
			kept = append(kept, entryAny)
		}
		if !uninstall && !found {
			kept = append(kept, newEventMapEntry(rule))
		}
		if len(kept) == 0 {
			delete(hooks, rule.Event)
		} else {
			hooks[rule.Event] = kept
		}
	}
	if !s.KeepHooksContainer && len(hooks) == 0 {
		delete(root, "hooks")
	}
	return touched
}

func (s *JSONHooksSpec) retireEventsSorted() []string {
	if len(s.Retired) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.Retired))
	for k := range s.Retired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *JSONHooksSpec) adoptEntry(entry map[string]any, rule JSONHookRule) {
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h == nil || h["type"] != "command" {
			continue
		}
		actual, _ := h["command"].(string)
		matched, isDirect := s.matchCmd(actual, rule.Command)
		if !matched || !isDirect {
			continue
		}
		h["command"] = rule.Command
		if rule.StatusMessage != "" {
			h["statusMessage"] = rule.StatusMessage
		}
		if rule.AdoptTimeout && rule.Timeout > 0 {
			h["timeout"] = rule.Timeout
		} else if rule.MaxTimeout > 0 {
			if t, ok := h["timeout"].(float64); !ok || t > float64(rule.MaxTimeout) {
				h["timeout"] = rule.MaxTimeout
			}
		}
	}
	if s.RewriteMatcher || len(hs) == 1 {
		if rule.Matcher == "" {
			delete(entry, "matcher")
		} else {
			entry["matcher"] = rule.Matcher
		}
	}
}

func newEventMapEntry(rule JSONHookRule) map[string]any {
	h := map[string]any{"type": "command", "command": rule.Command}
	if rule.Timeout > 0 {
		h["timeout"] = rule.Timeout
	}
	if rule.StatusMessage != "" {
		h["statusMessage"] = rule.StatusMessage
	}
	entry := map[string]any{"hooks": []any{h}}
	if rule.Matcher != "" {
		entry["matcher"] = rule.Matcher
	}
	return entry
}

func (s *JSONHooksSpec) wireFlatList(ops resolvedOps, old []byte) (WireResult, error) {
	det, _ := detectPath(s.Path)
	if s.OwnedMarker != "" && (len(bytes.TrimSpace(old)) > 0 || det.Present) && !strings.Contains(string(old), s.OwnedMarker) {
		return WireResult{Path: s.Path, Action: ActionUnchanged, Note: s.ForeignNote}, nil
	}
	root, _, err := s.parseRoot(old)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	existing, _ := root["hooks"].([]any)
	var kept []any
	for _, itemAny := range existing {
		item, _ := itemAny.(map[string]any)
		if item == nil {
			continue
		}
		if _, isOurs := s.matchFlatItem(item); !isOurs {
			kept = append(kept, item)
		}
	}
	for _, rule := range s.Rules {
		kept = append(kept, newFlatListItem(rule))
	}
	if _, ok := root["version"]; !ok {
		root["version"] = s.version()
	}
	root["hooks"] = kept
	next, err := marshalHookJSON(ops, old, root)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	ops.noteCreatedDirs(filepath.Dir(s.Path))
	action, err := ops.writeConfig(s.Path, old, next)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	return WireResult{Path: s.Path, Action: action}, nil
}

func (s *JSONHooksSpec) unwireFlatList(ops resolvedOps, old []byte) (UnwireResult, error) {
	det, _ := detectPath(s.Path)
	if s.OwnedMarker != "" && (len(bytes.TrimSpace(old)) > 0 || det.Present) && !strings.Contains(string(old), s.OwnedMarker) {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	root, _, err := s.parseRoot(old)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	existing, _ := root["hooks"].([]any)
	var kept []any
	var removed []string
	for _, itemAny := range existing {
		item, _ := itemAny.(map[string]any)
		if item == nil {
			continue
		}
		if rule, isOurs := s.matchFlatItem(item); isOurs {
			label := rule.Name
			if label == "" {
				label = rule.Event
			}
			removed = appendUnique(removed, label)
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 && s.RemoveWhenEmpty {
		if rerr := ops.removeFile(s.Path); rerr != nil {
			return UnwireResult{}, ops.formatErr(s.Path, rerr)
		}
		if s.AfterRemove != nil {
			if aerr := s.AfterRemove(s.Path); aerr != nil {
				return UnwireResult{}, aerr
			}
		}
		ops.pruneDir(filepath.Dir(s.Path), s.PruneEmptyParents)
		return UnwireResult{Path: s.Path, Action: ActionRemoved, RemovedKeys: removed}, nil
	}
	if len(removed) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	root["hooks"] = kept
	next, err := marshalHookJSON(ops, old, root)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	action, err := ops.writeConfig(s.Path, old, next)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	return UnwireResult{Path: s.Path, Action: action, RemovedKeys: removed}, nil
}

func (s *JSONHooksSpec) matchFlatItem(item map[string]any) (JSONHookRule, bool) {
	name, _ := item["name"].(string)
	trigger, _ := item["trigger"].(string)
	action, _ := item["action"].(map[string]any)
	cmd, _ := action["command"].(string)
	for _, rule := range s.Rules {
		if rule.Name != "" && name == rule.Name {
			return rule, true
		}
		if trigger == rule.Event && cmd != "" {
			if matched, _ := s.matchCmd(cmd, rule.Command); matched {
				return rule, true
			}
		}
	}
	if s.IsAnyDejaHook != nil {
		wrapped := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
		if s.IsAnyDejaHook(wrapped) {
			return JSONHookRule{Name: name, Event: trigger, Command: cmd}, true
		}
	} else if defaultMatchHookAnyDeja(cmd) {
		return JSONHookRule{Name: name, Event: trigger, Command: cmd}, true
	}
	return JSONHookRule{}, false
}

func newFlatListItem(rule JSONHookRule) map[string]any {
	return map[string]any{
		"name":    rule.Name,
		"trigger": rule.Event,
		"action":  map[string]any{"type": "command", "command": rule.Command},
	}
}

func marshalHookJSON(ops resolvedOps, old []byte, root map[string]any) ([]byte, error) {
	next, err := ops.marshalJSON(old, root)
	if err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(next, []byte("\n")) {
		next = append(next, '\n')
	}
	return next, nil
}

func defaultMatchHookCommand(actual, wantCmd string) (bool, bool) {
	actualSub := hookCommandSubcommand(actual)
	wantSub := hookCommandSubcommand(wantCmd)
	if actualSub == "" || wantSub == "" || actualSub != wantSub {
		return false, false
	}
	toks := splitShellWordsSimple(actual)
	if len(toks) > 0 && isDejaBinaryToken(toks[0]) {
		return true, true
	}
	return true, false
}

func defaultMatchHookAnyDeja(cmd string) bool {
	return hookCommandSubcommand(cmd) != ""
}

func hookCommandSubcommand(cmd string) string {
	toks := splitShellWordsSimple(cmd)
	for i := range len(toks) - 1 {
		if isDejaBinaryToken(toks[i]) {
			sub := strings.TrimRight(toks[i+1], ";&|")
			if strings.HasPrefix(sub, "hook-") {
				return sub
			}
		}
	}
	return ""
}

func splitShellWordsSimple(s string) []string {
	var words []string
	var cur strings.Builder
	quote := byte(0)
	inWord := false
	for i := range len(s) {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			inWord = true
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
			continue
		}
		cur.WriteByte(c)
		inWord = true
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}
