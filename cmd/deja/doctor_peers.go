package main

import (
	"fmt"
	"io"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
)

// doctorPeers reports the machines this one exchanges memory with, and when
// memory last moved each way.
//
// A sync that stops does not announce itself. The agent still answers, the
// history is still there, and it quietly stops growing — an expired key or a
// laptop that has not been opened in a fortnight look exactly like a week with
// nothing to send. This is the screen where that shows.
func doctorPeers(w io.Writer, dir string, now time.Time) {
	list := peers.Load()
	fmt.Fprintln(w, "Sync:")
	if len(list) == 0 {
		fmt.Fprintf(w, "  %-12s no other machines yet — `deja sync ssh <host>` once, then `deja sync` keeps them all in step\n", "peers")
		return
	}
	from := index.ImportedByMachine(dir)
	for _, p := range list {
		fmt.Fprintf(w, "  %-12s %s\n", hostForEcho(p.Host), peerLine(p, from[p.Host], now))
	}
}

// peerLine is one machine's state in one line: how long since memory moved,
// which way it has not moved, and how much of this index came from there.
func peerLine(p peers.Peer, sessions int, now time.Time) string {
	line := ""
	switch {
	case p.Last().IsZero():
		line = "never exchanged"
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
