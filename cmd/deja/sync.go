package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// importedSessionTotal counts the sessions this index holds that came from
// another machine.
func importedSessionTotal(dir string) int {
	total := 0
	for _, n := range index.ImportedSessionCounts(dir) {
		total += n
	}
	return total
}

func runSync(dir string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("sync needs export <dir>, import <dir>, or ssh <host>")
	}
	switch args[0] {
	case "ssh":
		return runSyncSSH(dir, args[1:])
	case "export":
		full := false
		rest := args[1:]
		out := ""
		for _, a := range rest {
			if a == "--full" {
				full = true
				continue
			}
			// A dropped flag fell back to the incremental path, which on a
			// second run has nothing left to send: `--ful` exported zero
			// records into an empty directory while the reader believed they
			// had carried their whole memory to another machine (#745).
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync export: unknown flag %q — only --full is accepted", a)
			}
			out = a
		}
		if out == "" {
			return fmt.Errorf("sync export needs a target dir")
		}
		if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
			return err
		}
		var n int
		var err error
		if full {
			n, err = index.ExportFull(dir, out)
		} else {
			n, err = index.Export(dir, out)
		}
		if err != nil {
			// `open …/deja-sync-b9849e838232-1785639771368128000.jsonl:
			// permission denied` names a file deja was about to create, with a
			// name nobody chose. The directory is what has to change, the same
			// as for the index (#798) and the notes file (#869) — this was the
			// last write path still handing back the syscall (#893).
			if parent := filepath.Dir(out); !dirExists(out) && !dirExists(parent) {
				return fmt.Errorf("cannot write the export into %s — %s is not there; the disk it lives on may have been unmounted", out, parent)
			}
			if errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("cannot write the export into %s — check that directory's permissions, or choose one you can write", out)
			}
			// A full or vanished disk in the same words as everywhere else
			// (#906); anything deja does not recognise still comes through
			// whole rather than being guessed at.
			if reason := writeFailureReason(err); reason != err.Error() {
				return fmt.Errorf("cannot write the export into %s — %s", out, reason)
			}
			return err
		}
		fmt.Fprintf(os.Stdout, "deja: exported %d records\n", n)
		// A watermark is per machine, not per destination: the second peer you
		// hand memory to gets an empty folder and the same "exported 0
		// records" that means "you are up to date" at the first one (#982).
		if n == 0 && !full && !hasSyncBatches(out) {
			fmt.Fprintf(os.Stdout, "deja: nothing has changed since the last export, and this folder holds no batch from this machine — `deja sync export %s --full` sends everything\n", out)
		}
		masked := 0
		if rs, rerr := index.Redactions(dir); rerr == nil {
			masked = rs.Total
		}
		fmt.Fprintf(os.Stderr, "deja: records were redacted at index time (%d masked). pattern redaction is a floor — review the export before moving it; rotate anything that leaked.\n", masked)
		return nil
	case "import":
		for _, a := range args[2:] {
			return fmt.Errorf("sync import: unexpected argument %q — it takes one directory", a)
		}
		sessionsBefore := importedSessionTotal(dir)
		n, err := index.Import(dir, args[1])
		// What arrived is printed first even when a file was refused: the
		// records that made it are in, and the reader needs both halves
		// (#891).
		if n > 0 {
			// Sessions, not only records: "records" is deja's unit, and what
			// arrived is what doctor and every other surface counts (#929).
			line := fmt.Sprintf("deja: imported %d records", n)
			if before, after := sessionsBefore, importedSessionTotal(dir); after > before {
				line += fmt.Sprintf(" — %d sessions from another machine", after-before)
			}
			fmt.Fprintln(os.Stdout, line)
		}
		// Whether or not anything arrived: a batch that is entirely someone
		// else's copy of what this machine forgot imports zero records, and
		// "imported 0 records" alone reads as an empty batch (#968).
		if skipped := index.ImportSkippedForgotten(); skipped > 0 {
			fmt.Fprintf(os.Stdout, "deja: %d record%s left out — they belong to sessions you forgot here (`deja forget --list`)\n", skipped, pluralS(skipped))
		}
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Fprintln(os.Stdout, "deja: imported 0 records")
			return nil
		}
		// The end of a move to a new machine is the same moment as an install,
		// and install proves it with real lines rather than a count (#929).
		printMemoryProof(dir, "deja now knows, from the machine you came from:")
		return nil
	default:
		return fmt.Errorf("unknown sync command %q", args[0])
	}
}

// hasSyncBatches reports whether a directory already holds exported batches.
func hasSyncBatches(out string) bool {
	entries, err := os.ReadDir(out)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "deja-sync-") && strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}
