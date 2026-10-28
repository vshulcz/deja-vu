package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// The warmup sentinel is a claim: its first line is the request stamp, and
// once the detached `deja index` starts it adds a second line naming itself —
// "owner <pid> <start> <host>". The start time is what tells the owner apart
// from a later process that got the same pid. Without the owner line, the only
// thing to go on was the age of the claim and of the status file, and a worker
// that went quiet under I/O contention for two minutes was replaced while it
// was still running, then replaced again (#5719).

// warmupClaimEnv carries the stamp of the claim a warmup child was started
// for, so it can tell its own claim from one that replaced it.
const warmupClaimEnv = "DEJA_WARMUP_CLAIM"

type warmupClaim struct {
	stamp int64
	owner *warmupOwner
}

type warmupOwner struct {
	pid   int
	start string
	host  string
}

func (o warmupOwner) line() string {
	return "owner " + strconv.Itoa(o.pid) + " " + o.start + " " + o.host
}

// parseWarmupClaim reads a sentinel's content. ok is false for an empty or
// unreadable stamp; a malformed owner line is ignored, which leaves the claim
// to the time-based rules.
func parseWarmupClaim(b []byte) (warmupClaim, bool) {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	stamp, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return warmupClaim{}, false
	}
	c := warmupClaim{stamp: stamp}
	if len(lines) > 1 {
		f := strings.Fields(lines[1])
		if len(f) == 4 && f[0] == "owner" {
			if pid, err := strconv.Atoi(f[1]); err == nil && pid > 0 {
				c.owner = &warmupOwner{pid: pid, start: f[2], host: f[3]}
			}
		}
	}
	return c, true
}

type ownerState int

const (
	ownerUnknown ownerState = iota // no owner line, another machine, or the OS would not say
	ownerLive
	ownerGone
)

// state reports whether the claim's owner is still running. A process with
// the owner's pid but another start time is a different process: the owner is
// gone and the pid was handed out again.
func (c warmupClaim) state() ownerState {
	if c.owner == nil || c.owner.host != warmupHost() {
		return ownerUnknown
	}
	start, alive, known := processStart(c.owner.pid)
	switch {
	case !known:
		return ownerUnknown
	case !alive || start != c.owner.start:
		return ownerGone
	default:
		return ownerLive
	}
}

// warmupHost names this machine in a claim. An index on a shared volume can
// be claimed from another machine, whose pids mean nothing here.
func warmupHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "-"
	}
	return strings.Join(strings.Fields(h), "_")
}

// readWarmupClaim reads the sentinel in dir.
func readWarmupClaim(dir string) (warmupClaim, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "warmup.sentinel"))
	if err != nil {
		return warmupClaim{}, false
	}
	return parseWarmupClaim(b)
}

// warmupOwnerLive reports a claim whose owner is known to be running, however
// long ago it last published progress.
func warmupOwnerLive(dir string) bool {
	c, ok := readWarmupClaim(dir)
	return ok && c.state() == ownerLive
}

// ownWarmupClaim reports whether the sentinel at path is still the claim this
// warmup child was started for.
func ownWarmupClaim(path string) (warmupClaim, bool) {
	want := os.Getenv(warmupClaimEnv)
	if want == "" {
		return warmupClaim{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return warmupClaim{}, false
	}
	c, ok := parseWarmupClaim(b)
	if !ok || strconv.FormatInt(c.stamp, 10) != want {
		return warmupClaim{}, false
	}
	return c, true
}

// adoptWarmupClaim writes this process into its claim as the owner. It runs
// first thing in the warmup child, before the freshness walk and before the
// index lock, so a child that is slow to start a walk or waiting for the lock
// already reads as alive. The parent records the child too, right after the
// spawn; this covers a parent that could not.
func adoptWarmupClaim() {
	path := os.Getenv("DEJA_WARMUP_SENTINEL")
	if path == "" {
		return
	}
	if c, ok := ownWarmupClaim(path); ok {
		recordWarmupOwner(path, c.stamp, os.Getpid())
	}
}

// recordWarmupOwner writes pid into the claim stamped stamp, if the sentinel
// still holds that claim and pid is running. A child that already finished
// and cleared its claim is not written back: the sentinel is gone, or holds
// another stamp. Should the write land just after such a clear, the claim
// names a process that is gone and the next request replaces it at once.
func recordWarmupOwner(path string, stamp int64, pid int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	c, ok := parseWarmupClaim(b)
	if !ok || c.stamp != stamp {
		return
	}
	start, alive, known := processStart(pid)
	if !known || !alive {
		return
	}
	o := warmupOwner{pid: pid, start: start, host: warmupHost()}
	if c.owner != nil && *c.owner == o {
		return
	}
	body := strconv.FormatInt(stamp, 10) + "\n" + o.line() + "\n"
	_ = atomicfile.Write(path, []byte(body), 0o600)
}

// clearWarmupSentinel drops the claim this warmup child was started for, and
// only that one: a child that outlived its claim — replaced as dead by an
// older deja, or by the time-based fallback — must not delete the claim of
// the build that replaced it. A child started without a claim stamp (an older
// parent, or the variable set by hand) removes the path as before.
func clearWarmupSentinel() {
	path := os.Getenv("DEJA_WARMUP_SENTINEL")
	if path == "" {
		return
	}
	if os.Getenv(warmupClaimEnv) == "" {
		_ = os.Remove(path)
		return
	}
	if _, ok := ownWarmupClaim(path); ok {
		_ = os.Remove(path)
	}
}

// warmupClaimHeld decides whether an existing claim stands. A claim whose
// owner is running stands however old it is; one whose owner is gone is
// replaced at once. Without an owner to ask, the claim's age decides, as it
// did before owners were recorded (#875, #1065).
func warmupClaimHeld(dir string, now time.Time, c warmupClaim) bool {
	switch c.state() {
	case ownerLive:
		return true
	case ownerGone:
		return false
	case ownerUnknown:
		return within(now.Sub(time.Unix(0, c.stamp)), warmupRetryAfter) && !warmupLooksDead(dir, now, c.stamp)
	}
	return false
}
