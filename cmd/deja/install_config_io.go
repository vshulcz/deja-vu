package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
)

// pruneGuidanceDirs drops the directories install had to create for a guidance
// file, once that file is gone. A skill lives at <config>/skills/deja-history/
// SKILL.md; both segments are deja's, and uninstall walked away from an empty
// skills/deja-history/ nobody else put there (#840). Matching those two names
// is the bound — every other guidance path (AGENTS.md, GEMINI.md) sits directly
// in a harness directory that is not ours to delete. os.Remove on a directory
// fails unless it is empty, which is the condition wanted: a skills/ that still
// holds someone else's skill stays.
func pruneGuidanceDirs(path string) {
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "deja-history" || !isRealDir(dir) {
		// Not a skill directory of deja's, but the file may still have been
		// the only thing in a tree deja made — the VS Code prompts folder,
		// say, which holds no `deja-history` and was left on every machine
		// (#3698). The record is what allows removing it.
		pruneCreatedDir(dir)
		return
	}
	if err := os.Remove(dir); err != nil {
		return
	}
	if dir = filepath.Dir(dir); filepath.Base(dir) == "skills" && isRealDir(dir) {
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
	pruneCreatedDir(dir)
}

// isRealDir reports whether p is a directory itself rather than a link to one.
// os.Remove unlinks a symlink whatever stands behind it, so the "only if empty"
// bound above does not hold for anyone who keeps skills/ in their dotfiles and
// symlinks it into place: the link would go while the skills it points at stay.
func isRealDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

// pruneCreatedDir removes the directory a config lived in, and every one deja
// made above it, once nothing is left in them. os.Remove fails on a directory
// that is not empty, which is half the rule; the other half is the record,
// because an empty folder the reader already had is theirs and used to go with
// the uninstall (#3239).
//
// Upward, because a writer creates a tree: `~/.pi/agent/extensions/deja` is
// four levels and stopping after one left the other three behind. On a bare
// home a full uninstall left thirty-three such directories (#3698).
func pruneCreatedDir(dir string) {
	for {
		if dir == "" || dir == string(filepath.Separator) || dir == filepath.Dir(dir) {
			return
		}
		if !isRealDir(dir) || !dirWiringCreated(dir) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// noteCreatedDirs records the directories a write is about to create, so the
// prune above can vouch for each of them. Every missing level, not just the
// file's own parent: MkdirAll makes the whole tree and only the last of them
// was ever written down (#3698).
func noteCreatedDirs(dir string) {
	if removingWiring {
		return
	}
	var missing []string
	for d := dir; d != "" && d != string(filepath.Separator) && d != filepath.Dir(d); d = filepath.Dir(d) {
		if isRealDir(d) {
			break
		}
		missing = append(missing, d)
	}
	for _, m := range slices.Backward(missing) {
		if !slices.Contains(createdDirsByThisRun, m) {
			createdDirsByThisRun = append(createdDirsByThisRun, m)
		}
	}
}

// backupOnce reports whether it created the snapshot, so an uninstall can take
// its own back out afterwards without touching one the user made.
func backupOnce(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil //nolint:nilerr // no file, nothing to back up
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// Configs can carry MCP credentials; the snapshot is owner-only even
	// when the live file is looser.
	if err := os.WriteFile(bak, b, 0o600); err != nil {
		return true, err
	}
	// Whose snapshot this is cannot be read back out of its bytes, so it is
	// recorded here: the uninstall deletes one only if deja took it (#3340).
	rememberSnapshot(bak)
	return true, nil
}

// backupOnceUnlessCreated is backupOnce for a file that was already there
// before this run; one this run created is deja's and needs no snapshot. The
// record holds the path as given, the write may have followed a symlink, so
// both forms are compared.
//
// Only an entry with the same file name is resolved. deja wrote each one as a
// regular file, so only its directories can be links, and a different name
// cannot resolve to path. Resolving every entry on every write made an install
// of n configs n² path walks.
func backupOnceUnlessCreated(path string) (bool, error) {
	name := filepath.Base(path)
	for _, p := range createdByThisRun {
		if p == path {
			return false, nil
		}
		// EqualFold: Windows resolution can change a name's case.
		if !strings.EqualFold(filepath.Base(p), name) {
			continue
		}
		if r, err := evalSymlinks(p); err == nil && r == path {
			return false, nil
		}
	}
	return backupOnce(path)
}

// removingWiring is set for the length of an uninstall run. Thirty-seven call
// sites write configs through writeIfChanged, and on the uninstall path each
// one computes "the file without deja in it" — which, for a file that does not
// exist, is an empty config it then creates (#676). The flag is process-wide
// because the command is: one run, one direction.
var removingWiring bool

// removingTargets names the targets the current uninstall run is removing.
// The shared-skill guard asks whether any other harness still reads
// ~/.agents/skills/deja-history/SKILL.md, and answers from the wiring record —
// which still lists every harness during `uninstall --all`, because
// recordWiring runs at the end. So each target in turn found another "still
// wanting" reader and the file was kept every time (#1683). A harness being
// removed in this same run is not a reader.
var removingTargets map[string]bool

// forceGuidance is set by `deja install --force`: replace a skill deja can see
// has been edited since it wrote it.
var forceGuidance bool

// skippingGuidance is set by `deja install --no-guidance` for the length of the
// run. The /deja command file is written after the target, so a target that
// names it in its own report has to know it will not be written (#5589).
var skippingGuidance bool

// configParseError names the file a parse refusal is about. The refusal is what
// a reader is sent to act on — doctor points at `deja install <targets>` when a
// rewire failed, and the parser's own words alone left them guessing which of
// the harness configs it had opened (#2214).
func configParseError(path string, err error) error {
	return fmt.Errorf("%s: %w", path, err)
}

// mcpBlock reads the object an MCP config keeps its servers in. A missing key
// is an empty block deja fills in; a key holding something else — a list, a
// string, null — is a config deja does not understand, and building a fresh
// object over it dropped whatever was there and reported an ordinary write
// (#2399). A config that will not parse is already refused this way, and the
// wrong shape is the case where something is actually lost.
func mcpBlock(root map[string]any, key, path string) (map[string]any, bool, error) { //nolint:unparam // the bool says the block exists, which no caller asks yet
	v, ok := root[key]
	if !ok || v == nil {
		return nil, false, nil
	}
	m, isObject := v.(map[string]any)
	if !isObject {
		// The writers that edit a file they opened themselves name it here;
		// the one that hands bytes back to its caller leaves the naming to it.
		if path == "" {
			return nil, false, fmt.Errorf("%q is not an object deja can edit — left as it was", key)
		}
		return nil, false, fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, key)
	}
	return m, true, nil
}

// snapshotIfSameJSON gives back the snapshot's bytes when the file deja is
// about to write holds the same JSON. Marshalling cannot know how the reader
// laid out what deja did not touch: Roo's default settings have an empty
// mcpServers object over three lines, and an uninstall wrote it back as `{}`
// though the .bak beside it had the original (#4423). Anything that decodes
// differently — a server added since, a file that is not JSON — keeps next.
// So does a file deja took nothing out of: its layout now is the reader's, not
// the snapshot's. Numbers are compared as written, not as float64s.
func snapshotIfSameJSON(path string, old, next []byte) []byte {
	if bytes.Equal(old, next) {
		return next
	}
	// JSONC too, while its comments are the snapshot's: opencode's own
	// `"$schema": "…",}` came back from a round trip with the brace moved,
	// and an emptied `"mcp": {}` split over two lines. Only deja's own
	// snapshot there: the widening is not meant to reach a .bak the reader
	// keeps beside a commented config.
	want, ok := decodeJSONCExact(next)
	if !ok {
		return next
	}
	if _, strict := decodeJSONExact(next); !strict && !snapshotTaken(path) {
		return next
	}
	if before, ok := decodeJSONCExact(old); ok && reflect.DeepEqual(before, want) {
		return next
	}
	bak := path + ".bak"
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		bak = resolved + ".bak"
	}
	b, err := os.ReadFile(bak)
	if err != nil {
		return next
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	if have, ok := decodeJSONCExact(b); !ok || !reflect.DeepEqual(want, have) {
		return next
	}
	if jsonCommentText(lfText(b)) != jsonCommentText(lfText(next)) {
		return next
	}
	return b
}

// decodeJSONExact decodes one JSON document with its numbers kept as text.
func decodeJSONExact(b []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return nil, false
	}
	return v, true
}

// dropOwnBackup removes the snapshot beside path when the snapshot is deja's
// own wiring and nothing else. A snapshot of the reader's config stays even
// when the live file has come back to exactly it: that copy is theirs, and
// TestUninstallLeavesNoFileOrDirItCreated has said so since #840 — "the user's
// own config and its snapshot are not ours to delete" (#2604). It runs on the
// paths that delete the file too, which is where a config deja wrote whole and
// then edited again left a .bak the uninstall called the reader's (#3340).
func dropOwnBackup(path string) {
	bak := path + ".bak"
	b, err := os.ReadFile(bak)
	if err != nil {
		return
	}
	if snapshotHoldsDejaWiring(b) && !holdsForeignDejaEntry(b) {
		_ = os.Remove(bak)
	}
}

// snapshotHoldsDejaWiring is mentionsDeja over what deja writes, not over
// what the reader wrote. In a JSON snapshot the reader's own "deja" — a name
// in an allow list, as doctor asks of an OpenClaw reader (#5256), or a word
// in a comment — is theirs, and counting it deleted the snapshot of a config
// deja never wired (#5281). Deja's entries are keyed by the name or run its
// commands, and both survive the decode.
func snapshotHoldsDejaWiring(b []byte) bool {
	v, ok := decodeJSONCExact(bytes.TrimPrefix(b, utf8BOM))
	if !ok {
		return mentionsDeja(b)
	}
	out, err := json.MarshalIndent(withoutDejaListNames(v), "", " ")
	if err != nil {
		return mentionsDeja(b)
	}
	return mentionsDeja(out)
}

// dejaEntryKeys are the keys deja puts its own name under to identify an entry.
var dejaEntryKeys = map[string]bool{"name": true, "id": true, "label": true}

// withoutDejaListNames is v without the list items and map values that are the
// bare name. A key named deja stays: that is deja's entry. So does a value under
// the keys deja names its own entries by (`"name": "deja"` in its plugin package,
// `"id"` and `"label"` in grok's server list). Any other value is the reader's own
// choice, as in OpenClaw's `plugins.slots.memory` (#5308).
func withoutDejaListNames(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, c := range t {
			if s, ok := c.(string); ok && !dejaEntryKeys[k] && strings.TrimSpace(s) == "deja" {
				continue
			}
			m[k] = withoutDejaListNames(c)
		}
		return m
	case []any:
		var l []any
		for _, c := range t {
			if s, ok := c.(string); ok && strings.TrimSpace(s) == "deja" {
				continue
			}
			l = append(l, withoutDejaListNames(c))
		}
		return l
	}
	return v
}

// holdsForeignDejaEntry reports whether a snapshot keeps an entry named "deja"
// that runs some other program. Its name is the marker mentionsDeja goes by,
// and install replaces such an entry with deja's own, so the snapshot is the
// only copy of it left. A TOML snapshot (codex, grok, trae) carries it as
// [mcp_servers.deja].
func holdsForeignDejaEntry(b []byte) bool {
	v, ok := decodeJSONCExact(bytes.TrimPrefix(b, utf8BOM))
	if !ok {
		return holdsForeignTOMLDejaEntry(lfText(b))
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch t := v.(type) {
		case map[string]any:
			for k, c := range t {
				if e, ok := c.(map[string]any); ok && k == "deja" && foreignEntry(e) {
					return true
				}
				if walk(c) {
					return true
				}
			}
		case []any:
			if slices.ContainsFunc(t, walk) {
				return true
			}
		}
		return false
	}
	return walk(v)
}

// foreignEntry reports whether an MCP entry launches something other than
// deja: deja's own always runs its `mcp` subcommand, whatever the binary is
// called, and a remote server has a url and no command at all.
func foreignEntry(e map[string]any) bool {
	if e["url"] != nil && e["command"] == nil {
		return true
	}
	if entryCommandName(e) == "" || entryRunsDeja(e) {
		return false
	}
	words, _ := e["command"].([]any)
	args, _ := e["args"].([]any)
	return !commandListRunsMCP(append(append([]any{}, words...), args...))
}

// holdsForeignTOMLDejaEntry is foreignEntry for a [mcp_servers.deja] table.
func holdsForeignTOMLDejaEntry(s string) bool {
	lines := tomlCodeLines(s)
	for _, b := range tomlMCPBlocks(s) {
		if b.key == "deja" && tomlBlockIsForeign(lines, b) {
			return true
		}
	}
	return false
}

// tomlBlockHasKey reports whether the table sets key itself.
func tomlBlockHasKey(lines []string, b tomlMCPBlock, key string) bool {
	for i := b.start + 1; i < b.end; i++ {
		if k, _, ok := tomlLineKeyValue(lines[i]); ok && k == key {
			return true
		}
	}
	return false
}

// tomlBlockIsForeign is foreignEntry for one table: a url and no command, or a
// command that never runs `mcp`.
func tomlBlockIsForeign(lines []string, b tomlMCPBlock) bool {
	if tomlBlockRunsDeja(lines, b) {
		return false
	}
	command, url, mcp := false, false, false
	for i := b.start + 1; i < b.end; i++ {
		key, value, ok := tomlLineKeyValue(lines[i])
		if !ok {
			continue
		}
		switch key {
		case "url":
			url = true
		case "command", "args":
			command = command || key == "command"
			value, i = tomlArrayValue(lines, i, b.end, value)
			for _, w := range tomlStringValues(value) {
				mcp = mcp || w == "mcp"
			}
		}
	}
	return (url && !command) || (command && !mcp)
}

// mentionsDeja reports whether a config snapshot carries deja's own wiring.
// The markers are what every generator writes: the subcommands the hooks call,
// and the name of the MCP server and extension entries.
func mentionsDeja(b []byte) bool {
	// The subcommands rather than the binary's name: a snapshot names whatever
	// path deja was installed from, which need not end in "deja" at all.
	for _, marker := range []string{
		"hook-prompt", "hook-context", "hook-tool", "hook-goose", "hook-plan",
		"hook-precompact", "hook-antigravity", "hook-codewhale", "deja:", "\"deja\"", "deja-recall",
		// The harnesses that do not write the word on its own: zed names the
		// server, dsh opens a block, codex and grok put the name in a TOML
		// table header, and aider only points at deja's context file. Without
		// them their snapshots survived an uninstall carrying deja's whole
		// entry, which is the one thing this exists to prevent (#2575, #2578).
		// TestEveryInstalledConfigIsRecognisableAsDejas installs every target
		// and holds this list against what they actually write.
		zedServerID, dshBlockStart, "[mcp_servers.deja]", "aider-context.md",
	} {
		if bytes.Contains(b, []byte(marker)) {
			return true
		}
	}
	// And the same pair the hook reader answers with: a deja-named binary
	// running one of deja's subcommands. The list above is markers, and the
	// statusline is written by none of them — `"command": "<path>/deja
	// statusline"` names no key of deja's and no hook — so a snapshot of
	// deja's own status bar survived the uninstall beside the config, naming a
	// binary the uninstall had just orphaned (#3684).
	return linesRunDeja(b)
}

// linesRunDeja reports whether any line of a config runs deja: a token named
// like a deja build beside one of deja's own subcommands. Both halves are
// required, so a path that merely contains the word and a subcommand named in
// prose are each not enough.
func linesRunDeja(b []byte) bool {
	for line := range strings.SplitSeq(lfText(b), "\n") {
		fields := strings.Fields(line)
		for i := 1; i < len(fields); i++ {
			sub := strings.Trim(fields[i], `"',;)`)
			if !dejaSubcommandInConfigs[sub] {
				continue
			}
			if hookTokenIsDejas(fields[i-1]) {
				return true
			}
		}
	}
	return false
}

// dejaSubcommandInConfigs is every subcommand deja writes into a config: its
// hooks, the status bar and the MCP server. Kept beside hookNames rather than
// folded into it — that map is the hook dispatch table and a test holds it
// against the real one.
var dejaSubcommandInConfigs = func() map[string]bool {
	m := map[string]bool{"statusline": true, "mcp": true}
	for name := range hookNames {
		m[name] = true
	}
	return m
}()

// lfText is the text of a config normalised to LF, for the writers that splice
// blocks by counting newlines. They then work in one convention and
// writeIfChanged puts the file's own endings back (#1668).
func lfText(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// matchLineEndings writes back the line endings the file already had. A
// Windows user's configs are CRLF, and install rewrote the JSON ones LF-only
// while leaving the TOML and YAML ones half and half — deja's appended block in
// LF inside a CRLF file (#1668). install_goose.go and guidance.go already did
// this for their own two files; every other writer comes through here.
//
// Only a file whose endings are *all* CRLF is treated as a CRLF file. A mixed
// one is left alone: converting it whole would rewrite lines deja never touched,
// which is the thing this exists to avoid.
func matchLineEndings(old, next []byte) []byte {
	if len(old) == 0 || !bytes.Contains(old, []byte("\r\n")) {
		return next
	}
	if bytes.Count(old, []byte("\n")) != bytes.Count(old, []byte("\r\n")) {
		return next
	}
	// Only the newlines that are not already CRLF, so a writer that converted
	// its own output first is not given a second carriage return.
	var b bytes.Buffer
	b.Grow(len(next) + bytes.Count(next, []byte("\n")))
	for i := range next {
		if next[i] == '\n' && (i == 0 || next[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(next[i])
	}
	return b.Bytes()
}

// createdDirsByThisRun collects the directories this process had to make. The
// record outlives the run, because the uninstall that prunes them is a
// different one (#3239).
var createdDirsByThisRun []string

// createdByThisRun collects the configs this process created, so the record can
// keep them and a later uninstall can take back a file that turns out to hold
// nothing but empty containers (#2583).
var createdByThisRun []string

// evalSymlinks is filepath.EvalSymlinks for the lookups that run once per
// recorded file, swappable so a test can count them.
var evalSymlinks = filepath.EvalSymlinks

// structurallyEmptyConfig reports that a config holds nothing but the empty
// containers deja's writers leave behind. #840 deletes a file whose content is
// gone entirely; the structured writers never produce zero bytes — an emptied
// mcp.json is `{"mcpServers":{}}` — so the same rule needs the same reading of
// "nothing left in it".
func structurallyEmptyConfig(b []byte) bool {
	trimmed := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			return -1
		}
		return r
	}, string(b))
	switch trimmed {
	case "", "{}", "[]", "null",
		`{"mcpServers":{}}`, `{"mcp":{}}`, `{"mcp":{"servers":{}}}`,
		`{"context_servers":{}}`, `{"servers":{}}`, "mcp_servers:",
		// The seed Muse needs before it starts (seedMuseSettings).
		`{"schema_version":1}`:
		return true
	}
	return false
}

// matchFinalNewline gives a file back ending as it came in. Every writer here
// finishes with a newline, and some editors never write one, so a config
// someone owned came back changed for nothing — #2606 the other way round
// (#2619). Beside matchLineEndings because it is the same rule: what the file
// was written in is not ours to convert. A file deja creates has no ending to
// keep and gets the newline every text file wants.
func matchFinalNewline(old, next []byte) []byte {
	if len(old) == 0 || bytes.HasSuffix(old, []byte("\n")) {
		return next
	}
	next = bytes.TrimSuffix(next, []byte("\n"))
	return bytes.TrimSuffix(next, []byte("\r"))
}

// readConfig reads a config a writer is about to edit.
//
// Every writer used to read it with `old, _ := os.ReadFile`, so a file that
// exists but cannot be read was empty to all of them and deja wrote a config
// holding only deja over it. A file that is not there is still empty — that is
// the ordinary first install — but anything else is the reader's config, and
// deja cannot edit what it cannot read (#2751).
func readConfig(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	rememberRead(path, b, err != nil)
	return bytes.TrimPrefix(b, utf8BOM), nil
}

// utf8BOM is what PowerShell 5.1 puts at the front of a file by default —
// `Set-Content`, `Out-File`, a `>` redirect — and what editors on Windows may
// too. Every JSON parser here refused such a config, so nine targets reported
// `invalid character '<BOM>' looking for beginning of value`: a remedy naming
// a character that cannot be seen, in a file its owner did not knowingly
// change, and one a harness may well read — VS Code's own readers strip a BOM
// (#3696). It belongs with the line endings and the indent: read past it,
// write it back.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// matchByteOrderMark puts back the mark the file began with, for a writer that
// worked on the text without it.
func matchByteOrderMark(old, next []byte) []byte {
	if !bytes.HasPrefix(old, utf8BOM) || bytes.HasPrefix(next, utf8BOM) || len(next) == 0 {
		return next
	}
	return append(append([]byte(nil), utf8BOM...), next...)
}

// fileStartsWithBOM reports whether the file on disk begins with the mark.
// Read from the file rather than from the bytes a writer holds, because
// readConfig takes it off before any of them sees the text.
func fileStartsWithBOM(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var head [3]byte
	n, _ := f.Read(head[:])
	return n == len(utf8BOM) && bytes.Equal(head[:], utf8BOM)
}

// readOnlyConfigError is the refusal for a config its owner made read-only.
// It is a permission error underneath, so the run's remedy names permissions.
type readOnlyConfigError struct{ path string }

func (e readOnlyConfigError) Error() string {
	return e.path + " is read-only, so deja left it as it was"
}

func (e readOnlyConfigError) Is(target error) bool { return target == fs.ErrPermission }

// configWritable refuses a config that is there and cannot be opened for
// writing. writeIfChanged replaces a file by renaming a temp file over it, which
// only the directory's mode governs: `chmod 444` on the file stopped the
// reader's own editor and not deja, and the result kept the 0444 (#4558).
// Asked by opening the file for writing, without truncating it, so whatever
// the platform enforces — a mode, an ACL, Windows' read-only attribute — is
// what decides.
//
// A config that is not there yet is writable only if the writer can make it:
// the pre-flight used to pass it unasked, so a target whose directory could
// not be created was refused after its other files were already wired (#5291).
func configWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		return f.Close()
	}
	if errors.Is(err, fs.ErrPermission) {
		return readOnlyConfigError{path}
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return configCreatable(path)
	}
	return nil
}

// configCreatable asks the nearest directory that exists above a missing
// config whether a file can be created in it, the way writeIfChanged would:
// by creating its scratch file there and removing it again. A mode bit is not
// the answer — it denies nothing on Windows or to root — and neither is
// creating the missing directories, which would leave them behind for a
// target that is then refused. The error names what the writer would have
// failed on, so errors.Is still sees a permission denial underneath.
func configCreatable(path string) error {
	missing := ""
	dir := filepath.Dir(path)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return &fs.PathError{Op: "mkdir", Path: dir, Err: syscall.ENOTDIR}
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		missing, dir = dir, parent
	}
	tmp, err := os.CreateTemp(dir, ".deja-tmp-")
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			if missing != "" {
				return &fs.PathError{Op: "mkdir", Path: missing, Err: pe.Err}
			}
			return &fs.PathError{Op: "open", Path: path, Err: pe.Err}
		}
		return err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func writeIfChanged(path string, old, next []byte) (string, error) {
	// The last guard for a file deja could not read: the writers go through
	// readConfig now, but this also covers the ones that write a file they
	// never read. An unreadable config used to be an empty one here, and
	// backupOnce hid it — it takes one snapshot per file and returns early
	// once a `.bak` is beside it, which is the ordinary state after any
	// earlier install (#2751).
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Before the comparison, not after: a CRLF config converted afterwards
	// would differ from `old` on every run, so each repeat install would
	// rewrite the file and report it changed.
	next = matchLineEndings(old, next)
	next = matchFinalNewline(old, next)
	// The byte order mark is read off the file rather than out of `old`:
	// readConfig strips it, so every writer works on the text without it and
	// this is the one place that knows the file had one (#3696). The
	// comparison is of the text, and the mark goes back on what is written.
	bom := fileStartsWithBOM(path)
	if removingWiring {
		next = snapshotIfSameJSON(path, old, next)
		next = snapshotIfOnlyEmptyBlocksDiffer(path, old, next)
	}
	// An empty config the reader made — `touch ~/.codex/config.toml` — comes
	// back empty, not deleted and not `{}`: the snapshot deja took of it says
	// it was there, and holding nothing is how it was (#4563).
	keptEmpty := false
	if removingWiring && (len(next) == 0 || structurallyEmptyConfig(next)) && !wiringCreated(path) {
		if b, ok := ownSnapshot(path); ok && len(bytes.TrimSpace(b)) == 0 {
			next, keptEmpty = b, true
		}
	}
	if bytes.Equal(old, next) {
		return "unchanged", nil
	}
	if err := yamlWriteBreaks(path, old, next); err != nil {
		return "", err
	}
	if err := configWritable(path); err != nil {
		return "", err
	}
	// Removing something must not leave more behind than it found. A file that
	// is not there has nothing in it to remove.
	if removingWiring {
		if action, done, err := writeIfChangedRemoval(path, old, next, keptEmpty); done {
			return action, err
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) && !removingWiring {
		createdByThisRun = append(createdByThisRun, path)
	}
	dir := filepath.Dir(path)
	noteCreatedDirs(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Config files are very often symlinks into a dotfiles repository. Writing
	// by rename would replace the link with a regular file: the repo stops
	// tracking the config, and the next stow or chezmoi run either clobbers
	// deja's wiring or stops on a conflict. Follow the link and write where it
	// points, so the link stays a link and the change lands in the repo.
	// A dangling link has nothing to follow and keeps the old behaviour.
	given := path
	if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil && resolved != path {
		path = resolved
		// The snapshot is taken under the resolved name a line below, and a
		// later run may ask under either spelling — a link that has since been
		// replaced by the real directory, a record written by an older deja.
		// Recording both is the only way back, since no resolution runs
		// backwards (review of #3340).
		rememberSnapshot(given + ".bak")
		noteCreatedDirs(filepath.Dir(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
	}
	// No snapshot of a file this run created: a second write to it in the
	// same run — roo allows deja's tool right after writing the entry — was
	// backing up deja's own file from a moment before, and the uninstall then
	// called it a config the reader already had (#3245).
	if _, err := backupOnceUnlessCreated(path); err != nil {
		return "", err
	}
	// On the way out, a snapshot that itself contains deja's wiring is deja's
	// own and nothing the user asked to keep: leaving it puts a file naming a
	// binary they just removed back in their config directory. Ownership is
	// read from the content rather than from who wrote it — installing a
	// harness whose config deja edits twice takes the snapshot on the second
	// write, so the uninstall that meets it did not create it and would
	// otherwise leave it (goose). A backup with no deja in it is the user's.
	if removingWiring {
		defer func() { dropOwnBackup(path) }()
	}
	tmp, terr := os.CreateTemp(filepath.Dir(path), ".deja-tmp-")
	if terr != nil {
		// The scratch file is deja's business; the reader's is the config it
		// could not write. A read-only ~/.codex was reported as a permission
		// error on ~/.codex/.deja-tmp-4168817699, which cannot be looked at,
		// chmod-ed or found (#1686, the shape of #865). Rewriting the path in
		// place keeps the error a *PathError, so errors.Is still sees the
		// permission underneath and the remedy still names permissions.
		//
		// Only for a permission denial: the destination is the right thing to
		// name when the directory refuses us, and the wrong thing when the
		// failure is about the scratch file itself — "config.toml: no such
		// file or directory" would send the reader after a directory that is
		// the actual problem.
		var pe *os.PathError
		if errors.Is(terr, fs.ErrPermission) && errors.As(terr, &pe) {
			pe.Path = path
		}
		return "", terr
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if bom {
		next = matchByteOrderMark(utf8BOM, next)
	}
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return "", err
	}
	// Preserve the live file's mode; brand-new configs start owner-only
	// because they may carry MCP credentials.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	beforeConfigReplace(path)
	if err := changedSinceRead(given, old); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	// The next write to this file is checked against the read it is built from.
	forgetRead(given)
	noteRunWrite(given, next)
	if len(old) == 0 {
		return "created", nil
	}
	return "updated", nil
}

// writeIfChangedRemoval is the part of writeIfChanged that only an uninstall
// reaches. The bool is true when the file was settled here and nothing is
// written.
func writeIfChangedRemoval(path string, old, next []byte, keptEmpty bool) (string, bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "unchanged", true, nil
	}
	// Nothing is left once deja's block comes out, which means deja wrote
	// the whole file: install created ~/.codex/AGENTS.md, uninstall
	// truncated it to zero bytes and left it there, plus a .bak of deja's
	// own guidance (#840). Deleting is what "remove what we added" means
	// here. Before backupOnce, so the backup of a file that was entirely
	// ours is not created either — the .bak of a config the user already
	// had still is.
	if len(next) == 0 && !keptEmpty {
		if err := changedSinceRead(path, old); err != nil {
			return "", true, err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", true, err
		}
		// Only a snapshot deja took itself. mentionsDeja is a content guess
		// and a loose one — a reader's own config that says the word, or
		// their own snapshot of an older deja block, matches it — which is
		// fine for deciding whether a file may be overwritten and not for
		// deciding whether one may be deleted (review of #3340).
		if snapshotTaken(path) {
			dropOwnBackup(path)
		}
		// The same prune the structured branch below does: this writer
		// wrote the file whole, and removing it can leave the tree deja
		// made for it standing empty (#3698).
		pruneCreatedDir(filepath.Dir(path))
		return "removed", true, nil
	}
	// The same rule for the structured writers, which never reach zero
	// bytes. Only for a file deja created: a config the reader already had
	// keeps its place, emptied of deja and of nothing else (#2583).
	if structurallyEmptyConfig(next) && wiringCreated(path) {
		if err := changedSinceRead(path, old); err != nil {
			return "", true, err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", true, err
		}
		pruneCreatedDir(filepath.Dir(path))
		if snapshotTaken(path) {
			dropOwnBackup(path)
		}
		return "removed", true, nil
	}
	return "", false, nil
}
