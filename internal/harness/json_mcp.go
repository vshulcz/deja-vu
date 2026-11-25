package harness

import (
	"bytes"
	"strings"
)

var _ HarnessSpec = (*JSONMCPSpec)(nil)

// JSONMCPSpec manages a JSON or JSONC MCP server entry inside a nested object
// path (e.g. "mcpServers" or "mcp.servers").
type JSONMCPSpec struct {
	HarnessName     string
	Path            string
	BlockKey        string
	EntryKey        string
	Entry           map[string]any
	IgnoreSwitchOff bool
	Ops             *Ops

	mem *memoryState
}

// Name returns the harness name.
func (s *JSONMCPSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the config file path.
func (s *JSONMCPSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the target config file exists on disk.
func (s *JSONMCPSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

func (s *JSONMCPSpec) blockPath() string {
	if strings.TrimSpace(s.BlockKey) == "" {
		return "mcpServers"
	}
	return s.BlockKey
}

func (s *JSONMCPSpec) primaryKey() string {
	if strings.TrimSpace(s.EntryKey) == "" {
		return "deja"
	}
	return s.EntryKey
}

// Probe parses the JSON/JSONC file and inspects deja MCP server entries.
func (s *JSONMCPSpec) Probe() (ProbeResult, error) {
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
	root, err := parseJSONObject(old)
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, ops.formatErr(s.Path, err)
	}
	block, _, err := lookupJSONBlock(root, s.blockPath(), false, true, nil, s.Path)
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, wrapJSONErr(ops, s.Path, err)
	}
	if block == nil {
		return res, nil
	}
	dejaKeys := findJSONProbeKeys(ops, s.primaryKey(), block)
	res.DejaKeys = dejaKeys
	res.Wired = len(dejaKeys) > 0
	for _, k := range dejaKeys {
		flags := jsonEntryDisabledFlags(block[k])
		if len(flags) > 0 {
			res.Disabled = true
			res.DisabledFor = appendUnique(res.DisabledFor, flags...)
		}
	}
	return res, nil
}

// Wire writes or updates the deja MCP entry in the JSON/JSONC config.
func (s *JSONMCPSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	useJSONC := len(bytes.TrimSpace(old)) > 0 && isJSONC(old)
	root, err := parseJSONObject(old)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	blockPath := s.blockPath()
	existingBlock, chain, err := lookupJSONBlock(root, blockPath, false, !useJSONC, nil, s.Path)
	if err != nil {
		return WireResult{}, wrapJSONErr(ops, s.Path, err)
	}
	activeKey, adoptedKey, leftKeys := resolveJSONTargetKey(ops, s.Path, blockPath, s.primaryKey(), existingBlock)

	var existingEntry any
	if existingBlock != nil {
		existingEntry = existingBlock[activeKey]
	}
	merged, preserved, disabledFor := mergeJSONEntry(ops, existingEntry, s.Entry, s.IgnoreSwitchOff)

	var next []byte
	if useJSONC {
		if existingBlock == nil {
			recordMissingJSONCBlocks(ops, s.Path, blockPath, len(chain))
		}
		if ops.ops != nil && ops.ops.EditJSONC != nil {
			next, err = ops.ops.EditJSONC(old, blockPath, activeKey, merged, false, len(splitDotPath(blockPath)))
		} else {
			next, err = setJSONCEntry(old, blockPath, activeKey, merged, func(bp string) {
				ops.recordBlockAdded(s.Path, bp)
			})
		}
		if err != nil {
			return WireResult{}, ops.formatErr(s.Path, err)
		}
	} else {
		var block map[string]any
		block, _, err = lookupJSONBlock(root, blockPath, true, true, &ops, s.Path)
		if err != nil {
			return WireResult{}, wrapJSONErr(ops, s.Path, err)
		}
		block[activeKey] = merged
		next, err = ops.marshalJSON(old, root)
		if err != nil {
			return WireResult{}, ops.formatErr(s.Path, err)
		}
	}
	action, err := ops.writeConfig(s.Path, old, next)
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
		PrevEntry:         existingEntry,
	}, nil
}

// Unwire removes the deja entry (and any adopted deja keys) from the config.
func (s *JSONMCPSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	useJSONC := isJSONC(old)
	root, err := parseJSONObject(old)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	blockPath := s.blockPath()
	block, chain, err := lookupJSONBlock(root, blockPath, false, !useJSONC, nil, s.Path)
	if err != nil || block == nil {
		// Nothing of deja can sit inside a missing or non-object block.
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	keysToRemove := collectRemoveKeys(ops, s.primaryKey(), ops.adoptedKeys(s.Path, blockPath), block)
	leftKeys := findRemainingJSONDejaKeys(ops, block, keysToRemove)
	if len(keysToRemove) == 0 {
		ops.forgetAdoptedKeys(s.Path, blockPath)
		return UnwireResult{Path: s.Path, Action: ActionUnchanged, LeftKeys: leftKeys}, nil
	}

	var next []byte
	var removed []string
	if useJSONC {
		if ops.ops != nil && ops.ops.EditJSONC != nil {
			next, removed, err = unwireJSONCWithHook(ops, s.Path, blockPath, old, block, chain, keysToRemove)
		} else {
			next, removed, err = removeJSONCKeys(
				old,
				blockPath,
				keysToRemove,
				func(bp string) bool { return ops.wasBlockAdded(s.Path, bp) },
				func(bp string) { ops.forgetBlockAdded(s.Path, bp) },
			)
		}
		if err != nil {
			return UnwireResult{}, ops.formatErr(s.Path, err)
		}
	} else {
		for _, k := range keysToRemove {
			if _, ok := block[k]; ok {
				delete(block, k)
				removed = append(removed, k)
			}
		}
		collapseAddedJSONChain(chain, &ops, s.Path)
		next, err = ops.marshalJSON(old, root)
		if err != nil {
			return UnwireResult{}, ops.formatErr(s.Path, err)
		}
	}
	ops.forgetAdoptedKeys(s.Path, blockPath)
	if len(removed) == 0 {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged, LeftKeys: leftKeys}, nil
	}
	action, err := ops.writeConfig(s.Path, old, next)
	if err != nil {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	return UnwireResult{
		Path:        s.Path,
		Action:      action,
		RemovedKeys: removed,
		LeftKeys:    leftKeys,
	}, nil
}
