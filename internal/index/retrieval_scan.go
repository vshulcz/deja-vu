package index

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// SearchWithRecovery is Search plus self-healing: a corrupt bucket (crash
// mid-append) triggers one full rebuild instead of erroring until the user
// runs --rebuild by hand.
func SearchWithRecovery(dir string, o query.Options, progress io.Writer) ([]model.Session, error) {
	r, err := SearchWithRecoveryDetailed(dir, o, progress)
	return r.Sessions, err
}

// SearchWithRecoveryDetailed is SearchWithRecovery with how the answer was
// found.
func SearchWithRecoveryDetailed(dir string, o query.Options, progress io.Writer) (SearchResult, error) {
	r, err := SearchDetailed(dir, o)
	if err == nil || !IsCorrupt(err) {
		return r, err
	}
	if autoUpdatesOff() {
		return SearchResult{}, &ManualStopError{damagedOrOutdated(err) + ", and automatic indexing is off — rebuild it with: deja index --rebuild"}
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(err), err)
	}
	if rerr := EnsureForSearch(dir, o, true, progress); rerr != nil {
		return SearchResult{}, rerr
	}
	return SearchDetailed(dir, o)
}

func scanRecords(dir string, m Manifest, o query.Options, offsets []int64) ([]model.Session, error) {
	return scanRecordsWithVariants(dir, m, o, offsets, nil)
}

// roleMatches accepts the role names the help text documents.
//
// `--role <user|assistant|tool>` is what `deja help` promises, and the stored
// name is "tool-output", so the documented spelling matched nothing at all —
// silently, with a healthy exit. The three roles that do work today (files,
// command, edit) are the only way to reach work records and appear in no help
// text; they keep working under their own names.
func roleMatches(stored, want string) bool {
	return search.RoleMatches(stored, want)
}

// harnessMatches accepts the name deja prints as well as the one it stores.
//
// Notes are stored under the harness "deja" and narrated during indexing as
// "notes" — so `deja index` said "notes: 5 sessions" and `--harness notes`
// then answered "no sessions match" and exited 0. A filter that rejects the
// name the tool just printed is a silent miss, which is the worst kind.
func harnessMatches(stored, want string) bool {
	if stored == want {
		return true
	}
	return stored == "deja" && want == "notes"
}

// roleFiles mirrors sources.RoleFiles without importing it: this package sits
// below sources, not above it.
const roleFiles = "files"

// roleCommand mirrors sources.RoleCommand.
const roleCommand = "command"

// roleToolOutput mirrors sources.RoleToolOutput.
const roleToolOutput = "tool-output"

// roleEdit mirrors sources.RoleEdit.
const roleEdit = "edit"

// roleWrote mirrors sources.RoleWrote: hashes of the lines a session wrote,
// under the path they went into. Never served — a reader asking about a file
// has no use for a line of hex, and it is not speech, not a command and not
// something anyone said.
const roleWrote = "wrote"

// roleSummary is a harness's digest of what it compacted away. Not speech, and
// not work either — but it belongs on this side of the line for the same reason
// the others do: a per-session read bound has to spend its budget on what
// someone said before it spends it on a machine's summary of that (#3384).
const roleSummary = "summary"

// isToolRole says whether a record holds the work rather than the talk about
// it. Tool records are bulk-repetitive by nature — the same command, the same
// paths, session after session — so a query that matches one matches hundreds,
// and the per-session bound has to spend its budget on speech first.
func isToolRole(role string) bool {
	return search.IsWorkRecord(role)
}

// wholeLogScanned, when set, is told of each record a scan with no offsets
// reads. Tests count it to see which queries walk the whole record log.
var wholeLogScanned func()

func scanRecordsWithVariants(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, error) {
	ss, _, err := scanRecordsTextMisses(dir, m, o, offsets, variants)
	return ss, err
}

// scanRecordsTextMisses is scanRecordsWithVariants that also counts the
// records read that do not pass the text check: none, and every session the
// read dropped was dropped for its role or the caller's filters, not because
// its postings named text it does not hold.
func scanRecordsTextMisses(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, int, error) {
	misses := 0
	by := map[string]*model.Session{}
	// Sessions come back in the order their first match was read. Ranging over
	// the map handed the exact tier a rotated order on every call, and the
	// directory lead serves that order as it is: a package rule in the fourth
	// of seven sessions was on the page in some calls and not in others.
	var order []string
	add := func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok || o.ExcludeSessions[meta.ID] || o.CutRecord(meta.ID, r.Time) {
			return
		}
		if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
			return
		}
		if !query.ProjectAnyMatches(meta.Project, meta.From, o.ProjectWants()) {
			return
		}
		if !fromMatches(meta.From, o.From) {
			return
		}
		if o.Since > 0 && meta.Updated.Before(o.Clock().Add(-o.Since)) {
			return
		}
		if !recordServable(r.Role, o) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			// Only the matching records are read here.
			cp.Held = meta.Counted
			s = &cp
			by[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	// Built once: the query side of the check does not change between records,
	// and this loop runs over every record a query without postings reaches
	// (#1885).
	matcher := newRecordMatcher(o, variants)
	if len(offsets) > 0 {
		f, err := openIndexFile(filepath.Join(dir, "records.bin"))
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = f.Close() }()
		offsets = sortedUniqueOffsets(offsets)
		if err := eachRecordAt(f, offsets, tablesFromManifest(m), func(r Record) {
			if matcher.matches(r) {
				add(r)
			} else {
				misses++
			}
		}); err != nil {
			return nil, 0, err
		}
	} else {
		if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if wholeLogScanned != nil {
				wholeLogScanned()
			}
			if matcher.matches(r) {
				add(r)
			} else {
				misses++
			}
		}); err != nil {
			return nil, 0, err
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, k := range order {
		s := by[k]
		orderPromotedNote(s)
		out = append(out, *s)
	}
	// The scoring and relevance tiers build their sessions here rather than
	// through sessionsForMetas, so the ignore rule has to hold at both.
	return ignoredByPolicy(out), misses, nil
}

func cutPostingsBySession(posts []posting, m Manifest, o query.Options) []posting {
	metaByOrd := sessionMetaByOrd(m)
	// Keep the complete posting-derived candidate set. Ranking needs the
	// candidate records to calculate BM25 document frequency and length.
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if meta, ok := metaAt(metaByOrd, p.Sid); ok && sessionMetaMatches(meta, o) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return capPostingsPerSession(out, perSessionCandidates)
}

// poolPostings keeps the postings of the sessions worth ranking in full when
// the caller reads only a window of the answer. A word in most of a store
// matched 40k sessions, and reading and scoring every one of them to show 15
// took 22 s and 2.6 GB (#5467). The pool is chosen from the postings and the
// manifest alone; the rest is described in the Unread it returns.
//
// Not for a search inside one session or one role: those score a part of each
// session the postings cannot see.
//
// holding is how many postings matched, which on the exact tier is how many
// records of the store hold the query.
//
// The scoped postings are those of the sessions o.PoolScope adds, read apart
// so they never decide this search's own tier.
func poolPostings(posts []posting, m Manifest, o query.Options, holding int) pooled {
	all := pooled{read: posts}
	if o.Window <= 0 || o.Session != "" || o.Role != "" {
		return all
	}
	n := search.PoolSize(o.Window)
	records := map[uint32]int{}
	for _, p := range posts {
		records[p.Sid]++
	}
	if len(records) <= n {
		return all
	}
	metaByOrd := sessionMetaByOrd(m)
	// What the ignore rule and the trust policy withhold is dropped here
	// rather than after the scan, so the unread count does not include it and
	// a denied session does not take a slot from one the page can show (#5488).
	pol, excluded := policy.Load(), sources.ExcludedHarnesses()
	sids := make([]uint32, 0, len(records))
	withheld := 0
	denied, allowed := map[uint32]bool{}, map[uint32]bool{}
	for sid := range records {
		meta, _ := metaAt(metaByOrd, sid)
		if excluded[strings.ToLower(meta.Harness)] || pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		if o.Activation != "" && !pol.Allows(o.Activation, meta.Project) {
			withheld++
			denied[sid] = true
			continue
		}
		sids = append(sids, sid)
		allowed[sid] = true
	}
	if len(sids) <= n {
		return all
	}
	slices.Sort(sids)
	stored := 0
	for _, meta := range m.Sessions {
		stored += meta.Counted
	}
	share := 0.0
	if stored > 0 {
		share = min(float64(holding)/float64(stored), 1)
	}
	cands := make([]search.PoolCandidate, len(sids))
	for i, sid := range sids {
		meta, _ := metaAt(metaByOrd, sid)
		s := sessionFromMeta(meta)
		s.Held = meta.Counted
		cands[i] = search.PoolCandidate{Session: s, Records: records[sid]}
	}
	keep, unread := search.Pool(cands, o, n, share)
	if keep == nil {
		return all
	}
	unread.Withheld = withheld
	kept := make(map[uint32]bool, n)
	for i, sid := range sids {
		if keep[i] {
			kept[sid] = true
		}
	}
	extra, scopeWhole := scopedPool(records, sids, kept, metaByOrd, o, n)
	out := pooled{unread: &unread, whole: scopeWhole, readSessions: len(kept), all: posts, kept: kept, allowed: allowed, denied: denied}
	for _, p := range posts {
		if kept[p.Sid] {
			out.read = append(out.read, p)
		} else if extra[p.Sid] && scopeWhole {
			out.scoped = append(out.scoped, p)
		}
	}
	return out
}

// pooled is what poolPostings split a match into.
type pooled struct {
	// read are the postings of the sessions ranked in full, and
	// readSessions how many sessions they come from.
	read         []posting
	readSessions int
	unread       *query.Unread
	// scoped and whole are what Options.PoolScope added: see scopedPool.
	scoped []posting
	whole  bool
	// all are the postings the pool was drawn from. Of their sessions, kept
	// were read, allowed and not kept are what Unread.Sessions counts, and
	// denied what Unread.Withheld counts. Those two were not checked against
	// the text.
	all                   []posting
	kept, allowed, denied map[uint32]bool
}

// verifyUnreadBudget is how many postings of the unread sessions are checked
// against the text before their count is estimated instead.
var verifyUnreadBudget = 20000

// verifyUnread makes the unread counts ones the text bears out. The pool is
// drawn from the postings, which say which records hold every word, not
// whether the words stand together the way a quoted phrase asks: "alpha beta"
// over 1,900 sessions that hold both words apart read the 5 that say it and
// counted the rest as matches, 106 in all (#5495).
//
// When every session read passed the text check, the postings were exact and
// the counts stand: a plain word costs nothing here. When every record read
// passed it and only the role filter dropped a session, the word matched in a
// command or a file list, which only a session whose postings are all tool
// records can be left with: those are checked and the counts cut by the ones
// that fail, where every unread match used to be read again. Otherwise the unread
// sessions are checked the same way, and the ones that pass are returned to
// be ranked with the pool, which leaves nothing unread. Past
// verifyUnreadBudget postings the counts are instead scaled by how many of the
// sample read with the pool passed, and Unread.Estimated says so.
func verifyUnread(dir string, m Manifest, o query.Options, variants map[string][]string, ss []model.Session, pool pooled, exactText bool) ([]model.Session, error) {
	u := pool.unread
	if len(ss) >= pool.readSessions || u.Sessions+u.Withheld == 0 {
		return nil, nil
	}
	if exactText {
		// Past the budget the counts are scaled below, as for a phrase.
		if done, err := cutRoleOnlyUnread(dir, m, o, variants, pool); done || err != nil {
			return nil, err
		}
	}
	var rest, denied []posting
	over := false
	for _, p := range pool.all {
		switch {
		case pool.denied[p.Sid]:
			denied = append(denied, p)
		case pool.allowed[p.Sid] && !pool.kept[p.Sid]:
			rest = append(rest, p)
		}
		if over = len(rest)+len(denied) > verifyUnreadBudget; over {
			break
		}
	}
	if !over {
		more, err := sessionsBorneOut(dir, m, o, variants, rest)
		if err != nil {
			return nil, err
		}
		withheld, err := sessionsBorneOut(dir, m, o, variants, denied)
		if err != nil {
			return nil, err
		}
		u.Sessions, u.Length, u.Withheld = 0, 0, len(withheld)
		return more, nil
	}
	passed, sampled := 0, len(u.Sample)
	for _, s := range ss {
		if _, ok := u.Sample[s.Key()]; ok {
			passed++
		}
	}
	if sampled == 0 {
		passed, sampled = len(ss), pool.readSessions
	}
	rate := float64(passed) / float64(sampled)
	u.Sessions = int(math.Round(float64(u.Sessions) * rate))
	u.Length *= rate
	u.Withheld = int(math.Round(float64(u.Withheld) * rate))
	u.Estimated = true
	return nil, nil
}

// cutRoleOnlyUnread takes out of the unread counts the sessions whose
// postings are all tool records and none of whose records this query may be
// served: a word only a command or a file list holds. done is false when
// they are past verifyUnreadBudget postings and nothing was checked.
func cutRoleOnlyUnread(dir string, m Manifest, o query.Options, variants map[string][]string, pool pooled) (done bool, err error) {
	u := pool.unread
	spoken := map[uint32]bool{}
	for _, p := range pool.all {
		if !p.Tool {
			spoken[p.Sid] = true
		}
	}
	var rest, denied []posting
	for _, p := range pool.all {
		if spoken[p.Sid] || pool.kept[p.Sid] {
			continue
		}
		switch {
		case pool.denied[p.Sid]:
			denied = append(denied, p)
		case pool.allowed[p.Sid]:
			rest = append(rest, p)
		}
		if len(rest)+len(denied) > verifyUnreadBudget {
			return false, nil
		}
	}
	byOrd := sessionMetaByOrd(m)
	failed := func(posts []posting) (map[string]bool, error) {
		held, heldErr := sessionsBorneOut(dir, m, o, variants, posts)
		if heldErr != nil {
			return nil, heldErr
		}
		ok := map[string]bool{}
		for _, s := range held {
			ok[s.Key()] = true
		}
		out := map[string]bool{}
		for _, p := range posts {
			meta, _ := metaAt(byOrd, p.Sid)
			if k := meta.Key(); !ok[k] {
				out[k] = true
			}
		}
		return out, nil
	}
	gone, err := failed(rest)
	if err != nil {
		return true, err
	}
	withheld, err := failed(denied)
	if err != nil {
		return true, err
	}
	if len(gone) > 0 && u.Sessions > 0 {
		left := max(u.Sessions-len(gone), 0)
		u.Length *= float64(left) / float64(u.Sessions)
		u.Sessions = left
		for k := range gone {
			delete(u.Sample, k)
		}
	}
	u.Withheld = max(u.Withheld-len(withheld), 0)
	return true, nil
}

// unreadPostingsChecked counts the postings sessionsBorneOut reads, for the
// tests that keep a plain word from reading its unread matches again.
var unreadPostingsChecked atomic.Int64

// sessionsBorneOut is the sessions whose records at posts pass the text check
// a search makes. The reads are split by session over a few workers: one
// record read at a time spent 0.85 s in pread on 3,100 sessions of a
// 40k-session store.
func sessionsBorneOut(dir string, m Manifest, o query.Options, variants map[string][]string, posts []posting) ([]model.Session, error) {
	// No offsets would scan the whole record log.
	if len(posts) == 0 {
		return nil, nil
	}
	unreadPostingsChecked.Add(int64(len(posts)))
	workers := min(runtime.NumCPU(), 4, (len(posts)+511)/512)
	offsets := make([][]int64, workers)
	for _, p := range posts {
		w := int(p.Sid) % workers
		offsets[w] = append(offsets[w], p.Off)
	}
	found := make([][]model.Session, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		if len(offsets[w]) == 0 {
			continue
		}
		wg.Go(func() {
			found[w], errs[w] = scanRecordsWithVariants(dir, m, o, offsets[w], variants)
		})
	}
	wg.Wait()
	var out []model.Session
	for w := range workers {
		if errs[w] != nil {
			return nil, errs[w]
		}
		out = append(out, found[w]...)
	}
	return out, nil
}

// scopedPool picks what else to read for o.PoolScope: every matching session
// of theirs outside kept, when the narrower search would read them all, so
// that answer can be cut from this one instead of searched again. Recall
// ranks the caller's project first and the machine after, and the second
// search of a common word cost a third of its time on a 40k-session store
// (#5486). whole is false when the narrower search would pool too.
//
// Its rule for reading everything: under its pool size, before or after the
// ignore rule and the trust policy drop what they withhold, it reads every
// candidate, those included.
func scopedPool(records map[uint32]int, sids []uint32, kept map[uint32]bool, metaByOrd map[uint32]*SessionMeta, o query.Options, n int) (extra map[uint32]bool, whole bool) {
	if len(o.PoolScope) == 0 {
		return nil, false
	}
	in := func(sid uint32) bool {
		meta, _ := metaAt(metaByOrd, sid)
		return query.ProjectAnyMatches(meta.Project, meta.From, o.PoolScope)
	}
	var scope []uint32
	for sid := range records {
		if in(sid) {
			scope = append(scope, sid)
		}
	}
	allowed := 0
	for _, sid := range sids {
		if in(sid) {
			allowed++
		}
	}
	if len(scope) > n && allowed > n {
		return nil, false
	}
	extra = map[uint32]bool{}
	for _, sid := range scope {
		if !kept[sid] {
			extra[sid] = true
		}
	}
	return extra, true
}

// perSessionCandidates bounds how many matching messages of one session are
// read to rank it. A common word is not spread evenly: "index" resolves 3258
// records across 162 sessions with a median of one apiece, and a single long
// session holds 1355 of them. Ranking scores a session by its best matching
// message, so reading the fourteen hundredth mention costs a record read and
// decides nothing.
const perSessionCandidates = 64

// capPostingsPerSession keeps at most n postings per session, sampled evenly
// across the session rather than truncated. Postings arrive in write order, so
// taking the first n would read the beginning of a long session and never its
// conclusion — which is the half that tends to be worth ranking.
//
// No session is ever dropped: this bounds how much of a session is read, not
// which sessions are considered, so the candidate set still covers everything
// the postings matched.
func capPostingsPerSession(posts []posting, n int) []posting {
	if n <= 0 {
		return posts
	}
	bySid := make(map[uint32]int, 16)
	speechBySid := make(map[uint32]int, 16)
	over := false
	for _, p := range posts {
		bySid[p.Sid]++
		if !p.Tool {
			speechBySid[p.Sid]++
		}
		if bySid[p.Sid] > n {
			over = true
		}
	}
	if !over {
		return posts
	}
	// Speech first, tool records with whatever budget is left. A long session
	// holds thousands of matching command lines and a handful of sentences about
	// the subject; sampling the two together spends the budget on the commands
	// and the sentence that would have ranked the session is never read.
	seen := make(map[uint32]int, len(bySid))
	seenTool := make(map[uint32]int, len(bySid))
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if bySid[p.Sid] <= n {
			out = append(out, p)
			continue
		}
		budget, total, counter := n, speechBySid[p.Sid], seen
		if p.Tool {
			budget, total, counter = n-speechBySid[p.Sid], bySid[p.Sid]-speechBySid[p.Sid], seenTool
			if budget <= 0 {
				continue
			}
		}
		if total <= budget {
			out = append(out, p)
			continue
		}
		i := counter[p.Sid]
		counter[p.Sid] = i + 1
		// Keep index i when it lands on one of budget evenly spaced slots.
		if i*budget/total != (i+1)*budget/total {
			out = append(out, p)
		}
	}
	return out
}

// sessionMetaByOrd is m's sessions by ordinal, shared: see metaViews.
func sessionMetaByOrd(m Manifest) map[uint32]*SessionMeta {
	return viewsFor(m).metaByOrd()
}

// fromMatches answers whether a session belongs to the machine asked for.
// "local" means this machine's own work, which is every session that did not
// arrive by sync — the one name a person always has, whatever their hosts are
// called.
func fromMatches(sessionFrom, want string) bool {
	if want == "" {
		return true
	}
	if strings.EqualFold(want, "local") {
		return sessionFrom == ""
	}
	return strings.EqualFold(sessionFrom, want)
}

func sessionMetaMatches(meta SessionMeta, o query.Options) bool {
	if o.ExcludeSessions[meta.ID] {
		return false
	}
	if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
		return false
	}
	if !query.ProjectAnyMatches(meta.Project, meta.From, o.ProjectWants()) {
		return false
	}
	if !fromMatches(meta.From, o.From) {
		return false
	}
	if o.Since > 0 && meta.Updated.Before(o.Clock().Add(-o.Since)) {
		return false
	}
	return true
}

func postingOffsets(posts []posting) []int64 {
	out := make([]int64, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.Off)
	}
	return out
}

func sortedUniqueOffsets(offsets []int64) []int64 {
	out := append([]int64(nil), offsets...)
	slices.Sort(out)
	n := 0
	for _, off := range out {
		if n == 0 || out[n-1] != off {
			out[n] = off
			n++
		}
	}
	return out[:n]
}

func sortedUniquePostings(posts []posting) []posting {
	out := append([]posting(nil), posts...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Off == out[j].Off {
			return out[i].Sid < out[j].Sid
		}
		return out[i].Off < out[j].Off
	})
	n := 0
	for _, p := range out {
		if n == 0 || out[n-1].Off != p.Off {
			out[n] = p
			n++
		}
	}
	return out[:n]
}
