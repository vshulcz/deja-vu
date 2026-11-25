package harness

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

var _ Spec = (*MarkedBlockSpec)(nil)

// MarkedBlockSpec manages marked blocks inside shared text, YAML, or TOML
// configuration files. Setting EndMarker selects paired-fence mode; otherwise
// LineMarker selects line-delimited block mode (such as Kimi's [[hooks]]).
type MarkedBlockSpec struct {
	HarnessName string
	Path        string

	// Paired-fence mode (when EndMarker != ""):
	StartMarker      string
	EndMarker        string
	MarkerSubject    string
	EmptyPlaceholder string
	PlaceholderNote  string
	HasEntry         func(doc string) bool
	IndentFunc       func(doc string) string
	RespectDocEnd    bool
	DocEndLine       func(line string) bool

	// Line-marker mode (when LineMarker != "" and EndMarker == ""):
	LineMarker   string
	TableHeader  string
	StopPrefixes []string

	// Shared generation, stripping, validation, and probing hooks:
	Block           string
	BuildBlock      func(oldDoc string) (block string, note string)
	PreValidateWire func(doc string) error
	ValidatePatched func(path string, old []byte, patchedDoc string) error
	IsDisabled      func(patchedDoc string) bool
	StripExtra      func(doc string) string
	IsWired         func(doc string) bool
	Ops             *Ops

	mem *memoryState
}

// Name returns the harness name.
func (s *MarkedBlockSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the managed config file path.
func (s *MarkedBlockSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the target config file exists on disk.
func (s *MarkedBlockSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

func (s *MarkedBlockSpec) isPairedMode() bool {
	return s.EndMarker != ""
}

func (s *MarkedBlockSpec) markerSubject() string {
	if s.MarkerSubject != "" {
		return s.MarkerSubject
	}
	return "deja's block in " + filepath.Base(s.Path)
}

func (s *MarkedBlockSpec) isDocEndLine(line string) bool {
	if s.DocEndLine != nil {
		return s.DocEndLine(line)
	}
	return yamlDocumentEnd(line)
}

func (s *MarkedBlockSpec) stopPrefixes() []string {
	if len(s.StopPrefixes) == 0 {
		return []string{"[", "#"}
	}
	return s.StopPrefixes
}

// CheckMarkers validates paired start/end fences in doc.
func (s *MarkedBlockSpec) CheckMarkers(doc string) error {
	if !s.isPairedMode() {
		return nil
	}
	start, end := -1, -1
	for i, line := range strings.Split(doc, "\n") {
		switch t := strings.TrimSpace(line); {
		case strings.HasPrefix(t, s.StartMarker):
			if start < 0 {
				start = i
			}
		case t == s.EndMarker:
			if end < 0 {
				end = i
			}
		}
	}
	subj := s.markerSubject()
	switch {
	case start < 0 && end < 0:
		return nil
	case start < 0:
		return fmt.Errorf("%s has no start marker — put it back, or delete the block entirely", subj)
	case end < 0:
		return fmt.Errorf("%s has no end marker — put it back, or delete the block entirely", subj)
	case end < start:
		return fmt.Errorf("%s has its end marker above its start marker — put them in order, or delete the block entirely", subj)
	}
	return nil
}

// Probe reads the target file and reports marker integrity and wiring state.
func (s *MarkedBlockSpec) Probe() (ProbeResult, error) {
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
	lfOld := string(normalizeLF(old))
	if s.isPairedMode() {
		if err := s.CheckMarkers(lfOld); err != nil {
			return ProbeResult{Path: s.Path, Present: true}, err
		}
		wired := strings.Contains(lfOld, s.StartMarker)
		if s.IsWired != nil {
			wired = s.IsWired(lfOld)
		}
		res.Wired = wired
		if wired {
			res.DejaKeys = []string{"deja"}
			if s.IsDisabled != nil && s.IsDisabled(lfOld) {
				res.Disabled = true
				res.DisabledFor = []string{"disabled"}
			}
		}
		return res, nil
	}
	wired := strings.Contains(lfOld, s.LineMarker)
	if s.IsWired != nil {
		wired = s.IsWired(lfOld)
	}
	res.Wired = wired
	if wired {
		res.DejaKeys = []string{"deja"}
	}
	return res, nil
}

// PlanWire computes the wired document bytes without writing to disk.
func (s *MarkedBlockSpec) PlanWire() (old, next []byte, note string, err error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err = ops.readConfig(s.Path)
	if err != nil {
		return nil, nil, "", ops.formatErr(s.Path, err)
	}
	lfOld := string(normalizeLF(old))
	if s.PreValidateWire != nil {
		if err := s.PreValidateWire(lfOld); err != nil {
			return nil, nil, "", ops.formatErr(s.Path, err)
		}
	}
	block := s.Block
	if s.BuildBlock != nil {
		block, note = s.BuildBlock(lfOld)
	}
	var patched string
	if s.isPairedMode() {
		patched, err = s.Patch(lfOld, block)
		if err != nil {
			return nil, nil, "", err
		}
		if note != "" && s.IsDisabled != nil && !s.IsDisabled(patched) {
			note = ""
		}
	} else {
		cleaned := lfOld
		if s.StripExtra != nil {
			cleaned = s.StripExtra(cleaned)
		}
		rest := strings.TrimRight(s.StripLines(cleaned), "\n")
		if rest != "" {
			rest += "\n\n"
		}
		patched = rest + block
	}
	if s.ValidatePatched != nil {
		if err := s.ValidatePatched(s.Path, old, patched); err != nil {
			return nil, nil, "", err
		}
	}
	return old, []byte(patched), note, nil
}

// PlanUnwire computes the unwired document bytes and reports whether a change is needed.
func (s *MarkedBlockSpec) PlanUnwire() (old, next []byte, changed bool, err error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err = ops.readConfig(s.Path)
	if err != nil {
		return nil, nil, false, ops.formatErr(s.Path, err)
	}
	lfOld := string(normalizeLF(old))
	if s.isPairedMode() {
		if len(old) == 0 || (!strings.Contains(lfOld, s.StartMarker) && !strings.Contains(lfOld, s.EndMarker)) {
			return old, old, false, nil
		}
		if err := s.CheckMarkers(lfOld); err != nil {
			return nil, nil, false, err
		}
		rest := s.stripPaired(lfOld)
		rest = s.maybeRestorePlaceholder(lfOld, rest)
		nextStr := rest + "\n"
		return old, []byte(nextStr), !bytes.Equal(old, matchLineEndings(old, []byte(nextStr))), nil
	}
	cleaned := lfOld
	if s.StripExtra != nil {
		cleaned = s.StripExtra(cleaned)
	}
	rest := strings.TrimRight(s.StripLines(cleaned), "\n")
	if rest != "" {
		rest += "\n"
	}
	return old, []byte(rest), rest != lfOld, nil
}

// Wire patches or writes the marked block into the target file.
func (s *MarkedBlockSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, next, note, err := s.PlanWire()
	if err != nil {
		return WireResult{}, err
	}
	ops.noteCreatedDirs(filepath.Dir(s.Path))
	out := matchLineEndings(old, next)
	action, err := ops.writeConfig(s.Path, old, out)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	disabled := s.IsDisabled != nil && s.IsDisabled(string(next))
	return WireResult{
		Path:              s.Path,
		Action:            action,
		PreservedDisabled: disabled,
		DisabledFor:       disabledFor(disabled),
		Note:              note,
	}, nil
}

// Unwire removes marked blocks from the target file, restoring any placeholder when appropriate.
func (s *MarkedBlockSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, next, changed, err := s.PlanUnwire()
	if err != nil {
		return UnwireResult{}, err
	}
	if !changed && bytes.Equal(old, next) {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	out := matchLineEndings(old, next)
	action, err := ops.writeConfig(s.Path, old, out)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	res := UnwireResult{Path: s.Path, Action: action}
	if action != ActionUnchanged {
		res.RemovedKeys = []string{"deja"}
	}
	return res, nil
}

// Patch replaces or inserts a paired-fence block in doc cleanly and idempotently.
func (s *MarkedBlockSpec) Patch(doc, block string) (string, error) {
	if err := s.CheckMarkers(doc); err != nil {
		return "", err
	}
	if s.PlaceholderNote != "" && s.EmptyPlaceholder != "" &&
		(hasLineEqualTrimmed(doc, s.EmptyPlaceholder) || strings.Contains(doc, s.PlaceholderNote)) {
		block = strings.Replace(block, s.StartMarker+"\n", s.StartMarker+"\n"+s.PlaceholderNote+"\n", 1)
	}
	if s.IndentFunc != nil {
		if pad := s.IndentFunc(doc); pad != "" {
			lines := strings.Split(block, "\n")
			for i := range lines {
				lines[i] = pad + lines[i]
			}
			block = strings.Join(lines, "\n")
		}
	}
	var out []string
	at, skipping := -1, false
	for line := range strings.SplitSeq(doc, "\n") {
		switch t := strings.TrimSpace(line); {
		case strings.HasPrefix(t, s.StartMarker):
			if at < 0 {
				at = len(out)
			}
			skipping = true
		case t == s.EndMarker:
			skipping = false
		case skipping:
		case s.EmptyPlaceholder != "" && t == s.EmptyPlaceholder:
		default:
			out = append(out, line)
		}
	}
	end := -1
	if s.RespectDocEnd {
		end = s.docEndIndex(out)
	}
	if at >= 0 && (end < 0 || at <= end) {
		out = append(out[:at], append([]string{block}, out[at:]...)...)
		return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n", nil
	}
	trimmed := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if trimmed == "" {
		return block + "\n", nil
	}
	if end >= 0 {
		head := strings.TrimRight(strings.Join(out[:end], "\n"), "\n")
		if head != "" {
			head += "\n"
		}
		tail := strings.TrimRight(strings.Join(out[end:], "\n"), "\n")
		return head + block + "\n" + tail + "\n", nil
	}
	return trimmed + "\n" + block + "\n", nil
}

// StripLines removes every line-marker block (LineMarker + optional TableHeader
// up to the next stop prefix line) from doc.
func (s *MarkedBlockSpec) StripLines(doc string) string {
	lines := strings.Split(doc, "\n")
	prefixes := s.stopPrefixes()
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != s.LineMarker {
			out = append(out, lines[i])
			continue
		}
		i++
		if s.TableHeader != "" && i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), s.TableHeader) {
			i++
		}
		for i < len(lines) {
			if hasAnyPrefix(strings.TrimSpace(lines[i]), prefixes) {
				break
			}
			i++
		}
		i--
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func (s *MarkedBlockSpec) stripPaired(doc string) string {
	var out []string
	skipping := false
	for line := range strings.SplitSeq(doc, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, s.StartMarker):
			skipping = true
		case t == s.EndMarker:
			skipping = false
		case skipping:
		case s.EmptyPlaceholder != "" && t == s.EmptyPlaceholder:
		default:
			out = append(out, line)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func (s *MarkedBlockSpec) maybeRestorePlaceholder(lfOld, rest string) string {
	if s.EmptyPlaceholder == "" {
		return rest
	}
	hasEntry := false
	if s.HasEntry != nil {
		hasEntry = s.HasEntry(rest)
	} else {
		hasEntry = defaultHasLayerContent(rest)
	}
	if hasEntry {
		return rest
	}
	hadNote := s.PlaceholderNote != "" && strings.Contains(lfOld, s.PlaceholderNote)
	if strings.TrimSpace(rest) != "" && !hadNote {
		return rest
	}
	if s.RespectDocEnd {
		lines := strings.Split(strings.TrimRight(rest, "\n"), "\n")
		if end := s.docEndIndex(lines); end >= 0 {
			lines = append(lines[:end], append([]string{s.EmptyPlaceholder}, lines[end:]...)...)
			return strings.Join(lines, "\n")
		}
	}
	trimmed := strings.TrimRight(rest, "\n")
	if trimmed != "" {
		trimmed += "\n"
	}
	return trimmed + s.EmptyPlaceholder
}

func (s *MarkedBlockSpec) docEndIndex(lines []string) int {
	for i, line := range lines {
		if s.isDocEndLine(line) {
			return i
		}
	}
	return -1
}

func hasLineEqualTrimmed(doc, want string) bool {
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func defaultHasLayerContent(doc string) bool {
	for line := range strings.SplitSeq(doc, "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch {
		case t == "" || strings.HasPrefix(t, "#"), strings.HasPrefix(line, "%"):
		case line == "---" || line == "..." || strings.HasPrefix(line, "--- #") || strings.HasPrefix(line, "... #"):
		default:
			return true
		}
	}
	return false
}
