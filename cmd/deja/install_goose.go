package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Goose takes MCP servers as an extensions block in config.yaml, and has two
// separate ways to put text in front of the model:
//
//   - .goosehints, read into the system prompt when a session starts. A
//     SessionStart hook runs before that read, so the hook can refresh the
//     file and the same session sees the new content.
//   - MOIM: with GOOSE_MOIM_MESSAGE_FILE set, the file is re-read every turn
//     and injected as a <turn-context> block, which also survives compaction.
//     It is env-only — config.yaml is not consulted — so only the wrapper can
//     turn it on.
//
// config.yaml is edited textually rather than through a YAML round-trip: it
// holds provider settings a user wrote by hand, and re-serialising drops the
// comments and ordering they left there.
// inlineYAMLValue returns the value written on the same line as a top-level
// key, or "" when the key is absent or opens a block.
func inlineYAMLValue(doc, key string) string {
	for _, line := range strings.Split(doc, "\n") {
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

// yamlBlockIsSequence reports whether the block that follows a key opens with
// a sequence item rather than a mapping entry, skipping blank lines and
// comments. A block that is empty, or holds mapping entries, is not one.
func yamlBlockIsSequence(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return false
		}
		return strings.HasPrefix(strings.TrimSpace(line), "- ")
	}
	return false
}

// yamlEntrySwitchedOff reports whether the entry `name` under the top-level
// key has been switched off: `enabled: false`, the spelling goose and hermes
// both read, or `disabled: true`. The writers below rebuild deja's entry
// whole, and they dropped the switch until they read it first (#4467).
func yamlEntrySwitchedOff(doc, topKey, name string) bool {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	in, child, entry, field := false, -1, -1, -1
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(line)
		if w == 0 {
			in, child, entry, field = yamlKeyLine(line, topKey), -1, -1, -1
			continue
		}
		if !in {
			continue
		}
		if child < 0 {
			child = w
		}
		key, rest, ok := hermesKeyLine(line)
		if w <= child {
			entry, field = -1, -1
			if w == child && ok && hermesKeyName(key) == name && strings.TrimSpace(stripYAMLComment(rest)) == "" {
				entry = w
			}
			continue
		}
		if entry < 0 || !ok {
			continue
		}
		if field < 0 {
			field = w
		}
		if w != field {
			continue
		}
		v := strings.ToLower(hermesYAMLScalar(rest))
		switch hermesKeyName(key) {
		case "enabled":
			if v == "false" {
				return true
			}
		case "disabled":
			if v == "true" {
				return true
			}
		}
	}
	return false
}

func installGoose(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	// A config written on Windows, or by an editor set that way, uses CRLF.
	// Every edit below splits and searches on "\n", so a key reads as
	// "extensions:\r" and matches nothing: deja added a second one and left a
	// file goose cannot read. Edit in LF and put the line endings back.
	body, crlf := normaliseNewlines(string(old))
	next := removeGooseExtension(body)
	// Removing our entry can leave the key it lived under with nothing in it,
	// and the insert below only recognises "extensions:" when a line follows
	// it — so a second install appended a second key and left the file with
	// "extensions:" twice, which is not a config goose can read. Dropping the
	// empty key first makes the two paths meet: either the key has other
	// extensions and ours joins them, or it is gone and one is written.
	next = dropEmptyYAMLKey(next, "extensions:")
	var note string
	if !uninstall {
		on := "true"
		if yamlEntrySwitchedOff(body, "extensions:", "deja") {
			on, note = "false", switchedOffNote
		}
		cmd, args := mcpCommandArgs(exe)
		pad := yamlBlockIndent(gooseExtensionsBlock(next))
		if pad == "" {
			pad = "  "
		}
		key, val := pad, pad+"  "
		var b strings.Builder
		fmt.Fprintf(&b, "%sdeja:\n%senabled: %s\n%stype: stdio\n%sname: deja\n", key, val, on, val, val)
		// Quote both: on Unix cmd is the exe path, on Windows the exe lands in
		// args — either way a YAML metacharacter in the path (a ": ", a " #")
		// would break the config Goose has to read back.
		fmt.Fprintf(&b, "%scmd: %s\n", val, yamlQuote(cmd))
		fmt.Fprintf(&b, "%sargs:\n", val)
		for _, a := range args {
			fmt.Fprintf(&b, "%s  - %s\n", val, yamlQuote(a))
		}
		fmt.Fprintf(&b, "%stimeout: 60\n", val)
		entry := b.String()
		// Already there, exactly: leave the file as it stands. Both writers
		// for this config remove their block and add it back, and removing
		// the only entry takes the key with it — so each re-appended its block
		// at the bottom, under the other's, and every `deja install goose`
		// swapped the two keys round and reported "updated" twice with nothing
		// to update. A config.yaml in a dotfiles repository showed a diff after
		// each upgrade's repair (#3689).
		if strings.Contains(body, entry) {
			return installResult{Path: path, Action: "unchanged", Note: note}, nil
		}
		// An inline value — `extensions: [a, b]` or `extensions: {…}` — is not
		// followed by a block, so the insert below missed it and appended a
		// second `extensions:` key. A parser takes the last of two, which is
		// deja's, and the user's extensions were gone without a word (#1697).
		if v := inlineYAMLValue(next, "extensions:"); v != "" {
			return installResult{}, fmt.Errorf("%s: extensions: %s is on one line, and deja edits the block form — move it to a block and run this again", path, v)
		}
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		at, err := yamlTopKeyEnd(next, "extensions:")
		if err != nil {
			return installResult{}, fmt.Errorf("%s: %w", path, err)
		}
		if at >= 0 {
			// goose keys extensions by name. Writing our mapping entry under a
			// key whose value is a sequence leaves a mapping and a sequence
			// under one key, which no parser accepts — a config that was
			// merely wrong for goose became one nothing can read, reported as
			// "updated" (#1697). Refuse instead, as the opencode writer does
			// when it cannot bound the block it was asked to edit.
			if yamlBlockIsSequence(next[at:]) {
				return installResult{}, fmt.Errorf("%s: extensions: holds a list, and goose keys extensions by name — deja cannot add itself to it", path)
			}
			next = next[:at] + entry + next[at:]
		} else {
			next += "extensions:\n" + entry
		}
	}
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	next = keepTrailingNewline(string(old), next)
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	if !uninstall {
		note = withOtherDejaNames(note, yamlDejaEntryNames(next, "extensions:"))
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a, Note: note}, werr
}

// normaliseNewlines returns the text with LF endings and whether it had CRLF,
// so an edit can be made in one convention and written back in the other.
func normaliseNewlines(s string) (string, bool) {
	if !strings.Contains(s, "\r\n") {
		return s, false
	}
	return strings.ReplaceAll(s, "\r\n", "\n"), true
}

// removeGooseExtension drops our entry and stops at the next key at the same
// indent, so a neighbouring extension survives.
//
// The entry is looked for at the indent the block itself uses, and at two
// spaces as well: that is what every build before #2614 wrote, whatever the
// rest of the block was indented by, and those files still have to come apart.
func removeGooseExtension(s string) string {
	lines := strings.Split(s, "\n")
	pad := yamlBlockIndent(gooseExtensionsBlock(s))
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
		if strings.TrimSpace(lines[i]) != "deja:" || indent == "" || (indent != pad && indent != "  ") {
			out = append(out, lines[i])
			continue
		}
		i++
		for i < len(lines) && (strings.HasPrefix(lines[i], indent+" ") || strings.TrimSpace(lines[i]) == "") {
			i++
		}
		i--
	}
	s = strings.Join(out, "\n")
	// An extensions key with nothing under it parses as null and Goose then
	// refuses the config.
	// Anchored at a line start: a bare Replace also matched the tail of
	// `my_extensions:` and broke the file.
	if strings.HasPrefix(s, "extensions:\n\n") {
		return s[len("extensions:\n"):]
	}
	return strings.Replace(s, "\nextensions:\n\n", "\n\n", 1)
}

// dropGoosePluginEntryIn takes deja's plugin out of the `plugins:` map in
// goose's config.yaml. goose adds an entry for each plugin it finds on its first
// start, keyed by the plugin's directory (plugins/discovery.rs
// filter_by_config), so uninstall removing only the directory left a key
// pointing at nothing (#4270).
func dropGoosePluginEntryIn(plugin string) (bool, error) {
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	old, err := readConfig(path)
	if err != nil || len(old) == 0 {
		return false, err
	}
	body, crlf := normaliseNewlines(string(old))
	next := dropGoosePluginEntry(body, plugin)
	if next == body {
		return false, nil
	}
	next = keepTrailingNewline(body, next)
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	_, err = writeIfChanged(path, old, []byte(next))
	return err == nil, err
}

// dropGoosePluginEntry removes the entry keyed by plugin from the top-level
// `plugins:` map, and the map with it when nothing else is under it. The key
// is read plain or quoted, the ways a YAML writer can spell a path.
func dropGoosePluginEntry(s, plugin string) string {
	lines := strings.Split(s, "\n")
	top := -1
	for i, l := range lines {
		if yamlIndentWidth(l) == 0 && yamlKeyLine(l, "plugins:") {
			top = i
			break
		}
	}
	if top < 0 {
		return s
	}
	for i := top + 1; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(l)
		if w == 0 {
			break
		}
		key, _, ok := strings.Cut(stripYAMLComment(t)+" ", ": ")
		if !ok || filepath.Clean(yamlScalar(key)) != filepath.Clean(plugin) {
			continue
		}
		j := i + 1
		for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || yamlIndentWidth(lines[j]) > w) {
			j++
		}
		// Blank lines after the entry belong to whatever follows it.
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		lines = append(lines[:i], lines[j:]...)
		return dropEmptyYAMLKey(strings.Join(lines, "\n"), "plugins:")
	}
	return s
}

// gooseExtensionsBlock returns the text after the extensions key, or "" when
// there is no block form of it to read an indent from.
func gooseExtensionsBlock(s string) string {
	return yamlKeyBlock(s, "extensions:")
}

// yamlKeyBlock returns the text after the top-level key's line, or "" when
// the file has no block form of it. The key line is matched as the writers
// match it, comment and trailing blanks included: reading only the bare
// spelling wrote deja's entry at two spaces over a block at four, and the
// reader's entries ended up nested in deja's — and went with it on uninstall
// (#4289).
func yamlKeyBlock(s, key string) string {
	if at := yamlKeyLineEnd(s, key); at >= 0 {
		return s[at:]
	}
	return ""
}

// yamlKeyLineEnd is the offset just past the first top-level line that is
// key, or -1 when there is none or it is the file's last line with no newline.
func yamlKeyLineEnd(s, key string) int {
	at := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		at += len(line)
		if yamlIndentWidth(line) == 0 && yamlKeyLine(line, key) {
			if !strings.HasSuffix(line, "\n") {
				return -1
			}
			return at
		}
	}
	return -1
}

// yamlBlockIndent returns the indent the entries under a key are written at.
// YAML accepts any consistent amount and people use four as readily as two, so
// an entry spliced in at a fixed two spaces would leave the entry after it
// nested inside ours rather than beside it (#2614). An empty block has no
// indent to follow and gives "".
func yamlBlockIndent(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		if indent == "" {
			return ""
		}
		return indent
	}
	return ""
}

func gooseConfigDir() string {
	return gooseConfigDirFor(runtime.GOOS)
}

// gooseConfigDirFor is gooseConfigDir on goos. goose takes GOOSE_PATH_ROOT and
// XDG_CONFIG_HOME only when absolute and falls back to its default otherwise;
// deja read a relative one against wherever it ran (#4285).
func gooseConfigDirFor(goos string) string {
	// Checked before XDG: Goose gives GOOSE_PATH_ROOT precedence over both.
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "config")
	}
	// Goose is one of the few that does not use ~/.config on Windows: its
	// config, data and state all sit under the Block vendor directory, and
	// etcetera's Windows strategy never reads XDG_CONFIG_HOME — which Git Bash
	// and scoop setups export, so checking it first wired a config goose
	// never opens (#4286).
	if goos == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir(), "AppData", "Roaming")
		}
		return filepath.Join(appData, "Block", "goose", "config")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "goose")
	}
	return filepath.Join(homeDir(), ".config", "goose")
}

// gooseHintsPath is where the session-start recall goes: AGENTS.md in goose's
// own config directory.
//
// It used to be `.goosehints` beside it, and that never reached the model on
// goose 1.48 — measured by pointing goose at a stub endpoint and reading the
// request it sent. Of the places a global file can live, only this one arrives:
//
//	<project>/.goosehints          reaches the model
//	<project>/AGENTS.md            reaches the model
//	~/.config/goose/AGENTS.md      reaches the model
//	~/.config/goose/.goosehints    does not
//	~/.goosehints                  does not
//	an ancestor directory's .goosehints  does not
//
// The file belongs to the reader, so deja edits its own marked block inside it
// and leaves every other line alone — the same discipline guidance already uses
// for an AGENTS.md.
func gooseHintsPath() string {
	return filepath.Join(gooseConfigDir(), "AGENTS.md")
}

// retiredGooseHintsPath is the file deja used to write. Install clears its
// block, because a stale copy of somebody's history sitting in a file nothing
// reads is the worst of both: invisible and wrong.
func retiredGooseHintsPath() string {
	return filepath.Join(gooseConfigDir(), ".goosehints")
}

const (
	gooseRecallStart = "<!-- deja recall:start -->"
	gooseRecallEnd   = "<!-- deja recall:end -->"
)

// gooseRecallBlock puts the body inside deja's markers, replacing an earlier
// block and leaving the rest of the file as it was.
func gooseRecallBlock(doc, body string) string {
	block := gooseRecallStart + "\n" + strings.TrimRight(body, "\n") + "\n" + gooseRecallEnd + "\n"
	start := strings.Index(doc, gooseRecallStart)
	if start < 0 {
		if strings.TrimSpace(doc) == "" {
			return block
		}
		return strings.TrimRight(doc, "\n") + "\n\n" + block
	}
	end := strings.Index(doc[start:], gooseRecallEnd)
	if end < 0 {
		return doc[:start] + block
	}
	tail := doc[start+end+len(gooseRecallEnd):]
	return doc[:start] + block + strings.TrimLeft(tail, "\n")
}

// installGooseAuto adds the session-start half: a plugin hook that refreshes
// the hints before Goose reads them, so plain `goose` recalls without any
// wrapper.
func installGooseAuto(exe string, uninstall bool) (installResult, error) {
	res, err := installGoose(exe, uninstall)
	if err != nil {
		return res, err
	}
	path := gooseHintsPath()
	// AGENTS.md is the reader's, so what happened to it is read off the file
	// rather than borrowed from the hook: install said it created one that was
	// there, and uninstall left its snapshot off the closing line (#4269).
	hintsBefore, hintsErr := os.ReadFile(path)
	hints := func() installResult {
		after, err := os.ReadFile(path)
		switch {
		case hintsErr != nil && err == nil:
			return installResult{Path: path, Action: "created"}
		case hintsErr == nil && err != nil:
			return installResult{Path: path, Action: "removed"}
		case hintsErr == nil && !bytes.Equal(hintsBefore, after):
			return installResult{Path: path, Action: "updated"}
		}
		return installResult{Path: path, Action: "unchanged"}
	}
	if uninstall {
		// The hook lives in its own plugin directory; leaving it behind means
		// Goose keeps running a command that no longer exists.
		plugin := filepath.Dir(filepath.Dir(gooseHookPath()))
		removed := isRealDir(plugin)
		_ = os.RemoveAll(plugin)
		if dropped, err := dropGoosePluginEntryIn(plugin); err != nil {
			return installResult{}, err
		} else if dropped && res.Action == "unchanged" {
			res.Action = "updated"
		}
		// And the directories deja made above it (#3698).
		pruneCreatedDir(filepath.Dir(plugin))
		// The recall now lives in the reader's own AGENTS.md, so uninstall
		// takes deja's block out and leaves the file. Removing it was right
		// while the target was a `.goosehints` deja owned outright; against
		// AGENTS.md it would delete whatever the reader had written there.
		if rerr := dropGooseRecallBlock(path); rerr != nil {
			return installResult{}, rerr
		}
		if rerr := dropRetiredGooseHints(); rerr != nil {
			return installResult{}, rerr
		}
		// Say what was taken away. Install names the hook file it creates and
		// uninstall named only config.yaml, so the plugin deja had written
		// went silently — where `uninstall codex-auto` says "also removed"
		// about the same thing (#3208).
		if removed {
			return wroteAll(installResult{Path: gooseHookPath(), Action: "removed"}, res, hints()), nil
		}
		return wroteAll(res, hints()), nil
	}
	if err := refreshGooseHints(); err != nil {
		return installResult{}, err
	}
	// The hook is the whole of what -auto adds over the plain target, and it
	// was written without a word: run `deja install goose` and then
	// `goose-auto` and every line said "unchanged" while the session-start
	// recall was being switched on. Report the file, the way omp-auto reports
	// its extension.
	// writeGooseHook resolves the launcher itself, so the binary goes in as it
	// came: wrapping it here would point the launcher at the launcher.
	action, err := writeGooseHook(exe)
	if err != nil {
		return installResult{}, err
	}
	// Whichever half moved. Reporting the hook alone hid a rewritten config,
	// and reporting the config alone hid the hook — which is the only thing
	// -auto adds, so `deja install goose` followed by `goose-auto` said
	// "unchanged" three times while switching session-start recall on.
	if action != "unchanged" {
		return wroteAll(installResult{Path: gooseHookPath(), Action: action}, hints(),
			installResult{Path: gooseRecipePath(), Action: action}), nil
	}
	return res, nil
}

// Hooks belong to a plugin: ~/.agents/plugins/<name>/hooks/hooks.json. The
// matcher field is a regex, and an invalid one makes Goose skip the rule
// silently, so SessionStart carries none.
//
// An absolute GOOSE_PATH_ROOT moves goose's user plugins to
// $GOOSE_PATH_ROOT/.agents/plugins (config/paths.rs get_dir), and a hook
// under the home directory then never ran (#4569).
func gooseHookPath() string {
	base := homeDir()
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		base = root
	}
	return filepath.Join(base, ".agents", "plugins", "deja", "hooks", "hooks.json")
}

func writeGooseHook(exe string) (string, error) {
	exe = hookCommandExe(exe)
	body, err := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": hookRun(exe, "hook-goose"),
					"timeout": 20,
				}},
			}},
			// Goose discards what a hook prints, so this one cannot answer
			// directly. It does not have to: the hook is handed the prompt,
			// and the MOIM file is re-read after hooks run — verified by
			// writing it from the hook and watching the new text, not the old,
			// arrive in the same turn. So the pair is a prompt-time channel
			// wherever MOIM is on, which is the `deja goose` wrapper.
			"UserPromptSubmit": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": hookRun(exe, "hook-goose-prompt"),
					"timeout": 20,
				}},
			}},
			// The prompt hook stamps the session live, which keeps it out of
			// its own MCP recall; when goose says the session is over the stamp
			// goes, so the next session can be answered with it now rather than
			// twenty minutes from now.
			"SessionEnd": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": hookRun(exe, "hook-session-end"),
					"timeout": 10,
				}},
			}},
			// A failed command's fix pair. Goose drops what every hook prints,
			// and its PostToolUseFailure carries no output (agent.rs builds it
			// from the tool name and input only), so the Stop hook reads the
			// turn's failed command out of sessions.db and hands the pair to
			// the model once, as the reason it blocks the turn from ending:
			// that reason reaches the model as a user message in the same turn
			// (1.46 and 1.53, measured).
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": hookRun(exe, "hook-stop"),
					"timeout": 20,
				}},
			}},
		},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	body = append(body, '\n')
	path := gooseHookPath()
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	old, err := readConfig(path)
	if err != nil {
		return "", err
	}
	return writeIfChanged(path, old, body)
}

// cmdGooseHook is what the SessionStart hook runs. Under the wrapper it
// refreshes the MOIM file instead, so the digest is not injected twice.
//
// The UserPromptSubmit half is hook-goose-prompt: same file, but the recall is
// searched against what the user just typed rather than chosen when the
// session opened.
func cmdGooseHook(_ string, _ []string) error {
	// The payload names the project, when the host puts it there. This door
	// discarded it and recalled from wherever the process stood, so a host that
	// runs its hooks from a plugin directory rather than the project got the
	// recall of nowhere (#2187). Decoded rather than unmarshalled, like the
	// other doors, so trailing bytes cost nothing.
	var input struct {
		CWD string `json:"cwd"`
	}
	_ = json.NewDecoder(bytes.NewReader(readHookStdin())).Decode(&input)
	return refreshGooseHintsFor(input.CWD)
}

// refreshGooseForPrompt rewrites the recall for what was just typed, so the
// block follows the conversation instead of staying on whatever the session
// opened with.
//
// Only under the wrapper, whose MOIM file belongs to one process. goose re-reads
// the global AGENTS.md every turn, but every session on the machine reads that
// same file: with plain goose a prompt in one project put its recall in front
// of every other running session, and on a stand B's recall reached A's next
// request (#4795). The session-start digest stays there; per-prompt recall
// needs a channel that is the session's own.
func refreshGooseForPrompt(dir string, payload []byte) error {
	if gooseRecallPath() == gooseHintsPath() {
		return nil
	}
	var input struct {
		// Goose calls it message; matcher_context carries the same text.
		Message   string `json:"message"`
		Prompt    string `json:"prompt"`
		CWD       string `json:"cwd"`
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(bytes.NewReader(payload)).Decode(&input)
	prompt := input.Message
	if prompt == "" {
		prompt = input.Prompt
	}
	// The project travels with the prompt: this re-encodes a payload of its
	// own, and dropping the cwd left the recall scoped to wherever the process
	// stood (#2187). Marshalled rather than quoted by hand, since strconv.Quote
	// writes \x escapes that are not JSON. The session goes too: it is what
	// stamps the session live and keeps recall from repeating itself in it.
	inner, err := json.Marshal(struct {
		Prompt    string `json:"prompt"`
		CWD       string `json:"cwd"`
		SessionID string `json:"session_id,omitempty"`
	}{prompt, input.CWD, input.SessionID})
	if err != nil {
		return err
	}
	var out strings.Builder
	if err := runHookPromptMode(dir, bytes.NewReader(inner), &out, true); err != nil {
		return err
	}
	// Silence means the history has nothing for this question. Leave the
	// session-start digest in place rather than blanking the file.
	if strings.TrimSpace(out.String()) == "" {
		return nil
	}
	return writeGooseRecall(out.String())
}

// clearGooseRecall takes deja's block out of wherever the recall lives, so a
// switch thrown mid-session stops the injection instead of freezing it.
func clearGooseRecall() error {
	path := gooseRecallPath()
	if path != gooseHintsPath() {
		// The MOIM file is deja's own; emptying it is the whole of it.
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		return os.Remove(path)
	}
	return dropGooseRecallBlock(path)
}

// writeGooseRecall puts the recall where goose will read it. The MOIM file is
// deja's own and holds nothing else; AGENTS.md is the reader's, so only the
// block between deja's markers is ours to rewrite — the prompt path wrote it
// raw and erased everything else in the file.
func writeGooseRecall(body string) error {
	path := gooseRecallPath()
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	next := []byte(body)
	if path != gooseHintsPath() {
		// deja's own file, one per wrapper: written plainly, since a config
		// writer's backup would outlive the session beside it.
		if bytes.Equal(old, next) {
			return nil
		}
		return os.WriteFile(path, next, 0o600)
	}
	next = []byte(gooseRecallBlock(string(old), body))
	if _, err := writeIfChanged(path, old, next); err != nil {
		return err
	}
	return dropRetiredGooseHints()
}

// gooseRecallPath is the hints file, or the MOIM file when the wrapper set
// one: whichever Goose is going to read this session.
func gooseRecallPath() string {
	if p := os.Getenv("GOOSE_MOIM_MESSAGE_FILE"); p != "" {
		return p
	}
	return gooseHintsPath()
}

func refreshGooseHints() error {
	return refreshGooseHintsFor("")
}

// refreshGooseHintsFor is refreshGooseHints for a caller that was told which
// project the call is about; "" leaves the chain to answer, which is what the
// wrapper and the installer want (#2187).
func refreshGooseHintsFor(cwd string) error {
	// The documented kill switch. Every other hook reads it; this one did not,
	// so `DEJA_RECALL=off` left deja writing a block into the reader's
	// AGENTS.md — and that file is re-read every turn, so it kept reaching the
	// model. Off has to mean nothing of deja's arrives, which means taking out
	// what an earlier run put there rather than only declining to write.
	if recallIsOff() {
		return clearGooseRecall()
	}
	digest, sessions, _, _, _, _, _ := cachedHookDigestFor(index.DefaultDir(), cwd, "")
	body := digest
	if sessions > 0 {
		body = frameRecall(startLead(gooseLead) + digest)
	}
	if strings.TrimSpace(body) == "" {
		body = gooseNoHistory
	}
	path := gooseRecallPath()
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeGooseRecall(body)
}

// dropGooseRecallBlock takes deja's block out of the file and removes the file
// only when nothing else was in it.
func dropGooseRecallBlock(path string) error {
	old, err := readConfig(path)
	if err != nil || len(old) == 0 {
		return nil
	}
	start := strings.Index(string(old), gooseRecallStart)
	if start < 0 {
		return nil
	}
	rest := string(old)[start:]
	end := strings.Index(rest, gooseRecallEnd)
	before, after := string(old)[:start], ""
	if end >= 0 {
		after = strings.TrimLeft(rest[end+len(gooseRecallEnd):], "\n")
	}
	// The block went in after the reader's text and a blank line; at the end
	// of the file that blank line leaves with it, or every round trip gave the
	// file back two newlines longer (#4269). With the reader's text after the
	// block it stays: a refresh drops the blank line below the block, and
	// taking the one above as well joined two paragraphs into one.
	if after == "" && strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	next := before + after
	if strings.TrimSpace(next) == "" {
		return os.Remove(path)
	}
	if before == "" {
		next = strings.TrimLeft(next, "\n")
	}
	_, err = writeIfChanged(path, old, []byte(next))
	return err
}

// dropRetiredGooseHints takes deja's block out of the file it used to write,
// and removes the file only when nothing else was in it.
//
// It said that and did not do it: a Stat and a Remove, with no look at the
// content. deja stopped writing this file when the block moved to AGENTS.md,
// so anyone keeping their own global hints there lost them — on every install,
// every uninstall, and every turn, since hook-goose calls this too (#3196).
// The rule already existed one function up, for the file that replaced it.
func dropRetiredGooseHints() error {
	path := retiredGooseHintsPath()
	old, err := readConfig(path)
	if err != nil || len(old) == 0 {
		return nil
	}
	if strings.Contains(string(old), gooseRecallStart) {
		return dropGooseRecallBlock(path)
	}
	// An older deja wrote this file whole and unmarked, so there is no block to
	// cut — but what it wrote is still recognisable: the recall frame, or the
	// placeholder it used when there was nothing to recall. Anything else in
	// the file is the reader's.
	if isRetiredGooseRecall(string(old)) {
		return os.Remove(path)
	}
	return nil
}

// isRetiredGooseRecall reports that a file holds what deja used to write here
// and nothing else.
func isRetiredGooseRecall(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	if t == strings.TrimSpace(gooseNoHistory) {
		return true
	}
	return strings.HasPrefix(t, strings.TrimSpace(recallFrameHeader)) &&
		strings.HasSuffix(t, strings.TrimSpace(recallFrameFooter))
}

// gooseNoHistory is what goes in the file when there is nothing to recall, so
// a later pass can tell deja's own placeholder from something the reader wrote.
const gooseNoHistory = "No matching history yet.\n"

const gooseLead = "The sessions below are from this project's recent history. " +
	"If any is relevant to what the user asks next, say so and use it. " +
	"If it genuinely helps, tell the user in one short line what was recalled; otherwise do not mention it.\n"

// cmdGoose turns MOIM on for the session it starts: recall is then re-read
// every turn rather than pinned to whatever the session began with, and it
// survives compaction.
//
// The file is this process's own. goose reads it by path and ignores the
// session id (platform_extensions/tom.rs get_moim), so one path for every
// wrapper handed each running session the recall the last prompt anywhere
// wrote: on a stand with two sessions in two projects, A's next request
// carried B's recall (#4795). The hooks inherit the variable, so the
// session's own hooks write the session's own file.
func cmdGoose(dir string, rest []string, sourceInstance string) error {
	if len(rest) == 0 {
		return cmdSearch(dir, []string{"goose"}, sourceInstance)
	}
	if os.Getenv("GOOSE_MOIM_MESSAGE_FILE") == "" {
		moim := gooseWrapperMOIMPath(os.Getpid())
		if err := os.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim); err != nil {
			return err
		}
		defer func() { _ = os.Remove(moim) }()
	}
	if err := refreshGooseHints(); err != nil {
		fmt.Fprintf(os.Stderr, "deja: could not refresh recall: %v\n", err)
	} else if n := gooseRecallCount(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: recalled %d past sessions into goose's context\n", n)
	}
	bin, err := exec.LookPath("goose")
	if err != nil {
		return fmt.Errorf("goose is not on PATH: %w", err)
	}
	cmd := exec.Command(bin, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Waited for through Ctrl-C, so the file goes with the session.
	return runOutlivingSignals(cmd)
}

// gooseWrapperMOIMPath is the recall file of one `deja goose` process.
func gooseWrapperMOIMPath(pid int) string {
	return filepath.Join(gooseConfigDir(), fmt.Sprintf("deja-recall-%d.md", pid))
}

func gooseRecallCount() int {
	b, err := os.ReadFile(gooseRecallPath())
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n  - Session:")
}
