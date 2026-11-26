package harness

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

var _ Spec = (*YAMLSectionSpec)(nil)

// YAMLProbeInfo summarizes deja entries inspected within a YAML mapping section.
type YAMLProbeInfo struct {
	DejaKeys    []string
	Disabled    bool
	DisabledFor []string
}

// YAMLSectionSpec manages deja entries inside a top-level YAML mapping section
// (for example Goose's "extensions:" or Hermes's "mcp_servers:").
type YAMLSectionSpec struct {
	HarnessName        string
	Path               string
	SectionKey         string
	EntryKey           string
	DefaultIndent      string
	DropEmptySection   bool
	RespectDocEnd      bool
	PadAppendedSection bool
	CheckInlineValue   bool
	ContainsExactEntry bool
	SequenceMessage    string
	Ops                *Ops

	BuildEntry     func(indent string, disabled bool) string
	Inspect        func(doc string) YAMLProbeInfo
	EditExisting   func(doc string) (next string, handled bool, disabled bool, note string, err error)
	CheckRemovable func(doc string) error
	RemoveEntries  func(doc string) string
	OtherDejaNames func(doc string) []string
	FormatNote     func(baseNote string, disabled bool, otherNames []string) string

	mem *memoryState
}

// Name returns the harness name.
func (s *YAMLSectionSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the YAML config file path.
func (s *YAMLSectionSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the YAML config file exists on disk.
func (s *YAMLSectionSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

func (s *YAMLSectionSpec) sectionKey() string {
	k := strings.TrimSpace(s.SectionKey)
	if k == "" {
		return "mcp_servers:"
	}
	if !strings.HasSuffix(k, ":") {
		return k + ":"
	}
	return k
}

func (s *YAMLSectionSpec) sectionName() string {
	return strings.TrimSuffix(s.sectionKey(), ":")
}

func (s *YAMLSectionSpec) entryKey() string {
	if strings.TrimSpace(s.EntryKey) == "" {
		return "deja"
	}
	return s.EntryKey
}

func (s *YAMLSectionSpec) defaultIndent() string {
	if s.DefaultIndent == "" {
		return "  "
	}
	return s.DefaultIndent
}

func (s *YAMLSectionSpec) wrapYAMLErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", s.Path, err)
}

func (s *YAMLSectionSpec) sequenceErr() error {
	if s.SequenceMessage != "" {
		return s.wrapYAMLErr(fmt.Errorf("%s", s.SequenceMessage))
	}
	return s.wrapYAMLErr(fmt.Errorf("%s holds a list, and cannot take a mapping entry — convert it to a map or remove the list first", s.sectionKey()))
}

// Probe parses top-level YAML structure and reports whether deja is wired in the section.
func (s *YAMLSectionSpec) Probe() (ProbeResult, error) {
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
	body := string(normalizeLF(old))
	at, err := yamlTopKeyEnd(ensureTrailingLF(body), s.sectionKey())
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, s.wrapYAMLErr(err)
	}
	if at >= 0 && yamlBlockIsSequence(ensureTrailingLF(body)[at:]) {
		return ProbeResult{Path: s.Path, Present: true}, s.sequenceErr()
	}
	info := s.inspectDoc(body)
	res.DejaKeys = info.DejaKeys
	res.Wired = len(info.DejaKeys) > 0
	res.Disabled = info.Disabled
	res.DisabledFor = info.DisabledFor
	return res, nil
}

// Wire inserts or updates deja's entry in the target YAML section.
func (s *YAMLSectionSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	lfOld := string(normalizeLF(old))
	secKey := s.sectionKey()

	if _, kerr := yamlTopKeyEnd(ensureTrailingLF(lfOld), secKey); kerr != nil {
		return WireResult{}, s.wrapYAMLErr(kerr)
	}
	if s.EditExisting != nil {
		updated, handled, disabled, baseNote, eerr := s.EditExisting(lfOld)
		if eerr != nil {
			return WireResult{}, s.wrapYAMLErr(eerr)
		}
		if handled {
			next := keepTrailingNewline(lfOld, updated)
			note := s.computeNote(baseNote, disabled, next)
			out := matchLineEndings(old, []byte(next))
			action, werr := ops.writeConfig(s.Path, old, out)
			if werr != nil {
				return WireResult{}, ops.formatErr(s.Path, werr)
			}
			return s.wireResult(action, disabled, disabledFor(disabled), note), nil
		}
	}

	if s.CheckRemovable != nil {
		if rerr := s.CheckRemovable(lfOld); rerr != nil {
			return WireResult{}, s.wrapYAMLErr(rerr)
		}
	}
	preInfo := s.inspectDoc(lfOld)
	disabled := preInfo.Disabled
	next := s.removeFromDoc(lfOld)
	if s.DropEmptySection {
		next = dropEmptyYAMLKey(next, secKey)
	}

	pad := yamlBlockIndent(yamlKeyBlock(next, secKey))
	if pad == "" {
		pad = s.defaultIndent()
	}
	entry := s.renderEntry(pad, disabled)
	if s.ContainsExactEntry && strings.Contains(lfOld, entry) {
		note := s.computeNote("", disabled, lfOld)
		return s.wireResult(ActionUnchanged, disabled, disabledFor(disabled), note), nil
	}
	if s.CheckInlineValue {
		if v := yamlInlineValue(next, secKey); v != "" {
			return WireResult{}, s.wrapYAMLErr(fmt.Errorf("%s %s is on one line, and deja edits the block form — move it to a block and run this again", secKey, v))
		}
	}
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	at, err := yamlTopKeyEnd(next, secKey)
	if err != nil {
		return WireResult{}, s.wrapYAMLErr(err)
	}
	if at >= 0 {
		if yamlBlockIsSequence(next[at:]) {
			return WireResult{}, s.sequenceErr()
		}
		next = next[:at] + entry + next[at:]
	} else {
		next = s.appendNewSection(next, secKey, entry, ops)
	}
	ops.noteCreatedDirs(filepath.Dir(s.Path))
	next = keepTrailingNewline(lfOld, next)
	note := s.computeNote("", disabled, next)
	out := matchLineEndings(old, []byte(next))
	action, err := ops.writeConfig(s.Path, old, out)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	return s.wireResult(action, disabled, disabledFor(disabled), note), nil
}

// Unwire removes deja's entries from the YAML section, dropping empty generated sections as configured.
func (s *YAMLSectionSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	lfOld := string(normalizeLF(old))
	if s.CheckRemovable != nil {
		if rerr := s.CheckRemovable(lfOld); rerr != nil {
			return UnwireResult{}, s.wrapYAMLErr(rerr)
		}
	}
	secKey := s.sectionKey()
	info := s.inspectDoc(lfOld)
	next := s.removeFromDoc(lfOld)
	if s.DropEmptySection {
		next = dropEmptyYAMLKey(next, secKey)
	} else if ops.wasBlockAdded(s.Path, s.sectionName()) {
		if trimmed := dropEmptyYAMLKey(next, secKey); trimmed != next {
			next = trimmed
			ops.forgetBlockAdded(s.Path, s.sectionName())
		}
	}
	next = keepTrailingNewline(lfOld, next)
	out := matchLineEndings(old, []byte(next))
	action, err := ops.writeConfig(s.Path, old, out)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	res := UnwireResult{Path: s.Path, Action: action}
	if action != ActionUnchanged {
		res.RemovedKeys = info.DejaKeys
	} else {
		res.LeftKeys = info.DejaKeys
	}
	return res, nil
}

func (s *YAMLSectionSpec) wireResult(action string, disabled bool, disFor []string, note string) WireResult {
	return WireResult{
		Path:              s.Path,
		Action:            action,
		PreservedDisabled: disabled,
		DisabledFor:       disFor,
		Note:              note,
	}
}

func (s *YAMLSectionSpec) appendNewSection(next, secKey, entry string, ops resolvedOps) string {
	section := secKey + "\n" + entry
	if s.RespectDocEnd {
		if end := findYAMLDocEndOffset(next); end >= 0 {
			out := next[:end] + section + next[end:]
			if !s.DropEmptySection {
				ops.recordBlockAdded(s.Path, s.sectionName())
			}
			return out
		}
	}
	if s.PadAppendedSection {
		section = "\n" + section
	}
	if !s.DropEmptySection {
		ops.recordBlockAdded(s.Path, s.sectionName())
	}
	return next + section
}

func (s *YAMLSectionSpec) renderEntry(indent string, disabled bool) string {
	if s.BuildEntry != nil {
		return s.BuildEntry(indent, disabled)
	}
	en := "true"
	if disabled {
		en = "false"
	}
	return indent + s.entryKey() + ":\n" + indent + "  enabled: " + en + "\n"
}

func (s *YAMLSectionSpec) inspectDoc(doc string) YAMLProbeInfo {
	if s.Inspect != nil {
		return s.Inspect(doc)
	}
	return inspectYAMLSection(doc, s.sectionKey(), s.entryKey())
}

func (s *YAMLSectionSpec) removeFromDoc(doc string) string {
	if s.RemoveEntries != nil {
		return s.RemoveEntries(doc)
	}
	return removeYAMLSectionEntry(doc, s.sectionKey(), s.entryKey())
}

func (s *YAMLSectionSpec) computeNote(baseNote string, disabled bool, doc string) string {
	var others []string
	if s.OtherDejaNames != nil {
		others = s.OtherDejaNames(doc)
	}
	if s.FormatNote != nil {
		return s.FormatNote(baseNote, disabled, others)
	}
	if baseNote != "" {
		return baseNote
	}
	if disabled {
		return "kept disabled in config"
	}
	return ""
}

func disabledFor(disabled bool) []string {
	if disabled {
		return []string{"enabled"}
	}
	return nil
}

func findYAMLDocEndOffset(doc string) int {
	end, at := -1, 0
	for _, line := range strings.SplitAfter(doc, "\n") {
		if yamlDocumentEnd(line) {
			end = at
		}
		at += len(line)
	}
	return end
}

// yamlKeyLine splits a "key: value" line at the mapping colon outside quotes.
func yamlKeyLine(line string) (key, rest string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if t == "" || t[0] == '-' || t[0] == '#' {
		return "", "", false
	}
	quote := byte(0)
	for i := range len(t) {
		switch c := t[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ':' && (i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t'):
			return t[:i], t[i+1:], true
		}
	}
	return "", "", false
}

func yamlKeyName(key string) string {
	return strings.Trim(strings.TrimSpace(key), `"'`)
}

func stripYAMLComment(v string) string {
	quote := byte(0)
	for i := range len(v) {
		switch c := v[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return v[:i]
		}
	}
	return v
}

func yamlDocumentEnd(line string) bool {
	return strings.TrimRight(stripYAMLComment(strings.TrimRight(line, "\r\n")), " \t") == "..."
}

func yamlIndentWidth(line string) int {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || trimmed == "\r" || trimmed == "\n" {
		return len(line)
	}
	return len(line) - len(trimmed)
}

func yamlLineBelongsTo(line, parent string) bool {
	return yamlIndentWidth(line) > yamlIndentWidth(parent)
}

func yamlExactKeyLine(line, key string) bool {
	line = strings.TrimRight(line, "\r\n")
	if i := strings.Index(line, "#"); i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
		line = line[:i]
	}
	return strings.TrimSpace(line) == key
}

func yamlTopKeyEnd(doc, key string) (int, error) {
	name := strings.TrimSuffix(key, ":")
	at, found := 0, -1
	for _, line := range strings.SplitAfter(doc, "\n") {
		start := at
		at += len(line)
		if yamlIndentWidth(line) != 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if yamlExactKeyLine(line, key) {
			if found >= 0 {
				return -1, fmt.Errorf("%s is written twice at the top level, and only the last one is read — keep one and run this again", key)
			}
			found = start + len(line)
			continue
		}
		head, rest, ok := strings.Cut(line, ":")
		if !ok || yamlKeyName(head) != name {
			continue
		}
		v := strings.TrimSpace(stripYAMLComment(rest))
		switch strings.ToLower(v) {
		case "{}", "[]", "~", "null", "''", `""`:
			continue
		}
		return -1, fmt.Errorf("%s is written as %q, and deja edits only the block form `%s` on a line of its own — rewrite it that way and run this again", key, strings.TrimSpace(line), key)
	}
	if found < 0 {
		return -1, nil
	}
	for scan := found; scan < len(doc); {
		end := strings.IndexByte(doc[scan:], '\n')
		if end < 0 {
			break
		}
		line := doc[scan : scan+end]
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			if yamlIndentWidth(line) > 0 {
				found = scan
			}
			break
		}
		scan += end + 1
		if t != "" {
			found = scan
		}
	}
	return found, nil
}

func yamlKeyBlock(s, key string) string {
	at := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		at += len(line)
		if yamlIndentWidth(line) == 0 && yamlExactKeyLine(line, key) {
			if !strings.HasSuffix(line, "\n") {
				return ""
			}
			return s[at:]
		}
	}
	return ""
}

func yamlBlockIndent(block string) string {
	for line := range strings.SplitSeq(block, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		return indent
	}
	return ""
}

func yamlInlineValue(doc, key string) string {
	for line := range strings.SplitSeq(doc, "\n") {
		if !strings.HasPrefix(line, key) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, key))
		if rest == "" || strings.HasPrefix(rest, "#") {
			return ""
		}
		return rest
	}
	return ""
}

func yamlBlockIsSequence(block string) bool {
	for line := range strings.SplitSeq(block, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return false
		}
		return strings.HasPrefix(t, "- ")
	}
	return false
}

func dropEmptyYAMLKey(s, key string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " ") != key {
			out = append(out, lines[i])
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		k := j
		for k < len(lines) && (strings.TrimSpace(lines[k]) == "" || strings.HasPrefix(strings.TrimSpace(lines[k]), "#")) {
			k++
		}
		if k > j && k < len(lines) && yamlLineBelongsTo(lines[k], lines[i]) {
			out = append(out, lines[i])
			continue
		}
		if j < len(lines) && yamlLineBelongsTo(lines[j], lines[i]) {
			out = append(out, lines[i])
			continue
		}
		i = j - 1
	}
	return strings.Join(out, "\n")
}

func keepTrailingNewline(old, next string) string {
	if strings.HasSuffix(old, "\n") && next != "" && !strings.HasSuffix(next, "\n") {
		return next + "\n"
	}
	return next
}

func inspectYAMLSection(doc, secKey, entryKey string) YAMLProbeInfo {
	lines := strings.Split(doc, "\n")
	var info YAMLProbeInfo
	inSec, childW, inEntry, fieldW := false, -1, false, -1
	curKey := ""
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(line)
		if w == 0 {
			inSec = yamlExactKeyLine(line, secKey)
			childW, inEntry, fieldW, curKey = -1, false, -1, ""
			continue
		}
		if !inSec {
			continue
		}
		if childW < 0 {
			childW = w
		}
		key, rest, ok := yamlKeyLine(line)
		if w <= childW {
			inEntry, fieldW, curKey = false, -1, ""
			if w == childW && ok && yamlKeyName(key) == entryKey && strings.TrimSpace(stripYAMLComment(rest)) == "" {
				inEntry = true
				curKey = entryKey
				info.DejaKeys = appendUnique(info.DejaKeys, entryKey)
			}
			continue
		}
		if !inEntry || !ok || curKey == "" {
			continue
		}
		if fieldW < 0 {
			fieldW = w
		}
		if w != fieldW {
			continue
		}
		if yamlKeyName(key) == "enabled" && strings.EqualFold(strings.TrimSpace(stripYAMLComment(rest)), "false") {
			info.Disabled = true
			info.DisabledFor = appendUnique(info.DisabledFor, "enabled")
		}
	}
	return info
}

func removeYAMLSectionEntry(doc, secKey, entryKey string) string {
	lines := strings.Split(doc, "\n")
	out := make([]string, 0, len(lines))
	inSec := false
	childW := -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			w := yamlIndentWidth(line)
			if w == 0 {
				inSec = yamlExactKeyLine(line, secKey)
				childW = -1
			} else if inSec {
				if childW < 0 {
					childW = w
				}
				if w == childW {
					key, rest, ok := yamlKeyLine(line)
					if ok && yamlKeyName(key) == entryKey && strings.TrimSpace(stripYAMLComment(rest)) == "" {
						j := i + 1
						for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || yamlIndentWidth(lines[j]) > w) {
							j++
						}
						for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
							j--
						}
						i = j - 1
						continue
					}
				}
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
