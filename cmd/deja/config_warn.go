package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/vshulcz/deja-vu/internal/userconfig"
)

func shouldWarnConfig(cmd string) bool {
	switch cmd {
	case "doctor", "version", "completion", "mcp", "serve":
		return false
	}
	return !strings.HasPrefix(cmd, "hook-")
}

// warnBrokenConfig reports syntax, validation, unknown-key, or permission
// issues in the user-owned config.toml on interactive CLI runs.
func warnBrokenConfig(cmd string, w io.Writer) {
	if !shouldWarnConfig(cmd) {
		return
	}
	diag := userconfig.Diagnose()
	if !diag.Exists {
		return
	}
	if diag.Err != nil {
		fmt.Fprintf(w, "deja: warning user config %s ignored: %v\n", diag.Path, diag.Err)
		return
	}
	if len(diag.Unknown) > 0 {
		fmt.Fprintf(w, "deja: warning user config %s has unknown keys (%s) that will be ignored\n",
			diag.Path, quotedList(diag.Unknown, 3))
	}
	for _, warn := range diag.Warnings {
		fmt.Fprintf(w, "deja: warning %s\n", warn)
	}
}
