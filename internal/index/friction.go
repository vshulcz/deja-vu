package index

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Friction is what this machine keeps tripping over: one specific error, in
// several separate sessions.
//
// The detection lives here rather than in the command because it runs twice —
// once over a session being ingested, to fingerprint what it hit, and once
// over the record log when someone asks. Two copies of these rules would drift,
// and the first symptom would be a first-screen line the command cannot
// reproduce.
type Friction struct {
	Text     string
	Sessions []SessionMeta
	Last     time.Time
	// Hash is the signature the sessions share, which WallHandle turns into
	// the name a person settles a wall by.
	Hash uint64
}

// WallHandle is the short name `deja friction` prints for a wall and
// `--settle` takes: the top half of its signature, so the same error keeps the
// same handle across a rebuild and across machines.
func WallHandle(hash uint64) string {
	return fmt.Sprintf("%08x", hash>>32)
}

// NewestStart is when the latest of these sessions began, which is what a
// settle is compared against.
func NewestStart(ms []SessionMeta) time.Time {
	var out time.Time
	for _, m := range ms {
		t := m.Started
		if t.IsZero() {
			t = m.Updated
		}
		if t.After(out) {
			out = t
		}
	}
	return out
}

const (
	// frictionSessionCap bounds what one session contributes to the manifest,
	// and is set high on purpose. The first screen names a count and points at
	// `deja friction`; a cap low enough to lose a hit makes the two disagree,
	// which reads as the tool being broken. Measured on 1149 sessions: at 6 the
	// screen said 8 where the command said 11, at 100 it said 10, and one
	// session alone carries over a hundred distinct error lines. The whole
	// field costs 2.7 KB across that corpus — the exactness is nearly free.
	frictionSessionCap = 256
	frictionLineMin    = 20
	// The cap is about recognising a line, not about how much of it fits on a
	// screen — every surface that prints one clips it to the width it has. Set
	// at 120 it dropped the errors that carry a path: a build naming a module
	// and a file, a registry naming an image, a database naming a host. Over
	// the store on this machine that was 2,319 lines naming a wall the list
	// knows, against 7,053 it recognised — a third again, thrown away for
	// length alone, and 1,406 of them fit inside 160 (#2438). A bound there
	// still is: a page of output pasted as one line is nobody's recognisable
	// wall, and hashing it makes a new wall every run.
	frictionLineMax = 200
	// FrictionMinSessions is how many separate sessions must hit an error
	// before it is worth saying. Twice is a coincidence.
	FrictionMinSessions = 3
)

func frictionHash(line string) uint64 {
	// Masked here rather than in FrictionLine: the numbers a machine hands out
	// must not split one failure into a wall per run, and the line a reader is
	// shown should still be the one that was printed, with its real port in it
	// (#2369).
	h := fnv.New64a()
	_, _ = h.Write([]byte(maskVolatileNumbers(line)))
	return h.Sum64()
}

// frictionHashes fingerprints what a session tripped over, for the manifest.
// Hashes rather than text, for the reason SessionMeta.Asked gives: the
// manifest is read on every search and the only thing a caller needs from it
// is whether two sessions hit the same wall.
func frictionHashes(ms []model.Message) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, m := range ms {
		if m.Role != roleToolOutput {
			continue
		}
		for raw := range strings.SplitSeq(m.Text, "\n") {
			line, ok := FrictionLine(raw)
			if !ok {
				continue
			}
			h := frictionHash(line)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
			if len(out) >= frictionSessionCap {
				return out
			}
		}
	}
	return out
}

// PromotedNoteMetas returns the sessions that carry a promoted note's state —
// the shape a decision has after it crosses a machine boundary, where the local
// notes file knows nothing about it. Manifest only, so a caller in a hook pays a
// map walk rather than a read (#2510).
//
// allow gates a session's project the way TopFriction's does.
func PromotedNoteMetas(dir string, allow func(project string) bool) []SessionMeta {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	excluded := sources.ExcludedHarnesses()
	var out []SessionMeta
	for _, meta := range m.Sessions {
		if meta.Lifecycle == "" {
			continue
		}
		if allow != nil && !allow(meta.Project) {
			continue
		}
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		out = append(out, meta)
	}
	return out
}

// FrictionSessions counts the sessions that hit one wall, by the signature a
// FixPair carries. The manifest already holds every session's hashes and is
// cached, so a caller in a hook pays a map walk rather than a read — which is
// what lets the line that arrives at the moment of a failure say how often this
// machine has been here (#2491).
//
// allow gates a session's project the way TopFriction's does.
func FrictionSessions(dir string, sig uint64, allow func(project string) bool) int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0
	}
	pol := policy.Load()
	excluded := sources.ExcludedHarnesses()
	n := 0
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		if slices.Contains(meta.Hit, sig) {
			n++
		}
	}
	return n
}

// FindFriction picks the wall worth showing on a screen with room for one. See
// TopFriction for allow.
//
// A wall someone settled is skipped: the screens that name one say this
// machine keeps hitting it, and the reader has said it does not (#5628).
func FindFriction(dir string, allow func(project string) bool) (Friction, bool) {
	out := OpenFriction(dir, 5, allow)
	if len(out) == 0 {
		return Friction{}, false
	}
	for _, f := range out {
		if !isLowSignalShellFriction(f.Text) {
			return f, true
		}
	}
	return out[0], true
}

func isLowSignalShellFriction(text string) bool {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "no matches found:") {
		return true
	}
	if rest, ok := strings.CutSuffix(s, " not found"); ok && rest != "" && strings.Trim(rest, "=") == "" {
		return true
	}
	return false
}

// TopFriction returns the walls this machine keeps running into, most-hit
// first. It runs over the manifest alone, so a caller on the first screen or
// in a session-start hook pays nothing for the search; only the sessions
// carrying a winning hash are read back, and only to recover the text the
// hash stands for.
//
// allow gates a session's project by the caller's activation, so an all-imported
// wall stays off a screen whose trust rule withholds imported memory. A nil
// allow counts every session; callers that filter per wall themselves (the
// environment block) pass nil and keep their own gate.
func TopFriction(dir string, n int, allow func(project string) bool) []Friction {
	return topFriction(dir, n, allow, nil)
}

// OpenFriction is TopFriction without the walls a person settled with `deja
// friction --settle`, for the surfaces that tell an agent or a reader what is
// still in the way. They are dropped before the cut, so a settled wall does not
// cost the next one its slot.
func OpenFriction(dir string, n int, allow func(project string) bool) []Friction {
	return topFriction(dir, n, allow, policy.LoadSettled())
}

func topFriction(dir string, n int, allow func(project string) bool, settled policy.Settled) []Friction {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	byHash := map[uint64][]SessionMeta{}
	// The ignore rule, here rather than in allow: allow takes a project, and
	// the rule matches on the session's path — measured in #2652, every one of
	// 305 ignored sessions on a real store matched by path and none by
	// project. So no caller can apply it, and both of these speak unasked: the
	// brief claimed five sessions for a wall where `deja friction` said three,
	// and the session-start block counted the same two (#2658).
	pol := policy.Load()
	excluded := sources.ExcludedHarnesses()
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		for _, h := range meta.Hit {
			byHash[h] = append(byHash[h], meta)
		}
	}
	type cluster struct {
		hash  uint64
		metas []SessionMeta
	}
	var cs []cluster
	for h, metas := range byHash {
		if len(metas) < FrictionMinSessions {
			continue
		}
		if settled.Hides(WallHandle(h), NewestStart(metas)) {
			continue
		}
		// Ties broken by key: the newest carrier's line is the wall's text,
		// and two carriers updated in the same second came out of a map walk
		// in either order, so the line shown changed from run to run.
		sort.Slice(metas, func(i, j int) bool {
			if !metas[i].Updated.Equal(metas[j].Updated) {
				return metas[i].Updated.After(metas[j].Updated)
			}
			return metas[i].Key() < metas[j].Key()
		})
		cs = append(cs, cluster{h, metas})
	}
	sort.Slice(cs, func(i, j int) bool {
		if len(cs[i].metas) != len(cs[j].metas) {
			return len(cs[i].metas) > len(cs[j].metas)
		}
		// A wall the machine stopped running into is history, not friction.
		if !cs[i].metas[0].Updated.Equal(cs[j].metas[0].Updated) {
			return cs[i].metas[0].Updated.After(cs[j].metas[0].Updated)
		}
		return cs[i].hash < cs[j].hash
	})
	// Recovered as the ranking is walked, not after a cut: `meta.Hit` is a
	// union that never forgets while the text is read back out of the records,
	// so a rewritten transcript leaves a hash nothing can show. Cutting first
	// spent the slot on it — a wall like that at the top left `deja brief`
	// with no friction line at all and the next wall unreported (#2544).
	//
	// Every hash still shares one pass per session: a session usually carries
	// several walls, and each read fills in whatever it holds.
	want := map[uint64]string{}
	for _, c := range cs {
		want[c.hash] = ""
	}
	var out []Friction
	rd := newFrictionReader(dir, m)
	rd.texts = loadFrictionTexts(dir, m, want)
	for i, c := range cs {
		if want[c.hash] == "" {
			// The walls after this one that are still unread, newest carrier
			// first: what the next calls will most likely read, so one pass
			// over the log can serve them too.
			rd.ahead = rd.ahead[:0]
			for _, next := range cs[i+1:] {
				if n > 0 && len(rd.ahead) >= n-len(out)-1 {
					break
				}
				if want[next.hash] == "" {
					rd.ahead = append(rd.ahead, next.metas[0])
				}
			}
			frictionTexts(rd, c.metas, want, c.hash)
		}
		if want[c.hash] == "" {
			continue
		}
		out = append(out, Friction{Text: want[c.hash], Sessions: c.metas, Last: c.metas[0].Updated, Hash: c.hash})
		if n > 0 && len(out) == n {
			break
		}
	}
	return out
}

// frictionTexts recovers what the wanted hashes stood for by reading back the
// sessions that carry one of them, filling in every hash a session yields —
// one session usually carries several walls, and reading it once per wall was
// the difference between one lookup and N.
func frictionTexts(rd *frictionReader, metas []SessionMeta, want map[uint64]string, target uint64) {
	for _, meta := range metas {
		if e, ok := rd.texts[meta.Key()]; ok {
			// The first line behind each wall, which is all the loop below
			// would take from this session.
			for h, line := range e.lines {
				if cur, wanted := want[h]; wanted && cur == "" {
					want[h] = line
				}
			}
			if want[target] != "" {
				return
			}
			continue
		}
		outputs, ok := rd.toolOutput(meta)
		if !ok {
			continue
		}
		for _, text := range outputs {
			for raw := range strings.SplitSeq(text, "\n") {
				line, ok := FrictionLine(raw)
				if !ok {
					continue
				}
				h := frictionHash(line)
				if cur, wanted := want[h]; wanted && cur == "" {
					want[h] = line
				}
			}
		}
		// Stop on this cluster's own text, not on any text: a session can fill
		// in a neighbour's hash while carrying nothing for this one.
		if want[target] != "" {
			return
		}
	}
}

// frictionReadBatch is how many sessions one pass over the record log reads
// for frictionTexts. Each pass walks the whole log, and a session start read it
// once per wall: six passes over 200 MB on a real store, most of the
// environment block's time.
const frictionReadBatch = 8

// frictionReader hands frictionTexts the tool output of a session. A session
// it has not read yet is read together with the ones in ahead, so the walls a
// ranking walks next share the pass. Only tool-output records are decoded: the
// rest of a session is skipped by its prefix.
type frictionReader struct {
	dir   string
	m     Manifest
	ahead []SessionMeta
	read  map[string][]string
	// texts answers for the sessions frictext.bin holds; nil reads them all.
	texts map[string]frictionEntry
}

func newFrictionReader(dir string, m Manifest) *frictionReader {
	return &frictionReader{dir: dir, m: m, read: map[string][]string{}}
}

func (rd *frictionReader) toolOutput(meta SessionMeta) ([]string, bool) {
	key := meta.Key()
	if out, ok := rd.read[key]; ok {
		return out, true
	}
	// A promoted note is reordered on load, so it keeps the one-session path.
	if meta.Harness == "deja" {
		return rd.loadOne(meta)
	}
	keys := map[string]bool{key: true}
	for _, next := range rd.ahead {
		if len(keys) >= frictionReadBatch {
			break
		}
		k := next.Key()
		if _, done := rd.read[k]; done || next.Harness == "deja" {
			continue
		}
		if _, held := rd.texts[k]; held {
			continue
		}
		keys[k] = true
	}
	got := make(map[string][]string, len(keys))
	err := eachRecordForKeysInRoles(filepath.Join(rd.dir, "records.bin"), tablesFromManifest(rd.m), keys,
		map[string]bool{roleToolOutput: true}, func(r Record) {
			// Of two transcripts sharing an id, the copy the row names, as
			// the fixes and the command tables take it: the other's records
			// sit in the log where the pass that read it put them, which a
			// rebuild does not (#5504).
			if !foreignPath(r.SourcePath, rd.m.Sessions[r.Key]) {
				got[r.Key] = append(got[r.Key], r.Text)
			}
		})
	if err != nil {
		// What one session's read would have said, session by session.
		return rd.loadOne(meta)
	}
	for k := range keys {
		rd.read[k] = got[k]
	}
	return rd.read[key], true
}

func (rd *frictionReader) loadOne(meta SessionMeta) ([]string, bool) {
	s, ok, err := loadSessionMeta(rd.dir, rd.m, meta)
	if err != nil || !ok {
		return nil, false
	}
	var out []string
	for _, msg := range s.Messages {
		if msg.Role == roleToolOutput {
			out = append(out, msg.Text)
		}
	}
	return out, true
}
