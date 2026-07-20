package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/redact"
)

func runShare(dir string, args []string, w io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("share needs id-prefix")
	}
	s, ok, err := findByPrefix(dir, args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", args[0])
	}
	printSanitized(w, digest.Share(s, digest.ShareBudget))
	return nil
}

func printSanitized(w io.Writer, text string) {
	// Redact the whole document at once: multiline secrets (PEM private key
	// blocks) never match when scanned line-by-line.
	redacted, _ := redact.Text(text)
	fmt.Fprint(w, redacted)
	if !strings.HasSuffix(redacted, "\n") {
		fmt.Fprintln(w)
	}
}
