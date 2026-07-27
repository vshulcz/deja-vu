package main

import (
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
func installGoose(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	next := removeGooseExtension(string(old))
	if !uninstall {
		cmd, args := mcpCommandArgs(exe)
		var b strings.Builder
		b.WriteString("  deja:\n    enabled: true\n    type: stdio\n    name: deja\n")
		fmt.Fprintf(&b, "    cmd: %s\n", cmd)
		b.WriteString("    args:\n")
		for _, a := range args {
			fmt.Fprintf(&b, "      - %s\n", a)
		}
		b.WriteString("    timeout: 60\n")
		entry := b.String()
		if i := strings.Index("\n"+next, "\nextensions:\n"); i >= 0 {
			at := i + len("\nextensions:\n") - 1
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
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, werr
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
func cmdGooseHook(_ string, _ []string) error {
	_ = readHookStdin()
	return refreshGooseHints()
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
	digest, sessions, _, _ := cachedHookDigest(index.DefaultDir())
	body := digest
	if sessions > 0 {
		body = frameRecall(gooseLead + digest)
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
func cmdGoose(dir string, rest []string) error {
	if len(rest) == 0 {
		return cmdSearch(dir, []string{"goose"})
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
