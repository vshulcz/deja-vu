package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// doctorPeers reports the machines this one exchanges memory with, and when
// memory last moved each way.
//
// A sync that stops does not announce itself. The agent still answers, the
// history is still there, and it quietly stops growing — an expired key or a
// laptop that has not been opened in a fortnight look exactly like a week with
// nothing to send. This is the screen where that shows.
func doctorPeers(w io.Writer, dir string, now time.Time) {
	list, why := peers.Snapshot()
	fmt.Fprintln(w, "Sync:")
	if why != "" {
		// Load treats a malformed file as "no peers" so a sync cannot be
		// stopped by one, and says doctor is where that surfaces. Without this
		// line it surfaced nowhere, and a broken file read as a machine that
		// simply has no peers (#1840).
		fmt.Fprintf(w, "  %-12s %s could not be read — %s\n", "peers", peers.Path(), safeForStatusline(why, 200))
		return
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "  %-12s no other machines yet — `deja sync ssh <host>` once, then `deja sync` keeps them all in step\n", "peers")
		return
	}
	from := importsByPeerName(dir)
	// The column is padded by what the terminal draws, not by how many runes
	// the name has: %-12s gave a 32-character name no padding at all and
	// treated a CJK name as one column per character (#1821). A name wider
	// than the column keeps its whole self and puts its state on the next
	// line, indented to where every other state starts.
	names := make([]string, len(list))
	width := doctorPeerColumn
	for i, p := range list {
		names[i] = hostForEcho(p.Host)
		if c := termwidth.Columns(names[i]); c > width && c <= doctorPeerColumnMax {
			width = c
		}
	}
	for i, p := range list {
		state := peerLine(p, peerSessionCount(from, p), now)
		pad := width - termwidth.Columns(names[i])
		if pad < 0 {
			fmt.Fprintf(w, "  %s\n  %s %s\n", names[i], strings.Repeat(" ", width), state)
			continue
		}
		fmt.Fprintf(w, "  %s%s %s\n", names[i], strings.Repeat(" ", pad), state)
	}
}

const (
	// doctorPeerColumn is the narrowest the name column gets, so a machine
	// called "laptop" does not sit alone against the left margin.
	doctorPeerColumn = 12
	// doctorPeerColumnMax is how far the column may grow for a long name
	// before that name gets a line of its own: past this the state would be
	// pushed off an ordinary terminal.
	doctorPeerColumnMax = 32
)

// peerStampedAhead reports a stamp this machine's clock cannot account for.
//
// A minute of slack rather than the bare `t.After(now)` the session side uses:
// a peer's timestamps are written by deja itself from time.Now(), so a file
// copied from a machine a few hundred milliseconds ahead — or an NTP step
// landing between the write and the read — would otherwise put "one of the two
// clocks is wrong" against a peer that is perfectly healthy. A minute is also
// the granularity the line already speaks in, since everything under it reads
// as "just now" (#1855).
func peerStampedAhead(t, now time.Time) bool {
	return index.StampedAhead(t, now.Add(peerClockSlack))
}

// peerClockSlack is how far ahead a stamp may be before it is worth a sentence.
const peerClockSlack = time.Minute

// peerLine is one machine's state in one line: how long since memory moved,
// which way it has not moved, and how much of this index came from there.
func peerLine(p peers.Peer, sessions int, now time.Time) string {
	line := ""
	switch {
	case p.Last().IsZero():
		line = "never exchanged"
	case peerStampedAhead(p.Last(), now):
		// The age would be negative, and everything under a minute reads as
		// "just now" — so the peer that most needs looking at read as the one
		// that just synced (#1855).
		line = "last exchange stamped " + p.Last().Local().Format("2006-01-02") +
			", later than this machine's clock — one of the two is wrong"
	default:
		line = "last exchange " + doctorAgo(now.Sub(p.Last()))
	}
	// Push and pull fail apart, and a host that takes what this machine sends
	// while sending nothing back is a broken sync that reads as a working one.
	switch {
	case p.LastPush.IsZero() && !p.LastPull.IsZero():
		line += ", nothing sent yet"
	case p.LastPull.IsZero() && !p.LastPush.IsZero():
		line += ", nothing received yet"
	}
	if sessions > 0 {
		line += fmt.Sprintf(", %s from there", doctorCount(sessions, "session"))
	}
	if p.LastError != "" {
		// The stored error usually quotes the host back, so it carries whatever
		// the name carried.
		line += " — last attempt failed: " + safeForStatusline(p.LastError, 200)
	}
	return line
}

// doctorAgo is a rough age. Anything under a minute is "just now": a sync that
// ran seconds ago and one that ran forty seconds ago are the same news.
func doctorAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%s ago", doctorCount(int(d.Minutes()), "minute"))
	case d < 24*time.Hour:
		return fmt.Sprintf("%s ago", doctorCount(int(d.Hours()), "hour"))
	default:
		return fmt.Sprintf("%s ago", doctorCount(int(d.Hours()/24), "day"))
	}
}

// importsByPeerName counts imported sessions under the same rule the peers
// list uses for identity. The stamp on an imported session is what the sending
// machine calls itself — a hostname, capitalised on macOS — while the row it
// belongs to is an ssh alias someone typed, so an exact lookup reported none of
// the sessions a machine had sent (#1876). A genuinely different name — alias
// `work`, hostname `build-box` — is still a miss, and nothing here can fix
// that.
func importsByPeerName(dir string) map[string]int {
	out := map[string]int{}
	for name, n := range index.ImportedByMachine(dir) {
		out[peers.Identity(name)] += n
	}
	return out
}

// peerSessionCount is what arrived from one machine, by either name it is known
// under: the alias in the row, or what the machine calls itself, learned from a
// pull (#1887).
func peerSessionCount(from map[string]int, p peers.Peer) int {
	if n := from[peers.Identity(p.Host)]; n > 0 {
		return n
	}
	if p.Machine == "" {
		return 0
	}
	return from[peers.Identity(p.Machine)]
}

// learnPeerMachine notes what the host deja just pulled from calls itself.
//
// The pairing is only ever visible here: the alias is what the connection used,
// and the origin is stamped on the records that arrived. `before` is the count
// per machine taken ahead of the import, so only what this exchange brought is
// attributed — a batch from a third machine already in the index must not
// rename the peer.
func learnPeerMachine(dir, host string, before map[string]int) {
	best, grown := "", 0
	for name, n := range importsByPeerName(dir) {
		if n > before[peers.Identity(name)] {
			best = name
			grown++
		}
	}
	// Only when one machine grew. A batch can carry records from more than one
	// origin — a machine that itself imported from a third — and another
	// process can import while this runs, so picking the largest of several
	// would sometimes write down a machine this host never sent. Learning
	// nothing leaves the count at zero, which is what it was.
	if grown != 1 || peers.Identity(best) == peers.Identity(host) {
		return
	}
	// Best effort: a sync that worked must not fail because a name could not be
	// written down.
	_ = peers.Learn(host, best)
}
