package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// runStatusline prints one line for a status bar: how much memory deja served
// to agents today. It must stay fast and quiet — no index access, no locks —
// because status bars call it constantly.
func runStatusline(stdin io.Reader, stdout io.Writer) error {
	drainStdin(stdin)
	recalls, bytes, injected := usage.TodayWithInjections(index.DefaultDir())
	if recalls == 0 {
		fmt.Fprint(stdout, "deja · no recalls yet today · 0 B injected")
		return nil
	}
	noun := "recalls"
	if recalls == 1 {
		noun = "recall"
	}
	fmt.Fprintf(stdout, "deja · %d %s · %s ctx today · %s injected", recalls, noun, humanBytes(int64(bytes)), humanBytes(int64(injected)))
	return nil
}

// Claude Code pipes session JSON to statusline commands. We don't need it,
// but leaving the pipe unread can block the caller on some platforms.
func drainStdin(r io.Reader) {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return // interactive terminal: nothing piped, don't block
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 1<<20))
}
