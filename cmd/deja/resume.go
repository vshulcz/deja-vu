package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// runResume turns a found session into the command that reopens it in its
// native harness. Prints the command by default; --exec runs it with the
// terminal attached.
func runResume(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("resume needs id-prefix")
	}
	doExec := false
	prefix := ""
	for _, a := range args {
		if a == "--exec" {
			doExec = true
			continue
		}
		prefix = a
	}
	if prefix == "" {
		return fmt.Errorf("resume needs id-prefix")
	}
	s, ok, err := findByPrefix(prefix)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", prefix)
	}
	dir, cmdline, err := resumeCommand(s)
	if err != nil {
		return err
	}
	if !doExec {
		if dir != "" {
			fmt.Fprintf(stdout, "cd %s && %s\n", shellQuote(dir), cmdline)
		} else {
			fmt.Fprintln(stdout, cmdline)
		}
		return nil
	}
	parts := strings.Fields(cmdline)
	c := exec.Command(parts[0], parts[1:]...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// resumeCommand maps a session to (workdir, command). workdir is empty when
// the harness resumes globally or the original directory is unknown.
func resumeCommand(s model.Session) (string, string, error) {
	if strings.HasPrefix(s.Project, "imported:") {
		return "", "", fmt.Errorf("session %s was synced from another machine — resume it there", short(s.ID))
	}
	switch s.Harness {
	case "claude":
		return claudeProjectDirFor(s), "claude --resume " + s.ID, nil
	case "codex":
		if s.Project == "history" {
			return "", "", fmt.Errorf("session %s is a one-off codex exec entry, nothing to resume", short(s.ID))
		}
		return "", "codex resume " + s.ID, nil
	case "opencode":
		dir := ""
		if s.Path != "" && s.Path != sources.OpencodeDB() {
			dir = s.Path // opencode sessions carry their project directory
		}
		return dir, "opencode -s " + s.ID, nil
	default:
		return "", "", fmt.Errorf("don't know how to resume %q sessions", s.Harness)
	}
}

// claudeProjectDirFor recovers the original working directory from the
// transcript location when the encoded project dir still exists on disk.
func claudeProjectDirFor(s model.Session) string {
	if s.Path == "" {
		return ""
	}
	base := sources.ClaudeProjectDirBase(s.Path)
	if base == "" {
		return ""
	}
	return sources.ResolveEncodedPath(base)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
