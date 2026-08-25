package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/peers"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
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

// ownCopyLine says how much of a folder is this machine's own export coming
// back. It counted one record with a plural verb — "1 record were already
// here" — the slip #1052 fixed on the neighbouring lines.
func ownCopyLine(own int) string {
	was := "were"
	if own == 1 {
		was = "was"
	}
	return fmt.Sprintf("deja: %d record%s %s already here word for word — this folder holds a copy of what this machine has\n", own, pluralS(own), was)
}

func runSync(dir string, args []string) error {
	// Bare `deja sync` is every machine deja already knows, both ways: the
	// point of a memory that spans machines is that keeping it in step is not
	// a chore anyone has to remember the hosts for.
	if len(args) == 0 || (len(args) == 1 && args[0] == "--full") {
		return runSyncAll(dir, len(args) == 1)
	}
	if len(args) < 2 {
		return fmt.Errorf("sync needs export <dir>, import <dir>, ssh <host> — or no argument at all, for every machine deja knows")
	}
	switch args[0] {
	case "ssh":
		return runSyncSSH(dir, args[1:])
	case "forget":
		// A machine deja knows is retried by every bare `deja sync`, and a
		// typo'd hostname is one of those forever — at the cost of the connect
		// timeout each time. peers.Forget did the work already and nothing
		// reached it (#1780).
		// Under the spelling deja stored, not the one typed: the line reports
		// which row is gone, and "LAPTOP forgotten" names no row (#1867).
		host := peers.Canonical(args[1])
		found, err := peers.Forget(host)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("deja does not know a machine called %q — `deja doctor` lists the ones it does", host)
		}
		fmt.Fprintf(os.Stdout, "deja: %s forgotten — bare `deja sync` will not try it again\n", hostForEcho(host))
		return nil
	case "export":
		full := false
		rest := args[1:]
		out := ""
		peer := ""
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			if a == "--full" {
				full = true
				continue
			}
			// Who this batch is for. Watermarks are per peer: without a name
			// every export shares one, so a backup taken by hand and a pull
			// from another machine each settle records the other still needs
			// — and the second one silently sends almost nothing.
			if a == "--peer" {
				if i+1 >= len(rest) {
					return fmt.Errorf("sync export: --peer needs a name")
				}
				i++
				peer = rest[i]
				continue
			}
			// A dropped flag fell back to the incremental path, which on a
			// second run has nothing left to send: `--ful` exported zero
			// records into an empty directory while the reader believed they
			// had carried their whole memory to another machine (#745).
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync export: unknown flag %q — only --full and --peer are accepted", a)
			}
			out = a
		}
		if out == "" {
			return fmt.Errorf("sync export needs a target dir")
		}
		if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
			// An index that cannot be written stops the export before a record
			// is read, and handed back `update: mkdir /…/index.db.tmp:
			// permission denied` — an internal path, on the same store where
			// search names the directory to fix (#1046).
			return ensureError(dir, err)
		}
		var n int
		var err error
		if full {
			n, err = index.ExportFull(dir, out)
		} else {
			// Folded, so one machine settles under one watermark whichever way
			// the name was spelled — a pull runs this on the remote with that
			// machine's hostname, capitalised on macOS, while the alias someone
			// types by hand usually is not (#1878).
			n, err = index.ExportTo(dir, out, peers.Identity(peer))
		}
		if err != nil {
			// `open …/deja-sync-b9849e838232-1785639771368128000.jsonl:
			// permission denied` names a file deja was about to create, with a
			// name nobody chose. The directory is what has to change, the same
			// as for the index (#798) and the notes file (#869) — this was the
			// last write path still handing back the syscall (#893).
			// The watermark for what just left is saved into the index, so an
			// unwritable index fails here too — and arrived as "check that
			// directory's permissions" for a destination that was writable and
			// already held the batch (#1046, the shape of #1031).
			if p := deniedPath(err); p != "" && !strings.HasPrefix(p, out) &&
				(errors.Is(err, fs.ErrPermission) || writeFailureReason(err) != err.Error()) {
				if n > 0 {
					return fmt.Errorf("%d records are written into %s, but deja could not record that they went: %w; the next export sends them again", n, search.SafeLine(out), ensureError(dir, err))
				}
				return ensureError(dir, err)
			}
			if parent := filepath.Dir(out); !dirExists(out) && !dirExists(parent) {
				return fmt.Errorf("cannot write the export into %s — %s is not there; the disk it lives on may have been unmounted", search.SafeLine(out), search.SafeLine(parent))
			}
			if errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("cannot write the export into %s — check that directory's permissions, or choose one you can write", search.SafeLine(out))
			}
			// A full or vanished disk in the same words as everywhere else
			// (#906); anything deja does not recognise still comes through
			// whole rather than being guessed at.
			if reason := writeFailureReason(err); reason != err.Error() {
				return fmt.Errorf("cannot write the export into %s — %s", search.SafeLine(out), reason)
			}
			return err
		}
		fmt.Fprintf(os.Stdout, "deja: exported %d record%s\n", n, pluralS(n))
		// A watermark is per machine, not per destination: the second peer you
		// hand memory to gets an empty folder and the same "exported 0
		// records" that means "you are up to date" at the first one (#982).
		// The path stays as written in the next line: it hands over a command
		// to paste, and a collapsed path names no directory. Same tension as
		// the recovery sentence in #1820 and the tombstone id in #1794.
		if n == 0 && !full && !hasSyncBatches(out) {
			fmt.Fprintf(os.Stdout, "deja: nothing has changed since the last export, and this folder holds no batch from this machine — `deja sync export %s --full` sends everything\n", out)
		}
		// Only when something was written: on a watermarked sync most runs
		// send nothing, and the line asked the reader to review a file that
		// does not exist (#1035).
		if n > 0 {
			masked := 0
			if rs, rerr := index.Redactions(dir); rerr == nil {
				masked = rs.Total
			}
			fmt.Fprintf(os.Stderr, "deja: records were redacted at index time (%d masked). pattern redaction is a floor — review the export before moving it; rotate anything that leaked.\n", masked)
		}
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
			line := fmt.Sprintf("deja: imported %d record%s", n, pluralS(n))
			if before, after := sessionsBefore, importedSessionTotal(dir); after > before {
				line += fmt.Sprintf(" — %d session%s from another machine", after-before, pluralS(after-before))
			}
			fmt.Fprintln(os.Stdout, line)
		}
		// Whether or not anything arrived: a batch that is entirely someone
		// else's copy of what this machine forgot imports zero records, and
		// "imported 0 records" alone reads as an empty batch (#968).
		// A shared folder is an outbox as well as an inbox, so a batch this
		// machine wrote comes back to it. Saying nothing made "imported 0
		// records" read as a failed transfer (#987). The claim is what deja
		// checked — the same session, instant and text — not who wrote it: a
		// batch carries no machine id (#955).
		if own := index.ImportSkippedOwn(); own > 0 {
			fmt.Fprint(os.Stdout, ownCopyLine(own))
		}
		// The count is a fact about this machine's memory, not about one
		// terminal moment: a peer who keeps sending sessions you forgot drops
		// records on every sync, and only the line below said so — gone the
		// moment the terminal scrolls (#1016).
		recordLastImport(dir, n, index.ImportSkippedForgotten(), index.ImportSkippedOwn())
		if skipped := index.ImportSkippedForgotten(); skipped > 0 {
			fmt.Fprintf(os.Stdout, "deja: %d record%s left out — they belong to sessions you forgot here (`deja forget --list`)\n", skipped, pluralS(skipped))
		}
		// A record with no session to attribute it to is dropped, and a silent
		// drop made "imported 2 records" from a 3-record batch read as a
		// complete transfer (#1118).
		if inc := index.ImportSkippedIncomplete(); inc > 0 {
			what := "it"
			if inc > 1 {
				what = "them"
			}
			fmt.Fprintf(os.Stdout, "deja: %d record%s left out — no session id to attribute %s to; the batch may be from a different tool or damaged\n", inc, pluralS(inc), what)
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
		printMemoryProofOf(dir, "deja now knows, from the machine you came from:", func(s model.Session) bool {
			return strings.HasPrefix(s.Project, "imported:")
		})
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

// lastImport is the one-line summary doctor reads back.
type lastImport struct {
	At      time.Time `json:"at"`
	Records int       `json:"records"`
	Forgot  int       `json:"forgotten_left_out,omitempty"`
	Own     int       `json:"already_here,omitempty"`
}

func lastImportPath(dir string) string { return dir + ".lastimport" }

func recordLastImport(dir string, records, forgot, own int) {
	b, err := json.Marshal(lastImport{At: time.Now(), Records: records, Forgot: forgot, Own: own})
	if err != nil {
		return
	}
	_ = os.WriteFile(lastImportPath(dir), b, 0o600)
}

func readLastImport(dir string) (lastImport, bool) {
	b, err := os.ReadFile(lastImportPath(dir))
	if err != nil {
		return lastImport{}, false
	}
	var li lastImport
	if json.Unmarshal(b, &li) != nil || li.At.IsZero() {
		return lastImport{}, false
	}
	return li, true
}
