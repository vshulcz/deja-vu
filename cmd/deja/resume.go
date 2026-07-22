package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// runResume turns a found session into the command that reopens it in its
// native harness. Prints the command by default; --exec runs it with the
// terminal attached.
func runResume(dir string, args []string, stdout io.Writer) error {
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
	s, ok, err := findByPrefix(dir, prefix)
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
		fmt.Fprintln(stdout, formatResumeCommand(dir, cmdline))
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

func formatResumeCommand(dir, cmdline string) string {
	if dir == "" {
		return cmdline
	}
	if runtime.GOOS == "windows" {
		dir = "'" + strings.ReplaceAll(dir, "'", "''") + "'"
		return fmt.Sprintf(`powershell.exe -NoProfile -Command "Set-Location -LiteralPath %s -ErrorAction Stop; %s"`, dir, cmdline)
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(dir), cmdline)
}

// resumeCommand maps a session to (workdir, command). workdir is empty when
// the harness resumes globally or the original directory is unknown.
// resumeIDPattern matches every supported harness's session identifiers
// (UUIDs, ses_... ids, hex prefixes). Anything else — whitespace, shell
// metacharacters, quotes, leading dashes — is refused so a crafted id read
// from a session store cannot alter the command deja builds or prints.
var resumeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func resumeCommand(s model.Session) (string, string, error) {
	if strings.HasPrefix(s.Project, "imported:") {
		return "", "", fmt.Errorf("session %s was synced from another machine — resume it there", digest.Short(s.ID))
	}
	if !resumeIDPattern.MatchString(s.ID) {
		return "", "", fmt.Errorf("session id %q contains characters deja will not place in a command", digest.Short(s.ID))
	}
	switch s.Harness {
	case "claude":
		return claudeProjectDirFor(s), "claude --resume " + s.ID, nil
	case "codex":
		if s.Project == "history" {
			return "", "", fmt.Errorf("session %s is a one-off codex exec entry, nothing to resume", digest.Short(s.ID))
		}
		return "", "codex resume " + s.ID, nil
	case "opencode":
		dir := ""
		if s.Path != "" && s.Path != sources.OpencodeDB() {
			dir = s.Path // opencode sessions carry their project directory
		}
		return dir, "opencode -s " + s.ID, nil
	case "antigravity":
		return "", "agy --conversation " + s.ID, nil
	case "aider":
		dir := filepath.Dir(s.Path)
		return "", "", fmt.Errorf("aider has no session resume — run aider in %s and it continues the same history", dir)
	case "gemini":
		return "", "", fmt.Errorf("gemini sessions reopen from inside the CLI: run gemini, then /chat resume")
	case "cursor":
		if strings.HasSuffix(s.Path, ".jsonl") {
			return "", "", fmt.Errorf("cursor CLI transcripts have no documented resume command yet")
		}
		return "", "", fmt.Errorf("cursor IDE chats reopen from the Cursor UI, not the terminal")
	case "grok":
		return "", "", fmt.Errorf("grok has no session resume — start grok in %s to continue", sources.GrokCWDForSession(s.Path))
	case "cline":
		if strings.HasPrefix(s.ID, "cline-task-") {
			return "", "", fmt.Errorf("legacy Cline VS Code tasks reopen from the extension's history UI, not the terminal")
		}
		return "", "cline --id " + s.ID, nil
	case "kimi":
		return "", "kimi --session " + s.ID, nil
	case "pi":
		return piProjectDirFor(s), "pi --session " + s.ID, nil
	case "copilot":
		return "", "copilot --resume=" + s.ID, nil
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

// piProjectDirFor recovers the original working directory from the
// transcript location when the encoded project dir still exists on disk.
func piProjectDirFor(s model.Session) string {
	if s.Path == "" {
		return ""
	}
	base := sources.PiProjectDirBase(s.Path)
	if base == "" {
		return ""
	}
	return sources.ResolveEncodedPath(base)
}
