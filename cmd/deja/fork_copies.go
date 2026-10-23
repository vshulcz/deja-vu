package main

import (
	"strconv"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// copySources maps each session in ss that is a fork or copy of another in ss
// to that other's position. A fork that names its source is believed; an
// unmarked one (claude --fork-session, opencode --fork) is read whole and
// judged by what it holds, which costs a read only for the pairs maybe says
// look alike (#5320).
func copySources(dir string, ss []model.Session, maybe func(src, c int, sameShape bool) bool) map[int]int {
	return copySourcesAmong(dir, ss, maybe, nil, nil)
}

// copySourcesAmong is copySources that asks only the positions in
// buckets[bucketOf(c)], ascending, whether they are c's source. A bucket must
// hold every src maybe(src, c, _) can hold for; a negative bucket asks none,
// and nil buckets ask every position.
func copySourcesAmong(dir string, ss []model.Session, maybe func(src, c int, sameShape bool) bool, buckets [][]int, bucketOf func(c int) int) map[int]int {
	out := map[int]int{}
	shapes := copyMessageShapes(ss)
	peers, classOf, identities := forkPeerClasses(ss, shapes)
	eligible := func(i, j int) bool {
		return i != j && ss[i].Harness == ss[j].Harness && maybe(j, i, shapes[i] == shapes[j])
	}
	// A bucket split by peer class, so an equal class is passed over whole
	// rather than walked by each of its members.
	split := map[int][]forkPeerClass{}
	bucketPeers := func(b int) []forkPeerClass {
		if got, ok := split[b]; ok {
			return got
		}
		var got []forkPeerClass
		at := map[int]int{}
		for _, j := range buckets[b] {
			k := classOf[j]
			g, ok := at[k]
			if !ok {
				g = len(got)
				at[k] = g
				got = append(got, forkPeerClass{equal: peers[k].equal, class: k})
			}
			got[g].at = append(got[g].at, j)
		}
		split[b] = got
		return got
	}
	needed := make([]bool, len(ss))
	for i, c := range ss {
		first := len(ss)
		groups := peers
		if buckets != nil {
			groups = nil
			if b := bucketOf(i); b >= 0 {
				groups = bucketPeers(b)
			}
		}
		for _, group := range groups {
			// Equal ordinary peers have neither ancestry nor an earlier start.
			// Keep the class pending without materializing its directed pairs.
			if group.class == classOf[i] && group.equal {
				continue
			}
			for _, j := range group.at {
				if j >= first {
					break
				}
				if !eligible(i, j) {
					continue
				}
				src := ss[j]
				if search.SourceOf(ss, src, c, src.Started) && !search.SourceOf(ss, c, src, c.Started) {
					first = j
				} else {
					needed[i], needed[j] = true, true
				}
			}
		}
		if first < len(ss) {
			out[i] = first
		}
	}
	if dir == "" {
		return out
	}
	forkWitnessEqualPeers(peers, out, needed, eligible)
	forkCopiesFromFull(dir, ss, identities, needed, out, eligible)
	return out
}

// forkClassKey groups sessions that look alike before they are read whole.
type forkClassKey struct {
	harness string
	shape   int
	fork    bool
	started time.Time
}

// forkPeerClass is the positions of one class; equal says they all started at
// the same instant and none is a marked fork.
type forkPeerClass struct {
	at    []int
	equal bool
	class int
}

// forkPeerClasses sorts ss into peer classes and names each session's
// identity.
func forkPeerClasses(ss []model.Session, shapes []int) ([]forkPeerClass, []int, []index.Identity) {
	classes := map[forkClassKey]int{}
	var peers []forkPeerClass
	classOf := make([]int, len(ss))
	identities := make([]index.Identity, len(ss))
	for i, s := range ss {
		identities[i] = index.Identity{Harness: s.Harness, ID: s.ID}
		key := forkClassKey{s.Harness, shapes[i], s.Kind == "fork", s.Started.Round(0).UTC()}
		k, ok := classes[key]
		if !ok {
			k = len(peers)
			classes[key] = k
			peers = append(peers, forkPeerClass{equal: s.Kind != "fork", class: k})
		} else if !ss[peers[k].at[0]].Started.Equal(s.Started) {
			peers[k].equal = false
		}
		peers[k].at = append(peers[k].at, i)
		classOf[i] = k
	}
	return peers, classOf, identities
}

// forkWitnessEqualPeers marks as needed the members of an equal class that
// may be a copy of, or the source of, another member.
func forkWitnessEqualPeers(peers []forkPeerClass, out map[int]int, needed []bool, eligible func(i, j int) bool) {
	for _, group := range peers {
		if !group.equal {
			continue
		}
		// A pure maybe predicate may differ between members. Find one outgoing
		// or incoming witness for each identity instead of storing every edge.
		for _, i := range group.at {
			if _, done := out[i]; done || needed[i] {
				continue
			}
			for _, j := range group.at {
				if eligible(i, j) {
					needed[i], needed[j] = true, true
					break
				}
			}
		}
		for _, j := range group.at {
			if needed[j] {
				continue
			}
			for _, i := range group.at {
				if _, done := out[i]; !done && eligible(i, j) {
					needed[i], needed[j] = true, true
					break
				}
			}
		}
	}
}

// forkCopiesFromFull reads the needed sessions whole and adds to out the ones
// that are copies of another by what they hold.
func forkCopiesFromFull(dir string, ss []model.Session, identities []index.Identity, needed []bool, out map[int]int, eligible func(i, j int) bool) {
	var ids []index.Identity
	seen := map[index.Identity]bool{}
	for i, id := range identities {
		if needed[i] && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	full, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return
	}
	whole := map[index.Identity]int{}
	fullShapes := copyMessageShapes(full)
	for i, s := range full {
		whole[index.Identity{Harness: s.Harness, ID: s.ID}] = i
	}
	// Partial peers can have different full records: regroup only after each
	// needed identity has been read. CopyOf depends on the message shape alone.
	classes := map[forkClassKey]int{}
	var complete [][]int
	var representatives []int
	fixed := make([]bool, len(ss))
	for i, id := range identities {
		_, fixed[i] = out[i]
		f, ok := whole[id]
		if !ok {
			continue
		}
		key := forkClassKey{harness: ss[i].Harness, shape: fullShapes[f]}
		k, ok := classes[key]
		if !ok {
			k = len(complete)
			classes[key] = k
			complete = append(complete, nil)
			representatives = append(representatives, f)
		}
		complete[k] = append(complete[k], i)
	}
	forkMatchByLastTurn(full, complete, representatives, fixed, out, eligible)
}

// forkTurnKey is one message as a copy and its source both hold it.
type forkTurnKey struct {
	harness, role, text string
	at                  int64
}

// forkMatchByLastTurn compares each class of whole sessions with the classes
// whose last turn it holds, and records the earliest source of each copy.
func forkMatchByLastTurn(full []model.Session, complete [][]int, representatives []int, fixed []bool, out map[int]int, eligible func(i, j int) bool) {
	// A copy holds its source's last turn, so a class is only compared with
	// the classes whose last turn it holds: a map hit per message instead of
	// every pair of classes (#5523). A source with the copy's own last turn
	// holds that turn too, so it is never one.
	keyOf := func(harness string, m model.Message) forkTurnKey {
		return forkTurnKey{harness, m.Role, m.Text, m.Time.UnixNano()}
	}
	lasts := make([]forkTurnKey, len(complete))
	hasLast := make([]bool, len(complete))
	byLast := map[forkTurnKey][]int{}
	for k, f := range representatives {
		if m, ok := search.LastTurn(full[f]); ok {
			lasts[k], hasLast[k] = keyOf(full[f].Harness, m), true
			byLast[lasts[k]] = append(byLast[lasts[k]], k)
		}
	}
	visited := make([]int, len(complete))
	for c, candidates := range complete {
		rep := full[representatives[c]]
		for _, m := range rep.Messages {
			key := keyOf(rep.Harness, m)
			if hasLast[c] && key == lasts[c] {
				continue
			}
			for _, src := range byLast[key] {
				if c == src || visited[src] == c+1 {
					continue
				}
				visited[src] = c + 1
				if !copyOf(rep, full[representatives[src]]) {
					continue
				}
				forkAssignSource(candidates, complete[src], fixed, out, eligible)
			}
		}
	}
}

// forkAssignSource gives each candidate not already fixed the earliest
// eligible position in its source class.
func forkAssignSource(candidates, srcs []int, fixed []bool, out map[int]int, eligible func(i, j int) bool) {
	for _, i := range candidates {
		if fixed[i] {
			continue
		}
		for _, j := range srcs {
			if previous, done := out[i]; done && previous <= j {
				break
			}
			if eligible(i, j) {
				out[i] = j
				break
			}
		}
	}
}

// copyOf is search.CopyOf; a test counts the comparisons through it.
var copyOf = search.CopyOf

// copyMessageShapes preserves every field used by CopyOf, including zero-time eligibility.
func copyMessageShapes(ss []model.Session) []int {
	shapes := make([]int, len(ss))
	classes := map[string]int{}
	var key []byte
	for i, s := range ss {
		key = key[:0]
		for _, m := range s.Messages {
			key = strconv.AppendQuote(key, m.Role)
			key = append(key, ':')
			key = strconv.AppendInt(key, m.Time.UnixNano(), 10)
			key = append(key, ':')
			key = strconv.AppendBool(key, m.Time.IsZero())
			key = append(key, ':')
			key = strconv.AppendQuote(key, m.Text)
			key = append(key, '\n')
		}
		shape := string(key)
		if _, ok := classes[shape]; !ok {
			classes[shape] = len(classes) + 1
		}
		shapes[i] = classes[shape]
	}
	return shapes
}

// dropCopiedHits leaves out a hit whose every matched turn sits, at the same
// time and in the same words, in the hit of the session it was copied from:
// the fork quoted its source back, and the answer said everything twice. A
// fork that matched on a turn of its own keeps its hit.
func dropCopiedHits(dir string, hits []search.Hit) []search.Hit {
	if len(hits) < 2 {
		return hits
	}
	// A copy's matched turns are a subset of its source's, so both hold the
	// copy's first matched turn: only hits that open alike are compared, which
	// keeps a broad answer of many hits from comparing every pair.
	groups := map[string][]int{}
	for i, h := range hits {
		for _, m := range h.Session.Messages {
			if !m.Time.IsZero() {
				k := h.Session.Harness + "\x00" + m.Role + "\x00" + strconv.FormatInt(m.Time.UnixNano(), 10) + "\x00" + m.Text
				groups[k] = append(groups[k], i)
				break
			}
		}
	}
	drop := map[int]bool{}
	for _, at := range groups {
		if len(at) < 2 {
			continue
		}
		ss := make([]model.Session, len(at))
		turns := make([]map[string]bool, len(at))
		for j, i := range at {
			ss[j] = hits[i].Session
			turns[j] = make(map[string]bool, len(ss[j].Messages))
			for _, m := range ss[j].Messages {
				if !m.Time.IsZero() {
					k := m.Role + "\x00" + strconv.FormatInt(m.Time.UnixNano(), 10) + "\x00" + m.Text
					turns[j][k] = true
				}
			}
		}
		buckets, rarest := holdersOfRarest(turns)
		for j := range copySourcesAmong(dir, ss, func(src, c int, sameShape bool) bool {
			return (sameShape && len(turns[c]) > 0) || heldTurnsOf(turns[src], turns[c])
		}, buckets, rarest) {
			drop[at[j]] = true
		}
	}
	if len(drop) == 0 {
		return hits
	}
	kept := make([]search.Hit, 0, len(hits)-len(drop))
	for i, h := range hits {
		if !drop[i] {
			kept = append(kept, h)
		}
	}
	return kept
}

// heldTurns reports whether every timed message of c is in src, the same role,
// time and text, and c has at least one.
func heldTurns(src, c map[string]bool) bool {
	for key := range c {
		if !src[key] {
			return false
		}
	}
	return len(c) > 0
}

// holdersOfRarest buckets the positions by the timed turns they hold, and
// picks for c the bucket of its rarest turn. A source holds every turn of its
// copy, so only that bucket is asked: hits that each matched a turn of their
// own are not compared in pairs (#5549).
func holdersOfRarest(turns []map[string]bool) ([][]int, func(c int) int) {
	var buckets [][]int
	of := map[string]int{}
	for j := range turns {
		for k := range turns[j] {
			b, ok := of[k]
			if !ok {
				b = len(buckets)
				of[k] = b
				buckets = append(buckets, nil)
			}
			buckets[b] = append(buckets[b], j)
		}
	}
	return buckets, func(c int) int {
		fewest := -1
		for k := range turns[c] {
			if b := of[k]; fewest < 0 || len(buckets[b]) < len(buckets[fewest]) {
				fewest = b
			}
		}
		return fewest
	}
}

// heldTurnsOf is heldTurns; a test counts the comparisons through it.
var heldTurnsOf = heldTurns

// markForkTitles prefixes the title of a session listed beside the session it
// was forked or copied from: both open on the same turn, so `last` showed two
// rows under one title.
func markForkTitles(dir string, ss []model.Session) {
	titles := make([]int, len(ss))
	var byTitle [][]int
	of := map[string]int{}
	for i, s := range ss {
		t := lastTitle(s)
		b, ok := of[t]
		if !ok {
			b = len(byTitle)
			of[t] = b
			byTitle = append(byTitle, nil)
		}
		titles[i] = b
		byTitle[b] = append(byTitle[b], i)
	}
	copies := copySourcesAmong(dir, ss, func(i, j int, _ bool) bool {
		src, c := ss[i], ss[j]
		if titles[i] != titles[j] {
			return false
		}
		return c.Parent == src.ID || (!c.Started.IsZero() && c.Started.Equal(src.Started))
	}, byTitle, func(c int) int { return titles[c] })
	for i, j := range copies {
		ss[i].Title = "fork of " + search.ShortID(ss[j].ID) + ": " + lastTitle(ss[i])
	}
}

func lastTitle(s model.Session) string {
	if s.Title != "" {
		return s.Title
	}
	return firstUserTitle(s)
}

// cutSession leaves out the records o.ExcludeUpTo keeps out of a session read
// whole: a fork asking gets its source from the fork point on (#5335).
func cutSession(s model.Session, o search.Options) model.Session {
	return cutSessionAt(s, o.ExcludeUpTo)
}

// cutSessionAt is cutSession with the cuts by session id.
func cutSessionAt(s model.Session, cut map[string]time.Time) model.Session {
	at, ok := cut[s.ID]
	if !ok {
		return s
	}
	kept := make([]model.Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		if m.Time.After(at) {
			kept = append(kept, m)
		}
	}
	s.Messages = kept
	return s
}
