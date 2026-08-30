package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
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
		fmt.Fprintf(w, "# %s · %s · %d session%s · %s%s\n\n", s.Kind, s.Time.Local().Format("2006-01-02 15:04"), s.Sessions, pluralS(s.Sessions), humanBytes(int64(s.Bytes)), snapshotTail(s))
		fmt.Fprintln(w, s.Digest)
		// This is the newest digest by its stamp (#2140), so a stamp from
		// ahead of the clock holds the spot until the clock catches up — and
		// the digest an agent actually received last is then not the one on
		// screen. The list above names the same fault in its own words.
		if index.StampedAhead(s.Time, time.Now()) {
			fmt.Fprintf(os.Stderr, "deja: this digest is stamped later than this machine's clock, so it leads by its stamp — a digest served since may be older by that stamp and sit below it\n")
		}
		return nil
	}
	events, total := usage.EventsCounted(dir, n)
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
		into := ""
		switch {
		case e.Into != "":
			into = " · into: " + e.Into
		case e.Unreadable:
			// The injection happened and the session it went to was in a
			// payload deja could not decode. Saying so is the difference from
			// a host that sent nothing at all (#2161).
			into = " · into: unknown (the host sent a payload deja could not read)"
		}
		fmt.Fprintf(w, "%s  %-14s %s%s%s%s\n", e.Time.Local().Format("2006-01-02 15:04"), e.Kind, humanBytes(int64(e.Bytes)), sess, into, mark)
	}
	if total > len(events) {
		// Nobody typed the 20 — it is the default above — and this is the
		// audit trail, where a list that stops without saying so reads as
		// everything deja served. The same sentence blame and show print
		// (#2299, #2296, #2305).
		fmt.Fprintf(w, "\nshowing %d of %d — `deja log %d` shows the rest\n", len(events), total, total)
	}
	fmt.Fprintln(w, "\nuse `deja log --last` to see the exact text of the most recent injected digest")
	// The log's stamps are deja's own, written at recall time, so one in the
	// future means the clock moved backwards since — and those events sit above
	// everything from then on, while the status bar leaves them out of its
	// counters. Two surfaces reading one file and disagreeing with nothing to
	// say why is the shape #696 rejected; `deja last` and `doctor` name it
	// already (#2105, #2107, #2122).
	if n := eventsStampedAhead(events, time.Now()); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d event%s stamped later than this machine's clock — %s at the top of this list, and the counters leave %s out\n",
			n, pluralS(n), pluralThatThose(n), pluralThoseOnes(n))
	}
	return nil
}

// pluralThoseOnes is the pronoun for the tail of that sentence, so it never
// says "leaves it out" about several events or "them" about one.
func pluralThoseOnes(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// eventsStampedAhead counts the listed events whose stamp is after now, by the
// same rule the other surfaces count sessions with.
func eventsStampedAhead(events []usage.Event, now time.Time) int {
	n := 0
	for _, e := range events {
		if index.StampedAhead(e.Time, now) {
			n++
		}
	}
	return n
}

// snapshotTail is the part of the header that depends on what the record
// happens to carry. The record knows which agent session received the digest
// and which terms fired it — both were added to explain an injection after the
// fact (#1494) — and only --json ever said them, so the surface a person types
// answered "what was injected" and never to whom or why (#2301). Fields the
// record does not carry print nothing, the way policy already did.
func snapshotTail(s usage.Snapshot) string {
	var b strings.Builder
	if s.Policy != "" {
		b.WriteString(" · policy: " + s.Policy)
	}
	switch {
	case s.Into != "":
		b.WriteString(" · into: " + s.Into)
	case s.Unreadable:
		// The list row says this; the header has to as well, or --last is
		// back to the ambiguity #2301 added it to remove (#2161).
		b.WriteString(" · into: unknown (the host sent a payload deja could not read)")
	}
	if len(s.Terms) > 0 {
		b.WriteString(" · terms: " + strings.Join(s.Terms, ", "))
	}
	return b.String()
}
