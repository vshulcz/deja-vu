package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// A Snapshot is the full text of one served digest, kept so `deja log` can
// show after the fact exactly what an agent received. Text comes from the
// index, so it is already redacted.
type Snapshot struct {
	Time     time.Time `json:"t"`
	Kind     string    `json:"kind"`
	Sessions int       `json:"sessions,omitempty"`
	Bytes    int       `json:"bytes"`
	// Policy names the rule set that allowed this injection, so the audit
	// trail explains itself ("local+imported", "local-only").
	Policy string `json:"policy,omitempty"`
	// Terms are the query terms behind a déjà vu firing, kept so a wrong
	// "you have been here" can be explained after the fact.
	Terms []string `json:"terms,omitempty"`
	// Into is the agent session this went to, as the harness names it. Without
	// it the log says what was injected and never to whom, so the only way to
	// tell whether a recall was used is the sentence the block asks the agent
	// to say — measured on a real store, that sentence appears after 22 of 1218
	// injections, which measures reporting rather than use.
	Into   string `json:"into,omitempty"`
	Digest string `json:"digest"`
}

const (
	snapshotsToKeep  = 20
	snapshotRotateAt = 512 << 10
)

// RecordRoom is the largest record this log holds without rotating on every
// write: the rotation keeps a fixed count, so the count and the threshold
// together decide the size. Above it the file sits over the threshold for good,
// and half of two concurrent injections is rewritten away (#1971). Exported so
// the packages that set the budgets — none of which this one can import — can
// be held to it.
const RecordRoom = snapshotRotateAt / snapshotsToKeep

// RecordSize is what an answer of n bytes weighs once it is a line in this log:
// the JSON envelope around it, plus what escaping costs.
//
// Escaping is a multiplier rather than a constant, which is what makes this a
// function. Measured against a budget of 8192: plain text costs 168 bytes, a
// real digest with its newlines 558, and text that is all newlines or all
// quotes doubles, since each is two bytes in JSON. Three, not two, because of
// what a truncated binary paste does: an invalid byte becomes the replacement
// character, three bytes, and the worst shape is invalid bytes alternating with
// newlines. The ceiling there is exactly 2.5: three bytes out for the invalid
// byte, two for the cheapest neighbour that stops it collapsing — brute-forced
// over every one and two byte repeat unit, and nothing beats it. A solid run of
// invalid bytes is not the worst case and looks like the best one, since
// ToValidUTF8 collapses a run to a single character.
//
// The other 512 is the envelope, which is only a constant because the terms are
// bounded before they are written (#1988).
//
// Two things are kept out of the multiplier rather than allowed for. Control
// bytes would cost six each as \u00XX escapes, and SafeText strips them before
// any of this text becomes a digest. So would <, > and & under encoding/json's
// default HTML escaping — and those are ordinary text nobody should strip, so
// the writer turns that escaping off instead (#1982).
//
// The envelope is the record's own fields — the stamp, the kind, the policy
// name, the terms, the receiving session — measured at 168 typical and 344 with
// a long policy name, eight terms and a 128-character session id.
func RecordSize(n int) int { return 3*n + 512 }

// SnapshotPath returns the injection-snapshot log for an index dir; a sibling
// file like the usage log, so it survives full rebuilds.
func SnapshotPath(indexDir string) string {
	return strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".injections.jsonl"
}

// RecordDigest records a served digest: the counting event plus a snapshot of
// the text. raw is the size of the source transcripts the digest distilled.
// Best-effort like all usage recording.
func RecordDigest(indexDir, kind, digest string, sessions int, raw int64) {
	RecordDigestPolicy(indexDir, kind, digest, sessions, raw, "")
}

// RecordDigestTerms is RecordDigest plus the query terms, for déjà vu audits,
// and the ids of the sessions the moment surfaced.
//
// The ids were missing, which meant a déjà vu moment — the user returning to
// ground they had covered before — left no trace on the session it was about.
// It is the only signal deja has that the *user* came back, as opposed to an
// agent pulling something, and it could not reach ranking at all.
func RecordDigestTerms(indexDir, kind, digest string, sessions int, raw int64, terms []string, ids ...string) {
	RecordDigestInto(indexDir, kind, digest, "", sessions, raw, terms, ids...)
}

// RecordDigestInto is RecordDigestTerms knowing which agent session received
// the injection, so a later reading of the store can ask whether it was used.
func RecordDigestInto(indexDir, kind, digest, into string, sessions int, raw int64, terms []string, ids ...string) {
	// One instant for both logs: the event and the digest describe the same
	// injection, and separate time.Now() calls left them microseconds apart
	// with nothing else to join them on (#2294).
	at := time.Now().UTC()
	recordFullAt(indexDir, kind, len(digest), sessions, sessions == 0, raw, ids, at)
	snapshotWriteIntoAt(indexDir, kind, digest, into, sessions, "", terms, at)
}

// RecordDigestPolicy is RecordDigest plus the name of the policy that allowed
// the injection, kept with the snapshot for `deja log`.
func RecordDigestPolicy(indexDir, kind, digest string, sessions int, raw int64, policyName string) {
	RecordDigestPolicyInto(indexDir, kind, digest, "", sessions, raw, policyName)
}

// RecordDigestPolicyInto is RecordDigestPolicy for a caller that knows which
// agent session received the digest. Without it the log says what was injected
// and never to whom, which is the whole reason Into exists — and the
// session-start hook, the commonest injection there is, was recording nothing
// while holding the id (#1949).
func RecordDigestPolicyInto(indexDir, kind, digest, into string, sessions int, raw int64, policyName string) {
	at := time.Now().UTC()
	recordFullAt(indexDir, kind, len(digest), sessions, sessions == 0, raw, nil, at)
	snapshotWriteIntoAt(indexDir, kind, digest, into, sessions, policyName, nil, at)
}

// RecordServedSnapshot writes the counting event and the digest snapshot for
// one served answer, stamped alike. The MCP surfaces used to call the two
// writers back to back, which took two instants for one act and left `deja
// log` and `deja log --last` disagreeing about when it happened (#2294).
func RecordServedSnapshot(indexDir, kind, digest string, sessions int, raw int64, ids []string, policyName string) {
	at := time.Now().UTC()
	recordFullAt(indexDir, kind, len(digest), sessions, sessions == 0, raw, ids, at)
	snapshotWriteIntoAt(indexDir, kind, digest, "", sessions, policyName, nil, at)
}

// SnapshotOnly stores the digest text without writing a counting event, for
// callers that already recorded one with extra fields.
func SnapshotOnly(indexDir, kind, digest string, sessions int) {
	snapshotWrite(indexDir, kind, digest, sessions, "")
}

// SnapshotPolicy is SnapshotOnly plus the policy name for the audit trail.
func SnapshotPolicy(indexDir, kind, digest string, sessions int, policyName string) {
	snapshotWrite(indexDir, kind, digest, sessions, policyName)
}

func snapshotWrite(indexDir, kind, digest string, sessions int, policyName string) {
	snapshotWriteTerms(indexDir, kind, digest, sessions, policyName, nil)
}

func snapshotWriteTerms(indexDir, kind, digest string, sessions int, policyName string, terms []string) {
	snapshotWriteInto(indexDir, kind, digest, "", sessions, policyName, terms)
}

// snapshotLineMax is the longest line the reader takes, and therefore the
// longest one worth writing. A record past it was written and then unreadable:
// `deja log` showed nothing for an injection that happened, and the next
// rotation rewrote the file without it (#2222).
const snapshotLineMax = 4 << 20

// snapshotDigestMax is how much of a digest goes into the log. The budget is
// the line's, and one byte of text can cost six of JSON — a control byte is
// written \u00XX — so this is the ceiling that holds however the text escapes,
// without a trial encode to find out.
//
// Generous next to what anything actually injects: a session-start digest is
// 8 KB and the MCP budget is 4 KB. It is `deja://session/…`, which records the
// whole session it served, that reaches for megabytes.
const snapshotDigestMax = (snapshotLineMax - snapshotEnvelope) / 6

// snapshotEnvelope is room for the rest of the record around the digest — the
// stamp, kind, terms, policy and the session it went into.
const snapshotEnvelope = 64 << 10

// clipDigest cuts a digest down to what this file can read back, saying where
// it cut. The head is kept, because that is what an agent was shown first, and
// `Bytes` still carries the size that was served: the clipping is this file's
// business, not a claim about the injection.
func clipDigest(digest string) string {
	if len(digest) <= snapshotDigestMax {
		return digest
	}
	head := digest[:runeBoundaryAt(digest, snapshotDigestMax)]
	return head + fmt.Sprintf("\n… (clipped: the whole digest was %d bytes)", len(digest))
}

// runeBoundaryAt is n backed up to where a rune starts, so a cut lands between
// characters rather than inside one. A half rune is not an error — the encoder
// writes U+FFFD for it — but it is a mojibake tail on the last line of a digest
// someone reads.
func runeBoundaryAt(s string, n int) int {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

func snapshotWriteInto(indexDir, kind, digest, into string, sessions int, policyName string, terms []string) {
	snapshotWriteIntoAt(indexDir, kind, digest, into, sessions, policyName, terms, time.Now().UTC())
}

// snapshotWriteIntoAt is snapshotWriteInto with the instant supplied, so a
// caller writing both logs for one injection can stamp them alike (#2294).
func snapshotWriteIntoAt(indexDir, kind, digest, into string, sessions int, policyName string, terms []string, at time.Time) {
	if digest == "" {
		return
	}
	p := SnapshotPath(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	b, err := marshalSnapshot(Snapshot{Time: at, Kind: kind, Sessions: sessions,
		Bytes: len(digest), Policy: policyName, Terms: terms, Into: into, Digest: clipDigest(digest)})
	if err != nil {
		return
	}
	// The record's own size decides how much room the rotation has to leave.
	rotateSnapshots(p, len(b)+1)
	// O_RDWR rather than O_WRONLY: the append needs to read the last byte, for
	// the reason the usage log opens the same way (#1901).
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	// A process killed between a record and its newline costs that record. It
	// cost every later one too: appended onto the partial line, the new digest
	// parsed as nothing either, and `deja log --last` reported no injection at
	// all on a machine that had just served two (#1965).
	if atomicfile.EndsMidLine(f) {
		b = append([]byte{'\n'}, b...)
	}
	_, _ = f.Write(append(b, '\n'))
}

func rotateSnapshots(p string, incoming int) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() < snapshotRotateAt {
		return
	}
	// Rewritten from what the reader accepts, so a rotation also drops a line
	// deja could not have written — a side effect worth naming rather than
	// discovering: the file shrinks by more than the rotation alone would
	// explain (#1946).
	snaps := snapshotsFrom(p, snapshotsToKeep) // last appended first
	// Keeping a fixed count says nothing about the size that triggered this. A
	// read of `deja://session/…` records a whole session here, unbudgeted, and
	// twenty of those sit above the threshold for good: the file then rotates on
	// every write, and every record appended while a rotation rebuilds is
	// rewritten away — measured at half of two concurrent injections (#1971).
	// Dropping the oldest until the rebuild fits leaves the next write with
	// nothing to do.
	var buf bytes.Buffer
	for {
		buf.Reset()
		for i := len(snaps) - 1; i >= 0; i-- {
			if b, err := marshalSnapshot(snaps[i]); err == nil {
				buf.Write(append(b, '\n'))
			}
		}
		// One record over the threshold is kept anyway: a log holding the last
		// digest and nothing else is still a log, and holding none is not.
		if buf.Len()+incoming < snapshotRotateAt || len(snaps) <= 1 {
			break
		}
		snaps = snaps[:len(snaps)-1] // drop the oldest
	}
	// Same shared-temp-name defect as the usage log above (#1319).
	_ = atomicfile.Write(p, buf.Bytes(), 0o600)
}

// usable reports whether a snapshot is one deja could have written: a stamp so
// it can be placed in time, a kind so it can be named, and the digest it exists
// to carry.
func (s Snapshot) usable() bool {
	return !s.Time.IsZero() && s.Kind != "" && s.Digest != ""
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
		// The same question the usage log asks — a stamp and a kind (#1917) —
		// plus the digest, which is the whole point of this file. Without it a
		// half-written line could print a heading with no kind, or a digest
		// dated the year 1 (#1946).
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.usable() {
			out = append(out, s)
		}
	}
	// Last appended first, and no further: this is what the rotation rewrites
	// from, and there the order decides which records survive. The sibling
	// rotation in usage.go keeps an event stamped ahead of the window rather
	// than dropping it, on the grounds that deleting a recorded event on a
	// guess about someone's clock cannot be undone — retention reads the file
	// as written, and only the reader below sorts (#2140).
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Snapshots returns up to n recorded digests for an index dir, newest first.
//
// By stamp, arrival order breaking ties. Reversing the file and stopping there
// is only newest-first while the file is append-ordered by time, and it is not
// always: several agents append to one file, and a clock that moved backwards
// writes an older stamp after a newer one — the case #2105 and #2122 handle
// elsewhere. `deja log --last` reads the first of these as the most recent
// digest, and it was the last one appended (#2140).
func Snapshots(indexDir string, n int) []Snapshot {
	out := snapshotsFrom(SnapshotPath(indexDir), 0)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
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
		// The same rule the counters read by, so a line is an event on both
		// surfaces or on neither (#1917).
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.usable() {
			out = append(out, e)
		}
	}
	// By stamp, arrival order breaking ties, for the reason Snapshots above
	// takes it. This list is read, never rewritten: the usage log's own
	// rotation reads the file separately (#2140).
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// marshalSnapshot writes one record without encoding/json's HTML escaping. That
// escaping turns <, > and & into six bytes each, and a digest is often code: an
// answer at the widest budget made of angle brackets weighed 49 kB against the
// 26 kB this log holds, which is the state #1971 is about. The escaping exists
// so a document can be embedded in HTML; this file is read by deja (#1982).
func marshalSnapshot(s Snapshot) ([]byte, error) {
	// Coerced to valid UTF-8 first, and not only for tidiness: an invalid byte
	// is written as the replacement character, and whether that costs three
	// bytes or six depends on the Go release — 1.25 escapes it, 1.27 writes it
	// raw. A record whose size depends on the toolchain is not a record with a
	// bound, and a transcript picks up invalid bytes from any truncated paste.
	s.Digest = strings.ToValidUTF8(s.Digest, "\ufffd")
	s.Terms = boundTerms(s.Terms)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	// Encode appends a newline; the callers add their own.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// termsRoom is what a record carries of the query terms behind a déjà vu
// firing. They are there so a wrong "you have been here" can be explained
// afterwards, and a term cut to its first characters still does that — while an
// uncut one does not fit the envelope RecordSize allows: `prompt.Terms` treats a
// pasted path or hash as one token, and one such word wrote a record 1.8 times
// its own bound (#1988).
const termsRoom = 256

// boundTerms is the terms as the record keeps them: the same terms, each cut so
// the set fits termsRoom, and nothing dropped — a term that is missing reads as
// a term that did not match.
func boundTerms(terms []string) []string {
	total := 0
	for _, t := range terms {
		total += len(t) + 3 // the quotes and the comma
	}
	if total <= termsRoom {
		return terms
	}
	each := termsRoom/max(len(terms), 1) - 3
	if each < 8 {
		each = 8
	}
	out := make([]string, len(terms))
	for i, t := range terms {
		if len(t) > each {
			t = strings.ToValidUTF8(t[:each], "") + "…"
		}
		out[i] = t
	}
	return out
}
