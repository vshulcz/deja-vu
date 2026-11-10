package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/gitread"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja repo candidates` proposes entries for the repository from this
// project's history and writes nothing. Three sources, best first: notes the
// person promoted (accepted is a decision, rejected stays rejected), notes they
// remembered, and what sessions said, read with the detectors deja already
// runs: a reversal ("reverted the cache, it broke ordering") is a dropped
// approach, a session's settled conclusion a decision. The last two are
// guesses, which is why the person picks.

var repoCandidateFlags = []string{"--json", "--limit", "--since"}

type repoCandidate struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Files    []string `json:"files,omitempty"`
	Commit   string   `json:"commit,omitempty"`
	Sessions int      `json:"sessions,omitempty"`
	From     string   `json:"from,omitempty"`
	Source   string   `json:"source"`
	Session  string   `json:"session,omitempty"`
	InFile   bool     `json:"in_file,omitempty"`
	Refused  string   `json:"refused,omitempty"`
	tier     int
	at       time.Time
}

func runRepoCandidates(dir string, w io.Writer, args []string) error {
	asJSON := false
	limit := 30
	var since time.Duration
	args = splitKnownValueFlags(args, "--limit", "--since")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("repo candidates: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("repo candidates: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--since":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("repo candidates: --since needs value")
			}
			i++
			d, err := parseDur(args[i])
			if err != nil {
				return err
			}
			since = d
		default:
			return unknownFlag("repo candidates", args[i], repoCandidateFlags)
		}
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	if err = index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	cands, err := collectRepoCandidates(dir, root, since, limit)
	if err != nil {
		return err
	}
	if asJSON {
		if cands == nil {
			cands = []repoCandidate{}
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(cands)
	}
	name := sources.ClaudeProjectName(root)
	if len(cands) == 0 {
		fmt.Fprintf(w, "no candidates for %s in your history — `deja repo add \"...\"` writes one by hand\n", search.SafeLine(name))
		return nil
	}
	fmt.Fprintf(w, "%d candidate%s for %s, best first. nothing is written until you pick\n", len(cands), pluralS(len(cands)), search.SafeLine(name))
	for i, c := range cands {
		mark := ""
		switch {
		case c.InFile:
			mark = "  [already in " + repoFileShown + "]"
		case c.Refused != "":
			mark = "  [refused: " + c.Refused + "]"
		}
		var meta []string
		if len(c.Files) > 0 {
			shown := c.Files
			if len(shown) > 2 {
				shown = shown[:2]
			}
			fileStr := strings.Join(shown, ", ")
			if len(c.Files) > 2 {
				fileStr += fmt.Sprintf(" +%d", len(c.Files)-2)
			}
			meta = append(meta, fileStr)
		}
		if c.Commit != "" {
			meta = append(meta, "@"+c.Commit)
		}
		from := c.Source
		if c.From != "" {
			from += ", " + c.From
		}
		meta = append(meta, from)
		fmt.Fprintf(w, "%2d. %s %-8s %s  (%s)%s\n", i+1, c.ID, c.Kind, search.SafeLine(c.Text), search.SafeLine(strings.Join(meta, " · ")), mark)
	}
	fmt.Fprintln(w, "add the ones that hold for everyone: deja repo add <n|id>...  (or `deja repo init` for the top picks)")
	return nil
}

// collectRepoCandidates gathers candidates for the project root is, best tier
// then newest first, with limit keeping the top; since and limit of zero mean all.
func collectRepoCandidates(dir, root string, since time.Duration, limit int) ([]repoCandidate, error) {
	names := digest.ProjectNameCandidates(root)
	inScope := func(project string) bool {
		if strings.HasPrefix(project, "repo:") {
			return false
		}
		for _, n := range names {
			if n != "" && index.ProjectInScope(project, n) {
				return true
			}
		}
		return false
	}
	pol := policy.Load()
	allowed := func(path, project string) bool {
		return pol.Allows(policy.ActivationSearch, project) && !pol.Ignored(path, project) && !sources.ExcludedProject(project)
	}
	var out []repoCandidate
	add := func(c repoCandidate) {
		text, _, err := repoSafeText(c.Text)
		if err != nil {
			c.Refused = err.Error()
			c.Text = search.SafeLine(squeezeTrim(c.Text, 120))
		} else {
			c.Text = text
		}
		c.ID = sources.RepoEntryID(c.Kind, c.Text)
		out = append(out, c)
	}
	for _, n := range sources.LoadPromotedNotes() {
		if !inScope(n.Project) || !allowed("", n.Project) {
			continue
		}
		var kind string
		switch n.State {
		case "accepted":
			kind = "decision"
		case "rejected":
			kind = "rejected"
		default:
			continue
		}
		text := strings.TrimSpace(n.Text)
		if text == "" {
			text = n.Title
		}
		harness, _, _ := strings.Cut(n.Session, ":")
		add(repoCandidate{Kind: kind, Text: text, From: repoFrom(harness, n.At), Source: "promoted note", tier: 0, at: n.At})
	}
	for _, s := range sources.LoadNotes() {
		if s.IsPromotedNote() || !inScope(s.Project) || !allowed(s.Path, s.Project) {
			continue
		}
		for _, m := range s.Messages {
			if len([]rune(strings.TrimSpace(m.Text))) < 25 {
				continue
			}
			add(repoCandidate{Kind: inferRepoKind(m.Text), Text: m.Text, From: repoFrom("deja", m.Time), Source: "your note", tier: 0, at: m.Time})
		}
	}
	if err := sessionRepoCandidates(dir, root, inScope, allowed, add); err != nil {
		return nil, err
	}
	for _, pair := range orientCoChangePairs(dir, names, root, orientFiles(dir, names, root), policy.ActivationAuto) {
		parts := strings.SplitN(pair, " (", 2)
		if len(parts) != 2 {
			continue
		}
		files := strings.SplitN(parts[0], " ↔ ", 2)
		if len(files) != 2 {
			continue
		}
		fileA := strings.TrimSpace(files[0])
		fileB := strings.TrimSpace(files[1])
		stats := strings.TrimSuffix(parts[1], ")")
		count := 3
		if n, err := fmt.Sscanf(stats, "%d of ", &count); n != 1 || err != nil {
			count = 3
		}
		add(repoCandidate{
			Kind:     "gotcha",
			Text:     fmt.Sprintf("Editing %s requires updating %s alongside it (%s)", fileA, fileB, stats),
			Files:    []string{fileA, fileB},
			Sessions: count,
			From:     repoFrom("deja", time.Now()),
			Source:   "coupled files",
			tier:     0,
			at:       time.Now(),
		})
	}
	enrichRepoCandidateFilesAndCommits(dir, root, out)
	return sortAndDedupeRepoCandidates(root, out, since, limit), nil
}

func sortAndDedupeRepoCandidates(root string, out []repoCandidate, since time.Duration, limit int) []repoCandidate {
	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	in := map[string]bool{}
	inText := map[string]bool{}
	inPrefix := map[string]bool{}
	if entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root)); err == nil {
		for _, e := range entries {
			in[e.ID] = true
			inText[sources.RepoEntryID("", e.Text)] = true
			if pkey := repoCandidatePrefixKey(e.Kind, e.Text); pkey != "" {
				inPrefix[pkey] = true
			}
		}
	}
	sort.SliceStable(out, repoCandidateLess(out))
	seen := map[string]int{}
	seenText := map[string]int{}
	seenPrefix := map[string]int{}
	perSessionKind := map[string]int{}
	perSessionTotal := map[string]int{}
	kept := make([]repoCandidate, 0, len(out))
	for _, c := range out {
		if !cutoff.IsZero() && c.at.Before(cutoff) {
			continue
		}
		tkey := sources.RepoEntryID("", c.Text)
		pkey := repoCandidatePrefixKey(c.Kind, c.Text)
		if idx := max(seen[c.ID], seenText[tkey], seenPrefix[pkey]); idx > 0 {
			mergeRepoCandidateInto(&kept[idx-1], c)
			continue
		}
		if c.Session != "" && c.tier > 0 {
			primary := ""
			if len(c.Files) > 0 {
				primary = c.Files[len(c.Files)-1]
			}
			sk := c.Session + ":" + c.Kind + ":" + primary
			if perSessionKind[sk] >= 1 || perSessionTotal[c.Session] >= 2 {
				continue
			}
			perSessionKind[sk]++
			perSessionTotal[c.Session]++
		}
		c.InFile = in[c.ID] || inText[tkey] || (pkey != "" && inPrefix[pkey])
		if c.Sessions <= 0 {
			c.Sessions = 1
		}
		kept = append(kept, c)
		seen[c.ID] = len(kept)
		seenText[tkey] = len(kept)
		if pkey != "" {
			seenPrefix[pkey] = len(kept)
		}
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	return kept
}

func repoCandidateLess(out []repoCandidate) func(i, j int) bool {
	return func(i, j int) bool {
		if out[i].tier != out[j].tier {
			return out[i].tier < out[j].tier
		}
		if (len(out[i].Files) > 0) != (len(out[j].Files) > 0) {
			return len(out[i].Files) > 0
		}
		if out[i].Session != "" && out[i].Session == out[j].Session {
			if len(out[i].Files) != len(out[j].Files) {
				return len(out[i].Files) > len(out[j].Files)
			}
			return len(out[i].Text) > len(out[j].Text)
		}
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.After(out[j].at)
		}
		return len(out[i].Text) > len(out[j].Text)
	}
}

func mergeRepoCandidateInto(dst *repoCandidate, src repoCandidate) {
	dst.Files = sources.CleanRepoFiles(append(dst.Files, src.Files...))
	if dst.Commit == "" && src.Commit != "" {
		dst.Commit = src.Commit
	}
	dst.Sessions++
}

// sessionRepoCandidates walks this project's sessions and subagents in focused
// work episodes: standing user rules typed by the person, gave-up reversals,
// and episode conclusions, binding each to the repository files edited in that
// episode window.
func sessionRepoCandidates(dir, root string, inScope func(string) bool, allowed func(string, string) bool, add func(repoCandidate)) error {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return err
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	pick := map[string]index.SessionMeta{}
	revoked := map[string]bool{}
	metaFiles := map[string][]string{}
	for _, m := range metas {
		if m.Harness == "deja" || !inScope(m.Project) || !allowed(m.Path, m.Project) {
			continue
		}
		for _, r := range m.Revokes {
			revoked[strings.ToLower(strings.TrimSpace(r))] = true
		}
	}
	recentBudget := 25
	for _, m := range metas {
		if m.Harness == "deja" || !inScope(m.Project) || !allowed(m.Path, m.Project) {
			continue
		}
		isSub := m.IsSpawned() || sources.IsSubagentPath(m.Path)
		var touchedInRepo []string
		for _, raw := range m.Touched {
			if rel := cleanRepoRelFile(root, raw); rel != "" {
				touchedInRepo = append(touchedInRepo, rel)
			}
		}
		touchedInRepo = sources.CleanRepoFiles(touchedInRepo)
		if isSub && len(touchedInRepo) == 0 {
			continue
		}
		key := m.Key()
		if len(touchedInRepo) <= 4 {
			metaFiles[key] = touchedInRepo
		}
		hasSignal := m.GaveUp || len(m.Rules) > 0 || strings.TrimSpace(m.Settled) != ""
		if hasSignal || recentBudget > 0 {
			pick[key] = m
			if !hasSignal {
				recentBudget--
			}
		}
	}
	if len(pick) == 0 {
		return nil
	}
	states := map[string]*repoEpState{}
	err = index.EachRecordInRoles(dir, []string{"user", "assistant", "edit"}, func(meta index.SessionMeta, r index.Record) {
		key := meta.Key()
		if _, ok := pick[key]; !ok {
			return
		}
		st := states[key]
		if st == nil {
			st = &repoEpState{}
			states[key] = st
		}
		if !r.Time.IsZero() {
			st.lastTime = r.Time
		}
		switch r.Role {
		case "edit":
			if rel := cleanRepoRelFile(root, r.Text); rel != "" {
				at := r.Time
				if at.IsZero() {
					at = st.lastTime
				}
				st.edits = append(st.edits, repoEditEvent{at: at, file: rel})
			}
			return
		case "user", "assistant":
			st.msgs = append(st.msgs, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
			st.editEnds = append(st.editEnds, len(st.edits))
		}
		if meta.GaveUp {
			emitGiveUpLines(meta, r, episodeFilesNear(st.edits, r.Time, metaFiles[key]), add)
		}
	})
	if err != nil {
		return err
	}
	for _, m := range metas {
		key := m.Key()
		if _, ok := pick[key]; !ok {
			continue
		}
		emitSessionConclusions(m, states[key], metaFiles[key], revoked, add)
	}
	return nil
}

type repoEditEvent struct {
	at   time.Time
	file string
}

type repoEpState struct {
	msgs     []model.Message
	editEnds []int
	edits    []repoEditEvent
	lastTime time.Time
}

func emitGiveUpLines(meta index.SessionMeta, r index.Record, files []string, add func(repoCandidate)) {
	for line := range strings.SplitSeq(r.Text, "\n") {
		l, ok := index.GiveUpLine(line)
		if !ok || strings.Contains(strings.ToLower(l), "deja") {
			continue
		}
		clean := cleanRepoCandidateLine(l)
		if !usableRepoCandidateLine(clean) {
			continue
		}
		add(repoCandidate{Kind: "rejected", Text: clean, Files: files, From: repoFrom(meta.Harness, r.Time), Source: "session " + shortID(meta.ID), Session: meta.ID, tier: 1, at: r.Time})
	}
}

func emitSessionConclusions(m index.SessionMeta, st *repoEpState, fallbackFiles []string, revoked map[string]bool, add func(repoCandidate)) {
	isSub := m.IsSpawned() || sources.IsSubagentPath(m.Path)
	var edits []repoEditEvent
	var msgs []model.Message
	var editEnds []int
	if st != nil {
		edits = st.edits
		msgs = st.msgs
		editEnds = st.editEnds
	}
	if !isSub {
		for _, rule := range m.Rules {
			r := cleanRepoCandidateLine(rule)
			if r == "" || revoked[strings.ToLower(r)] || !usableRepoCandidateLine(r) {
				continue
			}
			at, start, end := msgTimeAndEditsForText(msgs, editEnds, r, m.Updated, len(edits))
			files := episodeFilesForText(r, edits, start, end, at, fallbackFiles)
			add(repoCandidate{Kind: inferRepoKind(r), Text: trimRepoCandidateText(r), Files: files, From: repoFrom(m.Harness, at), Source: "user rule", Session: m.ID, tier: 0, at: at})
		}
		for i := 1; i < len(msgs); i++ {
			if msgs[i].Role != "user" || msgs[i-1].Role != "assistant" {
				continue
			}
			raw := strings.TrimSpace(msgs[i].Text)
			if raw == "" || strings.HasPrefix(raw, "!") || pseudoUserRe.MatchString(raw) || !isStandingCorrection(raw) {
				continue
			}
			c := cleanRepoCandidateLine(raw)
			if c == "" || len([]rune(c)) > 260 || revoked[strings.ToLower(c)] || !usableRepoCandidateLine(c) {
				continue
			}
			at := msgs[i].Time
			if at.IsZero() {
				at = m.Updated
			}
			end := len(edits)
			if i < len(editEnds) {
				end = editEnds[i]
			}
			start := 0
			if i-1 < len(editEnds) {
				start = editEnds[i-1]
			}
			files := episodeFilesForText(c, edits, start, end, at, fallbackFiles)
			add(repoCandidate{Kind: "gotcha", Text: trimRepoCandidateText(c), Files: files, From: repoFrom(m.Harness, at), Source: "user correction", Session: m.ID, tier: 0, at: at})
		}
	}
	if d := cleanRepoCandidateLine(m.Settled); d != "" && (!strings.HasSuffix(d, "…") || len(msgs) == 0) && digest.CarriesDecision(d) && usableRepoCandidateLine(d) {
		at, start, end := msgTimeAndEditsForText(msgs, editEnds, d, m.Updated, len(edits))
		files := episodeFilesForText(d, edits, start, end, at, fallbackFiles)
		add(repoCandidate{Kind: "decision", Text: trimRepoCandidateText(d), Files: files, From: repoFrom(m.Harness, at), Source: "session " + shortID(m.ID), Session: m.ID, tier: 1, at: at})
		return
	}
	if len(msgs) == 0 {
		return
	}
	s := model.Session{ID: m.ID, Harness: m.Harness, Project: m.Project, Messages: msgs}
	for _, d := range digest.SubstantialConclusions(s, promoteDecisionBudget, 2) {
		if d = cleanRepoCandidateLine(d); d != "" && digest.CarriesDecision(d) && usableRepoCandidateLine(d) {
			at, start, end := msgTimeAndEditsForText(msgs, editEnds, d, m.Updated, len(edits))
			files := episodeFilesForText(d, edits, start, end, at, fallbackFiles)
			add(repoCandidate{Kind: "decision", Text: trimRepoCandidateText(d), Files: files, From: repoFrom(m.Harness, at), Source: "session " + shortID(m.ID), Session: m.ID, tier: 1, at: at})
		}
	}
}

func msgTimeAndEditsForText(msgs []model.Message, editEnds []int, text string, fallbackTime time.Time, fallbackEnd int) (time.Time, int, int) {
	r := []rune(strings.TrimSpace(text))
	if len(r) > 40 {
		r = r[:40]
	}
	probe := strings.ToLower(string(r))
	if probe == "" {
		return fallbackTime, 0, fallbackEnd
	}
	for i, msg := range slices.Backward(msgs) {
		if strings.Contains(strings.ToLower(cleanRepoCandidateLine(msg.Text)), probe) {
			at := msg.Time
			if at.IsZero() {
				at = fallbackTime
			}
			end := fallbackEnd
			if i < len(editEnds) {
				end = editEnds[i]
			}
			start := 0
			for u := i - 1; u >= 0; u-- {
				if msgs[u].Role == "user" {
					if u < len(editEnds) {
						start = editEnds[u]
					}
					break
				}
			}
			return at, start, end
		}
	}
	return fallbackTime, 0, fallbackEnd
}

func episodeFilesForText(text string, edits []repoEditEvent, start, end int, at time.Time, fallback []string) []string {
	if mentioned := mentionedRepoFiles(text, edits[:end], fallback); len(mentioned) > 0 {
		return mentioned
	}
	if start > 0 && start < end {
		if turnFiles := episodeFilesNear(edits[start:end], at, nil); len(turnFiles) > 0 {
			return turnFiles
		}
	}
	return episodeFilesNear(edits[:end], at, fallback)
}

func mentionedRepoFiles(text string, edits []repoEditEvent, fallback []string) []string {
	low := strings.ToLower(text)
	var pool []string
	for _, e := range edits {
		if e.file != "" && !slices.Contains(pool, e.file) {
			pool = append(pool, e.file)
		}
	}
	for _, f := range fallback {
		if f != "" && !slices.Contains(pool, f) {
			pool = append(pool, f)
		}
	}
	var exact []string
	for _, f := range pool {
		if strings.Contains(low, strings.ToLower(f)) {
			exact = append(exact, f)
		}
	}
	if len(exact) > 0 {
		return sources.CleanRepoFiles(exact[:min(len(exact), 4)])
	}
	byBase := map[string][]string{}
	for _, f := range pool {
		base := strings.ToLower(filepath.Base(f))
		if strings.Contains(base, ".") {
			byBase[base] = append(byBase[base], f)
		}
	}
	var baseHits []string
	for base, files := range byBase {
		if len(files) == 1 && strings.Contains(low, base) {
			baseHits = append(baseHits, files[0])
		}
	}
	if len(baseHits) > 0 {
		slices.Sort(baseHits)
		return sources.CleanRepoFiles(baseHits[:min(len(baseHits), 4)])
	}
	return nil
}

func trimRepoCandidateText(s string) string {
	const maxRunes = 300
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	for i := maxRunes; i >= maxRunes/2; i-- {
		if i < len(r) && (r[i-1] == '.' || r[i-1] == '!' || r[i-1] == '?') && (i == len(r) || r[i] == ' ') {
			return strings.TrimSpace(string(r[:i]))
		}
	}
	cut := maxRunes - 1
	for i := cut; i >= max(0, cut-60); i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	trimmed := strings.TrimRight(string(r[:cut]), " \t.,;:(—-")
	return trimmed + "…"
}

func episodeFilesNear(edits []repoEditEvent, at time.Time, fallback []string) []string {
	if len(edits) == 0 {
		return fallback
	}
	var out []string
	for i := len(edits) - 1; i >= 0 && len(out) < 4; i-- {
		ev := edits[i]
		if !at.IsZero() && !ev.at.IsZero() {
			d := at.Sub(ev.at)
			if d < -2*time.Minute || d > 30*time.Minute {
				continue
			}
		}
		if !slices.Contains(out, ev.file) {
			out = append(out, ev.file)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return sources.CleanRepoFiles(out)
}

func cleanRepoRelFile(root, raw string) string {
	if root == "" || raw == "" {
		return ""
	}
	line, _, _ := strings.Cut(raw, "\n")
	p := strings.TrimSpace(line)
	p = strings.TrimPrefix(p, "file://")
	if p == "" {
		return ""
	}
	slash := filepath.ToSlash(p)
	for _, marker := range []string{"/.claude/worktrees/", "/.git/worktrees/"} {
		if idx := strings.Index(slash, marker); idx >= 0 {
			rest := slash[idx+len(marker):]
			if _, after, ok := strings.Cut(rest, "/"); ok {
				slash = after
			}
		}
	}
	rootSlash := strings.TrimSuffix(filepath.ToSlash(root), "/")
	if after, ok := strings.CutPrefix(slash, rootSlash+"/"); ok {
		slash = after
	} else if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return ""
		}
		slash = filepath.ToSlash(rel)
	}
	slash = strings.TrimPrefix(slash, "./")
	if slash == "" || slash == "." || strings.HasPrefix(slash, "/") || strings.HasPrefix(slash, "..") || strings.HasPrefix(slash, ".git/") || strings.HasPrefix(slash, ".deja/") || strings.HasPrefix(slash, ".claude/") || strings.HasPrefix(slash, "deja-vu-loop/") {
		return ""
	}
	if resolved := orientRepoTree(root).resolveRel(slash); resolved != "" {
		return resolved
	}
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(slash)))
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return slash
}

func enrichRepoCandidateFilesAndCommits(dir, root string, cands []repoCandidate) {
	if root == "" || len(cands) == 0 {
		return
	}
	fileSessions := map[string]int{}
	var knownFiles []string
	if metas, err := index.AllMeta(dir); err == nil {
		seenFile := map[string]bool{}
		for _, m := range metas {
			if m.Harness == "deja" {
				continue
			}
			perSession := map[string]bool{}
			for _, raw := range m.Touched {
				rel := cleanRepoRelFile(root, orientPath(raw, root))
				if rel == "" {
					continue
				}
				perSession[rel] = true
				if !seenFile[rel] {
					seenFile[rel] = true
					knownFiles = append(knownFiles, rel)
				}
			}
			for rel := range perSession {
				fileSessions[rel]++
			}
		}
	}
	commits := recentRepoGitCommits(root)
	for i := range cands {
		var resolved []string
		for _, f := range cands[i].Files {
			if rel := cleanRepoRelFile(root, f); rel != "" {
				resolved = append(resolved, rel)
			} else {
				resolved = append(resolved, f)
			}
		}
		cands[i].Files = sources.CleanRepoFiles(append(resolved, extractMentionedRepoFiles(root, cands[i].Text, knownFiles)...))
		maxSess := cands[i].Sessions
		for _, f := range cands[i].Files {
			if n := fileSessions[f]; n > maxSess {
				maxSess = n
			}
		}
		if maxSess > 0 {
			cands[i].Sessions = maxSess
		}
		if cands[i].Commit == "" && len(cands[i].Files) > 0 && len(commits) > 0 {
			cands[i].Commit = matchRepoCommit(cands[i].Files, cands[i].at, commits)
		}
	}
}

func extractMentionedRepoFiles(root, text string, knownFiles []string) []string {
	if root == "" || text == "" {
		return nil
	}
	low := strings.ToLower(text)
	var out []string
	if strings.Contains(low, ".deja/") || strings.Contains(low, " .deja ") || strings.HasSuffix(low, " .deja") {
		out = append(out, ".deja")
	}
	baseCounts := map[string]int{}
	for _, f := range knownFiles {
		baseCounts[strings.ToLower(filepath.Base(f))]++
	}
	for _, f := range knownFiles {
		if strings.Contains(low, strings.ToLower(f)) {
			out = append(out, f)
			continue
		}
		base := strings.ToLower(filepath.Base(f))
		if len(base) >= 6 && baseCounts[base] == 1 && strings.ContainsRune(base, '.') && strings.Contains(low, base) {
			out = append(out, f)
		}
	}
	for tok := range strings.FieldsSeq(text) {
		tok = strings.Trim(tok, "`\"'(),;:[]{}<>")
		if len(tok) < 5 || !strings.ContainsRune(tok, '.') || strings.HasPrefix(tok, "http") {
			continue
		}
		if rel := cleanRepoRelFile(root, tok); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

type repoGitCommit struct {
	sha   string
	at    time.Time
	files map[string]bool
}

func recentRepoGitCommits(root string) []repoGitCommit {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := gitread.Command(ctx, "-C", root, "log", "-n", "500", "--format=COMMIT:%h|%ct", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var res []repoGitCommit
	var cur *repoGitCommit
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if after, ok := strings.CutPrefix(line, "COMMIT:"); ok {
			sha, tsStr, ok := strings.Cut(after, "|")
			if !ok {
				continue
			}
			sec, convErr := strconv.ParseInt(tsStr, 10, 64)
			if convErr != nil {
				continue
			}
			res = append(res, repoGitCommit{sha: sources.CleanRepoCommit(sha), at: time.Unix(sec, 0), files: map[string]bool{}})
			cur = &res[len(res)-1]
			continue
		}
		if cur != nil {
			cur.files[filepath.ToSlash(line)] = true
		}
	}
	return res
}

func matchRepoCommit(files []string, at time.Time, commits []repoGitCommit) string {
	if at.IsZero() {
		return ""
	}
	bestSHA := ""
	bestDiff := 48 * time.Hour
	for _, c := range commits {
		if c.sha == "" {
			continue
		}
		matched := false
		for _, f := range files {
			if c.files[f] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		d := c.at.Sub(at)
		if d < -2*time.Hour || d > 48*time.Hour {
			continue
		}
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			bestDiff = d
			bestSHA = c.sha
		}
	}
	return bestSHA
}

func isStandingCorrection(s string) bool {
	if !isCorrection(s) {
		return false
	}
	if strings.Contains(s, "\n") || len([]rune(s)) > 220 {
		return false
	}
	low := strings.ToLower(strings.TrimSpace(s))
	for _, chatty := range []string{
		"давай ", "надо еще копать", "надо ещё копать", "а почему у нас",
		"я только захожу в диалог", "1. ", "2. ", "пока не надо", "не надо такое пока",
		"не надо тогда пока", "пока что", "ок, без ", "круто получилось",
		"ты слишком длинно пишешь", "в 4689 и в остальных", "блять",
	} {
		if strings.Contains(low, chatty) {
			return false
		}
	}
	hasSignal := false
	for _, signal := range []string{
		"не надо", "не делай", "не пушь", "не трогай", "не создавай", "не удаляй",
		"никогда", "всегда ", "пусть будет", "стоп,", "стоп ", "нельзя ",
		"never ", "do not ", "don't ", "must not ", "always ", "stop ", "instead of ",
	} {
		if strings.Contains(low, signal) {
			hasSignal = true
			break
		}
	}
	if !hasSignal {
		return false
	}
	for _, anchor := range []string{
		".deja", ".git", ".go", ".md", ".html", ".json", ".yaml", ".yml", ".sh", ".py", ".ts", ".js",
		"git ", "пушить", "пуш", "коммит", "commit", "push", "merge", "pr ", "ветк", "branch",
		"тест", "test", "билд", "build", "файл", "file", "папк", "dir", "repo", "репо",
		"readme", "changelog", "config", "конфиг", "флаг", "flag", "хук", "hook", "mcp",
	} {
		if strings.Contains(low, anchor) {
			return true
		}
	}
	return false
}

func cleanRepoCandidateLine(s string) string {
	s = stripMarkdownLinks(s)
	s = strings.ReplaceAll(s, "**", "")
	s = strings.TrimLeft(s, "#*->• \t")
	for len(s) > 2 && s[0] >= '0' && s[0] <= '9' && (s[1] == '.' || s[1] == ')') {
		s = strings.TrimSpace(s[2:])
	}
	return strings.Trim(s, "#*->• \t")
}

func stripMarkdownLinks(s string) string {
	for _, scheme := range []string{"](file://", "](https://", "](http://"} {
		for {
			idx := strings.Index(s, scheme)
			if idx < 0 {
				break
			}
			open := strings.LastIndex(s[:idx], "[")
			end := strings.Index(s[idx+2:], ")")
			if open < 0 || end < 0 {
				break
			}
			label := s[open+1 : idx]
			s = s[:open] + label + s[idx+2+end+1:]
		}
	}
	return s
}

func usableRepoCandidateLine(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 45 {
		return false
	}
	if strings.HasSuffix(s, "?") || strings.HasSuffix(s, ":") || strings.Contains(s, " | ") || strings.HasPrefix(s, "|") || strings.HasPrefix(s, "#") || strings.Contains(s, "```") || strings.Contains(s, " ### ") || strings.Contains(s, " ## ") || strings.Count(s, " - ") >= 2 {
		return false
	}
	low := strings.ToLower(s)
	for _, noise := range []string{
		"о чем речь",
		"откатывать ли ",
		"откатывать нечего",
		"временно откатыва",
		"проверяю, что новый тест",
		"temporarily revert",
		"should we revert",
		"сводка того, что",
		"локальный бинарник пересобран",
		"откатил ветку ",
		"откатил feat/",
		"ветка удалена",
		"полностью закрыт:",
		"вмержен (",
		"вмержен в main",
		"смержен в main",
		"запустил третьего агента",
		"тесты:",
		"коммит запушен",
		"все тесты зелёные",
		"все тесты зеленые",
		"all tests pass",
		"если и там ноль, фичу откатываю",
		"дальше по списку:",
		"отрицательное знание",
		"git checkout` на ",
		"жду ревьюера",
		"читаю текущий",
		"баг найден, но не починен",
		"сборка `changelog.md`",
		"сборка changelog.md",
		"i'm validating",
		"i’m validating",
		"i'm tracing",
		"i’m tracing",
		"i'll inspect",
		"i’ll inspect",
		"i've sent",
		"i’ve sent",
		"both prs are merged",
		"needs your ok",
		"мои прямые тесты",
		"мой `-inline-answer`",
		"не шиплю блоат",
		"откатываю неверную",
		"откатываю всё",
		"откатываю —",
		"варианты: оставляем",
		"или откатываю коммит",
		"ревьюер прав по",
		"пока идёт ревью",
		"пока идет ревью",
		"will detect it. ✓",
		"итого за ночь",
		"coverage-гейте",
		"ребейз завершён",
		"ребейз завершен",
		"перехожу на main",
		"проверяю, смержилась",
		"вставленный goroutine-трейс",
		"deja-vu-loop",
		"свежий срез по всем",
		"три новых мержа",
		"issuecomment-",
		"что лежит в `.deja/`",
		"что лежит в .deja/",
		"вариант b (",
		"ты сейчас попал",
		"проверил две идеи",
		"займусь следующей",
		"сначала коммичу",
		"один ревьюер",
		"осталось на ",
		"опубликован ответ",
		"вмержены в main",
		"смержены в main",
		"fixed on fix/",
		"fixed on feat/",
		"missing coverage for",
		"the report is correct",
		"remains unverified because",
		"описана причина",
		"оставлено ревью",
		"автоматически смержен",
		"прошёл все ",
		"прошел все ",
		"тестовых шардов",
		" — было ",
		": - ",
		" 2. ",
		"поставил его на",
		"will be automatically merged",
		"результаты глубокого инженерного",
		"в /tmp/",
		"в `/tmp/",
		"причина спада в том",
		"ветка уже запушена",
		"чего не могу проверить локально",
		"было c,",
		"было c−",
		"стало a",
		"до → после",
		"ниже — что",
		"хочешь, чтобы я",
		"что уже работает",
		"где магии сейчас нет",
	} {
		if strings.Contains(low, noise) {
			return false
		}
	}
	for _, prefix := range []string{
		"pr #",
		"issue #",
		"draft pr #",
		"pull request #",
		"closed pr #",
		"closes #",
		"you asked whether ",
		"no benchmarks were ",
		"before the fix, ",
		"i would first ",
		"починил (",
		"починил сразу",
		"поправку оставил в ",
		"релиз 0.",
		"завёл ",
		"завел ",
		"откатил ",
		"откатил:",
		"откатил.",
		"merged pr ",
		"opened pr ",
		"opened issue ",
		"открыл issue ",
		"открыл pr ",
		"сразу закрываем",
		"сначала:",
		"а теперь —",
		"добавлю ",
		"добавляю ",
		"теперь добавлю ",
		"возвращаюсь,",
		"промах — я уже",
	} {
		if strings.HasPrefix(low, prefix) {
			return false
		}
	}
	return true
}

func repoCandidatePrefixKey(kind, text string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(cleanRepoCandidateLine(text)), " "))
	if idx := strings.LastIndex(norm, " ("); idx > 20 && strings.HasSuffix(norm, "sessions)") {
		norm = strings.TrimSpace(norm[:idx])
	}
	r := []rune(norm)
	if len(r) < 35 {
		return ""
	}
	if len(r) > 65 {
		r = r[:65]
	}
	return kind + ":" + string(r)
}

// repoFrom is the provenance an entry carries into the repository: the agent
// and the day, which a teammate can read, and nothing that names this machine.
func repoFrom(harness string, t time.Time) string {
	harness = strings.TrimSpace(harness)
	if t.IsZero() {
		return harness
	}
	day := t.UTC().Format("2006-01-02")
	if harness == "" {
		return day
	}
	return harness + " " + day
}
