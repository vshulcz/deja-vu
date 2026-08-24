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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	var f file
	b, err := os.ReadFile(Path())
	if err != nil {
		return nil
	}
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	out := f.Peers[:0:0]
	for _, p := range f.Peers {
		if p.Host = strings.TrimSpace(p.Host); p.Host == "" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
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
	list := Load()
	i := -1
	for k := range list {
		if list[k].Host == host {
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
	list := Load()
	out := list[:0:0]
	found := false
	for _, p := range list {
		if p.Host == host {
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
