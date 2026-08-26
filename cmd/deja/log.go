package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// runLog answers "what did deja actually feed my agents": recent usage events
// as a table, or the verbatim text of the last served digest with --last.
func runLog(dir string, args []string) error {
	return runLogTo(os.Stdout, dir, args)
}

func runLogTo(w io.Writer, dir string, args []string) error {
	n := 20
	jsonOut := false
	last := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--last":
			last = true
		default:
			x, err := strconv.Atoi(a)
			if err != nil {
				return fmt.Errorf("log: unknown flag %q", a)
			}
			// A number is not a flag, and saying so sends the reader looking
			// for a flag they never typed (#733).
			if x <= 0 {
				return fmt.Errorf("log: how many entries to show must be positive, got %q", a)
			}
			n = x
		}
	}
	if last {
		snaps := usage.Snapshots(dir, 1)
		if len(snaps) == 0 {
			// `--json` answers in JSON, including when the answer is that
			// there is nothing: this shape is one object, and null is how a
			// missing object is spelled. The sentence went out under the flag
			// too, so a script polling this got a parse error (#1975).
			if jsonOut {
				fmt.Fprintln(w, "null")
				return nil
			}
			fmt.Fprintln(w, "deja: no injected digests recorded yet — they appear after a hook or MCP recall fires")
			return nil
		}
		s := snaps[0]
		if jsonOut {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		}
		pol := ""
		if s.Policy != "" {
			pol = " · policy: " + s.Policy
		}
		fmt.Fprintf(w, "# %s · %s · %d session%s · %s%s\n\n", s.Kind, s.Time.Local().Format("2006-01-02 15:04"), s.Sessions, pluralS(s.Sessions), humanBytes(int64(s.Bytes)), pol)
		fmt.Fprintln(w, s.Digest)
		return nil
	}
	events := usage.Events(dir, n)
	if jsonOut {
		// A nil slice encodes as null, and null is not an empty list: len()
		// raises, iteration raises, `jq '.[]'` errors. Every other
		// machine-readable output in deja keeps its shape when there is
		// nothing to report, and this is the one a script polls (#733).
		if events == nil {
			events = []usage.Event{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	}
	if len(events) == 0 {
		fmt.Fprintln(w, "deja: no usage recorded yet — events appear when agents search, recall, or receive injected context")
		return nil
	}
	for _, e := range events {
		mark := ""
		if e.FoundNothing() {
			mark = "  (empty result)"
		}
		sess := ""
		if e.Sessions > 0 {
			sess = fmt.Sprintf(" · %d session%s", e.Sessions, pluralS(e.Sessions))
		}
		fmt.Fprintf(w, "%s  %-14s %s%s%s\n", e.Time.Local().Format("2006-01-02 15:04"), e.Kind, humanBytes(int64(e.Bytes)), sess, mark)
	}
	fmt.Fprintln(w, "\nuse `deja log --last` to see the exact text of the most recent injected digest")
	return nil
}
