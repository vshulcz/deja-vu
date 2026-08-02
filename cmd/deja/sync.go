package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

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
			if errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("cannot write the export into %s — check that directory's permissions, or choose one you can write", out)
			}
			return err
		}
		fmt.Fprintf(os.Stdout, "deja: exported %d records\n", n)
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
		n, err := index.Import(dir, args[1])
		// What arrived is printed first even when a file was refused: the
		// records that made it are in, and the reader needs both halves
		// (#891).
		if n > 0 {
			fmt.Fprintf(os.Stdout, "deja: imported %d records\n", n)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Fprintln(os.Stdout, "deja: imported 0 records")
		}
		return nil
	default:
		return fmt.Errorf("unknown sync command %q", args[0])
	}
}
