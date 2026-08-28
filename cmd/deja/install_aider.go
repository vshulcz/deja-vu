package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

// aider has neither an MCP client nor hooks, but read-only files are re-read
// from disk on every message rather than pasted once — so a file deja keeps
// current is live context for the whole session.
//
// Refreshing it is what needs a trigger. aider's own --load can run a command
// at startup, but /run stops on "Add 0.0k tokens of command output to the
// chat?" even when the output is empty, so an installer cannot use it without
// costing a keystroke per session. `deja aider` does the refresh instead and
// hands the arguments straight to aider.
func aiderContextPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "deja", "aider-context.md")
}

func installAider(_ string, uninstall bool) (installResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return installResult{}, err
	}
	path := filepath.Join(home, ".aider.conf.yml")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	next := removeAiderReadEntry(string(old))
	if !uninstall {
		next = addAiderReadEntry(next, aiderContextPath())
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	if werr != nil {
		return installResult{}, werr
	}
	if uninstall {
		_ = os.Remove(aiderContextPath())
		return installResult{Path: path, Action: a}, nil
	}
	// Write the file now: aider fails the read outright if it is missing, and
	// the first session should not be the one that discovers that.
	if err := refreshAiderContext(index.DefaultDir()); err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: a}, nil
}

// addAiderReadEntry keeps whatever list is already under read: — a user with
// their own CONVENTIONS.md there must not lose it.
func addAiderReadEntry(s, ctx string) string {
	entry := "  - " + ctx + "\n"
	if i := strings.Index("\n"+s, "\nread:\n"); i >= 0 {
		at := i + len("\nread:\n") - 1
		return s[:at] + entry + s[at:]
	}
	// The scalar form takes a single file; promote it to a list so both survive.
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(line, "read: "); ok && strings.TrimSpace(v) != "" {
			return strings.Replace(s, line+"\n", "read:\n  - "+strings.TrimSpace(v)+"\n"+entry, 1)
		}
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s + "read:\n" + entry
}

func removeAiderReadEntry(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "- ") && strings.Contains(l, "aider-context.md") {
			continue
		}
		out = append(out, l)
	}
	// A read: key with nothing under it is not the file we found: drop the key
	// as well, or the next aider start reads a null list.
	for i := 0; i < len(out); i++ {
		if strings.TrimRight(out[i], " \t") != "read:" {
			continue
		}
		if i+1 < len(out) && strings.HasPrefix(strings.TrimSpace(out[i+1]), "- ") {
			continue
		}
		out = append(out[:i], out[i+1:]...)
		i--
	}
	return strings.Join(out, "\n")
}

// refreshAiderContext regenerates the read-only file from the same digest the
// hooks inject elsewhere, so aider users get what every other harness gets.
func refreshAiderContext(dir string) error {
	// In-process rather than shelling out to hook-context: the wrapper stands
	// between the user and their editor, and a subprocess here would also make
	// the installer depend on its own binary being runnable.
	digest, sessions, _, _, _ := cachedHookDigest(dir)
	body := digest
	if sessions > 0 {
		body = frameRecall(startLead(aiderLead) + digest)
	}
	if strings.TrimSpace(body) == "" {
		// No history for this project yet. An empty file still has to exist:
		// aider refuses to start when a configured read file is missing.
		//
		// The line says what to do about it because of where it is read. This
		// file is written at install, when there is usually no index yet, and
		// only `deja aider` rewrites it afterwards. Someone who runs plain
		// `aider` therefore sees this text in every session forever — driven
		// through the real interface, it read as "deja has nothing", when what
		// it means is "nothing has refreshed this".
		body = "No matching history yet — start aider as `deja aider` and this file fills with what this project already knows.\n"
	}
	path := aiderContextPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, _ := os.ReadFile(path)
	_, err := writeIfChanged(path, old, []byte(body))
	return err
}

const aiderLead = "The sessions below are from this project's recent history. " +
	"If any is relevant to what the user asks next, say so and use it. " +
	"If it genuinely helps, tell the user in one short line what was recalled; otherwise do not mention it.\n"

// cmdAider refreshes the context file and then becomes aider. Everything after
// the subcommand belongs to aider, including flags deja also has.
func cmdAider(dir string, rest []string, sourceInstance string) error {
	// `deja aider` with nothing after it is ambiguous, and the harmless
	// reading is the right default: search for the word rather than start
	// an editor the user did not ask for.
	if len(rest) == 0 {
		return cmdSearch(dir, []string{"aider"}, sourceInstance)
	}
	if err := refreshAiderContext(dir); err != nil {
		// A failed recall is not a reason to keep the user out of their editor.
		fmt.Fprintf(os.Stderr, "deja: could not refresh recall: %v\n", err)
	} else if n := aiderRecallCount(); n > 0 {
		// The digest lands silently inside aider's read-only files, so this
		// line is the only thing telling the user memory is in there.
		fmt.Fprintf(os.Stderr, "deja: recalled %d past sessions into aider's read-only context\n", n)
	}
	bin, err := exec.LookPath("aider")
	if err != nil {
		return fmt.Errorf("aider is not on PATH: %w", err)
	}
	cmd := exec.Command(bin, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func aiderRecallCount() int {
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		return 0
	}
	return bytes.Count(b, []byte("\n  - Session:"))
}
