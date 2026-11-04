package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// hookDigestTTL bounds how long a cached session-start digest is considered
// fresh. Older entries are still served instantly — startup must never wait
// on digest work — but a detached refresh is kicked so the next session gets
// a current one (stale-while-revalidate).
// A
// minute-old digest is indistinguishable at session start, and the cache
// turns the common hook path from ~120ms of index work into one file read.
const hookDigestTTL = 60 * time.Second

type hookCacheEntry struct {
	At  time.Time `json:"at"`
	CWD string    `json:"cwd"`
	// Gate records the recall mode and the policy the digest was built
	// under. A cache hit returns before either is consulted, so serving an
	// entry built under different rules would let a cached digest outlive
	// DEJA_RECALL=off or a policy that now forbids it.
	Gate     string `json:"gate,omitempty"`
	Digest   string `json:"digest"`
	Sessions int    `json:"sessions"`
	// Withheld counts the candidates the trust policy dropped for this
	// digest. Old entries decode as zero, which reads as "nothing withheld"
	// and is what they meant.
	Withheld    int      `json:"withheld,omitempty"`
	Raw         int64    `json:"raw"`
	TaskMatched []string `json:"task_matched,omitempty"`
	// Projects names the projects behind the digest, so the injection log can
	// be held to a rule or a forget without reading its prose (#2349). Old
	// entries decode as none, which is what they carried.
	Projects []string `json:"projects,omitempty"`
	// IDs are the sessions this digest carries, so a cache hit logs the same
	// thing a fresh build does. Without them the session-start hook was the
	// one surface whose repetition could not be counted at all: 606 injections
	// in six weeks with no record of what was in them (#2038). Old entries
	// decode as nil and log nothing, which is what they knew.
	IDs []string `json:"ids,omitempty"`
	// Without holds the digest as it reads with each of IDs left out. A
	// resumed session is left out of its own digest, and a recent one is
	// usually in it, so without these every resume of it rebuilt the digest
	// on the startup path and the refresh put it straight back (#4224). Old
	// entries decode as none, and a resume falls back to building.
	Without map[string]hookDigestVariant `json:"without,omitempty"`
}

// hookDigestVariant is a session-start digest built with one session left
// out. Withheld is the entry's: the policy drops the same candidates either
// way.
type hookDigestVariant struct {
	Digest      string   `json:"digest"`
	Sessions    int      `json:"sessions"`
	Raw         int64    `json:"raw"`
	TaskMatched []string `json:"task_matched,omitempty"`
	IDs         []string `json:"ids,omitempty"`
	Projects    []string `json:"projects,omitempty"`
}

// servedWithout is what the entry serves to a session that must not see
// itself, and whether it has that at all. The entry holds a variant for one
// session left out; a caller leaving out two that are both in it builds.
func (e hookCacheEntry) servedWithout(exclude ...string) (hookDigestVariant, bool) {
	var in []string
	for _, id := range exclude {
		if id != "" && slices.Contains(e.IDs, id) && !slices.Contains(in, id) {
			in = append(in, id)
		}
	}
	switch len(in) {
	case 0:
		return hookDigestVariant{Digest: e.Digest, Sessions: e.Sessions, Raw: e.Raw, TaskMatched: e.TaskMatched, IDs: e.IDs, Projects: e.Projects}, true
	case 1:
		v, ok := e.Without[in[0]]
		// The variant backfills from the sessions behind the digest, and a
		// fork leaves out its sources too, which the full digest may not hold
		// (#5138).
		if !ok || slices.ContainsFunc(exclude, func(id string) bool { return id != "" && slices.Contains(v.IDs, id) }) {
			return hookDigestVariant{}, false
		}
		return v, true
	}
	return hookDigestVariant{}, false
}

func hookCachePath(dir, cwd string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(cwd))
	return fmt.Sprintf("%s.hookcache-%08x", dir, h.Sum32())
}

// hookGate identifies the rules a digest was built under: the recall mode and
// the auto-activation policy. Both are consulted only deep inside
// hookDigestResult, which a cache hit never reaches.
func hookGate() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL")))
	// The shape of the entry is part of what it was built under: one written
	// before the digest recorded its projects would be served for its whole
	// TTL and logged as a digest that came from nowhere (#2349).
	//
	// And the notes file, because the block leads with the decisions promoted
	// in this project. Without it a decision the reader had just removed with
	// `deja forget` kept being handed to every session start until the TTL ran
	// out — search agreed it was gone and the block went on quoting it, which
	// is the one thing the privacy command must not do (#2537). A promotion
	// made a moment ago has the same problem in reverse. Stat, not read.
	return "v3|" + mode + "|" + policy.Load().Describe(policy.ActivationAuto) + "|" + notesStamp()
}

// notesStamp identifies the notes file as it is now. A file that cannot be
// stat'd stamps as absent, which is itself a state worth telling apart from a
// file with content in it.
func notesStamp() string {
	fi, err := os.Stat(sources.NotesFile())
	if err != nil {
		return "none"
	}
	return strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// hookProjectPath is the directory a payload is about. Cursor leaves cwd empty
// and names the project in workspace_roots instead (measured on cursor-agent
// 2026.09.02), so a hook reading cwd alone fell back to whatever directory the
// harness happened to spawn it in — which is not the project on every host.
func hookProjectPath(cwd string, roots []string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	for _, r := range roots {
		if strings.TrimSpace(r) != "" {
			return r
		}
	}
	return ""
}

// hookCWD is where the hook is standing: what the payload says, else the
// project directory the harness exported, else the process's own — so a host
// that exports the directory without naming it in the payload is not treated
// as standing nowhere (#759), and one that names it in the payload without
// exporting anything is not either.
//
// deja used to write the payload's answer back into its own environment. After
// #2183 nothing read it to decide anything: the doors take the project from the
// payload, and the two children that need one are handed it — the refresh child
func cachedHookDigest(dir string) (string, int, int64, []string, int, []string, []string) {
	return cachedHookDigestFor(dir, "", "")
}

// cachedHookDigestFor is cachedHookDigest for a caller that was told which
// project the call is about. The payload is that authority: deja used to write
// it into its own environment and read it back, which answered a second payload
// in the same process with the first one's project (#2182, #2185).
//
// exclude is the session asking. A harness whose session-start seam runs
// after the first message is stored — opencode's system transform — would
// otherwise be served its own opening prompt as history (#4199). It also holds
// what counts as the asker: the session that spawned it, live and asking
// through it (#4548).
func cachedHookDigestFor(dir, fromPayload string, exclude ...string) (string, int, int64, []string, int, []string, []string) {
	cwd := hookCWD(fromPayload)
	if recallIsOff() {
		return "", 0, 0, nil, 0, nil, nil
	}
	// Before the cache read: a hit returns without reaching the version guard
	// in hookDigestResult, so an index left behind by an upgrade was served
	// stale and never rebuilt (#777). The cached digest is still the user's
	// own history, so serve it — just ask for the rebuild too.
	// A deleted index is the same situation with the manifest gone: the cache
	// answered from a store that no longer exists and asked for nothing, so it
	// kept serving that snapshot forever — every session recorded after the
	// deletion invisible to the hook (#874). Caches do get wiped: ~/.cache is
	// fair game for cleanup tools and CI images.
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
	}
	gate := hookGate()
	p := hookCachePath(dir, cwd)
	if b, err := os.ReadFile(p); err == nil {
		var e hookCacheEntry
		if json.Unmarshal(b, &e) == nil && e.Digest != "" && e.CWD == cwd && e.Gate == gate {
			if v, ok := e.servedWithout(exclude...); ok {
				if time.Since(e.At) >= hookDigestTTL {
					// Serve stale instantly; a detached self-refresh rebuilds
					// the cache off the startup path.
					requestHookRefresh(dir, cwd)
				}
				return v.Digest, v.Sessions, v.Raw, v.TaskMatched, e.Withheld, v.IDs, v.Projects
			}
		}
	}
	if !slices.ContainsFunc(exclude, func(id string) bool { return id != "" }) {
		// The digest goes out now and the resume variants come from the
		// detached refresh: rendering one per served session here put them
		// on the startup path of every first start in a project.
		digest, sessions, raw, taskMatched, withheld, ids, projects := hookDigestResultFor(dir, cwd, "")
		writeHookCache(dir, cwd, digest, sessions, raw, taskMatched, withheld, ids, projects)
		if len(ids) > 0 {
			requestHookRefresh(dir, cwd)
		}
		return digest, sessions, raw, taskMatched, withheld, ids, projects
	}
	// A resume against an entry without the digest that leaves it out. The
	// cache is the project's, read by every session that opens in it; one
	// without the asker would hide that session from the next one, so the
	// refresh writes it with every variant instead.
	digest, sessions, raw, taskMatched, withheld, ids, projects := hookDigestResultFor(dir, cwd, exclude...)
	requestHookRefresh(dir, cwd)
	return digest, sessions, raw, taskMatched, withheld, ids, projects
}

func writeHookCache(dir, cwd, digest string, sessions int, raw int64, taskMatched []string, withheld int, ids, projects []string) {
	writeHookCacheEntry(dir, cwd, hookCacheEntry{Digest: digest, Sessions: sessions, Raw: raw, TaskMatched: taskMatched, Withheld: withheld, IDs: ids, Projects: projects})
}

func writeHookCacheEntry(dir, cwd string, e hookCacheEntry) {
	if e.Digest == "" {
		return
	}
	e.At, e.CWD, e.Gate = time.Now(), cwd, hookGate()
	// Renamed into place: a hook reading while the refresh writes would
	// otherwise see a torn entry and rebuild.
	if b, err := json.Marshal(e); err == nil {
		_ = atomicfile.Write(hookCachePath(dir, cwd), b, 0o600)
	}
}

// requestHookRefresh spawns a detached `deja hook-refresh` for cwd; a
// same-named sentinel prevents stampedes. Best effort by design.
func requestHookRefresh(dir, cwd string) {
	if os.Getenv("DEJA_HOOK_REFRESH") != "" {
		return
	}
	sentinel := hookCachePath(dir, cwd) + ".refreshing"
	// Waiting for the index lock can outlast the window, as in warmup (#5719).
	if b, err := os.ReadFile(sentinel); err == nil {
		if claim, ok := parseWarmupClaim(b); ok && claim.state() == ownerLive {
			return
		}
	}
	if fi, err := os.Stat(sentinel); err == nil && time.Since(fi.ModTime()) < 2*time.Minute {
		return
	}
	stamp := time.Now().UnixNano()
	if err := os.WriteFile(sentinel, []byte(strconv.FormatInt(stamp, 10)), 0o600); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.CommandContext(context.Background(), exe, "hook-refresh")
	cmd.Env = append(os.Environ(), "DEJA_HOOK_REFRESH=1", "CLAUDE_PROJECT_DIR="+cwd)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = cmd.Stdout
	if pid, err := startRefresher(cmd); err == nil {
		recordWarmupOwner(sentinel, stamp, pid)
	} else {
		_ = os.Remove(sentinel)
	}
}

// startRefresher starts the detached process and names it. Under `go test` the
// executable is the test binary; spawning it as a refresher would rerun the
// suite (and hung the Windows runner).
var startRefresher = func(cmd *exec.Cmd) (int, error) {
	if strings.HasSuffix(strings.TrimSuffix(cmd.Path, ".exe"), ".test") {
		return 0, errors.New("not spawning the test binary")
	}
	return startDetachedPID(cmd)
}

// runHookRefresh recomputes the session-start digest for the cwd in the
// environment and rewrites its cache entry.
func runHookRefresh(dir string) {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// The digest is recomputed from the index, so refreshing it off a stale
	// index only re-serves the same snapshot: a project whose newest session
	// reversed an earlier decision kept handing the agent the earlier one for
	// as long as the user went without running the CLI (#913). This runs
	// detached, off the startup path, so the incremental build costs the hook
	// nothing; a directory that cannot be written stays stale, as before.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		return
	}
	writeHookCacheEntry(dir, cwd, hookDigestEntryFor(dir, ""))
	_ = os.Remove(hookCachePath(dir, cwd) + ".refreshing")
}

func hookDigest(dir string) string {
	digest, _, _, _, _, _, _ := hookDigestResult(dir)
	return digest
}

func hookDigestResult(dir string) (string, int, int64, []string, int, []string, []string) {
	return hookDigestResultFor(dir, "", "")
}

// hookDigestResultFor is hookDigestResult for a caller that was told which
// project the call is about, rather than one reading it back out of the
// environment, where a project deja itself had written stayed for every later
// call in the process (#2182, #2185).
func hookDigestResultFor(dir, fromPayload string, exclude ...string) (string, int, int64, []string, int, []string, []string) {
	e := hookDigestBuild(dir, fromPayload, exclude, false, nil)
	return e.Digest, e.Sessions, e.Raw, e.TaskMatched, e.Withheld, e.IDs, e.Projects
}

// hookDigestEntryFor is the digest as the cache keeps it: with every session
// it serves, and as it reads with each of them left out.
func hookDigestEntryFor(dir, fromPayload string) hookCacheEntry {
	return hookDigestBuild(dir, fromPayload, nil, true, nil)
}

// withProjectsOf adds the projects of sessions already chosen to a name list,
// without disturbing its order: the names the caller guessed come first, and
// what the store answered with follows.
func withProjectsOf(names []string, ss []model.Session) []string {
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[strings.ToLower(n)] = true
	}
	out := names
	for _, s := range ss {
		if p := strings.ToLower(s.Project); p != "" && !have[p] {
			have[p] = true
			out = append(out, s.Project)
		}
	}
	return out
}

// hookDigestRenders counts digests rendered, for tests that need to tell a
// cache hit from a rebuild.
var hookDigestRenders atomic.Int64

// cut leaves out each session's records at or before its time, the turns a
// fork asking already holds as its copy of a source that went on (#5335).
func hookDigestBuild(dir, fromPayload string, exclude []string, variants bool, cut map[string]time.Time) (entry hookCacheEntry) {
	withheld := 0
	defer func() { _ = recover() }()
	trace := os.Getenv("DEJA_TRACE") == "1"
	t0 := time.Now()
	mark := func(stage string) {
		if trace {
			fmt.Fprintf(os.Stderr, "trace %-16s %6.1fms\n", stage, float64(time.Since(t0).Microseconds())/1000)
			t0 = time.Now()
		}
	}
	_ = mark
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL")))
	if mode == search.RecallOff {
		return hookCacheEntry{}
	}
	// A store from an older index version must be rebuilt before it is read:
	// this path never calls Ensure, so otherwise the first prompts after an
	// upgrade recall nothing and say nothing about why.
	// Damaged as well as stale: a kill during a write leaves records the
	// manifest still describes, and this path answers from them (#800).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
		return hookCacheEntry{}
	}
	cwd := fromPayload
	if cwd == "" {
		cwd = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return hookCacheEntry{}
		}
	}
	// The two git probes (worktree list for identity, status/log for the
	// task signal) are independent forks — overlap them.
	taskCh := make(chan []string, 1)
	go func() { taskCh <- changedTaskFiles(cwd) }()
	names := digest.ProjectNameCandidates(cwd)
	mark("names+worktrees")
	pol := policy.Load()
	mark("policy")
	// The standing decisions are scoped by the same trust policy as the session
	// digest: a project the policy withholds from auto-activation must not leak
	// its decisions here either.
	var allowedNames []string
	for _, n := range names {
		if pol.Allows(policy.ActivationAuto, n) {
			allowedNames = append(allowedNames, n)
		}
	}
	conventions := projectConventions(allowedNames)
	var ss []model.Session
	seen := map[string]bool{}
	lookupNames := names
	if mode == search.RecallAggressive {
		recent, err := index.Recent(dir, 12)
		if err == nil {
			lookupNames = nil
			for _, s := range recent {
				lookupNames = append(lookupNames, s.Project)
			}
		}
	}
	taskFiles := <-taskCh
	mark("git-taskfiles")
	// Four times what a safe-mode digest serves, so the novelty ordering below
	// has something to choose from. Without changed files this used to be three
	// — exactly what the digest serves — so every candidate had just been shown,
	// unseen was always empty, and each session start in a project repeated the
	// last one word for word. That is what #2038 measured as 87.5% repeats and
	// read as a ranking problem. Measured on a seeded store of 300 sessions, the
	// wider pool costs 1 ms of a 22 ms session start.
	const perName = 12
	// The cwd as well as the names it can guess: a session started in a
	// subdirectory keeps that directory's name, and no list of names reaches
	// down to it (#2040).
	tailLimit := 150
	if len(cut) > 0 {
		tailLimit = 0
	}
	if got, err := index.RecentProjectsUnderTail(dir, lookupNames, cwd, perName, tailLimit); err == nil {
		for _, s := range got {
			if !pol.Allows(policy.ActivationAuto, s.Project) {
				withheld++
				continue
			}
			k := s.Key()
			if seen[k] {
				continue
			}
			seen[k] = true
			ss = append(ss, s)
		}
	}
	mark("load-sessions")
	// What every variant shares is read once: the environment block is
	// stamped as served when it goes out, and orient reads the build's
	// tables.
	envDone := false
	var envText string
	var envFrom []string
	environment := func() (string, []string) {
		if !envDone {
			envDone = true
			// Inside the cached result, not after it: this costs a manifest
			// scan plus one session read, which is ten times the rest of the
			// hook, and the clusters it reports change over weeks rather than
			// turns.
			if !environmentServedRecently(dir) {
				if env, from := environmentBlockFrom(dir, policy.ActivationAuto); env != "" {
					envText, envFrom = "\n"+env+"\n", from
					// Stamped on the way out rather than by the check: the
					// check is also made by callers that may not deliver
					// (#1806).
					stampEnvironmentServed(dir)
				}
			}
			mark("environment")
		}
		return envText, envFrom
	}
	orientDone := false
	var orient string
	orientBlock := func() string {
		if !orientDone {
			orientDone = true
			orient = orientDigestBlock(dir, cwd, allowedNames, policy.ActivationAuto)
			mark("orient")
		}
		return orient
	}
	// Each variant works on its own copy: the steps below sort and trim in
	// place, and the next variant has to start from the order the store gave.
	render := func(exclude []string) hookCacheEntry {
		hookDigestRenders.Add(1)
		variant := slices.DeleteFunc(slices.Clone(ss), func(s model.Session) bool { return s.ID != "" && slices.Contains(exclude, s.ID) })
		if len(cut) > 0 {
			variant = slices.DeleteFunc(variant, func(s model.Session) bool { return len(cutSessionAt(s, cut).Messages) == 0 })
			for i := range variant {
				variant[i] = cutSessionAt(variant[i], cut)
			}
		}
		// A subagent run is the parent's work seen from inside, and what it says
		// on its own is the process talk a spawned agent produces — "Sending
		// verdict", "Worktrees cleaned up". Indexing already treats these runs as
		// secondary (#3009 keeps the task, the answer and the edits); the digest picked
		// them like any other session and let one lead the block ahead of the
		// session that settled the work (#3368). Kept only when the project has
		// nothing else: a subagent's transcript still beats an empty digest.
		if kept := withoutSubagentRuns(variant); len(kept) > 0 {
			variant = kept
		}
		if len(variant) == 0 {
			// A project can have settled decisions and no recent session left to
			// show them (the transcript was forgotten, or aged past the window).
			// The standing decisions still apply, so serve them alone rather than
			// going out empty.
			if conventions != "" {
				return hookCacheEntry{Digest: conventions, Withheld: withheld, Projects: allowedNames}
			}
			// withheld travels even with nothing to show: it is the only thing
			// that separates "the rule hid all of it" from "no history here".
			return hookCacheEntry{Withheld: withheld}
		}
		// Digest and scoring only ever use the recent tail; hauling a marathon
		// session's megabytes through word sets is pure waste.
		for i := range variant {
			if len(variant[i].Messages) > 150 {
				variant[i].Messages = variant[i].Messages[len(variant[i].Messages)-150:]
			}
		}
		scores, matched := taskScores(variant, taskFiles)
		sort.Slice(variant, func(i, j int) bool {
			if scores[variant[i].Key()] != scores[variant[j].Key()] {
				return scores[variant[i].Key()] > scores[variant[j].Key()]
			}
			return variant[i].Updated.After(variant[j].Updated)
		})
		if len(variant) > 12 {
			variant = variant[:12]
		}
		mark("task-scores")
		// A rejected session belongs last, and the mark has to travel with it: the
		// block listed the correction as a separate item and left the session it
		// corrects unmarked (#761).
		variant, _ = orderForInjection(variant)
		variant, unseen := leadWithUnseen(dir, names, variant)
		for i := range variant {
			index.MarkUserRules(dir, &variant[i], names)
		}
		// A session found by where its work happened carries the name of the
		// directory it was started in, which is not one the cwd can guess — the
		// safe-mode filter is a name list, so the evidence that admitted the
		// session admits its name with it (#2040). The trust policy has already
		// had its say on each of these, one session at a time, above.
		result := search.BuildAutoRecall(variant, search.AutoRecallOptions{
			Mode: mode, ProjectNames: withProjectsOf(names, variant), TaskScores: scores, Unseen: unseen})
		mark("build-digest")
		if result.Sessions == 0 {
			matched = nil
		}
		text := result.Text
		if result.Sessions > 0 {
			if rejectedWarning := shownInjectionWarning(variant, sessionsMatchingIDs(variant, result.IDs)); rejectedWarning != "" {
				text = rejectedWarning + text
			}
		}
		env, envFrom := environment()
		text += env
		// Where the work is, not what was said about it: the digest above is
		// sessions, and an agent opening a repository it does not know spends its
		// first turns finding the command and the files instead. Same read as the
		// environment block, from tables the build already wrote.
		if om := orientBlock(); om != "" {
			text += "\n" + om
		}
		// The project's standing decisions lead the block: they are the user's own
		// settled choices, and an agent should read them before the session digest,
		// not after. Query-independent, so a convention surfaces even when nothing
		// in the task names it — the gap plain recall cannot close.
		if conventions != "" {
			text = conventions + "\n" + text
		}
		mark("conventions")
		// The candidates that never made it into the digest were never served:
		// counting their transcripts here inflated the distillation ratio deja
		// prints about itself (1 session distilled, 3 sessions' bytes claimed).
		// The projects behind what actually went out, so the injection log can be
		// held to a rule or a forget without reading the digest's prose (#2349).
		return hookCacheEntry{Digest: text, Sessions: result.Sessions, Raw: result.RawBytes, TaskMatched: matched, Withheld: withheld, IDs: result.IDs, Projects: digestProjects(variant, envFrom)}
	}
	entry = render(exclude)
	if variants && entry.Digest != "" {
		for _, id := range entry.IDs {
			v := render([]string{id})
			if entry.Without == nil {
				entry.Without = map[string]hookDigestVariant{}
			}
			entry.Without[id] = hookDigestVariant{Digest: v.Digest, Sessions: v.Sessions, Raw: v.Raw, TaskMatched: v.TaskMatched, IDs: v.IDs, Projects: v.Projects}
		}
		mark("variants")
	}
	return entry
}

// digestProjects names the projects a session-start digest was built from: the
// sessions it shows, plus the ones behind the environment block, which names
// none of them in its own text.
func digestProjects(ss []model.Session, env []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, s := range ss {
		add(s.Project)
	}
	for _, p := range env {
		add(p)
	}
	return out
}

// warmupDeadAfter is how long a warmup may go without publishing progress
// before the next hook treats it as dead and starts another. It is far longer
// than any gap between progress reports — those come at every phase and every
// harness — and far shorter than warmupRetryAfter, which was the whole wait a
// killed build used to cost.
const warmupDeadAfter = 2 * time.Minute

// warmupJustRequested reports that a build was asked for moments ago and has
// not published progress yet. The sentinel carries the time of the request,
// which is exactly what this needs.
func warmupJustRequested(dir string) bool {
	c, ok := readWarmupClaim(dir)
	if !ok {
		return false
	}
	stamp := c.stamp
	// Ahead of the clock is stale too, as it is for the status file (#889):
	// a sentinel stamped hours ahead kept recall saying a build was on its way
	// while nothing built.
	return within(time.Since(time.Unix(0, stamp)), warmupStatusStale)
}

// within reports whether an age is inside limit either side of now. A stamp
// from the future makes the age negative, and a bare "age < limit" took it
// for fresh for as long as the clock stayed behind.
func within(age, limit time.Duration) bool {
	return age < limit && age > -limit
}

// warmupLooksDead reports whether the build the sentinel stands for has
// stopped without clearing it. A warmup killed mid-build — a closed laptop, an
// OOM, a terminal that took its process group with it — left the sentinel
// behind, and for the next ten minutes every hook returned nothing, spawned
// nothing and said nothing (#875).
func warmupLooksDead(dir string, now time.Time, stamp int64) bool {
	if st := readWarmupStatus(dir); st != nil {
		return false
	}
	return !within(now.Sub(time.Unix(0, stamp)), warmupDeadAfter)
}

func requestWarmup(dir string) {
	if os.Getenv("DEJA_WARMUP_SENTINEL") != "" || installBuildsIndex || index.Manual() {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sentinel := filepath.Join(dir, "warmup.sentinel")
	now := time.Now()
	f, err := os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return
		}
		b, readErr := os.ReadFile(sentinel)
		claim, parsed := parseWarmupClaim(b)
		// The claim is the O_EXCL create; the stamp lands a moment later. A
		// sentinel read inside that window is empty, and treating empty as
		// unreadable made the second hook delete another hook's claim and
		// spawn a build of its own — two rebuilds over one directory whenever
		// two projects started together (#1065). The file's own mtime says
		// when the claim was made.
		if readErr == nil && !parsed {
			if fi, statErr := os.Stat(sentinel); statErr == nil {
				claim, parsed = warmupClaim{stamp: fi.ModTime().UnixNano()}, true
			}
		}
		// A claim stands while its owner runs, however long since its last
		// progress report: under I/O contention a live worker went quiet for
		// two minutes and a second, then a third, was started beside it
		// (#5719).
		if readErr == nil && parsed && warmupClaimHeld(dir, now, claim) {
			return
		}
		if os.Remove(sentinel) != nil {
			return
		}
		f, err = os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return
		}
	}
	if _, err = fmt.Fprintln(f, now.UnixNano()); err != nil {
		_ = f.Close()
		return
	}
	if err = f.Close(); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := spawnWarmup(exe, sentinel); err != nil {
		return
	}
}

func startDetachedWarmup(exe, sentinel string) error {
	cmd := exec.CommandContext(context.Background(), exe, "index")
	cmd.Env = append(os.Environ(), "DEJA_WARMUP_SENTINEL="+sentinel)
	// The child clears the sentinel only while it still holds this claim.
	b, err := os.ReadFile(sentinel)
	claim, claimed := parseWarmupClaim(b)
	if err == nil && claimed {
		cmd.Env = append(cmd.Env, warmupClaimEnv+"="+strconv.FormatInt(claim.stamp, 10))
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = cmd.Stdout
	pid, err := startDetachedPID(cmd)
	if err != nil {
		return err
	}
	// Named here as well as by the child itself, so the claim has an owner
	// from the moment the child exists, even if it stalls before it runs.
	if claimed {
		recordWarmupOwner(sentinel, claim.stamp, pid)
	}
	return nil
}

// limitMarkers are the strings harnesses print when a session dies on quota.
// Matched only in a short message: a reply that discusses quotas is not one.
var limitMarkers = []string{
	"usage limit reached",
	"rate limit reached",
	"usage limit will reset",
	"quota exceeded",
	"out of free quota",
	"upgrade to continue",
	"5-hour limit",
	"weekly limit",
}

// limitNotice reports whether a message is a harness's usage-limit or overload
