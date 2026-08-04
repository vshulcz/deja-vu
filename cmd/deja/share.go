package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/redact"
)

func runShare(dir string, args []string, w io.Writer) error {
	if len(args) < 1 {
		return idPrefixNeeded(dir, "share needs an id-prefix", "share needs id-prefix")
	}
	// Everything after the id used to be ignored, so `deja share <id> --to
	// out.md` printed the share to the terminal and wrote no file — and said
	// nothing about either (#1002). promote refuses unknown flags; this is the
	// same command shape one letter away.
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("share: unknown flag %q — share writes to stdout (redirect it), and `deja promote <id> --to <path>` is the one that writes a file", a)
		}
	}
	s, ok, err := findByPrefix(dir, args[0])
	noteAmbiguousPrefix(dir, args[0], "sharing")
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
	redacted, counts := redact.Text(text)
	fmt.Fprint(w, redacted)
	if !strings.HasSuffix(redacted, "\n") {
		fmt.Fprintln(w)
	}
	// The boundary line goes to stderr so piped output stays clean. Precise
	// non-claims: pattern redaction is a floor, not a guarantee.
	// Secrets are redacted at index time, so by the time a share is built
	// most of them are already markers in the text and this pass finds
	// nothing new. Counting only what this pass replaced reported "0 secrets
	// masked" on a document visibly full of them — the opposite of what the
	// line is for.
	masked := strings.Count(redacted, redact.Marker)
	for _, n := range counts {
		masked += n
	}
	fmt.Fprintf(os.Stderr, "deja: %d secrets masked in this share. pattern redaction is a floor — review before sending; rotate anything that leaked.\n", masked)
}
