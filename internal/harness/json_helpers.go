package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

type nonObjectBlockError struct {
	path string
	key  string
}

func (e *nonObjectBlockError) Error() string {
	return fmt.Sprintf("%s: %q is not an object deja can edit — left as it was", e.path, e.key)
}

func wrapJSONErr(ops resolvedOps, path string, err error) error {
	var nonObj *nonObjectBlockError
	if errors.As(err, &nonObj) {
		return err
	}
	return ops.formatErr(path, err)
}

type jsonChainFrame struct {
	parent map[string]any
	key    string
	path   string
	obj    map[string]any
}

func lookupJSONBlock(root map[string]any, blockPath string, create, allowNull bool, ops *resolvedOps, filePath string) (map[string]any, []jsonChainFrame, error) {
	parts := splitDotPath(blockPath)
	cur := root
	var chain []jsonChainFrame
	for i, part := range parts {
		subPath := joinDotPath(parts[:i+1])
		raw, exists := cur[part]
		if !exists || (allowNull && raw == nil) {
			if !create {
				return nil, chain, nil
			}
			child := map[string]any{}
			cur[part] = child
			if ops != nil {
				ops.recordBlockAdded(filePath, subPath)
			}
			chain = append(chain, jsonChainFrame{parent: cur, key: part, path: subPath, obj: child})
			cur = child
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			errKey := part
			if i == len(parts)-1 {
				if len(parts) > 1 {
					errKey = strings.Join(parts, ".")
				} else {
					errKey = blockPath
				}
			}
			return nil, nil, &nonObjectBlockError{path: filePath, key: errKey}
		}
		chain = append(chain, jsonChainFrame{parent: cur, key: part, path: subPath, obj: m})
		cur = m
	}
	return cur, chain, nil
}

func lookupJSONBlockOptional(root map[string]any, blockPath string, allowNull bool, filePath string) (map[string]any, []jsonChainFrame, bool) {
	block, chain, err := lookupJSONBlock(root, blockPath, false, allowNull, nil, filePath)
	if err != nil || block == nil {
		return nil, nil, false
	}
	return block, chain, true
}

func recordMissingJSONCBlocks(ops resolvedOps, filePath, blockPath string, have int) {
	parts := splitDotPath(blockPath)
	for i := have; i < len(parts); i++ {
		ops.recordBlockAdded(filePath, joinDotPath(parts[:i+1]))
	}
}

func unwireJSONCWithHook(
	ops resolvedOps,
	path, blockPath string,
	old []byte,
	block map[string]any,
	chain []jsonChainFrame,
	keysToRemove []string,
) ([]byte, []string, error) {
	parts := splitDotPath(blockPath)
	var removed []string
	for _, k := range keysToRemove {
		if _, ok := block[k]; ok {
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return old, nil, nil
	}
	dropFrom := len(parts)
	if len(removed) == len(block) && ops.wasBlockAdded(path, blockPath) {
		dropFrom = len(parts) - 1
		ops.forgetBlockAdded(path, blockPath)
		for i := len(parts) - 2; i >= 0; i-- {
			prefix := joinDotPath(parts[:i+1])
			if len(chain[i].obj) != 1 || !ops.wasBlockAdded(path, prefix) {
				break
			}
			dropFrom = i
			ops.forgetBlockAdded(path, prefix)
		}
	}
	cur := old
	for i, k := range removed {
		df := len(parts)
		if i == len(removed)-1 {
			df = dropFrom
		}
		var err error
		cur, err = ops.ops.EditJSONC(cur, blockPath, k, nil, true, df)
		if err != nil {
			return nil, nil, err
		}
	}
	return cur, removed, nil
}

func collapseAddedJSONChain(chain []jsonChainFrame, ops *resolvedOps, filePath string) {
	for _, frame := range slices.Backward(chain) {
		if len(frame.obj) > 0 || !ops.wasBlockAdded(filePath, frame.path) {
			return
		}
		delete(frame.parent, frame.key)
		ops.forgetBlockAdded(filePath, frame.path)
	}
}

func resolveJSONTargetKey(ops resolvedOps, path, blockPath, primary string, block map[string]any) (string, string, []string) {
	if block == nil {
		return primary, "", nil
	}
	dejaServers := findJSONServerKeys(ops, block)
	active := primary
	adopted := ""
	if _, hasPrimary := block[primary]; !hasPrimary {
		for _, prev := range ops.adoptedKeys(path, blockPath) {
			if _, ok := block[prev]; ok {
				active = prev
				adopted = prev
				break
			}
		}
		if adopted == "" && len(dejaServers) > 0 {
			active = dejaServers[0]
			adopted = active
		}
	}
	if adopted != "" {
		ops.recordAdoptedKey(path, blockPath, adopted)
	}
	var left []string
	for _, k := range dejaServers {
		if k != active {
			left = append(left, k)
		}
	}
	return active, adopted, left
}

func collectRemoveKeys(ops resolvedOps, primary string, adopted []string, block map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		if _, ok := block[k]; ok {
			seen[k] = true
			out = append(out, k)
		}
	}
	add(primary)
	for _, k := range adopted {
		if raw, ok := block[k]; ok && (k == primary || isJSONMapDejaEntry(ops, raw)) {
			add(k)
		}
	}
	return out
}

func findJSONProbeKeys(ops resolvedOps, primary string, block map[string]any) []string {
	if block == nil {
		return nil
	}
	var out []string
	if _, ok := block[primary]; ok {
		out = append(out, primary)
	}
	var rest []string
	for k, v := range block {
		if k != primary && isJSONMapDejaEntry(ops, v) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func findJSONServerKeys(ops resolvedOps, block map[string]any) []string {
	var keys []string
	for k, v := range block {
		if isJSONMapDejaServer(ops, v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func findJSONDejaKeys(ops resolvedOps, block map[string]any) []string {
	var keys []string
	for k, v := range block {
		if isJSONMapDejaEntry(ops, v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func findRemainingJSONDejaKeys(ops resolvedOps, block map[string]any, removed []string) []string {
	drop := map[string]bool{}
	for _, k := range removed {
		drop[k] = true
	}
	var left []string
	for _, k := range findJSONDejaKeys(ops, block) {
		if !drop[k] {
			left = append(left, k)
		}
	}
	return left
}

func isJSONMapDejaEntry(ops resolvedOps, raw any) bool {
	m, ok := raw.(map[string]any)
	return ok && ops.isDejaEntry(m)
}

func isJSONMapDejaServer(ops resolvedOps, raw any) bool {
	m, ok := raw.(map[string]any)
	return ok && ops.isDejaServer(m)
}

func defaultIsJSONDejaEntry(ops resolvedOps, m map[string]any) bool {
	if m == nil {
		return false
	}
	if t, ok := m["transport"].(map[string]any); ok && defaultIsJSONDejaEntry(ops, t) {
		return true
	}
	if cmd, ok := m["command"].(string); ok {
		return ops.isDejaCommand(cmd, anyStringSlice(m["args"]))
	}
	if cmdSlice := anyStringSlice(m["command"]); len(cmdSlice) > 0 {
		return ops.isDejaCommand(cmdSlice[0], append(cmdSlice[1:], anyStringSlice(m["args"])...))
	}
	return false
}

func anyStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonEntryDisabledFlags(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var flags []string
	if v, ok := m["disabled"].(bool); ok && v {
		flags = append(flags, "disabled")
	}
	if v, ok := m["enabled"].(bool); ok && !v {
		flags = append(flags, "enabled")
	}
	return flags
}

func mergeJSONEntry(ops resolvedOps, existing any, desired map[string]any, ignoreSwitchOff bool) (map[string]any, bool, []string) {
	out := make(map[string]any, len(desired))
	maps.Copy(out, desired)
	old, ok := existing.(map[string]any)
	if !ok || !ops.isDejaEntry(old) {
		return out, false, nil
	}
	merged := make(map[string]any, len(old)+len(desired))
	maps.Copy(merged, old)
	maps.Copy(merged, desired)
	if _, hasCmd := merged["command"]; hasCmd {
		delete(merged, "transport")
	}
	if ignoreSwitchOff {
		if _, explicit := desired["disabled"]; !explicit {
			delete(merged, "disabled")
		}
		if _, explicit := desired["enabled"]; !explicit {
			delete(merged, "enabled")
		}
		return merged, false, nil
	}
	flags := jsonEntryDisabledFlags(merged)
	return merged, len(flags) > 0, flags
}

func marshalJSONLike(old []byte, root map[string]any) ([]byte, error) {
	unit := detectIndentUnit(string(normalizeLF(old)))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", unit)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func appendUnique(dst []string, items ...string) []string {
	seen := map[string]bool{}
	for _, x := range dst {
		seen[x] = true
	}
	for _, x := range items {
		if !seen[x] {
			seen[x] = true
			dst = append(dst, x)
		}
	}
	return dst
}
