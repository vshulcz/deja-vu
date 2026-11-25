package harness

import (
	"sort"
	"strings"
)

var _ Spec = (*TOMLMCPSpec)(nil)

// TOMLMCPSpec manages a TOML MCP server table (such as [mcp_servers.deja])
// including switched-off flag preservation and sub-table cleanup.
type TOMLMCPSpec struct {
	HarnessName     string
	Path            string
	BlockHeader     string
	Block           string
	LegacyKeys      []string
	OwnedKeys       []string
	IgnoreSwitchOff bool
	Ops             *Ops

	mem *memoryState
}

// Name returns the harness name.
func (s *TOMLMCPSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the TOML config file path.
func (s *TOMLMCPSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the TOML config file exists on disk.
func (s *TOMLMCPSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

func (s *TOMLMCPSpec) targetSegments() []string {
	hdr := strings.TrimSpace(s.BlockHeader)
	if hdr == "" {
		hdr = "[mcp_servers.deja]"
	}
	if segs, _, ok := parseTOMLHeaderLine(hdr); ok && len(segs) > 0 {
		return segs
	}
	return []string{"mcp_servers", "deja"}
}

func (s *TOMLMCPSpec) ownedKeySet() map[string]bool {
	keys := s.OwnedKeys
	if len(keys) == 0 {
		keys = []string{"type", "command", "args"}
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// Probe validates the TOML structure and reports existing deja server blocks.
func (s *TOMLMCPSpec) Probe() (ProbeResult, error) {
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
	norm := string(normalizeLF(old))
	if strings.TrimSpace(norm) == "" {
		return res, nil
	}
	targetSegs := s.targetSegments()
	if verr := validateTOMLForWire(norm, targetSegs); verr != nil {
		return ProbeResult{Path: s.Path, Present: true}, ops.formatErr(s.Path, verr)
	}
	blocks := parseTOMLBlocks(norm)
	dejaBlocks := findTOMLProbeBlocks(ops, blocks, targetSegs)
	for _, b := range dejaBlocks {
		res.DejaKeys = append(res.DejaKeys, b.entryKey())
		flags := b.disabledFlags()
		if len(flags) > 0 {
			res.Disabled = true
			res.DisabledFor = appendUnique(res.DisabledFor, flags...)
		}
	}
	res.Wired = len(res.DejaKeys) > 0
	return res, nil
}

// Wire inserts or updates the deja TOML server block while preserving reader-owned
// lines, sub-tables, and switched-off flags.
func (s *TOMLMCPSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	norm := string(normalizeLF(old))
	targetSegs := s.targetSegments()
	if verr := validateTOMLForWire(norm, targetSegs); verr != nil {
		return WireResult{}, ops.formatErr(s.Path, verr)
	}
	blocks := parseTOMLBlocks(norm)
	prefixSegs := targetSegs[:len(targetSegs)-1]
	prefixKey := strings.Join(prefixSegs, ".")
	dejaBlocks := findTOMLDejaBlocks(ops, blocks, targetSegs)
	activeKey, adoptedKey, activeBlock, extraOwn, leftKeys := resolveTOMLTarget(
		ops, s.Path, prefixKey, targetSegs[len(targetSegs)-1], blocks, dejaBlocks, prefixSegs,
	)

	var preserved bool
	var disabledFor []string
	blockText := ensureTrailingLF(strings.TrimSpace(s.Block))
	var next string

	switch {
	case len(extraOwn) > 1:
		for i := range extraOwn {
			if !s.IgnoreSwitchOff {
				for _, f := range extraOwn[i].disabledFlags() {
					preserved = true
					disabledFor = appendUnique(disabledFor, f)
				}
			}
		}
		if preserved {
			blockText = injectTOMLDisabledFlags(blockText, disabledFor)
		}
		stripped := removeTOMLRanges(norm, collectPrefixBlocks(blocks, targetSegs))
		next = spliceTOMLBlock(stripped, nil, blockText)
	case activeBlock != nil:
		if !s.IgnoreSwitchOff {
			flags := activeBlock.disabledFlags()
			if len(flags) > 0 {
				preserved = true
				disabledFor = flags
			}
		}
		if !isTOMLBlockForeign(ops, activeBlock.body) && !tomlBlockHasKey(activeBlock.body, "url") {
			next = updateTOMLBlockOwned(norm, *activeBlock, blockText, s.ownedKeySet(), s.IgnoreSwitchOff)
		} else {
			if adoptedKey != "" {
				blockText = replaceTOMLHeaderLine(blockText, formatTOMLHeader(append(append([]string(nil), prefixSegs...), activeKey)))
			}
			if preserved {
				blockText = injectTOMLDisabledFlags(blockText, disabledFor)
			}
			next = spliceTOMLBlock(norm, activeBlock, blockText)
		}
	default:
		next = spliceTOMLBlock(norm, nil, blockText)
	}

	action, err := ops.writeConfig(s.Path, old, []byte(next))
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	return WireResult{
		Path:              s.Path,
		Action:            action,
		AdoptedKey:        adoptedKey,
		LeftKeys:          leftKeys,
		PreservedDisabled: preserved,
		DisabledFor:       disabledFor,
	}, nil
}

// Unwire removes the deja TOML block, any adopted deja blocks, legacy keys, and all
// child sub-tables beneath them.
func (s *TOMLMCPSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	norm := string(normalizeLF(old))
	if strings.TrimSpace(norm) == "" {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	targetSegs := s.targetSegments()
	if verr := validateTOMLSyntaxAndArrayConflict(norm, targetSegs); verr != nil {
		return UnwireResult{}, ops.formatErr(s.Path, verr)
	}
	blocks := parseTOMLBlocks(norm)
	prefixSegs := targetSegs[:len(targetSegs)-1]
	prefixKey := strings.Join(prefixSegs, ".")
	primary := targetSegs[len(targetSegs)-1]

	removePrefixes := [][]string{targetSegs}
	for _, ak := range ops.adoptedKeys(s.Path, prefixKey) {
		if ak == "" || ak == primary {
			continue
		}
		akSegs := append(append([]string(nil), prefixSegs...), ak)
		if shouldRemoveAdoptedTOMLKey(ops, blocks, akSegs) {
			removePrefixes = append(removePrefixes, akSegs)
		}
	}
	for _, lk := range s.LegacyKeys {
		if lk != "" {
			removePrefixes = append(removePrefixes, append(append([]string(nil), prefixSegs...), lk))
		}
	}

	var toDrop []tomlBlock
	removedKeySet := map[string]bool{}
	for _, b := range blocks {
		if b.arrayTable {
			continue
		}
		for _, pfx := range removePrefixes {
			if tomlPathHasPrefix(b.headerPath, pfx) {
				toDrop = append(toDrop, b)
				if len(b.headerPath) == len(pfx) {
					removedKeySet[b.entryKey()] = true
				}
				break
			}
		}
	}

	dejaBlocks := findTOMLDejaBlocks(ops, blocks, targetSegs)
	var leftKeys []string
	for _, b := range dejaBlocks {
		k := b.entryKey()
		if !removedKeySet[k] {
			leftKeys = append(leftKeys, k)
		}
	}
	sort.Strings(leftKeys)

	if len(toDrop) == 0 {
		ops.forgetAdoptedKeys(s.Path, prefixKey)
		return UnwireResult{Path: s.Path, Action: ActionUnchanged, LeftKeys: leftKeys}, nil
	}

	next := removeTOMLRanges(norm, toDrop)
	ops.forgetAdoptedKeys(s.Path, prefixKey)
	var removedKeys []string
	for k := range removedKeySet {
		removedKeys = append(removedKeys, k)
	}
	sort.Strings(removedKeys)

	action, err := ops.writeConfig(s.Path, old, []byte(next))
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	return UnwireResult{
		Path:        s.Path,
		Action:      action,
		RemovedKeys: removedKeys,
		LeftKeys:    leftKeys,
	}, nil
}
