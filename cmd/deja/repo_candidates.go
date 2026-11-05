package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
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
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	From    string `json:"from,omitempty"`
	Source  string `json:"source"`
	Session string `json:"session,omitempty"`
	InFile  bool   `json:"in_file,omitempty"`
	Refused string `json:"refused,omitempty"`
	tier    int
	at      time.Time
}

func runRepoCandidates(dir string, w io.Writer, args []string) error {
	asJSON := false
	limit := 30
	var since time.Duration
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			if i+1 >= len(args) {
				return errors.New("repo candidates: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("repo candidates: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--since":
			if i+1 >= len(args) {
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
		from := c.Source
		if c.From != "" {
			from += ", " + c.From
		}
		fmt.Fprintf(w, "%2d. %s %-8s %s  (%s)%s\n", i+1, c.ID, c.Kind, search.SafeLine(c.Text), search.SafeLine(from), mark)
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
		if strings.HasPrefix(s.ID, "deja-note-") || !inScope(s.Project) || !allowed(s.Path, s.Project) {
			continue
		}
		for _, m := range s.Messages {
			if len([]rune(strings.TrimSpace(m.Text))) < 25 {
				continue
			}
			add(repoCandidate{Kind: inferRepoKind(m.Text), Text: m.Text, From: repoFrom("deja", m.Time), Source: "your note", tier: 0, at: m.Time})
		}
	}
	if err := sessionRepoCandidates(dir, inScope, allowed, add); err != nil {
		return nil, err
	}
	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	in := map[string]bool{}
	inPrefix := map[string]bool{}
	if entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root)); err == nil {
		for _, e := range entries {
			in[e.ID] = true
			if pkey := repoCandidatePrefixKey(e.Kind, e.Text); pkey != "" {
				inPrefix[pkey] = true
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].tier != out[j].tier {
			return out[i].tier < out[j].tier
		}
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.After(out[j].at)
		}
		return len(out[i].Text) > len(out[j].Text)
	})
	seen := map[string]bool{}
	seenPrefix := map[string]bool{}
	kept := out[:0]
	for _, c := range out {
		pkey := repoCandidatePrefixKey(c.Kind, c.Text)
		if seen[c.ID] || (pkey != "" && seenPrefix[pkey]) || (!cutoff.IsZero() && c.at.Before(cutoff)) {
			continue
		}
		seen[c.ID] = true
		if pkey != "" {
			seenPrefix[pkey] = true
		}
		c.InFile = in[c.ID] || (pkey != "" && inPrefix[pkey])
		kept = append(kept, c)
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	return kept, nil
}

// sessionRepoCandidates reads this project's sessions: each reversal line said
// by the user or the agent, and each session's settled conclusion when it
// carries a decision.
// sessionRepoCandidates walks this project's sessions: standing user rules
// typed by the person, gave-up sessions for the line that names the reversal,
// and each session's settled conclusion when it carries a durable decision.
func sessionRepoCandidates(dir string, inScope func(string) bool, allowed func(string, string) bool, add func(repoCandidate)) error {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return err
	}
	pick := map[string]index.SessionMeta{}
	revoked := map[string]bool{}
	for _, m := range metas {
		if m.Harness == "deja" || m.Kind != "" || sources.IsSubagentPath(m.Path) || !inScope(m.Project) || !allowed(m.Path, m.Project) {
			continue
		}
		for _, r := range m.Revokes {
			revoked[strings.ToLower(strings.TrimSpace(r))] = true
		}
	}
	for _, m := range metas {
		if m.Harness == "deja" || m.Kind != "" || sources.IsSubagentPath(m.Path) || !inScope(m.Project) || !allowed(m.Path, m.Project) {
			continue
		}
		key := m.Harness + ":" + m.ID
		pick[key] = m
		for _, rule := range m.Rules {
			r := cleanRepoCandidateLine(rule)
			if r == "" || revoked[strings.ToLower(r)] || !usableRepoCandidateLine(r) {
				continue
			}
			add(repoCandidate{Kind: inferRepoKind(r), Text: squeezeTrim(r, 300), From: repoFrom(m.Harness, m.Updated), Source: "user rule", Session: m.ID, tier: 0, at: m.Updated})
		}
		if d := strings.TrimSpace(m.Settled); d != "" && digest.CarriesDecision(d) && usableRepoCandidateLine(d) {
			add(repoCandidate{Kind: "decision", Text: squeezeTrim(d, 300), From: repoFrom(m.Harness, m.Updated), Source: "session " + shortID(m.ID), Session: m.ID, tier: 1, at: m.Updated})
		}
	}
	if len(pick) == 0 {
		return nil
	}
	// The agent's turns, kept per session for the conclusion read below; only
	// this project's sessions, so the walk holds little whatever the store is.
	said := map[string]*model.Session{}
	err = index.EachRecordInRoles(dir, []string{"user", "assistant"}, func(meta index.SessionMeta, r index.Record) {
		key := meta.Harness + ":" + meta.ID
		if _, ok := pick[key]; !ok {
			return
		}
		if r.Role == "assistant" {
			s := said[key]
			if s == nil {
				s = &model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project}
				said[key] = s
			}
			s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
		if !meta.GaveUp {
			return
		}
		for line := range strings.SplitSeq(r.Text, "\n") {
			l, ok := index.GiveUpLine(line)
			if !ok || strings.Contains(strings.ToLower(l), "deja") {
				continue
			}
			clean := cleanRepoCandidateLine(l)
			if !usableRepoCandidateLine(clean) {
				continue
			}
			add(repoCandidate{Kind: "rejected", Text: clean, From: repoFrom(meta.Harness, r.Time), Source: "session " + shortID(meta.ID), Session: meta.ID, tier: 1, at: r.Time})
		}
	})
	if err != nil {
		return err
	}
	for key, s := range said {
		m := pick[key]
		for _, d := range digest.SubstantialConclusions(*s, promoteDecisionBudget, 2) {
			if d = strings.TrimSpace(d); d != "" && digest.CarriesDecision(d) && usableRepoCandidateLine(d) {
				add(repoCandidate{Kind: "decision", Text: squeezeTrim(d, 300), From: repoFrom(m.Harness, m.Updated), Source: "session " + shortID(m.ID), Session: m.ID, tier: 1, at: m.Updated})
			}
		}
	}
	return nil
}

func cleanRepoCandidateLine(s string) string {
	s = strings.Trim(s, "*->• \t")
	for len(s) > 2 && s[0] >= '0' && s[0] <= '9' && (s[1] == '.' || s[1] == ')') {
		s = strings.TrimSpace(s[2:])
	}
	return strings.Trim(s, "*->• \t")
}

func usableRepoCandidateLine(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 35 {
		return false
	}
	if strings.HasSuffix(s, "?") || strings.HasSuffix(s, ":") || strings.Contains(s, " | ") || strings.HasPrefix(s, "|") {
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
		"тесты:**",
		"коммит запушен",
		"все тесты зелёные",
		"все тесты зеленые",
		"all tests pass",
		"если и там ноль, фичу откатываю",
		"дальше по списку:",
		"отрицательное знание",
		"git checkout` на ",
	} {
		if strings.Contains(low, noise) {
			return false
		}
	}
	for _, prefix := range []string{
		"починил (",
		"релиз 0.",
		"завёл ",
		"завел ",
		"откатил ",
		"откатил:",
		"откатил.",
		"merged pr ",
		"opened pr ",
	} {
		if strings.HasPrefix(low, prefix) {
			return false
		}
	}
	return true
}

func repoCandidatePrefixKey(kind, text string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(cleanRepoCandidateLine(text)), " "))
	r := []rune(norm)
	if len(r) < 40 {
		return ""
	}
	if len(r) > 65 {
		r = r[:65]
	}
	return kind + "\x00" + string(r)
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
