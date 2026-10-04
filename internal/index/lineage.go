package index

import (
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// Lineage is ids and every session that counts as one of them when recall
// decides what not to answer with.
//
// A session id names one transcript, and an agent can be writing more than one:
// a sub-agent it spawned is its own work seen from inside, and the hooks that
// say which session is live fire under the parent's id, so the child's id is
// never stamped (#4547).
//
// The other way too: a spawned agent asking — an opencode task session runs
// its own hooks — is its parent's work, and the parent is live and the one
// that asked it (#4548).
//
// And a fork's source: the fork opens with a copy of its turns, so the source
// handed back is the fork's own opening under another id (#4549, #4251). The
// harness that says which session it forked from is believed. Claude Code and
// opencode say nothing, and there sessions that open on the same turn at the
// same millisecond are copies of one conversation. Only the copy the asker
// already holds is left out: a session whose last record the asker has too.
// A fork that went on past what it copied holds work its source never saw, so
// the source resumed still gets it, and so does a sibling fork. A session that
// merely shares the project is still there.
//
// heads are sessions read off their own store for an asker the index does not
// hold yet — a fork's first prompts come before any build has seen it.
//
// Read off the manifest, which recall has already loaded: no record is read,
// so the cost is one pass over the sessions in memory.
func Lineage(dir string, ids map[string]bool, heads ...model.Session) map[string]bool {
	out := make(map[string]bool, len(ids))
	for id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	// A head is an asker the index has not seen yet, so it is the newer of any
	// session it opens alike with: everything indexed it matches, it copied.
	// An indexed asker is checked against what it holds.
	headOpenings := map[uint64]bool{}
	askers := map[uint64][]SessionMeta{}
	parentOf := func(kind, parent string) {
		if parent != "" && (spawnedKind(kind) || kind == "fork") {
			out[parent] = true
		}
	}
	for _, h := range heads {
		if ids[h.ID] {
			parentOf(h.Kind, h.Parent)
			if o := SessionOpening(h); o != 0 {
				headOpenings[o] = true
			}
		}
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return out
	}
	for _, meta := range m.Sessions {
		if ids[meta.ID] {
			parentOf(meta.Kind, meta.Parent)
			if meta.Opening != 0 {
				askers[meta.Opening] = append(askers[meta.Opening], meta)
			}
		}
	}
	held := heldRecords(dir, m)
	for _, meta := range m.Sessions {
		if spawnedKind(meta.Kind) && meta.Parent != "" && ids[meta.Parent] {
			out[meta.ID] = true
		}
		if meta.Opening == 0 || ids[meta.ID] {
			continue
		}
		if headOpenings[meta.Opening] {
			out[meta.ID] = true
			continue
		}
		for _, a := range askers[meta.Opening] {
			if held(a, meta) {
				out[meta.ID] = true
				break
			}
		}
	}
	return out
}

// heldRecords answers whether asker holds other's last record, the same role,
// time and text: other is then a copy the asker carries, and nothing in it is
// news to the asker. Records are read only for sessions that open alike, which
// takes a fork, so a store without forks reads none. A read that fails answers
// no, which keeps the session on the page.
func heldRecords(dir string, m Manifest) func(asker, other SessionMeta) bool {
	path := filepath.Join(dir, "records.bin")
	tables := tablesFromManifest(m)
	sets := map[string]map[string]bool{}
	key := func(r Record) string {
		return r.Role + "\x00" + strconv.FormatInt(r.Time.UnixNano(), 10) + "\x00" + r.Text
	}
	return func(asker, other SessionMeta) bool {
		k := asker.Harness + ":" + asker.ID
		set, ok := sets[k]
		if !ok {
			if recs, err := recordsForKey(path, tables, k); err == nil {
				set = make(map[string]bool, len(recs))
				for _, r := range recs {
					set[key(r)] = true
				}
			}
			sets[k] = set
		}
		if len(set) == 0 {
			return false
		}
		recs, err := recordsForKey(path, tables, other.Harness+":"+other.ID)
		if err != nil {
			return false
		}
		// What the harness writes into a source after the fork is not work:
		// a backgrounded session gains "No response requested." and a task
		// notification, and the fork that holds every turn before them got
		// its source back as recall (#4251).
		for i := len(recs) - 1; i >= 0; i-- {
			if !afterForkNoise(recs[i]) {
				return set[key(recs[i])]
			}
		}
		return false
	}
}

// afterForkNoise reports a record the harness wrote on its own rather than a
// turn anyone took: an envelope under the user role, Claude Code's reply to a
// turn that wanted none, an empty tool output.
func afterForkNoise(r Record) bool {
	t := strings.TrimSpace(r.Text)
	switch {
	case t == "":
		return true
	case r.Role == roleSummary:
		return true
	case r.Role == "user":
		return harnessPreamble(t)
	case r.Role == "assistant":
		return t == "No response requested."
	}
	return false
}

// HasSession reports whether the index holds a session with this id.
func HasSession(dir, id string) bool {
	if id == "" {
		return false
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return false
	}
	for _, meta := range m.Sessions {
		if meta.ID == id {
			return true
		}
	}
	return false
}

// spawnedKind reports whether a session's kind is the harness's word for an
// agent another session spawned.
func spawnedKind(kind string) bool {
	switch kind {
	case "sidechain", "subagent":
		return true
	}
	return false
}

// openingTextBytes is how much of the opening turn the fingerprint reads: the
// index caps a message's text and a transcript read straight off disk does not.
const openingTextBytes = 256

// SessionOpening fingerprints the turn a session opens with: its harness, the
// first user turn's time to the millisecond, and the start of its text after
// redaction, which the index applies before it stores anything. Zero when the
// session has no timed user turn, so a store without times never matches.
func SessionOpening(s model.Session) uint64 {
	for _, msg := range s.Messages {
		// A compacted session opens on the harness's summary, filed under its
		// own role; a fork of it opens on the same record.
		if (msg.Role != "user" && msg.Role != roleSummary) || msg.Time.IsZero() {
			continue
		}
		// The text as the index keeps it, whichever side this runs on: the
		// manifest fingerprints stored messages, a hook a transcript read off
		// disk, and a compacted session's first turn opens on the harness's
		// preamble, which the index strips.
		text, _ := redact.Text(nfcfold.Compose(strings.TrimSpace(stripSelfRecall(msg.Text))))
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if len(text) > openingTextBytes {
			text = text[:openingTextBytes]
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(s.Harness))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.FormatInt(msg.Time.UnixMilli(), 10)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(text))
		if sum := h.Sum64(); sum != 0 {
			return sum
		}
		return 1
	}
	return 0
}
