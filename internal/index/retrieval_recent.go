package index

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// NewestFirstMeta orders sessions for every "most recent" answer deja gives.
func NewestFirstMeta(a, b SessionMeta) bool { return newestFirstMeta(a, b) }

// newestFirstMeta orders sessions for every "most recent" answer deja gives.
//
// A timestamp alone is not an order. Sessions come out of a map, so a store
// where several share a stamp — a day's work imported in one go, a harness
// that stamps per-day, transcripts restored from a backup — reordered itself
// on every run: six calls to `deja last`, six different answers (#713).
// Identity breaks the tie because it is the one thing that does not change.
func newestFirstMeta(a, b SessionMeta) bool {
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	return a.ID < b.ID
}

// newestFirstSession is newestFirstMeta for loaded sessions.
func newestFirstSession(a, b model.Session) bool {
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	return a.ID < b.ID
}

// Recent returns the n most recently updated sessions.
func Recent(dir string, n int) ([]model.Session, error) {
	return RecentMatching(dir, n, query.Options{})
}

// RecentMatching is Recent over the sessions o lets through.
func RecentMatching(dir string, n int, o query.Options) ([]model.Session, error) {
	ss, _, err := RecentMatchingCounted(dir, n, o)
	return ss, err
}

// RecentMatchingCounted is RecentMatching with the number of sessions that
// matched before the cut. The listing shows ten and said nothing about the
// rest, which reads as the whole answer — the misread #1632 closed for search
// (#2638) — and the count is already in hand here.
func RecentMatchingCounted(dir string, n int, o query.Options) ([]model.Session, int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, 0, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, 0, err
	}
	out := make([]model.Session, 0, len(m.Sessions))
	for _, meta := range m.Sessions {
		if !sessionMetaMatches(meta, o) {
			continue
		}
		out = append(out, sessionFromMeta(meta))
	}
	// This walk serves sessions without going through sessionsForMetas, so it
	// is the one path #2070's rule never reached: `deja last` led with the
	// background agents' own temp tree, 253 rows of 400 on a real store, while
	// every ranked surface filtered it out (#2541).
	out = ignoredByPolicy(out)
	total := len(out)
	return newestN(out, n), total, nil
}

// newestN orders sessions newest first and keeps the first n, or all of them
// for n <= 0. The screen's home list asks for 60 of 27k on a large history:
// sorting all of them to keep the top cost ~18 ms of every tab switch, so a
// bounded list holds the newest n seen so far and most sessions are turned
// away with one comparison against its oldest.
func newestN(ss []model.Session, n int) []model.Session {
	cmp := func(a, b model.Session) int {
		if newestFirstSession(a, b) {
			return -1
		}
		if newestFirstSession(b, a) {
			return 1
		}
		return 0
	}
	if n <= 0 || len(ss) <= n*4 {
		slices.SortFunc(ss, cmp)
		if n > 0 && len(ss) > n {
			ss = ss[:n]
		}
		return ss
	}
	top := make([]model.Session, 0, n)
	for _, s := range ss {
		if len(top) == n && !newestFirstSession(s, top[n-1]) {
			continue
		}
		i, _ := slices.BinarySearchFunc(top, s, cmp)
		if len(top) < n {
			top = append(top, model.Session{})
		}
		copy(top[i+1:], top[i:len(top)-1])
		top[i] = s
	}
	return top
}

// displayPath contracts the home directory to ~ in user-facing messages.
func displayPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

// RecentInProject is RecentProject under the scope rule the automatic paths
// use: a session belongs to this project or it does not. `deja handoff`, which
// picks a session for another agent when nobody named one, walked the loose
// helper and packaged a client's acme/api from a directory named api (#2336) —
// the shape #2333 closed on the session-start hook.
func RecentInProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
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
	// Scoped before the cut, not after: filtering a window of the loose
	// helper's answer would drop this project's sessions whenever another
	// project's newer ones filled it.
	var metas []SessionMeta
	scope := newProjectScope(project)
	for _, meta := range m.Sessions {
		if scope.has(meta.Project) {
			metas = append(metas, meta)
		}
	}
	metas = metasNotIgnored(metas)
	sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

// RecentProject finds sessions whose project name contains the given string —
// a browsing helper, loose on purpose, the way `--project` is on the surfaces a
// person types. A caller deciding on its own which sessions belong to the
// directory it is standing in wants RecentInProject instead (#2336).
func RecentProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
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
	project = strings.ToLower(project)
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		p := strings.ToLower(meta.Project)
		if p == project || (project != "" && strings.Contains(p, project)) {
			metas = append(metas, meta)
		}
	}
	metas = metasNotIgnored(metas)
	sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

// recordServable says whether a record may be served for a query.
//
// Both retrieval paths have to ask this and only one of them did. Exact search
// asked it per record while scanning; the relevance tier never scans, so it
// served every record of every session it ranked — a --role=user recall could
// come back holding assistant text, and an ordinary one could be carried by a
// file list or a command that exact search hides on purpose.
//
// Work records are why the question is not simply o.Role. A file list is a
// record of what a turn touched, not something said; an invocation is an
// action, not an answer; a replaced span is the file's old contents. All three
// are indexed and searchable, and served only when asked for by name.
//
// So is a harness's compaction summary (#3384). It names every topic the
// session touched, and served by default it carried a marathon into the
// answer for questions the session never discussed.
func recordServable(role string, o query.Options) bool {
	if o.Role != "" && !roleMatches(role, o.Role) {
		return false
	}
	for _, work := range []string{roleFiles, roleCommand, roleEdit, roleSummary} {
		if role == work && o.Role != work {
			return false
		}
	}
	// Hashes are never served, even under --role: there is nothing in a line of
	// hex for a person to read, and blame reads them through the record log
	// rather than through a query.
	if role == roleWrote {
		return false
	}
	return true
}

// defaultServableRoles is the role filter for a query without --role: every
// role is wanted ("*": true) except the work and summary roles that are only
// served when asked for by name.
var defaultServableRoles = map[string]bool{
	"*":         true,
	roleFiles:   false,
	roleCommand: false,
	roleEdit:    false,
	roleSummary: false,
	roleWrote:   false,
}

// servableRoles returns the role filter map for roleWanted that matches
// recordServable(role, o), so non-servable records are skipped at their header
// prefix before flate decompression.
func servableRoles(o query.Options) map[string]bool {
	if o.Role == "" {
		return defaultServableRoles
	}
	if o.Role == roleWrote {
		return map[string]bool{}
	}
	m := map[string]bool{o.Role: true}
	if o.Role == "tool" {
		m[roleToolOutput] = true
	}
	return m
}

// sessionsServable loads the sessions and keeps only the records this query is
// allowed to be served, dropping any session left holding nothing.
//
// A session that emptied here was ranked entirely on records the caller is not
// allowed to see — a file list, a command, or, under --role, the other side of
// the conversation. Returning it with no messages would be worse than dropping
// it: the count would say a match exists and the result would show nothing.
// What this costs, measured on a 1,365-session index (170 MB of record text,
// 43% of it tool output, 63% of the messages recall hands back):
//
//   - a relevance-tier search materialises every message of every session it
//     ranked — 104 to 124 MB across 29 to 50 sessions per query, 200-400 ms —
//     to print a few clipped lines. `Limit` does not bound it: it governs what
//     is shown, not what is loaded.
//   - the per-prompt hook, which cuts its ranking to twelve first, still loads
//     41 MB across seven sessions on this project, one of them 37,289 messages,
//     in 60 ms warm and 385 ms cold.
//
// The whole session is loaded because the caller picks which message to show
// after ranking, and it reads the text to do it. Bounding that — a window
// around the match rather than the session — is the open question (#2592).
func sessionsServableWindowed(dir string, metas []SessionMeta, matchedOffsets map[uint32][]int64, o query.Options) ([]model.Session, error) {
	all, err := sessionsForMetasInRolesWindowed(dir, metas, servableRoles(o), matchedOffsets)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, s := range all {
		kept := s.Messages[:0]
		for _, m := range s.Messages {
			if recordServable(m.Role, o) && !o.CutRecord(s.ID, m.Time) {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			continue
		}
		s.Messages = kept
		out = append(out, s)
	}
	return out, nil
}

// ignoredByPolicy is the trust rule that says a directory's sessions are not
// to be recalled. It was applied at one call site — the CLI's own search — so
// `deja doctor` printed "not recalled */.claude/jobs/*" while the per-prompt
// hook injected those sessions into every message, which is the most automatic
// surface deja has and the one where the rule matters most. The same shape as
// #2070: a rule in one path of several is a rule half the callers do not have.
//
// Applied here because this is where every tier and every surface turns a
// manifest entry into a session it can serve.
func ignoredByPolicy(ss []model.Session) []model.Session {
	pol := policy.Load()
	// A harness the reader excluded is excluded from the answers too, not only
	// from the next build. The list was applied at ingest alone, so a store
	// that was already indexed when the line was added kept being served —
	// and the case that matters is the harness the reader is sitting in: its
	// live transcript is in the index, and a recall answered with the session
	// that was asking (#3965).
	excluded := sources.ExcludedHarnesses()
	if len(pol.IgnorePatterns()) == 0 && len(excluded) == 0 {
		return ss
	}
	out := ss[:0:0]
	for _, s := range ss {
		if excluded[strings.ToLower(s.Harness)] {
			continue
		}
		if pol.Ignored(s.Path, s.Project) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ProjectsTouchedByIgnore names the projects holding at least one session the
// ignore rule keeps out of recall.
//
// For the callers that read the commands table rather than the sessions. That
// table keys by project, and the rule matches on the session's path or its
// project — measured on a real store under the default `*/.claude/jobs/*`, all
// 305 ignored sessions matched by path and none by project, and the project
// they sit in is called "run". So a table keyed by project cannot apply the
// rule exactly, and the honest approximation is to drop a project the rule
// touches at all: a project mixing ignored and kept sessions is under-reported,
// which is the right way to be wrong for a line that speaks without being asked
// (#2652).
func ProjectsTouchedByIgnore(dir string) map[string]bool {
	if dir == "" {
		dir = DefaultDir()
	}
	metas, err := AllMeta(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	out := map[string]bool{}
	for _, meta := range metas {
		if !sources.HarnessExcluded(meta.Harness) && pol.Ignored(meta.Path, meta.Project) {
			out[meta.Project] = true
		}
	}
	return out
}

// IgnoredMatching counts the sessions a listing would have shown if the ignore
// rule did not cover them. Every other rule that withholds rows in deja says
// how many; this one dropped 253 of 400 on a real store and said nothing
// (#2554). Manifest only, so the listing pays a walk over metas it has just
// read from the same cached manifest.
func IgnoredMatching(dir string, o query.Options) int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0
	}
	pol := policy.Load()
	n := 0
	for _, meta := range m.Sessions {
		if sources.HarnessExcluded(meta.Harness) || !sessionMetaMatches(meta, o) {
			continue
		}
		if pol.Ignored(meta.Path, meta.Project) {
			n++
		}
	}
	return n
}

// metasNotIgnored is ignoredByPolicy one step earlier, on the metas a walk is
// about to cut a window out of. sessionsForMetas filters what it loads, which
// is right, but by then the window is chosen: three ignored sessions at the top
// of a project left `handoff` and the session-start block with nothing while
// three real ones sat below the cut (#2541).
func metasNotIgnored(metas []SessionMeta) []SessionMeta {
	return metasNotIgnoredBy(policy.Load(), metas)
}

// metaIgnoredOrExcluded reports whether a manifest entry is withheld by the
// ignore policy or by the excluded-harness list (#2541, #3965).
func metaIgnoredOrExcluded(pol policy.Policy, excluded map[string]bool, meta SessionMeta) bool {
	return excluded[strings.ToLower(meta.Harness)] || pol.Ignored(meta.Path, meta.Project)
}

func excludedHarnesses() map[string]bool {
	return sources.ExcludedHarnesses()
}

func metaIgnoredOrExcludedLoad() (policy.Policy, map[string]bool) {
	return policy.Load(), sources.ExcludedHarnesses()
}

func metasNotIgnoredBy(pol policy.Policy, metas []SessionMeta) []SessionMeta {
	excluded := sources.ExcludedHarnesses()
	out := metas[:0:0]
	for _, meta := range metas {
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		out = append(out, meta)
	}
	return out
}

// sessionsForMetas loads full sessions for the given metas in ONE pass over
// records.bin. The per-session variant re-scanned the whole log for every
// session, which turned a session-start hook into hundreds of milliseconds.
func sessionsForMetas(dir string, metas []SessionMeta) ([]model.Session, error) {
	return sessionsForMetasInRoles(dir, metas, nil)
}

func sessionsForMetasInRoles(dir string, metas []SessionMeta, roles map[string]bool) ([]model.Session, error) {
	return sessionsForMetasInRolesWindowed(dir, metas, roles, nil)
}

func sessionsForMetasInRolesWindowed(dir string, metas []SessionMeta, roles map[string]bool, matchedOffsets map[uint32][]int64) ([]model.Session, error) {
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	out, err := sessionsForMetasInWindowed(dir, m, metas, roles, matchedOffsets)
	// Read again when a pass swapped in during the read, as loadSessionMeta
	// does, without the sessions it took away (#5449).
	for range sessionRereads {
		cur, moved := liveManifest(dir, m)
		if !moved {
			break
		}
		m = cur
		metas = slices.DeleteFunc(slices.Clone(metas), func(meta SessionMeta) bool {
			_, ok := m.Sessions[meta.Key()]
			return !ok
		})
		if sessionReread != nil {
			sessionReread()
		}
		out, err = sessionsForMetasInWindowed(dir, m, metas, roles, matchedOffsets)
	}
	if err != nil {
		return nil, err
	}
	for i := range out {
		orderPromotedNote(&out[i])
	}
	return ignoredByPolicy(out), nil
}

// sessionRereads is how many times a session read goes again for a pass that
// swapped in under it. Two: a second pass can land while the first reread
// runs, and that reread then reads the records it zeroes (#5460).
const sessionRereads = 2

// sessionReread, when set, runs before each reread. Tests land a pass there.
var sessionReread func()

// liveManifest is the manifest dir holds now, and whether it is another than
// m: a pass committed since m was read, and records m listed live may have
// been zeroed in the log they share (#5449). Every pass stamps its build
// time, and one that zeroes records lists them dead.
func liveManifest(dir string, m Manifest) (Manifest, bool) {
	cur, err := readManifestCached(dir)
	if err != nil || len(cur.DeadRecords) == len(m.DeadRecords) && cur.BuiltAt.Equal(m.BuiltAt) && cur.Generation == m.Generation {
		return m, false
	}
	return cur, true
}

// sessionsForMetasInWindowed is the read sessionsForMetas makes with m's tables.
func sessionsForMetasInWindowed(dir string, m Manifest, metas []SessionMeta, roles map[string]bool, matchedOffsets map[uint32][]int64) ([]model.Session, error) {
	tbl := tablesFromManifest(m)
	want := make(map[string]int, len(metas))
	out := make([]model.Session, len(metas))
	var matchedByKey map[string][]int64
	if matchedOffsets != nil {
		matchedByKey = make(map[string][]int64, len(metas))
	}
	for i, meta := range metas {
		k := meta.Key()
		want[k] = i
		out[i] = sessionFromMeta(meta)
		if matchedByKey != nil {
			out[i].Held = meta.Counted
			matchedByKey[k] = matchedOffsets[meta.Ord]
		}
	}
	keys := make(map[string]bool, len(want))
	for k := range want {
		keys[k] = true
	}
	err := eachRecordForKeysInRolesWindowed(filepath.Join(dir, "records.bin"), tbl, keys, roles, matchedByKey, func(r Record) {
		if i, ok := want[r.Key]; ok {
			out[i].Messages = append(out[i].Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
	})
	return out, err
}

// RecentProjects is RecentProject for several project names at once: one
// manifest read and one records pass instead of names × sessions scans.
func RecentProjects(dir string, projects []string, perName int) ([]model.Session, error) {
	return RecentProjectsUnder(dir, projects, "", perName)
}

// RecentProjectsUnder is RecentProjects plus the sessions whose work happened
// inside root, whatever they are called.
//
// A project is a name derived from where a session was started, and a caller
// can guess the names above it — a subdirectory finds its repository (#2039).
// Downward it cannot: there is no list of the names a repository's
// subdirectories might have produced, so standing at the root found nothing of
// what happened inside it (#2040). The files a session touched are already in
// the manifest and say where the work was, so the root asks that instead of
// guessing.
func RecentProjectsUnder(dir string, projects []string, root string, perName int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
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
	return sessionsForMetas(dir, recentProjectMetas(m, projects, root, perName))
}

// RecentProjectMetasUnder is RecentProjectsUnder without the records, and
// only for sessions updated since the given time: the manifest rows alone,
// for a caller that picks one before it reads any text. The time cut comes
// first, so the scope rules run over a handful of rows rather than the store.
func RecentProjectMetasUnder(dir string, projects []string, root string, since time.Time, perName int) ([]SessionMeta, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	fresh := make(map[string]SessionMeta)
	for k, meta := range m.Sessions {
		if !meta.Updated.Before(since) {
			fresh[k] = meta
		}
	}
	if len(fresh) == 0 {
		return nil, nil
	}
	m.Sessions = fresh
	return recentProjectMetas(m, projects, root, perName), nil
}

func recentProjectMetas(m Manifest, projects []string, root string, perName int) []SessionMeta {
	seen := map[string]bool{}
	var metas []SessionMeta
	// Grouped by project once: a repository with many worktrees hands in a
	// name for each, and walking the whole manifest per name was hundreds of
	// passes over every session on the session-start hook.
	byProject := map[string][]SessionMeta{}
	if len(projects) > 0 {
		for _, meta := range m.Sessions {
			byProject[meta.Project] = append(byProject[meta.Project], meta)
		}
	}
	lowered := make(map[string]string, len(byProject))
	for name := range byProject {
		lowered[name] = strings.ToLower(name)
	}
	pol := policy.Load()
	for _, project := range projects {
		project = strings.ToLower(project)
		if project == "" {
			continue
		}
		var mine []SessionMeta
		for name, group := range byProject {
			// The same scope rule the ranked path uses. A substring test here
			// put a client's acme/api into a session start in /work/api,
			// injected under "sessions from this project" (#2333).
			if lowerInScope(lowered[name], project) {
				mine = append(mine, group...)
			}
		}
		mine = metasNotIgnoredBy(pol, mine)
		sort.Slice(mine, func(i, j int) bool { return newestFirstMeta(mine[i], mine[j]) })
		if perName > 0 && len(mine) > perName {
			mine = mine[:perName]
		}
		for _, meta := range mine {
			k := meta.Key()
			if !seen[k] {
				seen[k] = true
				metas = append(metas, meta)
			}
		}
	}
	// And what happened under the caller's own checkout, however it was named.
	// Ranked among themselves by recency and cut to the same per-name cap, so
	// a repository with many subdirectories contributes as much as any one
	// name does; from there they compete with the rest on the caller's own
	// score and recency rule.
	if under := metasWorkingUnder(m, root, seen); len(under) > 0 {
		sort.Slice(under, func(i, j int) bool { return newestFirstMeta(under[i], under[j]) })
		if perName > 0 && len(under) > perName {
			under = under[:perName]
		}
		metas = append(metas, under...)
	}
	return metas
}

// metasWorkingUnder is the sessions whose touched files sit under root and
// which no name has already claimed. Nothing when root is empty, so a caller
// that does not know where it stands is unaffected.
//
// A repository, not any directory: measured on a real store, standing in a
// home directory admitted 697 of 768 sessions with touched files — every
// project on the machine, injected under a line that says "from this project's
// recent history" (#2343's shape). The checkout is the boundary a person means
// by "this project", so anything that is not one answers with nothing.
func metasWorkingUnder(m Manifest, root string, seen map[string]bool) []SessionMeta {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	repo := sources.RepoRoot(root)
	if repo == "" || sameDir(repo, homeDir()) {
		// A checkout at the home directory — a dotfiles repo — is a repository
		// whose "work" is the whole machine. Everything under it keeps its own
		// name, as before.
		return nil
	}
	prefix := filepath.ToSlash(filepath.Clean(root))
	if prefix == "" || prefix == "/" || prefix == "." {
		return nil
	}
	prefix += "/"
	var out []SessionMeta
	for _, meta := range m.Sessions {
		k := meta.Key()
		if seen[k] {
			continue
		}
		for _, p := range meta.Touched {
			if !underDir(p, prefix) {
				continue
			}
			seen[k] = true
			out = append(out, meta)
			break
		}
	}
	return metasNotIgnored(out)
}

// underDir reports whether p sits under a slash-terminated directory prefix,
// without lowering either side into a new string: this runs over every touched
// path of every session in the manifest, on the session-start hook, and the
// lowering alone was 12 MB of garbage on a large store.
func underDir(p, prefix string) bool {
	p = filepath.ToSlash(p)
	if len(p) <= len(prefix) {
		return false
	}
	return strings.EqualFold(p[:len(prefix)], prefix)
}

// sameDir compares two directories the way the filesystem would for this
// purpose: case-insensitively, since the callers are a cwd and a home.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// IsPromotedNote reports whether a session is a promoted note, on this machine
// or on the one it came from. Import renames sessions to imported-<hash>, so
// the id alone stops answering the question after a sync (#975).
func IsPromotedNote(harness, id, origID string) bool {
	return model.IsPromotedNote(harness, id, origID)
}

// orderPromotedNote keeps a promoted note's corrections newest-first. The
// parser writes them that way (#812), but an incremental build appends the new
// lines to what the log already holds, so the record order stopped saying which
// answer holds: the snippet an agent reads led with the answer that had been
// overturned (#944). Ordering on read fixes indexes already on disk too.
func orderPromotedNote(s *model.Session) {
	if !IsPromotedNote(s.Harness, s.ID, s.OrigID) || len(s.Messages) < 2 {
		return
	}
	// Newest first by time; when a batch carries no timestamps — a hand-made
	// one, or an older export — the file order is the record: the parser
	// reverses it for the same reason (#812, #975).
	sort.SliceStable(s.Messages, func(i, j int) bool {
		a, b := s.Messages[i], s.Messages[j]
		if a.Time.Equal(b.Time) {
			return i > j
		}
		return a.Time.After(b.Time)
	})
}
