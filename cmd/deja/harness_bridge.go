package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func newCmdHarnessOps(openclaw bool) (*harness.Ops, *error, *[]byte) {
	var ioErr error
	var lastOld []byte
	ops := &harness.Ops{
		ReadFile: func(p string) ([]byte, error) {
			b, err := readConfig(p)
			if err != nil {
				ioErr = err
				return nil, err
			}
			lastOld = b
			return b, nil
		},
		WriteFile: func(p string, old, next []byte) (string, error) {
			a, err := writeIfChanged(p, old, next)
			if err != nil {
				ioErr = err
				return "", err
			}
			return a, nil
		},
		RemoveFile: func(p string) error {
			err := os.Remove(p)
			if err != nil {
				ioErr = err
			}
			return err
		},
		NoteCreatedDirs:  noteCreatedDirs,
		PruneDir:         pruneCreatedDir,
		RecordBlockAdded: noteBlockAdded,
		WasBlockAdded:    blockWasAdded,
		ForgetBlockAdded: forgetBlockAdded,
		RecordAdoptedKey: func(p, blockPath, key string) {
			if blockPath == "mcp_servers" {
				noteBlockAdded(p, "mcp_servers."+quoteTOMLMCPKey(key))
				return
			}
			noteBlockAdded(p, blockPath+"."+key)
		},
		AdoptedKeys: func(p, blockPath string) []string {
			raw := blocksAddedWithPrefix(p, blockPath+".")
			out := make([]string, 0, len(raw))
			for _, full := range raw {
				k := strings.TrimPrefix(full, blockPath+".")
				if blockPath == "mcp_servers" {
					k = unquoteTOMLKey(k)
				}
				if k != "" {
					out = append(out, k)
				}
			}
			return out
		},
		ForgetAdoptedKeys: func(p, blockPath string) {
			for _, full := range blocksAddedWithPrefix(p, blockPath+".") {
				k := strings.TrimPrefix(full, blockPath+".")
				if blockPath == "mcp_servers" {
					for _, name := range tomlRecordedNames(unquoteTOMLKey(k)) {
						forgetBlockAdded(p, name)
					}
					continue
				}
				forgetBlockAdded(p, full)
			}
		},
		IsDejaCommand: func(cmd string, args []string) bool {
			anyArgs := make([]any, len(args))
			for i, a := range args {
				anyArgs[i] = a
			}
			return commandIsDeja(cmd) || entryIsDejaServer(map[string]any{"command": cmd, "args": anyArgs})
		},
		IsDejaEntry:  entryRunsDeja,
		IsDejaServer: entryIsDejaServer,
		MarshalJSON: func(old []byte, root map[string]any) ([]byte, error) {
			next, err := marshalConfigLike(old, root)
			if err != nil {
				return nil, err
			}
			return append(next, '\n'), nil
		},
		EditJSONC: func(old []byte, blockPath, key string, entry map[string]any, uninstall bool, dropFrom int) ([]byte, error) {
			text := lfText(old)
			var entryStr string
			if !uninstall {
				var err error
				entryStr, err = jsoncEntryText(entry)
				if err != nil {
					return nil, err
				}
			}
			next, err := jsoncSetEntry(text, blockPath, key, entryStr, uninstall, dropFrom)
			if err != nil {
				return nil, err
			}
			return []byte(next), nil
		},
		FormatError: func(p string, err error) error {
			if err == nil {
				return nil
			}
			if ioErr != nil && errors.Is(err, ioErr) {
				return err
			}
			if openclaw && !configIsJSONC(lastOld) {
				return openclawParseError(p, lastOld)
			}
			return configParseError(p, err)
		},
	}
	return ops, &ioErr, &lastOld
}

func isQuotedTOMLKey(k string) bool {
	if len(k) < 2 {
		return false
	}
	return (k[0] == '"' && k[len(k)-1] == '"') || (k[0] == '\'' && k[len(k)-1] == '\'')
}

func quoteTOMLMCPKey(k string) string {
	if strings.ContainsAny(k, ".[]") && !isQuotedTOMLKey(k) {
		return strconv.Quote(k)
	}
	return k
}

func unquoteTOMLKey(k string) string {
	if u, err := strconv.Unquote(k); err == nil {
		return u
	}
	if len(k) >= 2 && k[0] == '\'' && k[len(k)-1] == '\'' {
		return k[1 : len(k)-1]
	}
	return k
}

func literalJSONKey(key string) string {
	return strings.ReplaceAll(key, ".", `\.`)
}

func runSkillFileSpec(path string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	if uninstall && len(old) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	ops, ioErr, _ := newCmdHarnessOps(false)
	ops.WriteFile = func(p string, prev, next []byte) (string, error) {
		a, werr := writeGuidanceFile(p, prev, next)
		if werr != nil {
			*ioErr = werr
			return "", werr
		}
		switch a {
		case "created":
			return "wrote", nil
		case "kept":
			return "updated", nil
		default:
			return a, nil
		}
	}
	spec := &harness.FileAssetSpec{
		Path:              path,
		Content:           []byte(skillFile(skillBody)),
		GuardUserEdits:    false,
		PruneEmptyParents: 1,
		DropRetired: func(p string, uninst bool) error {
			if uninst {
				return nil
			}
			return dropRetiredSkillFile(p)
		},
		Ops: ops,
	}
	if uninstall {
		ures, uerr := spec.Unwire()
		if uerr != nil {
			return installResult{}, uerr
		}
		return installResult{Path: path, Action: ures.Action}, nil
	}
	wres, werr := spec.Wire()
	if werr != nil {
		return installResult{}, werr
	}
	return installResult{Path: path, Action: wres.Action}, nil
}

func runJSONMCPSpec(path, blockKey string, entry map[string]any, uninstall, openclaw bool) (installResult, error) {
	ops, _, _ := newCmdHarnessOps(openclaw)
	specKey := blockKey
	if !openclaw {
		specKey = literalJSONKey(blockKey)
	}
	spec := &harness.JSONMCPSpec{
		Path:     path,
		BlockKey: specKey,
		EntryKey: "deja",
		Entry:    entry,
		Ops:      ops,
	}
	if uninstall {
		ures, err := spec.Unwire()
		if err != nil {
			return installResult{}, err
		}
		return installResult{
			Path:   path,
			Action: ures.Action,
			Note:   leftNamedDejaEntriesNote(ures.LeftKeys),
		}, nil
	}
	wres, err := spec.Wire()
	if err != nil {
		return installResult{}, err
	}
	note := replacedEntryNote(wres.PrevEntry, entry)
	if wres.PreservedDisabled {
		if note != "" {
			note += "; "
		}
		note += switchedOffNote
	}
	note = withOtherDejaNames(note, wres.LeftKeys)
	return installResult{
		Path:   path,
		Action: wres.Action,
		Note:   note,
	}, nil
}

func runTOMLMCPSpec(path, header, block string, legacyKeys []string, uninstall bool) (installResult, error) {
	ops, _, _ := newCmdHarnessOps(false)
	spec := &harness.TOMLMCPSpec{
		Path:        path,
		BlockHeader: header,
		Block:       block,
		LegacyKeys:  legacyKeys,
		Ops:         ops,
	}
	if uninstall {
		ures, err := spec.Unwire()
		if err != nil {
			return installResult{}, err
		}
		left := make([]string, len(ures.LeftKeys))
		for i, k := range ures.LeftKeys {
			left[i] = quoteTOMLMCPKey(k)
		}
		return installResult{
			Path:   path,
			Action: ures.Action,
			Note:   leftNamedDejaEntriesNote(left),
		}, nil
	}
	wres, err := spec.Wire()
	if err != nil {
		return installResult{}, err
	}
	if wres.AdoptedKey != "" {
		var note string
		if wres.Action != "unchanged" {
			note = fmt.Sprintf("updated %q, which already ran deja, instead of adding a second entry", wres.AdoptedKey)
		}
		return installResult{Path: path, Action: wres.Action, Note: note}, nil
	}
	var note string
	if wres.PreservedDisabled {
		note = switchedOffNote
	}
	return installResult{Path: path, Action: wres.Action, Note: note}, nil
}

func newDoctorJSONOps() (*harness.Ops, *error, *[]byte) {
	ops, ioErr, lastOld := newCmdHarnessOps(false)
	ops.ReadFile = func(p string) ([]byte, error) {
		b, err := doctorReadConfig(p)
		if err != nil {
			*ioErr = err
			return nil, err
		}
		*lastOld = b
		return b, nil
	}
	ops.IsDejaEntry = func(m map[string]any) bool { return mcpEntryRunsDeja(m) }
	return ops, ioErr, lastOld
}

func probeDoctorJSONWired(path, key string) bool {
	ops, ioErr, lastOld := newDoctorJSONOps()
	spec := &harness.JSONMCPSpec{
		Path:     path,
		BlockKey: literalJSONKey(key),
		Ops:      ops,
	}
	res, err := spec.Probe()
	if err != nil {
		if *ioErr == nil {
			return strings.Contains(string(*lastOld), `"deja"`)
		}
		return false
	}
	return res.Wired
}

func probeDoctorJSONKeys(path, key string) []string {
	ops, _, _ := newDoctorJSONOps()
	spec := &harness.JSONMCPSpec{
		Path:     path,
		BlockKey: literalJSONKey(key),
		Ops:      ops,
	}
	res, err := spec.Probe()
	if err != nil {
		return nil
	}
	return res.DejaKeys
}

func probeDoctorTOMLWired(path string) bool {
	return len(probeDoctorTOMLKeys(path)) > 0
}

func probeDoctorTOMLKeys(path string) []string {
	ops, ioErr, lastOld := newCmdHarnessOps(false)
	spec := &harness.TOMLMCPSpec{
		Path:        path,
		BlockHeader: "[mcp_servers.deja]",
		Ops:         ops,
	}
	res, err := spec.Probe()
	if err == nil {
		out := make([]string, len(res.DejaKeys))
		for i, k := range res.DejaKeys {
			out[i] = quoteTOMLMCPKey(k)
		}
		return out
	}
	if *ioErr != nil || len(*lastOld) == 0 {
		return nil
	}
	return scanDoctorTOMLKeysFallback(lfText(*lastOld))
}

func scanDoctorTOMLKeysFallback(text string) []string {
	lines := tomlCodeLines(text)
	seen := map[string]bool{}
	var key string
	var runsDeja bool
	flush := func() {
		if key != "" && (key == "deja" || runsDeja) {
			seen[key] = true
		}
		key = ""
		runsDeja = false
	}
	for i := 0; i < len(lines); i++ {
		if h, ok := tomlMCPHeader(lines[i]); ok {
			flush()
			if !strings.Contains(h, ".") || strings.HasPrefix(h, `"`) {
				key = h
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(tomlCode(lines[i])), "[") {
			flush()
			continue
		}
		if key == "" {
			continue
		}
		k, v, ok := tomlLineKeyValue(lines[i])
		if !ok {
			continue
		}
		if k == "command" || k == "args" {
			v, i = tomlArrayValue(lines, i, len(lines), v)
			for _, w := range tomlStringValues(v) {
				if commandIsDeja(w) {
					runsDeja = true
				}
			}
		}
	}
	flush()
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func runYAMLSectionSpec(spec *harness.YAMLSectionSpec, uninstall bool) (installResult, error) {
	if spec.Ops == nil {
		ops, _, _ := newCmdHarnessOps(false)
		spec.Ops = ops
	}
	if uninstall {
		ures, err := spec.Unwire()
		if err != nil {
			return installResult{}, err
		}
		return installResult{Path: spec.Path, Action: ures.Action, Note: ures.Note}, nil
	}
	wres, err := spec.Wire()
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: spec.Path, Action: wres.Action, Note: wres.Note}, nil
}

func runJSONHooksSpec(spec *harness.JSONHooksSpec, uninstall bool) (installResult, error) {
	if spec.Ops == nil {
		ops, _, _ := newCmdHarnessOps(false)
		spec.Ops = ops
	}
	if uninstall {
		ures, err := spec.Unwire()
		if err != nil {
			return installResult{}, err
		}
		return installResult{Path: spec.Path, Action: ures.Action, Note: ures.Note}, nil
	}
	wres, err := spec.Wire()
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: spec.Path, Action: wres.Action, Note: wres.Note}, nil
}

func runMarkedBlockSpec(spec *harness.MarkedBlockSpec, uninstall bool) (installResult, error) {
	if spec.Ops == nil {
		ops, _, _ := newCmdHarnessOps(false)
		spec.Ops = ops
	}
	if uninstall {
		ures, err := spec.Unwire()
		if err != nil {
			return installResult{}, err
		}
		return installResult{Path: spec.Path, Action: ures.Action, Note: ures.Note}, nil
	}
	wres, err := spec.Wire()
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: spec.Path, Action: wres.Action, Note: wres.Note}, nil
}
