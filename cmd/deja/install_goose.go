package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Goose has no hooks: the only lifecycle strings in the binary belong to its
// Nushell shell integration. What it has is an extensions block in
// config.yaml for MCP, and .goosehints, which is read into the prompt when a
// session starts.
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
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "goose")
	}
	return filepath.Join(homeDir(), ".config", "goose")
}

func gooseHintsPath() string {
	return filepath.Join(gooseConfigDir(), ".goosehints")
}

// installGooseAuto adds the session-start half. Goose reads .goosehints when
// a session starts and never again, and nothing in Goose can refresh it, so
// `deja goose` regenerates the file and hands over — the same shape aider
// needs, for the same reason.
func installGooseAuto(exe string, uninstall bool) (installResult, error) {
	res, err := installGoose(exe, uninstall)
	if err != nil {
		return res, err
	}
	path := gooseHintsPath()
	if uninstall {
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
	return res, nil
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
	path := gooseHintsPath()
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

// cmdGoose refreshes the hints file and then becomes goose.
func cmdGoose(_ string, rest []string) error {
	if err := refreshGooseHints(); err != nil {
		fmt.Fprintf(os.Stderr, "deja: could not refresh recall: %v\n", err)
	} else if n := gooseRecallCount(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: recalled %d past sessions into goose's hints\n", n)
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
	b, err := os.ReadFile(gooseHintsPath())
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n  - Session:")
}
