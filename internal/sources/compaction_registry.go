package sources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// compactionScanFiles bounds the fallback that looks for a session by its id in
// files whose path does not name it: the hook runs on the host's critical path.
// compactionProbeWindow and compactionProbeTotalBytes bound how much of each
// unnamed transcript (head+tail) and total fallback scan we read so an unknown
// session_id cannot stall a prompt hook reading hundreds of megabytes (#6133).
const (
	compactionScanFiles       = 64
	compactionProbeWindow     = 64 << 10
	compactionProbeTotalBytes = 8 << 20
	compactionMaxDirEntries   = 512
	compactionMaxStatCalls    = 128
)

type compactionLookupStats struct {
	DirEntries int
	StatCalls  int
	ProbeBytes int64
}

type compactionShallowSpec struct {
	dir         string
	rels        []string
	subDirs     []string
	skipSubdirs map[string]bool
}

type compactionDatedSpec struct {
	dir               string
	maxDepth          int
	wantPrefix        string
	skipDirs          map[string]bool
	onlyIfMissingDirs []string
}

type compactionSearchPlan struct {
	direct    []string
	shallow   []compactionShallowSpec
	datedDirs []compactionDatedSpec
	walks     []compactionDatedSpec
	histories []string
	dbs       []string
	custom    func(id string, stats *compactionLookupStats) []string
}

func hasCompactionSearchPlan(harness string) bool {
	return compactionPlanBuilders[harness] != nil
}

func canReadCompactionTailByHarness(harness string) bool {
	switch harness {
	case "claude", "codex", "codebuddy", "qoder", "cherrystudio", "antigravity", "gemini":
		return true
	default:
		return false
	}
}

// ReadCompactionSession captures a session through the reader that indexes it,
// for a host whose transcript has no bounded tail reader of its own. The host
// names the session by its id, and usually its harness or transcript file; the
// file comes from the registry — the one the host named when it is a file of
// that store, else the store's file that holds the id — and the session is the
// one the harness's own parser reads out of it under that id. Nothing is
// guessed: no session with that id, no capture.
//
// hint is the workspace the hook runs in. It stands in for the session's own
// only when the session records none and its project is the hint's.
func ReadCompactionSession(harness, nativeSessionID, transcriptPath, hint string) (CompactionTranscript, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	var candidates []compactionCandidate
	if transcriptPath != "" {
		candidates = compactionCandidatesForPath(harness, transcriptPath)
	}
	if len(candidates) == 0 && harness != "" {
		candidates = compactionCandidatesByID(harness, nativeSessionID)
	}
	if len(candidates) == 0 {
		return CompactionTranscript{}, ErrUnsupportedCompactionTranscript
	}
	for _, c := range candidates {
		if transcriptPath == "" && canReadCompactionTailByHarness(c.harness) {
			tr, err := ReadCompactionTranscript(c.path, nativeSessionID)
			if err == nil && len(tr.Session.Messages) > 0 {
				if tr.Harness == "" {
					tr.Harness = c.harness
				}
				if tr.Workspace == "" {
					tr.Workspace = compactionSessionWorkspace(c.harness, c.path, tr.Session)
				}
				if tr.Workspace == "" && hint != "" && tr.Session.Project != "" && tr.Session.Project == projectName(hint) {
					tr.Workspace = hint
				}
				if tr.Session.Path == "" {
					tr.Session.Path = c.path
				}
				return tr, nil
			}
			if errors.Is(err, ErrTranscriptIdentity) {
				continue
			}
		}
		info, err := os.Stat(c.path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		sessions, _ := c.kind.Parse(c.path, 0)
		for _, s := range sessions {
			if s.ID != nativeSessionID || len(s.Messages) == 0 {
				continue
			}
			if s.Path == "" {
				s.Path = c.path
			}
			workspace := compactionSessionWorkspace(c.harness, c.path, s)
			if workspace == "" && hint != "" && s.Project != "" && s.Project == projectName(hint) {
				workspace = hint
			}
			return CompactionTranscript{
				Session: s, Harness: c.harness, NativeSessionID: nativeSessionID, Workspace: workspace,
				Path: c.path, Fingerprint: sessionFingerprint(s), SourceSize: info.Size(), SourceMTime: info.ModTime(),
				// The parser gives no byte offsets, so the compaction-to-edit
				// metric has nothing to count against.
				MetricComplete: false,
			}, nil
		}
	}
	return CompactionTranscript{}, fmt.Errorf("%w: no session %q in %s", ErrTranscriptIdentity, nativeSessionID, compactionCandidateNames(candidates))
}

type compactionCandidate struct {
	harness string
	path    string
	kind    FileKind
}

func compactionCandidateNames(cs []compactionCandidate) string {
	seen := map[string]bool{}
	var names []string
	for _, c := range cs {
		if !seen[c.harness] {
			seen[c.harness] = true
			names = append(names, c.harness)
		}
	}
	return strings.Join(names, ",")
}

// CompactionTranscriptHarness is the harness whose store holds path, or "" when
// no store deja reads claims it.
func CompactionTranscriptHarness(path string) string {
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if compactionKindMatches(k, path) != "" {
				return h.Name
			}
		}
	}
	return ""
}

// compactionKindMatches is the spelling of path the kind claims: the host may
// hand over a path through a symlink (/tmp on macOS) that the store root does
// not use, or the other way round.
func compactionKindMatches(k FileKind, path string) string {
	if k.Match == nil {
		return ""
	}
	for _, p := range compactionPathSpellings(path) {
		if k.Match(p) {
			return p
		}
	}
	return ""
}

// compactionPathSpellings is path as the host gave it, resolved, and resolved
// back under the home as deja spells it. A host hands over the path it
// resolved — /private/tmp/... on macOS for a home under /tmp — while the
// store roots keep the configured spelling, so neither of the first two
// matched a root and Grok's capture failed with transcript_unavailable.
func compactionPathSpellings(path string) []string {
	out := []string{path}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out
	}
	if resolved != path {
		out = append(out, resolved)
	}
	home := Home()
	if realHome, err := filepath.EvalSymlinks(home); err == nil && realHome != home {
		if rel, err := filepath.Rel(realHome, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = append(out, filepath.Join(home, rel))
		}
	}
	return out
}

// compactionHarnessByLayout names the harness of a file its PreCompact names
// from the file's own layout, for the hosts whose payload says nothing else and
// whose store can sit under a root deja does not scan: VS Code Copilot Chat's
// GitHub.copilot-chat/transcripts/<id>.jsonl in any profile, and Copilot CLI's
// session-state/<id>/events.jsonl (Grok and Reasonix write an events.jsonl too,
// so the directory has to say Copilot).
func compactionHarnessByLayout(path string) string {
	switch {
	case copilotAgentTranscript(path):
		return "copilot-chat"
	case filepath.Base(path) == "events.jsonl" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "session-state":
		return "copilot"
	}
	return ""
}

func compactionCandidatesForPath(harness, path string) []compactionCandidate {
	var out []compactionCandidate
	for _, h := range Registry() {
		if harness != "" && h.Name != harness {
			continue
		}
		for _, k := range h.Kinds {
			if p := compactionKindMatches(k, path); p != "" {
				out = append(out, compactionCandidate{h.Name, p, k})
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if harness != "" {
		if owner := CompactionTranscriptHarness(path); owner != "" && owner != harness {
			return nil
		}
	}
	if harness == "" {
		harness = compactionHarnessByLayout(path)
	}
	if harness == "" {
		return nil
	}
	// A file outside the root the store is read from — a host told to keep its
	// sessions elsewhere — is still that harness's format.
	for _, h := range Registry() {
		if h.Name != harness {
			continue
		}
		for _, k := range h.Kinds {
			if k.Parse != nil {
				out = append(out, compactionCandidate{h.Name, path, k})
			}
		}
	}
	return out
}

// compactionCandidatesByID finds the files of harness that can hold the
// session without enumerating entire multi-year stores: first direct and
// shallow per-project lookups, then bounded newest-first directory walks that
// skip filenames carrying another session id, then bounded history/DB checks.
func compactionCandidatesByID(harness, id string) []compactionCandidate {
	out, _ := compactionCandidatesByIDObserved(harness, id)
	return out
}

func compactionCandidatesByIDObserved(harness, id string) ([]compactionCandidate, compactionLookupStats) {
	var stats compactionLookupStats
	h, ok := compactionHarnessEntry(harness)
	if !ok {
		return nil, stats
	}
	builder := compactionPlanBuilders[h.Name]
	if builder == nil {
		return nil, stats
	}
	plan := builder(id, compactionSafeRelID(id))
	kindOf := func(p string) (FileKind, bool) {
		for _, k := range h.Kinds {
			if k.Match != nil && k.Match(p) {
				return k, true
			}
		}
		return FileKind{}, false
	}
	r := &compactionSearchRunner{
		harness:  h.Name,
		id:       id,
		kindOf:   kindOf,
		stats:    &stats,
		seenPath: map[string]bool{},
	}
	if plan.custom != nil {
		for _, p := range plan.custom(id, r.stats) {
			if k, ok := kindOf(p); ok {
				return []compactionCandidate{{h.Name, p, k}}, stats
			}
		}
	}
	if named := r.findDirect(plan.direct); len(named) > 0 {
		return named, stats
	}
	if named := r.findShallow(plan.shallow); len(named) > 0 {
		return named, stats
	}
	if named := r.findWalks(plan.datedDirs, true); len(named) > 0 {
		return named, stats
	}
	if named := r.findWalks(plan.walks, true); len(named) > 0 {
		return named, stats
	}
	r.collectOpaquePaths(plan.histories, false)
	r.collectOpaquePaths(plan.dbs, h.Name == "hermes")
	return r.probeRecent(), stats
}

func compactionHarnessEntry(harness string) (Harness, bool) {
	for _, h := range StoresToRead() {
		if h.Name == harness {
			return h, true
		}
	}
	return Harness{}, false
}

func compactionSafeRelID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

type compactionDatedPath struct {
	path string
	mod  int64
	kind FileKind
}

type compactionSearchRunner struct {
	harness  string
	id       string
	kindOf   func(string) (FileKind, bool)
	stats    *compactionLookupStats
	seenPath map[string]bool
	recent   []compactionDatedPath
}

func (r *compactionSearchRunner) statRegular(p string) (os.FileInfo, bool) {
	if p == "" || r.seenPath[p] || r.stats.StatCalls >= compactionMaxStatCalls {
		return nil, false
	}
	r.seenPath[p] = true
	r.stats.StatCalls++
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	return info, true
}

func (r *compactionSearchRunner) findDirect(paths []string) []compactionCandidate {
	var out []compactionCandidate
	for _, p := range paths {
		if _, ok := r.statRegular(p); !ok {
			continue
		}
		if k, ok := r.kindOf(p); ok {
			out = append(out, compactionCandidate{r.harness, p, k})
		}
	}
	return out
}

func (r *compactionSearchRunner) findShallow(specs []compactionShallowSpec) []compactionCandidate {
	for _, sh := range specs {
		if sh.dir == "" || r.stats.DirEntries >= compactionMaxDirEntries || r.stats.StatCalls >= compactionMaxStatCalls {
			continue
		}
		entries, err := os.ReadDir(sh.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if r.stats.DirEntries >= compactionMaxDirEntries || r.stats.StatCalls >= compactionMaxStatCalls {
				break
			}
			r.stats.DirEntries++
			if !e.IsDir() || sh.skipSubdirs[e.Name()] {
				continue
			}
			subBase := filepath.Join(sh.dir, e.Name())
			for _, rel := range sh.rels {
				cand := filepath.Join(subBase, rel)
				if _, ok := r.statRegular(cand); !ok {
					continue
				}
				if k, ok := r.kindOf(cand); ok {
					return []compactionCandidate{{r.harness, cand, k}}
				}
			}
			for _, sd := range sh.subDirs {
				if r.stats.StatCalls >= compactionMaxStatCalls {
					break
				}
				r.stats.StatCalls++
				subEntries, subErr := os.ReadDir(filepath.Join(subBase, sd))
				if subErr != nil {
					continue
				}
				for _, se := range subEntries {
					if r.stats.DirEntries >= compactionMaxDirEntries {
						break
					}
					r.stats.DirEntries++
					if se.IsDir() {
						continue
					}
					cand := filepath.Join(subBase, sd, se.Name())
					if k, ok := r.kindOf(cand); ok {
						return []compactionCandidate{{r.harness, cand, k}}
					}
				}
			}
		}
	}
	return nil
}

func (r *compactionSearchRunner) findWalks(specs []compactionDatedSpec, descending bool) []compactionCandidate {
	for _, spec := range specs {
		if spec.dir == "" || r.stats.DirEntries >= compactionMaxDirEntries || r.stats.StatCalls >= compactionMaxStatCalls {
			continue
		}
		if anyDirExists(spec.onlyIfMissingDirs) {
			continue
		}
		if found := r.walkDirBounded(spec, spec.dir, 1, descending); len(found) > 0 {
			return found
		}
	}
	return nil
}

func anyDirExists(dirs []string) bool {
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (r *compactionSearchRunner) walkDirBounded(spec compactionDatedSpec, dir string, depth int, descending bool) []compactionCandidate {
	if r.stats.DirEntries >= compactionMaxDirEntries || r.stats.StatCalls >= compactionMaxStatCalls {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if descending {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	}
	for _, e := range entries {
		if r.stats.DirEntries >= compactionMaxDirEntries || r.stats.StatCalls >= compactionMaxStatCalls {
			return nil
		}
		r.stats.DirEntries++
		name := e.Name()
		p := filepath.Join(dir, name)
		if e.IsDir() {
			if spec.skipDirs[name] || depth >= spec.maxDepth {
				continue
			}
			if found := r.walkDirBounded(spec, p, depth+1, descending); len(found) > 0 {
				return found
			}
			continue
		}
		if spec.wantPrefix != "" && !strings.Contains(name, spec.wantPrefix) {
			continue
		}
		k, ok := r.kindOf(p)
		if !ok {
			continue
		}
		if pathMatchesCompactionID(p, r.id) {
			if _, ok := r.statRegular(p); ok {
				return []compactionCandidate{{r.harness, p, k}}
			}
			continue
		}
		if filenameCarriesDifferentSessionID(name, r.id) {
			continue
		}
		if info, ok := r.statRegular(p); ok {
			r.recent = append(r.recent, compactionDatedPath{p, info.ModTime().UnixNano(), k})
		}
	}
	return nil
}

func (r *compactionSearchRunner) collectOpaquePaths(paths []string, allowNonRegularPG bool) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if allowNonRegularPG && IsHermesPGStore(p) {
			if !r.seenPath[p] {
				r.seenPath[p] = true
				if k, ok := r.kindOf(p); ok {
					r.recent = append(r.recent, compactionDatedPath{p, 0, k})
				}
			}
			continue
		}
		if info, ok := r.statRegular(p); ok {
			if k, ok := r.kindOf(p); ok {
				r.recent = append(r.recent, compactionDatedPath{p, info.ModTime().UnixNano(), k})
			}
		}
	}
}

func (r *compactionSearchRunner) probeRecent() []compactionCandidate {
	if len(r.recent) == 0 {
		return nil
	}
	sort.Slice(r.recent, func(i, j int) bool { return r.recent[i].mod > r.recent[j].mod })
	var out []compactionCandidate
	needle := []byte(r.id)
	var totalRead int64
	for i, f := range r.recent {
		if i >= compactionScanFiles {
			break
		}
		keep, stop := compactionCandidateProbeMatch(r.harness, f.path, r.id, needle, &totalRead)
		r.stats.ProbeBytes = totalRead
		if stop {
			break
		}
		if !keep {
			continue
		}
		out = append(out, compactionCandidate{r.harness, f.path, f.kind})
	}
	return out
}

var (
	compactionUUIDRE        = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	compactionGeminiShortRE = regexp.MustCompile(`(?i)^session-\d{4}-\d{2}-\d{2}t\d{2}-\d{2}-([0-9a-f]{8})\.json$`)
)

func pathMatchesCompactionID(p, id string) bool {
	if id == "" {
		return false
	}
	base := filepath.Base(p)
	stem := trimCompactionSuffixes(base)
	if idStemMatches(stem, id) {
		return true
	}
	dir := filepath.Dir(p)
	for range 3 {
		name := filepath.Base(dir)
		if name == "." || name == string(filepath.Separator) || name == "" {
			break
		}
		if name == id || name == "session_"+id || name == "session-"+id || name == "sess_"+id {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}

func trimCompactionSuffixes(base string) string {
	for _, suf := range []string{".messages.json", ".jsonl.zst", ".jsonl.gz", ".jsonl", ".json", ".pb"} {
		if trimmed, ok := strings.CutSuffix(base, suf); ok {
			return trimmed
		}
	}
	return base
}

func idStemMatches(stem, id string) bool {
	return stem == id ||
		strings.HasSuffix(stem, "-"+id) ||
		strings.HasSuffix(stem, "_"+id) ||
		strings.HasPrefix(stem, id+"-") ||
		strings.HasPrefix(stem, id+"_")
}

func filenameCarriesDifferentSessionID(base, wantID string) bool {
	lowerBase := strings.ToLower(base)
	lowerWant := strings.ToLower(wantID)
	if lowerWant != "" && strings.Contains(lowerBase, lowerWant) {
		return false
	}
	if strings.HasPrefix(lowerBase, "rollout-") && (strings.HasSuffix(lowerBase, ".jsonl") || strings.HasSuffix(lowerBase, ".jsonl.zst")) {
		return true
	}
	if uuid := compactionUUIDRE.FindString(lowerBase); uuid != "" {
		if len(lowerWant) == 8 && strings.HasPrefix(uuid, lowerWant) {
			return false
		}
		return true
	}
	if m := compactionGeminiShortRE.FindStringSubmatch(lowerBase); len(m) == 2 {
		if len(lowerWant) >= 8 && !strings.HasPrefix(lowerWant, m[1]) {
			return true
		}
	}
	return false
}

var compactionPlanBuilders = map[string]func(id string, safeID bool) compactionSearchPlan{
	"claude":       compactionPlanClaude,
	"codearts":     compactionPlanCodeArts,
	"doubao":       compactionPlanDoubao,
	"codex":        compactionPlanCodex,
	"trae":         compactionPlanTrae,
	"opencode":     compactionPlanOpencode,
	"mcode":        compactionPlanMcode,
	"aider":        compactionPlanAider,
	"amp":          compactionPlanAmp,
	"gemini":       compactionPlanGemini,
	"cursor":       compactionPlanCursor,
	"antigravity":  compactionPlanAntigravity,
	"grok":         compactionPlanGrok,
	"hermes":       compactionPlanHermes,
	"goose":        compactionPlanGoose,
	"qwen":         compactionPlanQwen,
	"gigacode":     compactionPlanGigaCode,
	"qoder":        compactionPlanQoder,
	"kimi":         compactionPlanKimi,
	"cline":        compactionPlanCline,
	"cherrystudio": compactionPlanCherryStudio,
	"senpi":        compactionPlanSenpi,
	"gjc":          compactionPlanGjc,
	"kimchi":       compactionPlanKimchi,
	"clawcode":     compactionPlanClawCode,
	"commandcode":  compactionPlanCommandCode,
	"zcode":        compactionPlanZCode,
	"kiro":         compactionPlanKiro,
	"kilocode":     compactionPlanKilocode,
	"roo":          compactionPlanRoo,
	"crush":        compactionPlanCrush,
	"continue":     compactionPlanContinue,
	"pi":           compactionPlanPi,
	"prime":        compactionPlanPrime,
	"omp":          compactionPlanOmp,
	"openclaw":     compactionPlanOpenClaw,
	"copilot":      compactionPlanCopilot,
	"copilot-chat": compactionPlanCopilotChat,
	"codewhale":    compactionPlanCodeWhale,
	"codebuddy":    compactionPlanCodeBuddy,
	"reasonix":     compactionPlanReasonix,
	"muse":         compactionPlanMuse,
	"deepseek":     compactionPlanDeepSeek,
	"zed":          compactionPlanZed,
	"devin":        compactionPlanDevin,
	"junie":        compactionPlanJunie,
	"jetbrains":    compactionPlanJetBrains,
	"windsurf":     compactionPlanWindsurf,
	"mimo":         compactionPlanMimo,
	"teleagent":    compactionPlanTeleAgent,
	"omnigent":     compactionPlanOmnigent,
	"orca":         compactionPlanOrca,
	"ante":         compactionPlanAnte,
	"deja":         compactionPlanDeja,
}

func compactionPlanClaude(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	projDir := claudeProjectsDirName()
	for _, r := range ClaudeRoots() {
		if !safeID {
			continue
		}
		p.direct = append(p.direct, filepath.Join(r, id+".jsonl"))
		if filepath.Base(r) == projDir {
			p.shallow = append(p.shallow, compactionShallowSpec{dir: r, rels: []string{id + ".jsonl"}})
		} else {
			p.shallow = append(p.shallow,
				compactionShallowSpec{dir: filepath.Join(r, projDir), rels: []string{id + ".jsonl"}},
				compactionShallowSpec{dir: r, rels: []string{id + ".jsonl"}, skipSubdirs: map[string]bool{projDir: true}},
			)
		}
	}
	if hist := ClaudeHistoryFile(); hist != "" {
		p.histories = append(p.histories, hist)
	}
	if db := ClaudeMemDBPath(); db != "" {
		p.dbs = append(p.dbs, db)
	}
	return p
}

func compactionPlanCodeArts(id string, safeID bool) compactionSearchPlan {
	root := CodeArtsRoot()
	if !safeID || root == "" {
		return compactionSearchPlan{}
	}
	return compactionSearchPlan{
		direct:  []string{filepath.Join(root, id, "history.jsonl")},
		shallow: []compactionShallowSpec{{dir: root, rels: []string{filepath.Join(id, "history.jsonl")}}},
	}
}

func compactionPlanDoubao(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{
		walks: []compactionDatedSpec{{dir: DoubaoRoot(), maxDepth: doubaoMaxDepthWalk}},
	}
}

func compactionPlanCodex(_ string, _ bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, r := range CodexRoots() {
		for _, dir := range codexSessionDirs(r) {
			p.datedDirs = append(p.datedDirs, compactionDatedSpec{dir: dir, maxDepth: 4, wantPrefix: "rollout-"})
		}
	}
	p.histories = []string{filepath.Join(CodexRoot(), "history.jsonl")}
	return p
}

func compactionPlanTrae(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, dir := range TraeSessionDirs() {
		if safeID {
			p.direct = append(p.direct, filepath.Join(dir, id+".json"), filepath.Join(dir, id+".jsonl"))
		}
		p.datedDirs = append(p.datedDirs, compactionDatedSpec{dir: dir, maxDepth: 4, wantPrefix: "rollout-"})
	}
	p.histories = []string{filepath.Join(TraeRoot(), "history.jsonl")}
	return p
}

func compactionPlanOpencode(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	if safeID {
		p.direct = []string{filepath.Join(OpencodeDiffDir(), id+".json")}
	}
	p.dbs = []string{OpencodeDB()}
	return p
}

func compactionPlanMcode(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{
		custom: func(id string, stats *compactionLookupStats) []string {
			if p := mcodeSessionPathBounded(id, stats); p != "" {
				return []string{p}
			}
			return nil
		},
	}
}

func compactionPlanAider(_ string, _ bool) compactionSearchPlan {
	var histories []string
	if env := os.Getenv("AIDER_CHAT_HISTORY_FILE"); env != "" {
		histories = append(histories, env)
	}
	histories = append(histories, filepath.Join(Home(), aiderHistoryName))
	for _, dir := range aiderProjects() {
		histories = append(histories, filepath.Join(dir, aiderHistoryName))
	}
	roots := AiderRoots()
	walks := make([]compactionDatedSpec, 0, len(roots))
	for _, r := range roots {
		histories = append(histories, filepath.Join(r, aiderHistoryName))
		walks = append(walks, compactionDatedSpec{dir: r, maxDepth: 3})
	}
	return compactionSearchPlan{histories: histories, walks: walks}
}

func compactionPlanAmp(id string, safeID bool) compactionSearchPlan {
	root := AmpRoot()
	if !safeID || root == "" {
		return compactionSearchPlan{}
	}
	return compactionSearchPlan{
		direct:  []string{filepath.Join(root, id+".json")},
		shallow: []compactionShallowSpec{{dir: root, rels: []string{id + ".json"}}},
	}
}

func compactionPlanGemini(_ string, _ bool) compactionSearchPlan {
	var p compactionSearchPlan
	skip := map[string]bool{"checkpoints": true, "tasks": true}
	for _, r := range GeminiRoots() {
		tmpDir := filepath.Join(r, "tmp")
		p.datedDirs = append(p.datedDirs,
			compactionDatedSpec{dir: tmpDir, maxDepth: 4, skipDirs: skip},
			compactionDatedSpec{dir: r, maxDepth: 3, skipDirs: skip, onlyIfMissingDirs: []string{tmpDir}},
		)
	}
	return p
}

func compactionPlanCursor(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	if safeID {
		p.shallow = []compactionShallowSpec{{
			dir:  filepath.Join(CursorCLIRoot(), "projects"),
			rels: []string{filepath.Join("agent-transcripts", id+".jsonl"), filepath.Join("agent-transcripts", id, id+".jsonl")},
		}}
	}
	p.dbs = CursorDBs()
	return p
}

func compactionPlanAntigravity(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	var p compactionSearchPlan
	for _, r := range AntigravityRoots() {
		p.direct = append(p.direct,
			filepath.Join(r, "brain", id, ".system_generated", "logs", "transcript.jsonl"),
			filepath.Join(r, "brain", id, ".system_generated", "logs", "transcript_full.jsonl"),
		)
	}
	return p
}

func compactionPlanGrok(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	sessRoot := filepath.Join(GrokRoot(), "sessions")
	if safeID {
		p.direct = []string{filepath.Join(sessRoot, id, "updates.jsonl")}
		p.shallow = []compactionShallowSpec{{dir: sessRoot, rels: []string{filepath.Join(id, "updates.jsonl")}}}
	}
	p.dbs = []string{GrokDB()}
	return p
}

func compactionPlanHermes(_ string, _ bool) compactionSearchPlan {
	dbs := slices.Clone(HermesDBs())
	if dsn := HermesPGDSN(); dsn != "" {
		if pg := HermesPGStorePath(dsn); pg != "" {
			dbs = append(dbs, pg)
		}
	}
	return compactionSearchPlan{dbs: dbs}
}

func compactionPlanGoose(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, dir := range GooseSessionsDirs() {
		if safeID {
			p.direct = append(p.direct, filepath.Join(dir, id+".jsonl"))
		}
		p.walks = append(p.walks, compactionDatedSpec{dir: dir, maxDepth: 2})
	}
	p.dbs = GooseDBs()
	return p
}

func compactionPlanQwen(id string, safeID bool) compactionSearchPlan {
	return compactionPlanQwenFamily(QwenRoot(), id, safeID)
}

func compactionPlanGigaCode(id string, safeID bool) compactionSearchPlan {
	return compactionPlanQwenFamily(GigaCodeRoot(), id, safeID)
}

func compactionPlanQwenFamily(root, id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	return compactionSearchPlan{
		direct:  []string{filepath.Join(root, id+".jsonl")},
		shallow: []compactionShallowSpec{{dir: filepath.Join(root, "projects"), rels: []string{filepath.Join("chats", id+".jsonl")}}},
	}
}

func compactionPlanQoder(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := QoderRoot()
	return compactionSearchPlan{
		direct:  []string{filepath.Join(root, id+".jsonl")},
		shallow: []compactionShallowSpec{{dir: filepath.Join(root, "projects"), rels: []string{id + ".jsonl"}}},
	}
}

func compactionPlanKimi(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	sessRoot := filepath.Join(KimiRoot(), "sessions")
	return compactionSearchPlan{
		direct:  []string{filepath.Join(sessRoot, id, "agents", "main", "wire.jsonl")},
		shallow: []compactionShallowSpec{{dir: sessRoot, rels: []string{filepath.Join(id, "agents", "main", "wire.jsonl")}}},
	}
}

func compactionPlanCline(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	p := compactionSearchPlan{
		direct: []string{filepath.Join(ClineSessionsDir(), id, id+".messages.json")},
	}
	for _, r := range ClineLegacyRoots() {
		p.direct = append(p.direct, filepath.Join(r, "tasks", id, "api_conversation_history.json"))
	}
	return p
}

func compactionPlanCherryStudio(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	seen := map[string]bool{}
	for _, r := range CherryStudioRoots() {
		seen[r] = true
		if safeID {
			p.direct = append(p.direct, filepath.Join(r, id+".jsonl"))
			p.shallow = append(p.shallow, compactionShallowSpec{dir: r, rels: []string{id + ".jsonl"}})
		}
	}
	for _, r := range CherryStudioAllRoots() {
		if seen[r] {
			continue
		}
		p.walks = append(p.walks, compactionDatedSpec{dir: r, maxDepth: 3})
	}
	return p
}

func compactionPlanSenpi(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := SenpiRoot()
	if safeID {
		p.direct = []string{filepath.Join(root, id+".jsonl")}
	}
	p.walks = []compactionDatedSpec{{dir: root, maxDepth: 3}}
	return p
}

func compactionPlanGjc(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := GjcRoot()
	if safeID {
		p.direct = []string{filepath.Join(root, id+".jsonl")}
		p.shallow = []compactionShallowSpec{{dir: root, rels: []string{id + ".jsonl"}}}
	}
	p.walks = []compactionDatedSpec{{dir: root, maxDepth: 3}}
	return p
}

func compactionPlanKimchi(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := KimchiRoot()
	if safeID {
		p.direct = []string{filepath.Join(root, id+".jsonl")}
	}
	p.walks = []compactionDatedSpec{{dir: root, maxDepth: 3}}
	return p
}

func compactionPlanClawCode(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	var p compactionSearchPlan
	for _, r := range ClawCodeRoots() {
		p.direct = append(p.direct, filepath.Join(r, id+".jsonl"), filepath.Join(r, id+".json"))
		p.shallow = append(p.shallow, compactionShallowSpec{dir: r, rels: []string{id + ".jsonl", id + ".json"}})
	}
	return p
}

func compactionPlanCommandCode(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := CommandCodeRoot()
	return compactionSearchPlan{
		direct: []string{filepath.Join(root, id+".jsonl")},
		shallow: []compactionShallowSpec{
			{dir: root, rels: []string{id + ".jsonl"}},
			{dir: filepath.Join(root, "projects"), rels: []string{id + ".jsonl"}},
		},
	}
}

func compactionPlanZCode(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	if safeID {
		root := ZCodeRoot()
		legacy := ZCodeLegacyRoot()
		p.direct = []string{filepath.Join(root, id+".jsonl"), filepath.Join(legacy, id+".json")}
		p.shallow = []compactionShallowSpec{
			{dir: root, rels: []string{id + ".jsonl"}},
			{dir: legacy, rels: []string{id + ".json"}},
		}
	}
	p.dbs = []string{ZCodeDB()}
	return p
}

func compactionPlanKiro(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := KiroRoot()
	if safeID {
		p.direct = []string{
			filepath.Join(root, "cli", id+".jsonl"),
			filepath.Join(root, "sess_"+id, "messages.jsonl"),
			filepath.Join(root, id, "messages.jsonl"),
		}
		p.shallow = []compactionShallowSpec{{
			dir:  root,
			rels: []string{filepath.Join("sess_"+id, "messages.jsonl"), filepath.Join(id, "messages.jsonl")},
		}}
	}
	p.dbs = []string{KiroDB()}
	return p
}

func compactionPlanKilocode(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	if safeID {
		for _, r := range KiloRoots() {
			p.direct = append(p.direct, filepath.Join(r, "tasks", id, "api_conversation_history.json"))
		}
	}
	p.dbs = []string{KiloDB()}
	return p
}

func compactionPlanRoo(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	var p compactionSearchPlan
	for _, r := range append(RooRoots(), RooCLIRoot()) {
		p.direct = append(p.direct, filepath.Join(r, "tasks", id, "api_conversation_history.json"))
	}
	return p
}

func compactionPlanCrush(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: CrushDBs()}
}

func compactionPlanContinue(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := ContinueRoot()
	return compactionSearchPlan{
		direct: []string{filepath.Join(root, "sessions", id+".json"), filepath.Join(root, id+".json")},
	}
}

func compactionPlanPi(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := PiRoot()
	if safeID {
		p.direct = []string{filepath.Join(root, id+".jsonl")}
	}
	p.walks = []compactionDatedSpec{{dir: root, maxDepth: 3}}
	return p
}

func compactionPlanPrime(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	if safeID {
		p.direct = []string{filepath.Join(PrimeRoot(), id+".jsonl")}
	}
	for _, r := range PrimeRoots() {
		p.walks = append(p.walks, compactionDatedSpec{dir: r, maxDepth: 3})
	}
	return p
}

func compactionPlanOmp(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, r := range OmpSessionRoots() {
		if safeID {
			p.direct = append(p.direct, filepath.Join(r, id+".jsonl"))
		}
		p.walks = append(p.walks, compactionDatedSpec{dir: r, maxDepth: 3})
	}
	return p
}

func compactionPlanOpenClaw(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	root := OpenClawRoot()
	if safeID {
		p.shallow = []compactionShallowSpec{{dir: root, rels: []string{filepath.Join("sessions", id+".jsonl")}}}
	}
	p.walks = []compactionDatedSpec{{dir: root, maxDepth: 3}}
	p.dbs = OpenClawAgentDBs()
	return p
}

func compactionPlanCopilot(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	return compactionSearchPlan{direct: []string{filepath.Join(CopilotRoot(), id, "events.jsonl")}}
}

func compactionPlanCopilotChat(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, r := range CopilotChatRoots() {
		if safeID {
			p.direct = append(p.direct,
				filepath.Join(r, "globalStorage", "emptyWindowChatSessions", id+".jsonl"),
				filepath.Join(r, "globalStorage", "emptyWindowChatSessions", id+".json"),
				filepath.Join(r, id+".jsonl"),
				filepath.Join(r, id+".json"),
			)
			p.shallow = append(p.shallow, compactionShallowSpec{
				dir: filepath.Join(r, "workspaceStorage"),
				rels: []string{
					filepath.Join("chatSessions", id+".jsonl"),
					filepath.Join("chatSessions", id+".json"),
					filepath.Join(copilotAgentParentDir, copilotAgentDirName, id+".jsonl"),
				},
			})
		}
		p.walks = append(p.walks, compactionDatedSpec{
			dir:               r,
			maxDepth:          4,
			onlyIfMissingDirs: []string{filepath.Join(r, "globalStorage"), filepath.Join(r, "workspaceStorage")},
		})
	}
	return p
}

func compactionPlanCodeWhale(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	var p compactionSearchPlan
	for _, r := range CodeWhaleRoots() {
		p.direct = append(p.direct, filepath.Join(r, id+".json"))
		p.shallow = append(p.shallow, compactionShallowSpec{dir: r, rels: []string{id + ".json"}})
	}
	return p
}

func compactionPlanCodeBuddy(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	var p compactionSearchPlan
	for _, r := range CodeBuddyRoots() {
		p.direct = append(p.direct, filepath.Join(r, id+".jsonl"))
		if filepath.Base(r) == "projects" {
			p.shallow = append(p.shallow, compactionShallowSpec{dir: r, rels: []string{id + ".jsonl"}})
		} else {
			p.shallow = append(p.shallow,
				compactionShallowSpec{dir: filepath.Join(r, "projects"), rels: []string{id + ".jsonl"}},
				compactionShallowSpec{dir: r, rels: []string{id + ".jsonl"}, skipSubdirs: map[string]bool{"projects": true}},
			)
		}
	}
	return p
}

func compactionPlanReasonix(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, r := range ReasonixRoots() {
		if safeID {
			p.direct = append(p.direct,
				filepath.Join(r, "desktop-sessions-v5", "by-id", id, "events.frames"),
				filepath.Join(r, "sessions-v4", id, "events.frames"),
				filepath.Join(r, "sessions", id+".jsonl"),
				filepath.Join(r, id, "events.frames"),
				filepath.Join(r, id+".jsonl"),
			)
			p.shallow = append(p.shallow, compactionShallowSpec{
				dir: filepath.Join(r, "projects"),
				rels: []string{
					filepath.Join("sessions-v4", id, "events.frames"),
					filepath.Join("sessions", id+".jsonl"),
					filepath.Join(id, "events.frames"),
					id + ".jsonl",
				},
			})
		}
		p.walks = append(p.walks, compactionDatedSpec{dir: r, maxDepth: 4})
	}
	return p
}

func compactionPlanMuse(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{
		custom: func(id string, stats *compactionLookupStats) []string {
			if p := museSessionPathBounded(id, stats); p != "" {
				return []string{p}
			}
			return nil
		},
	}
}

func compactionPlanDeepSeek(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := DeepSeekRoot()
	names := []string{"session.v4.jsonl.zstd", "session.v4.jsonl", "session.v3.jsonl.zstd", "session.v3.jsonl", "session.jsonl.zstd", "session.jsonl"}
	var direct []string
	for _, n := range names {
		direct = append(direct, filepath.Join(root, "session-"+id, n), filepath.Join(root, id, n))
	}
	return compactionSearchPlan{
		direct:  direct,
		shallow: []compactionShallowSpec{{dir: root, subDirs: []string{"session-" + id, id}}},
	}
}

func compactionPlanZed(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: []string{ZedDB()}}
}

func compactionPlanDevin(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: []string{DevinSessionsDB(), DevinLegacySessionsDB()}}
}

func compactionPlanJunie(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := JunieRoot()
	return compactionSearchPlan{
		direct:  []string{filepath.Join(root, id, "events.jsonl")},
		shallow: []compactionShallowSpec{{dir: root, rels: []string{filepath.Join(id, "events.jsonl")}}},
	}
}

func compactionPlanJetBrains(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{
		walks: []compactionDatedSpec{{dir: JetBrainsRoot(), maxDepth: 3}},
	}
}

func compactionPlanWindsurf(id string, safeID bool) compactionSearchPlan {
	if !safeID {
		return compactionSearchPlan{}
	}
	root := WindsurfRoot()
	return compactionSearchPlan{
		direct: []string{filepath.Join(root, "cascade", id+".pb"), filepath.Join(root, id+".pb")},
	}
}

func compactionPlanMimo(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: []string{MimoDB()}}
}

func compactionPlanTeleAgent(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: TeleAgentDBs()}
}

func compactionPlanOmnigent(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: []string{OmnigentDB()}}
}

func compactionPlanOrca(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{dbs: []string{OrcaDB()}}
}

func compactionPlanAnte(id string, safeID bool) compactionSearchPlan {
	var p compactionSearchPlan
	for _, r := range AnteRoots() {
		if safeID {
			p.direct = append(p.direct, filepath.Join(r, id, "events.jsonl"), filepath.Join(r, id, "dialog.json"))
		}
		if !SubagentsOff() {
			p.walks = append(p.walks, compactionDatedSpec{dir: r, maxDepth: 3})
		}
	}
	return p
}

func compactionPlanDeja(_ string, _ bool) compactionSearchPlan {
	return compactionSearchPlan{histories: append(NotesFiles(), RepoMemoryFiles()...)}
}

func compactionCandidateProbeMatch(harness, path, id string, needle []byte, totalRead *int64) (keep, stop bool) {
	switch {
	case isCompactionCompressedTranscript(path):
		// Compressed rollouts name the session id in their filename; skip full
		// archive decompression when probing for an unnamed id.
		return false, false
	case isCompactionTextTranscript(path):
		if *totalRead >= compactionProbeTotalBytes {
			return false, true
		}
		found, n := fileContainsTokenBounded(path, needle)
		*totalRead += n
		return found, false
	case isCompactionDBPath(path):
		if checked, found := compactionDBContainsID(harness, path, id); checked {
			return found, false
		}
		return true, false
	default:
		return true, false
	}
}

func isCompactionTextTranscript(path string) bool {
	return strings.HasSuffix(path, ".jsonl") ||
		strings.HasSuffix(path, ".json") ||
		strings.HasSuffix(path, ".txt") ||
		strings.HasSuffix(path, ".md")
}

func isCompactionCompressedTranscript(path string) bool {
	return strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".gz")
}

func isCompactionDBPath(path string) bool {
	return strings.HasSuffix(path, ".db") ||
		strings.HasSuffix(path, ".sqlite") ||
		strings.HasSuffix(path, ".sqlite3") ||
		strings.HasSuffix(path, ".vscdb")
}

func fileContainsTokenBounded(path string, needle []byte) (bool, int64) {
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return false, 0
	}
	sz := info.Size()
	if sz <= 2*compactionProbeWindow {
		buf := make([]byte, sz)
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return false, int64(n)
		}
		return bytes.Contains(buf[:n], needle), int64(n)
	}
	buf := make([]byte, compactionProbeWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, int64(n)
	}
	if bytes.Contains(buf[:n], needle) {
		return true, int64(n)
	}
	m, err := f.ReadAt(buf, sz-compactionProbeWindow)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, int64(n + m)
	}
	return bytes.Contains(buf[:m], needle), int64(n + m)
}

func compactionDBContainsID(harness, path, id string) (checked, found bool) {
	esc := sqlEscape(id)
	var queries []string
	switch harness {
	case "goose", "hermes", "devin", "crush", "grok":
		queries = []string{`select 1 from sessions where id='` + esc + `' limit 1`}
	case "opencode", "kilocode", "mimo", "zcode":
		queries = []string{`select 1 from session where id='` + esc + `' limit 1`}
	case "teleagent":
		queries = []string{
			`select 1 from session where id='` + esc + `' limit 1`,
			`select 1 from session_v2 where id='` + esc + `' limit 1`,
		}
	case "omnigent":
		queries = []string{`select 1 from conversations where id='` + esc + `' limit 1`}
	case "orca":
		queries = []string{`select 1 from agent_session_records where session_id='` + esc + `' limit 1`}
	case "zed":
		queries = []string{`select 1 from threads where id='` + esc + `' limit 1`}
	case "kiro":
		queries = []string{`select 1 from conversations_v2 where conversation_id='` + esc + `' limit 1`}
	case "openclaw":
		queries = []string{`select 1 from transcript_events where session_id='` + esc + `' limit 1`}
	default:
		return false, false
	}
	for _, q := range queries {
		out, err := sqliteOutput(path, q)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(out)) != "" {
			return true, true
		}
		checked = true
	}
	return checked, false
}

// compactionSessionWorkspace is the directory a session recorded running in,
// read where each store keeps it — the same places resume reads it from.
func compactionSessionWorkspace(harness, path string, s model.Session) string {
	switch harness {
	case "pi", "gjc", "kimchi", "senpi", "omp", "openclaw":
		return PiHeaderCwd(path)
	case "cherrystudio":
		if cwd := PiHeaderCwd(path); cwd != "" {
			return cwd
		}
		return ClaudeSessionDir(path)
	case "prime":
		return PrimeSessionDir(path)
	case "kimi":
		return KimiSessionDir(path)
	case "qwen":
		dir, _ := QwenSessionDir(path)
		return dir
	case "gigacode":
		dir, _ := GigaCodeSessionDir(path)
		return dir
	case "grok":
		if strings.HasSuffix(path, "updates.jsonl") {
			return GrokCWDForSession(path)
		}
	case "cursor":
		if cwd, _ := CursorChatCWD(path); cwd != "" {
			return cwd
		}
		if base := CursorTranscriptProjectDirBase(path); base != "" {
			return ResolveEncodedPath(base)
		}
	case "copilot":
		return copilotHeadCWD(path)
	case "copilot-chat":
		return CopilotChatWorkspaceDir(path)
	case "cline":
		return ClineSessionDir(path)
	case "trae", "codex":
		_, cwd, _, _ := codexRolloutHead(path)
		return cwd
	case "claude", "qoder":
		return ClaudeSessionDir(path)
	case "mcode":
		return McodeSessionWorkspace(s.ID)
	case "devin":
		return DevinSessionDir(path, s.ID)
	case "omnigent":
		return OmnigentSessionDir(path, s.ID)
	case "orca":
		return OrcaSessionDir(path, s.ID)
	case "ante":
		return AnteSessionDir(path)
	}
	// opencode-shaped stores keep the directory as the session's path.
	if s.Path != "" && s.Path != path {
		if info, err := os.Stat(s.Path); err == nil && info.IsDir() {
			return s.Path
		}
	}
	return ""
}
