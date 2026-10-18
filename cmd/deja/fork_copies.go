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
func copySources(dir string, ss []model.Session, maybe func(src, c model.Session) bool) map[int]int {
	out := map[int]int{}
	var unread [][2]int
	for i, c := range ss {
		for j, src := range ss {
			if i == j || c.Harness != src.Harness || !maybe(src, c) {
				continue
			}
			if search.SourceOf(ss, src, c, src.Started) && !search.SourceOf(ss, c, src, c.Started) {
				out[i] = j
				break
			}
			unread = append(unread, [2]int{i, j})
		}
	}
	if len(unread) == 0 || dir == "" {
		return out
	}
	var ids []index.Identity
	seen := map[int]bool{}
	for _, p := range unread {
		for _, k := range p {
			if !seen[k] {
				seen[k] = true
				ids = append(ids, index.Identity{Harness: ss[k].Harness, ID: ss[k].ID})
			}
		}
	}
	full, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return out
	}
	whole := map[string]model.Session{}
	for _, s := range full {
		whole[s.Harness+":"+s.ID] = s
	}
	for _, p := range unread {
		i, j := p[0], p[1]
		if _, done := out[i]; done {
			continue
		}
		c, okc := whole[ss[i].Harness+":"+ss[i].ID]
		src, oks := whole[ss[j].Harness+":"+ss[j].ID]
		if okc && oks && search.CopyOf(c, src) {
			out[i] = j
		}
	}
	return out
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
		for j, i := range at {
			ss[j] = hits[i].Session
		}
		for j := range copySources(dir, ss, func(src, c model.Session) bool { return heldTurns(src, c) }) {
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
func heldTurns(src, c model.Session) bool {
	key := func(m model.Message) string {
		return m.Role + "\x00" + strconv.FormatInt(m.Time.UnixNano(), 10) + "\x00" + m.Text
	}
	have := make(map[string]bool, len(src.Messages))
	for _, m := range src.Messages {
		if !m.Time.IsZero() {
			have[key(m)] = true
		}
	}
	n := 0
	for _, m := range c.Messages {
		if m.Time.IsZero() {
			continue
		}
		if !have[key(m)] {
			return false
		}
		n++
	}
	return n > 0
}

// markForkTitles prefixes the title of a session listed beside the session it
// was forked or copied from: both open on the same turn, so `last` showed two
// rows under one title.
func markForkTitles(dir string, ss []model.Session) {
	copies := copySources(dir, ss, func(src, c model.Session) bool {
		if lastTitle(src) != lastTitle(c) {
			return false
		}
		return c.Parent == src.ID || (!c.Started.IsZero() && c.Started.Equal(src.Started))
	})
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
