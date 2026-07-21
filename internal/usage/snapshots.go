package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A Snapshot is the full text of one served digest, kept so `deja log` can
// show after the fact exactly what an agent received. Text comes from the
// index, so it is already redacted.
type Snapshot struct {
	Time     time.Time `json:"t"`
	Kind     string    `json:"kind"`
	Sessions int       `json:"sessions,omitempty"`
	Bytes    int       `json:"bytes"`
	Digest   string    `json:"digest"`
}

const (
	snapshotsToKeep  = 20
	snapshotRotateAt = 512 << 10
)

// SnapshotPath returns the injection-snapshot log for an index dir; a sibling
// file like the usage log, so it survives full rebuilds.
func SnapshotPath(indexDir string) string {
	return strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".injections.jsonl"
}

// RecordDigest records a served digest: the counting event plus a snapshot of
// the text. raw is the size of the source transcripts the digest distilled.
// Best-effort like all usage recording.
func RecordDigest(indexDir, kind, digest string, sessions int, raw int64) {
	RecordResultRaw(indexDir, kind, len(digest), sessions, sessions == 0, raw)
	if digest == "" {
		return
	}
	p := SnapshotPath(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	rotateSnapshots(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Snapshot{Time: time.Now().UTC(), Kind: kind, Sessions: sessions, Bytes: len(digest), Digest: digest})
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

func rotateSnapshots(p string) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() < snapshotRotateAt {
		return
	}
	snaps := snapshotsFrom(p, snapshotsToKeep) // newest first
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	for i := len(snaps) - 1; i >= 0; i-- {
		if b, err := json.Marshal(snaps[i]); err == nil {
			_, _ = f.Write(append(b, '\n'))
		}
	}
	_ = f.Close()
	_ = os.Rename(tmp, p)
}

// snapshotsFrom reads a snapshot file and returns up to n entries, newest
// first. n <= 0 means all.
func snapshotsFrom(p string, n int) []Snapshot {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Snapshot
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var s Snapshot
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Digest != "" {
			out = append(out, s)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Snapshots returns up to n recorded digests for an index dir, newest first.
func Snapshots(indexDir string, n int) []Snapshot {
	return snapshotsFrom(SnapshotPath(indexDir), n)
}

// Events returns up to n usage events, newest first. n <= 0 means all.
func Events(indexDir string, n int) []Event {
	f, err := os.Open(Path(indexDir))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Kind != "" {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
