// Package peers remembers the machines this one exchanges memory with.
//
// A sync had to be told its host every time, so with three machines it was six
// commands and a thing to remember rather than a thing that happens. Worse,
// nothing recorded that an exchange had happened at all: a laptop whose ssh key
// stopped working kept answering from a history that quietly stopped growing,
// and there was no screen anywhere that would have said so.
package peers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Peer is one machine and when memory last moved each way. Push and pull are
// kept apart because they fail apart: a host that accepts what this machine
// sends but has stopped sending back is a broken sync that looks fine.
type Peer struct {
	Host     string    `json:"host"`
	LastPush time.Time `json:"last_push,omitempty"`
	LastPull time.Time `json:"last_pull,omitempty"`
	// LastError is why the most recent exchange failed, empty once one
	// succeeds. A peer that has been failing for a week is the thing this file
	// exists to make visible.
	LastError string `json:"last_error,omitempty"`
}

// Last is the more recent of the two directions.
func (p Peer) Last() time.Time {
	if p.LastPull.After(p.LastPush) {
		return p.LastPull
	}
	return p.LastPush
}

type file struct {
	Peers []Peer `json:"peers"`
}

// Path is where the list lives. Beside policy.json rather than inside the
// index: which machines you sync with outlives any one index, and a rebuild
// must not lose them.
func Path() string {
	if p := os.Getenv("DEJA_PEERS_FILE"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(sources.Home(), ".config")
	}
	return filepath.Join(base, "deja", "peers.json")
}

// Load reads the list. Any read or parse failure means no peers — a sync must
// not break because a config file is malformed, and doctor is where that gets
// reported.
func Load() []Peer {
	list, _ := Snapshot()
	return list
}

// Snapshot is Load and Problem from one read of the file, for a caller that
// reports both. Asking twice re-read a file that a sync may be rewriting
// between the two calls, so the list and the reason could describe different
// states of it.
func Snapshot() ([]Peer, string) {
	var f file
	b, err := os.ReadFile(Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Never synced is not a fault.
			return nil, ""
		}
		return nil, err.Error()
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err.Error()
	}
	out := f.Peers[:0:0]
	seen := map[string]int{}
	for _, p := range f.Peers {
		if p.Host = strings.TrimSpace(p.Host); p.Host == "" {
			continue
		}
		// One machine named twice was two rows, and Record only ever updated
		// the first: the other showed a months-old failure beside the same
		// machine reporting a sync this morning, and a bare `deja sync` opened
		// two connections to it (#1853). A file can hold the same host twice
		// through a hand-edit or a restored backup, and — until #1867 — through
		// deja's own writes, when the two syncs spelled the host differently.
		// The row keeps the spelling it was written with: that is what the
		// report prints and what sessions_from_there is counted against.
		if at, ok := seen[identity(p.Host)]; ok {
			out[at] = mergePeers(out[at], p)
			continue
		}
		seen[identity(p.Host)] = len(out)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, ""
}

// Problem reports why the file could not be read, and "" when there is nothing
// wrong — including when there is no file at all, which is a machine that has
// never synced rather than a fault.
//
// Load deliberately treats every failure as "no peers", so that a malformed
// config cannot stop a sync; its comment says doctor is where that surfaces.
// This is the half that lets doctor say it (#1840). A caller that wants both
// takes Snapshot, which reads the file once.
func Problem() string {
	_, why := Snapshot()
	return why
}

// identity is what makes two names one machine. ssh lowercases a host before
// it matches it and a DNS name is case-insensitive (RFC 4343), so `Laptop` and
// `laptop` are one machine — comparing byte for byte gave it two rows, two
// connections from a bare `deja sync`, and a watermark each, so the second
// pushed everything the first had already delivered (#1867).
//
// Only the part after the last @: the half before it is an account name, and
// `Root@box` and `root@box` are two logins.
func identity(host string) string {
	at := strings.LastIndex(host, "@")
	return host[:at+1] + strings.ToLower(host[at+1:])
}

// Identity is that rule for a caller outside this package: anything keyed by a
// machine name has to agree with the list about which names are one machine.
// `deja doctor` counts imported sessions by the name the sending machine calls
// itself, which is a hostname — capitalised on macOS — against an ssh alias
// that usually is not (#1876).
func Identity(host string) string {
	return identity(strings.TrimSpace(host))
}

// Canonical is how a host named on the command line should be spelled for the
// rest of a run: the spelling deja already stored for that machine, if it knows
// it. Watermarks are namespaced by the peer string, so `deja sync ssh laptop`
// against a row written `Laptop` had a watermark of its own and pushed
// everything the machine already had (#1867).
func Canonical(host string) string {
	host = strings.TrimSpace(host)
	for _, p := range Load() {
		if identity(p.Host) == identity(host) {
			return p.Host
		}
	}
	return host
}

// mergePeers folds two rows for one machine. Each direction keeps its newest
// timestamp, and the error belongs to whichever exchange happened last: an old
// failure is not news about a machine that has synced since.
func mergePeers(a, b Peer) Peer {
	out := a
	if b.LastPush.After(out.LastPush) {
		out.LastPush = b.LastPush
	}
	if b.LastPull.After(out.LastPull) {
		out.LastPull = b.LastPull
	}
	if b.Last().After(a.Last()) {
		out.LastError = b.LastError
	}
	return out
}

// Record notes an exchange with a host: which way it went, and whether it
// worked. A host is added the first time it is named, so `deja sync ssh mini`
// once is all it takes for `deja sync` to know about mini afterwards.
func Record(host string, pulled bool, when time.Time, err error) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	// Locked around the read, the edit and the write: without it two syncs
	// finishing at once each wrote back the list they had read, and the second
	// one dropped the first one's machine (#1883).
	return withLock(func() error {
		return recordLocked(host, pulled, when, err)
	})
}

func recordLocked(host string, pulled bool, when time.Time, err error) error {
	list := Load()
	i := -1
	for k := range list {
		if identity(list[k].Host) == identity(host) {
			i = k
			break
		}
	}
	if i < 0 {
		list = append(list, Peer{Host: host})
		i = len(list) - 1
	}
	if err != nil {
		// The failure is recorded, the timestamp is not: "last exchanged
		// tuesday" has to mean memory actually moved.
		list[i].LastError = trimError(err.Error())
	} else if when.IsZero() {
		// A caller with nothing to report — a push that found nothing to send,
		// so no connection was opened. The host is remembered; what it did
		// last time is not overwritten with a zero (#1780).
	} else {
		list[i].LastError = ""
		if pulled {
			list[i].LastPull = when.UTC()
		} else {
			list[i].LastPush = when.UTC()
		}
	}
	return save(list)
}

// Forget drops a host from the list. Reports whether it was there.
func Forget(host string) (bool, error) {
	host = strings.TrimSpace(host)
	var found bool
	err := withLock(func() error {
		var ferr error
		found, ferr = forgetLocked(host)
		return ferr
	})
	return found, err
}

// forgetLocked is Forget with the list already held.
func forgetLocked(host string) (bool, error) {
	list := Load()
	out := list[:0:0]
	found := false
	for _, p := range list {
		if identity(p.Host) == identity(host) {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return false, nil
	}
	return true, save(out)
}

func save(list []Peer) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Host < list[j].Host })
	b, err := json.MarshalIndent(file{Peers: list}, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Written whole through a temp file: a sync interrupted midway must not
	// leave a half-written list that Load then silently reads as no peers.
	// atomicfile rather than a temp of our own, because ours was named for the
	// file rather than for the writer — two syncs wrote into the one temp and
	// renamed it in turn, so one of them could publish a list holding half of
	// each, or fail outright on a temp the other had already renamed (#1883).
	return atomicfile.Write(path, append(b, '\n'), 0o600)
}

// trimError keeps a failure short enough to sit on one line of a report. The
// whole of an ssh failure is several lines of remote banner.
func trimError(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 160
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
