package index

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// FindByPrefix is FindByPrefixIn over every harness.
func FindByPrefix(dir, p string) (model.Session, bool, error) {
	return FindByPrefixIn(dir, p, "")
}

// FindByPrefixIn resolves a prefix among one harness's sessions, or among all
// of them when harness is empty. Filtering after the pick handed back the
// newest match across every harness and then refused it, so `show c --harness
// codex` said no session matches while a codex session started with "c".
func FindByPrefixIn(dir, p, harness string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	// Every id has "" as a prefix, so an empty one used to resolve to whichever
	// session sorted first — and PrefixMatches has always answered 0 for it.
	// That is the disagreement #853 is about, with the sign flipped: the count
	// says nothing matches and the resolver opens something anyway. It reached a
	// user through the MCP resource URI (#1728), which is a boundary that can be
	// guarded, but the shared lookup is where the two answers have to agree.
	if p == "" {
		return model.Session{}, false, nil
	}
	// Non-blocking: the session-start hook reaches this through the handoff
	// tip, and a blocking lock made the agent wait out the entire rebuild —
	// twelve seconds on a real corpus, on the very upgrade that triggers one.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	// Every id has the empty string as a prefix, so a blank selector matched
	// whichever session came first and the caller handed back a transcript
	// nobody asked for. The MCP resource reader guards its own (#1728) and the
	// CLI now guards share and ctx (#2259); doing it here ends the class.
	if strings.TrimSpace(p) == "" {
		return model.Session{}, false, nil
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	matches := resolveSelectorMatches(inHarness(m.Sessions, harness), p, nil)
	if len(matches) == 0 {
		return model.Session{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return newestFirstMeta(matches[i], matches[j]) })
	return loadSessionMeta(dir, m, matches[0])
}

// resolveSelectorMatches runs the six-rung selector resolution ladder shared by
// FindByPrefixIn and PrefixMatchesIn (#853, #921, #1049, #1620, #2401, #4667)
// so the resolver and the match counter cannot drift apart.
func resolveSelectorMatches(sessions map[string]SessionMeta, p string, allow func(project string) bool) []SessionMeta {
	if strings.TrimSpace(p) == "" {
		return nil
	}
	allowed := func(meta SessionMeta) bool {
		return allow == nil || allow(meta.Project)
	}
	var matches []SessionMeta
	var exact []SessionMeta
	for _, meta := range sessions {
		if !allowed(meta) {
			continue
		}
		if strings.HasPrefix(meta.ID, p) {
			matches = append(matches, meta)
			if meta.ID == p {
				exact = append(exact, meta)
			}
		}
	}
	// A complete id is that session, even when it also starts a newer one's:
	// "abc" opened abc2, and --json refused it with advice to type more.
	if len(exact) > 0 {
		return exact
	}
	// The id the session came with, before the loose pass: import rewrites the
	// id and OrigID keeps the old one (#1049), and a reader who typed that id in
	// full should not be handed a local session that merely contains it as a
	// substring. Nothing consulted OrigID at all, so `show`/`resume`/`ctx` said
	// no session matches about a session deja holds and prints that id for.
	if len(matches) == 0 {
		for _, meta := range sessions {
			if !allowed(meta) {
				continue
			}
			if meta.OrigID != "" && strings.HasPrefix(meta.OrigID, p) {
				matches = append(matches, meta)
			}
		}
	}
	// The id Claude Code's remote control shows, alone or as the
	// claude.ai/code URL it sits in. No file is named by it, so a session
	// continued from the phone or the browser was "no session matches" (#4667).
	if len(matches) == 0 {
		if rid := sources.RemoteSessionID(p); rid != "" {
			for _, meta := range sessions {
				if !allowed(meta) {
					continue
				}
				if meta.RemoteID != "" && strings.HasPrefix(meta.RemoteID, rid) {
					matches = append(matches, meta)
				}
			}
		}
	}
	if len(matches) == 0 {
		for _, meta := range sessions {
			if !allowed(meta) {
				continue
			}
			if idLooselyMatches(meta.ID, p) || (meta.OrigID != "" && idLooselyMatches(meta.OrigID, p)) {
				matches = append(matches, meta)
			}
		}
	}
	// Last: the `harness:id` shape deja prints in `forget --list` and in
	// promote's receipts, which every reading command refused (#921).
	if len(matches) == 0 {
		if harness, id := splitSelector(p); harness != "" {
			for _, meta := range sessions {
				if !allowed(meta) {
					continue
				}
				if strings.EqualFold(meta.Harness, harness) &&
					(strings.HasPrefix(meta.ID, id) || idLooselyMatches(meta.ID, id) ||
						(meta.OrigID != "" && (strings.HasPrefix(meta.OrigID, id) || idLooselyMatches(meta.OrigID, id)))) {
					matches = append(matches, meta)
				}
			}
		}
	}
	// Last, and only when every exact form found nothing: the same id in the
	// other case. A uuid is case-insensitive by RFC 4122 and harnesses print it
	// either way, so a pasted id was answered with "no session matches" about a
	// session deja holds (#1620). Kept last because ids elsewhere are not all
	// uuids — where case carries meaning, the exact match above has already won.
	if len(matches) == 0 {
		for _, meta := range sessions {
			if !allowed(meta) {
				continue
			}
			if idFoldMatches(meta.ID, p) || (meta.OrigID != "" && idFoldMatches(meta.OrigID, p)) {
				matches = append(matches, meta)
			}
		}
	}
	return matches
}

// idFoldMatches is the prefix and loose tests with case ignored, for the last
// rung of the ladder in FindByPrefix and PrefixMatches (#1620).
func idFoldMatches(id, p string) bool {
	lid, lp := strings.ToLower(id), strings.ToLower(p)
	return strings.HasPrefix(lid, lp) || idLooselyMatches(lid, lp)
}

// PrefixMatches counts the sessions a prefix resolves to. FindByPrefix picks
// the newest of them, which is the right default and a silent one: a
// single-character prefix matched eleven sessions on a real store and the
// reader had no way to know they were looking at a choice rather than at the
// only answer.
func PrefixMatches(dir, p string) int {
	return PrefixMatchesAllowed(dir, p, nil)
}

// PrefixMatchesAllowed counts the same way for the sessions a caller is
// allowed to see. The note a reader gets about an ambiguous prefix used to
// count every match, so a rule withholding a peer's work still announced that
// the work exists, and the advice — reach for a longer prefix — led to a
// session that answers with the rule instead (#2401). A nil allow counts
// everything, which is what a caller with no rules of its own wants.
func PrefixMatchesAllowed(dir, p string, allow func(project string) bool) int {
	return PrefixMatchesIn(dir, p, "", allow)
}

// PrefixMatchesIn counts within one harness, the way FindByPrefixIn resolves.
func PrefixMatchesIn(dir, p, harness string, allow func(project string) bool) int {
	if dir == "" {
		dir = DefaultDir()
	}
	if strings.TrimSpace(p) == "" {
		return 0
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0
	}
	return len(resolveSelectorMatches(inHarness(m.Sessions, harness), p, allow))
}

// inHarness keeps one harness's sessions; an empty harness keeps all of them
// except harnesses excluded by DEJA_EXCLUDE_HARNESSES.
func inHarness(ss map[string]SessionMeta, harness string) map[string]SessionMeta {
	excluded := excludedHarnesses()
	if harness == "" && len(excluded) == 0 {
		return ss
	}
	out := map[string]SessionMeta{}
	for k, meta := range ss {
		if excluded[strings.ToLower(meta.Harness)] {
			continue
		}
		if harness == "" || strings.EqualFold(meta.Harness, harness) {
			out[k] = meta
		}
	}
	return out
}

// idLooselyMatches accepts what a reader can actually copy off the screen.
// Result lines elide the middle of a long id, so the copied text is a head and
// a tail rather than a prefix (#707) — and it carries the "…" itself, which
// appears in no id, so a prefix or substring test can never hit it (#853).
func idLooselyMatches(id, p string) bool {
	if idElided(p) {
		return idMatchesElided(id, p)
	}
	return strings.Contains(id, p)
}

// idElided reports whether p is an id as a result line printed it.
func idElided(p string) bool { return strings.Contains(p, "…") }

// idMatchesElided reads the printed form back: the head and tail it stands for.
// A destructive selector takes only this half of idLooselyMatches — a substring
// selector would make `forget --session s1` also match `deja-note-claude-s1`
// and destroy the decision #690 and #841 keep (#855).
func idMatchesElided(id, p string) bool {
	head, tail, ok := strings.Cut(p, "…")
	return ok && strings.HasPrefix(id, head) && strings.HasSuffix(id, tail)
}

// PrefixHarnesses names the harnesses holding a session whose id is exactly the
// given string.
//
// Two harnesses can carry the same id — that is what #698 is about, and it
// happens naturally when a transcript is copied between tools. There the advice
// "use a longer prefix" cannot be followed, because the ids are the same
// string; --harness is the only thing that separates them (#719).
func PrefixHarnesses(dir, id string) []string {
	if dir == "" {
		dir = DefaultDir()
	}
	if id == "" {
		return nil
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	excluded := excludedHarnesses()
	var out []string
	for _, meta := range m.Sessions {
		if excluded[strings.ToLower(meta.Harness)] {
			continue
		}
		if meta.OrigID == id {
			out = append(out, meta.Harness)
			continue
		}
		if meta.ID == id {
			out = append(out, meta.Harness)
		}
	}
	sort.Strings(out)
	return out
}

// FindByIdentity resolves the exact composite identity emitted by machine
// search and recent output. Unlike the human prefix command, it never guesses
// between harnesses or accepts a shortened native ID.
func FindByIdentity(dir, harness, id string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	// The floor, before the records: this loader serves a whole session to
	// `show` and to the MCP tools, and a store written before deja learned to
	// mask something it now masks holds text this build would not write. The
	// ranked paths rebuild first; this one has no ranking to hang that on, so
	// it says what the state is and serves nothing (#3617).
	if mustRebuildBeforeAnswering(m, version) {
		return model.Session{}, false, ErrStoreWithheld
	}
	meta, ok := metaForIdentity(m, harness, id)
	if !ok {
		return model.Session{}, false, nil
	}
	return loadSessionMeta(dir, m, meta)
}

func metaForIdentity(m Manifest, harness, id string) (SessionMeta, bool) {
	if meta, ok := m.Sessions[model.SessionKey(harness, id)]; ok {
		return meta, true
	}
	for _, cand := range m.Sessions {
		if cand.Harness == harness && cand.OrigID == id {
			return cand, true
		}
	}
	return SessionMeta{}, false
}

// Identity names one session the way machine output does.
type Identity struct {
	Harness string
	ID      string
}

// Key returns the identity's canonical "harness:id" composite identifier.
func (id Identity) Key() string { return model.SessionKey(id.Harness, id.ID) }

// FindManyByIdentity is FindByIdentity for a list. Records live in one
// append-only log with no per-session offsets, so resolving a single identity
// streams the whole of records.bin; calling it in a loop streams it once per
// session. `deja files` did that 250 times over a 59 MB log and spent 1.6 s of
// its 1.9 s there (#1069). One pass, all keys.
//
// Sessions come back in the order asked for, with identities the manifest does
// not know dropped.
func FindManyByIdentity(dir string, ids []Identity) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if len(ids) == 0 {
		return nil, nil
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(ids))
	for _, want := range ids {
		if meta, found := metaForIdentity(m, want.Harness, want.ID); found {
			metas = append(metas, meta)
		}
	}
	if len(metas) == 0 {
		return nil, nil
	}
	return sessionsForMetas(dir, metas)
}

// ChildrenOf lists the sessions an agent spawned from this one, newest first.
// Only the harnesses that record the edge themselves produce any: deja does
// not guess a parent from timing or naming (#1385).
func ChildrenOf(dir, id string) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if id == "" {
		return nil, nil
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	excluded := excludedHarnesses()
	var out []model.Session
	for _, meta := range m.Sessions {
		if excluded[strings.ToLower(meta.Harness)] {
			continue
		}
		// A fork names its source as its parent too, and nothing spawned it.
		if meta.Parent == id && meta.Kind != model.KindFork {
			out = append(out, sessionFromMeta(meta))
		}
	}
	sort.Slice(out, func(i, j int) bool { return newestFirstSession(out[i], out[j]) })
	return out, nil
}

// FindByID looks a session up when only its id is known. Hook payloads carry
// one without naming the harness, and the id is unique in practice: the
// harnesses that generate them use uuids or their own prefixed ids.
func FindByID(dir, id string) (model.Session, bool, error) {
	return FindByIDPreferProject(dir, id, "")
}

// FindByIDPreferProject is FindByID for a caller that knows where it is
// standing. A preference, not a filter: a session outside the project still
// answers when nothing inside it does.
// The freshest match is the right guess only while the copies are the same
// conversation; two projects can hold a session with one id, and then the
// freshest is whichever was touched last, which has nothing to do with the one
// asking (#1999). A project that names one of them settles it, and the rest of
// the rule is unchanged.
func FindByIDPreferProject(dir, id, project string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if id == "" {
		return model.Session{}, false, nil
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	best, found := pickSessionMetaByID(m.Sessions, project, func(meta SessionMeta) bool {
		return meta.ID == id
	})
	if !found {
		best, found = pickSessionMetaByID(m.Sessions, project, func(meta SessionMeta) bool {
			return meta.OrigID == id
		})
	}
	if !found {
		return model.Session{}, false, nil
	}
	return loadSessionMeta(dir, m, best)
}

func pickSessionMetaByID(sessions map[string]SessionMeta, project string, match func(SessionMeta) bool) (SessionMeta, bool) {
	excluded := excludedHarnesses()
	var best SessionMeta
	var found bool
	bestNear := -1
	for _, meta := range sessions {
		if excluded[strings.ToLower(meta.Harness)] || !match(meta) {
			continue
		}
		near := -1
		if project != "" {
			near = projectDistance(meta.Project, project)
		}
		switch {
		case !found, nearerProject(near, bestNear):
			best, found, bestNear = meta, true, near
		case near == bestNear && betterIDMatch(meta, best):
			best = meta
		}
	}
	return best, found
}

// projectDistance measures a session's project against the directory the caller
// is standing in. A project is recorded as the directory's own name, sometimes
// with its parent — "app" or "w/app" — so the caller's path is matched by its
// last segments rather than whole.
//
// And by its ancestors' too: a session started in a subdirectory is re-projected
// onto the repository root once it has touched enough files under it
// (projectFromPaths), so the recorded name can be two segments the cwd only
// reaches by walking up. Bare names match at the leaf alone, where the caller
// actually is — an ancestor matching by base would make every worktree named
// "wt" the same project.
//
// It answers with how far up the match was found — 0 where the caller stands —
// so a session recorded against this very directory outranks one recorded
// against a directory three levels above it. Without that, an agent someone ran
// from their home directory could outrank the project's own session by being
// the fresher of the two.
func projectDistance(recorded, cwd string) int {
	if recorded == "" || cwd == "" {
		return -1
	}
	cwd = path.Clean(filepath.ToSlash(cwd))
	if strings.EqualFold(recorded, path.Base(cwd)) {
		return 0
	}
	for dir, up := cwd, 0; ; dir, up = path.Dir(dir), up+1 {
		parent, base := path.Base(path.Dir(dir)), path.Base(dir)
		if parent == "." || parent == "/" || base == "." || base == "/" {
			return -1
		}
		if strings.EqualFold(recorded, parent+"/"+base) {
			return up
		}
	}
}

func sameProject(recorded, cwd string) bool { return projectDistance(recorded, cwd) >= 0 }

// nearerProject reports whether a match this far from the caller beats the one
// already held. A match at any distance beats none; among matches the nearer
// wins, and equals fall through to betterIDMatch.
func nearerProject(near, held int) bool {
	if near < 0 {
		return false
	}
	return held < 0 || near < held
}

// betterIDMatch picks between two sessions carrying the same id (#1997). The
// freshest is the best guess for the one that just compacted; the harness name
// breaks a tie, and since the manifest is keyed harness:id it always breaks —
// so the answer never depends on map order.
func betterIDMatch(candidate, held SessionMeta) bool {
	if !candidate.Updated.Equal(held.Updated) {
		return candidate.Updated.After(held.Updated)
	}
	return candidate.Harness < held.Harness
}

func loadSessionMeta(dir string, m Manifest, meta SessionMeta) (model.Session, bool, error) {
	key := meta.Key()
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), key)
	// A pass that swapped in during the read zeroes the records it replaced
	// in the log this one shares, and the session read empty (#5449). Once
	// more from the live manifest; a failed read too, since the new log's tail
	// does not decode with the old tables.
	for range sessionRereads {
		cur, moved := liveManifest(dir, m)
		if !moved {
			break
		}
		if _, ok := cur.Sessions[key]; !ok {
			return model.Session{}, false, nil
		}
		m = cur
		if sessionReread != nil {
			sessionReread()
		}
		recs, err = recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), key)
	}
	if err != nil {
		return model.Session{}, false, err
	}
	s := sessionFromMeta(meta)
	for _, r := range recs {
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	orderPromotedNote(&s)
	return s, true, nil
}
