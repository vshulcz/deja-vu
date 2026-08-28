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

func installGoose(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
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
	if !uninstall {
		cmd, args := mcpCommandArgs(exe)
		var b strings.Builder
		b.WriteString("  deja:\n    enabled: true\n    type: stdio\n    name: deja\n")
		// Quote both: on Unix cmd is the exe path, on Windows the exe lands in
		// args — either way a YAML metacharacter in the path (a ": ", a " #")
		// would break the config Goose has to read back.
		fmt.Fprintf(&b, "    cmd: %s\n", yamlQuote(cmd))
		b.WriteString("    args:\n")
		for _, a := range args {
			fmt.Fprintf(&b, "      - %s\n", yamlQuote(a))
		}
		b.WriteString("    timeout: 60\n")
		entry := b.String()
		// An inline value — `extensions: [a, b]` or `extensions: {…}` — is not
		// followed by a block, so the insert below missed it and appended a
		// second `extensions:` key. A parser takes the last of two, which is
		// deja's, and the user's extensions were gone without a word (#1697).
		if v := inlineYAMLValue(next, "extensions:"); v != "" {
			return installResult{}, fmt.Errorf("%s: extensions: %s is on one line, and deja edits the block form — move it to a block and run this again", path, v)
		}
		if i := strings.Index("\n"+next, "\nextensions:\n"); i >= 0 {
			at := i + len("\nextensions:\n") - 1
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
			if next != "" && !strings.HasSuffix(next, "\n") {
				next += "\n"
			}
			next += "extensions:\n" + entry
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, werr
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
func removeGooseExtension(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "deja:" || !strings.HasPrefix(lines[i], "  ") || strings.HasPrefix(lines[i], "   ") {
			out = append(out, lines[i])
			continue
		}
		i++
		for i < len(lines) && (strings.HasPrefix(lines[i], "    ") || strings.TrimSpace(lines[i]) == "") {
			i++
		}
		i--
	}
	s = strings.Join(out, "\n")
	// An extensions key with nothing under it parses as null and Goose then
	// refuses the config.
	return strings.Replace(s, "extensions:\n\n", "\n", 1)
}

func gooseConfigDir() string {
	// Checked before XDG: Goose gives GOOSE_PATH_ROOT precedence over both.
	if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
		return filepath.Join(root, "config")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "goose")
	}
	// Goose is one of the few that does not use ~/.config on Windows: its
	// config, data and state all sit under the Block vendor directory.
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Block", "goose", "config")
		}
	}
	return filepath.Join(homeDir(), ".config", "goose")
}

func gooseHintsPath() string {
	return filepath.Join(gooseConfigDir(), ".goosehints")
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
	if uninstall {
		// The hook lives in its own plugin directory; leaving it behind means
		// Goose keeps running a command that no longer exists.
		_ = os.RemoveAll(filepath.Dir(filepath.Dir(gooseHookPath())))
		if _, serr := os.Stat(path); serr == nil {
			if rerr := os.Remove(path); rerr != nil {
				return installResult{}, rerr
			}
		}
		return res, nil
	}
	if err := refreshGooseHints(); err != nil {
		return installResult{}, err
	}
	if err := writeGooseHook(); err != nil {
		return installResult{}, err
	}
	return res, nil
}

// Hooks belong to a plugin: ~/.agents/plugins/<name>/hooks/hooks.json. The
// matcher field is a regex, and an invalid one makes Goose skip the rule
// silently, so SessionStart carries none.
func gooseHookPath() string {
	return filepath.Join(homeDir(), ".agents", "plugins", "deja", "hooks", "hooks.json")
}

func writeGooseHook() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": shellQuote(exe) + " hook-goose",
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
					"command": shellQuote(exe) + " hook-goose-prompt",
					"timeout": 20,
				}},
			}},
		},
	}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := gooseHookPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, _ := os.ReadFile(path)
	_, werr := writeIfChanged(path, old, body)
	return werr
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

// refreshGooseForPrompt writes prompt-scoped recall where goose will read it.
//
// Only the MOIM file is re-read per turn; .goosehints is read once when the
// session opens. Overwriting the hints file mid-session would therefore
// replace the recall that is already in front of the model with nothing the
// model will ever see, so without MOIM this leaves the session as it is.
func refreshGooseForPrompt(dir string, payload []byte) error {
	if os.Getenv("GOOSE_MOIM_MESSAGE_FILE") == "" {
		return nil
	}
	var input struct {
		// Goose calls it message; matcher_context carries the same text.
		Message string `json:"message"`
		Prompt  string `json:"prompt"`
		CWD     string `json:"cwd"`
	}
	_ = json.NewDecoder(bytes.NewReader(payload)).Decode(&input)
	prompt := input.Message
	if prompt == "" {
		prompt = input.Prompt
	}
	// The project travels with the prompt: this re-encodes a payload of its
	// own, and dropping the cwd left the recall scoped to wherever the process
	// stood (#2187). Marshalled rather than quoted by hand, since strconv.Quote
	// writes \x escapes that are not JSON.
	inner, err := json.Marshal(struct {
		Prompt string `json:"prompt"`
		CWD    string `json:"cwd"`
	}{prompt, input.CWD})
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
	path := gooseRecallPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, _ := os.ReadFile(path)
	_, err = writeIfChanged(path, old, []byte(out.String()))
	return err
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
	digest, sessions, _, _, _, _ := cachedHookDigestFor(index.DefaultDir(), cwd)
	body := digest
	if sessions > 0 {
		body = frameRecall(startLead(gooseLead) + digest)
	}
	if strings.TrimSpace(body) == "" {
		body = "No matching history yet.\n"
	}
	path := gooseRecallPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, _ := os.ReadFile(path)
	_, err := writeIfChanged(path, old, []byte(body))
	return err
}

const gooseLead = "The sessions below are from this project's recent history. " +
	"If any is relevant to what the user asks next, say so and use it. " +
	"If it genuinely helps, tell the user in one short line what was recalled; otherwise do not mention it.\n"

// cmdGoose turns MOIM on for the session it starts: recall is then re-read
// every turn rather than pinned to whatever the session began with, and it
// survives compaction.
func cmdGoose(dir string, rest []string, sourceInstance string) error {
	if len(rest) == 0 {
		return cmdSearch(dir, []string{"goose"}, sourceInstance)
	}
	moim := filepath.Join(gooseConfigDir(), "deja-recall.md")
	if os.Getenv("GOOSE_MOIM_MESSAGE_FILE") == "" {
		if err := os.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim); err != nil {
			return err
		}
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
	return cmd.Run()
}

func gooseRecallCount() int {
	b, err := os.ReadFile(gooseRecallPath())
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n  - Session:")
}
