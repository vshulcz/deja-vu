package main

import (
	"flag"
	"os"
	"path/filepath"
)

func starTipLine(dir string, served int) string {
	if served < 25 || dir == "" {
		return ""
	}
	stamp := filepath.Join(dir, "star-tip-shown")
	if _, err := os.Stat(stamp); err == nil {
		return ""
	}
	if err := os.WriteFile(stamp, []byte("1\n"), 0o600); err != nil {
		return ""
	}
	return "  If it helps      gh repo star vshulcz/deja-vu · https://github.com/vshulcz/deja-vu"
}

func maybeStarTip(dir string, served int) string {
	if flag.Lookup("test.v") != nil {
		return ""
	}
	return starTipLine(dir, served)
}
