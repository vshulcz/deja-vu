package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
)

func digestHandoff(s model.Session) string { return digest.Handoff(s, handoffBudget) }

// clipboardCommand is the local copy tool, when there is one and the
// clipboard it fills is the reader's: over ssh it would fill the server's.
func clipboardCommand() []string {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return nil
	}
	var cands [][]string
	switch runtime.GOOS {
	case "darwin":
		cands = [][]string{{"pbcopy"}}
	case "linux", "freebsd", "openbsd", "netbsd":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			cands = append(cands, []string{"wl-copy"})
		}
		cands = append(cands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
}

// tuiCopy puts text on the clipboard: through the local tool when there is
// one, and otherwise through OSC 52, which the terminal carries to the
// clipboard of the machine the reader is sitting at, ssh or not.
func tuiCopy(osc func(string), text string) error {
	if c := clipboardCommand(); c != nil {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	osc("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
	return nil
}
