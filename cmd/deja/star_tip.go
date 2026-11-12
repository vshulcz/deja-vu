package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

func starTipTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	if os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func starTipLine(dir string, served int) string {
	if served < 15 || dir == "" {
		return ""
	}
	stamp := filepath.Join(dir, "star-tip-shown")
	if _, err := os.Stat(stamp); err == nil {
		return ""
	}
	if err := atomicfile.Write(stamp, []byte("1\n"), 0o600); err != nil {
		return ""
	}
	return "  If it helps      gh repo star vshulcz/deja-vu · https://github.com/vshulcz/deja-vu"
}

func maybeStarTip(dir string, served int) string {
	if flag.Lookup("test.v") != nil || !starTipTTY(os.Stderr) {
		return ""
	}
	return starTipLine(dir, served)
}
