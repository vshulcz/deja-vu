package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/vault"
)

// rememberCandidatesFile stores auto-extracted candidate gotchas and rules
// inside the sealed index directory without touching project repos or notes.
const rememberCandidatesFile = "remember_candidates.jsonl"

// maxRememberCandidates bounds the sealed remember_candidates.jsonl sidecar.
const maxRememberCandidates = 200

// storedRememberCandidate persists one session-end extracted rule, reversal, or
// confirmed fix-pair candidate in the sealed sidecar store.
type storedRememberCandidate struct {
	ID       string    `json:"id"`
	Project  string    `json:"project"`
	Kind     string    `json:"kind"`
	Text     string    `json:"text"`
	Files    []string  `json:"files,omitempty"`
	Sessions int       `json:"sessions,omitempty"`
	From     string    `json:"from,omitempty"`
	Source   string    `json:"source"`
	Session  string    `json:"session,omitempty"`
	At       time.Time `json:"at"`
}

// rememberCandidatesPath returns the sealed sidecar file path under dir.
func rememberCandidatesPath(dir string) string {
	return filepath.Join(dir, rememberCandidatesFile)
}

// readRememberCandidates loads sealed session-end candidates from dir, returning
// nil when the sidecar does not exist or the vault is locked.
func readRememberCandidates(dir string) []storedRememberCandidate {
	if dir == "" {
		return nil
	}
	raw, err := vault.ReadSealedFile(dir, rememberCandidatesPath(dir), vault.DomainSidecar)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var out []storedRememberCandidate
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var item storedRememberCandidate
		if json.Unmarshal(line, &item) != nil || strings.TrimSpace(item.Text) == "" || strings.TrimSpace(item.Kind) == "" {
			continue
		}
		if item.ID == "" {
			item.ID = sources.RepoEntryID(item.Kind, item.Text)
		}
		out = append(out, item)
	}
	return out
}

// writeRememberCandidates seals up to maxRememberCandidates newest-first rows
// into dir/remember_candidates.jsonl.
func writeRememberCandidates(dir string, rows []storedRememberCandidate) {
	if dir == "" {
		return
	}
	if _, err := vault.CheckUnlocked(dir); err != nil {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.After(rows[j].At)
		}
		if rows[i].Sessions != rows[j].Sessions {
			return rows[i].Sessions > rows[j].Sessions
		}
		return rows[i].ID < rows[j].ID
	})
	if len(rows) > maxRememberCandidates {
		rows = rows[:maxRememberCandidates]
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return
		}
	}
	_ = vault.WriteSealedFile(dir, rememberCandidatesPath(dir), vault.DomainSidecar, buf.Bytes(), 0o600)
}

// captureSessionEndCandidates extracts confirmed fix pairs, standing user rules,
// and episode reversals for cwd during detached session-end refresh and stores
// them in the sealed sidecar without creating repo files or global notes.
func captureSessionEndCandidates(dir, cwd string) {
	if dir == "" || strings.TrimSpace(cwd) == "" || redact.Disabled() {
		return
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = filepath.Clean(cwd)
	}
	proj := sources.ClaudeProjectName(root)
	if proj == "" || sources.ExcludedProject(proj) {
		return
	}
	pol := policy.Load()
	if !pol.Allows(policy.ActivationAuto, proj) {
		return
	}
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
	allowed := func(path, project string) bool {
		return pol.Allows(policy.ActivationAuto, project) && !pol.Ignored(path, project) && !sources.ExcludedProject(project)
	}

	var incoming []storedRememberCandidate
	for _, p := range index.ReadFixes(dir) {
		if cand, ok := confirmedFixCandidate(root, proj, p, inScope, allowed); ok {
			incoming = append(incoming, cand)
		}
	}
	_ = sessionRepoCandidates(dir, root, inScope, allowed, func(c repoCandidate) {
		if c.Source != "user rule" && c.Source != "user correction" && c.Kind != "rejected" {
			return
		}
		safe, _, serr := repoSafeText(c.Text)
		if serr != nil {
			return
		}
		sessions := max(c.Sessions, 1)
		at := c.at
		if at.IsZero() {
			at = time.Now().UTC()
		}
		incoming = append(incoming, storedRememberCandidate{
			ID:       sources.RepoEntryID(c.Kind, safe),
			Project:  proj,
			Kind:     c.Kind,
			Text:     safe,
			Files:    sources.CleanRepoFiles(c.Files),
			Sessions: sessions,
			From:     c.From,
			Source:   c.Source,
			Session:  c.Session,
			At:       at,
		})
	})

	sessByID := map[string]map[string]bool{}
	for _, r := range incoming {
		if r.Session == "" {
			continue
		}
		if sessByID[r.ID] == nil {
			sessByID[r.ID] = map[string]bool{}
		}
		sessByID[r.ID][r.Session] = true
	}
	for i := range incoming {
		incoming[i].Sessions = max(incoming[i].Sessions, len(sessByID[incoming[i].ID]))
	}

	existing := readRememberCandidates(dir)
	merged := mergeRememberCandidates(existing, incoming)
	if len(merged) == 0 {
		return
	}
	writeRememberCandidates(dir, merged)
}

// confirmedFixCandidate builds a repo-safe gotcha candidate from a confirmed,
// non-viewer FixPair belonging to the current project scope.
func confirmedFixCandidate(
	root, proj string,
	p index.FixPair,
	inScope func(string) bool,
	allowed func(string, string) bool,
) (storedRememberCandidate, bool) {
	if p.Candidate || index.IsViewerFixPair(p) || p.Error == "" || !inScope(p.Project) || !allowed("", p.Project) {
		return storedRememberCandidate{}, false
	}
	if isUnitTestFailureLine(p.Error) || orientSkipFixError(p.Error) {
		return storedRememberCandidate{}, false
	}
	rawCmd, ok := withoutFailedExit(strings.TrimPrefix(strings.TrimSpace(search.SafeText(p.Command)), "$ "))
	if !ok || rawCmd == "" {
		return storedRememberCandidate{}, false
	}
	cleanCmd := strings.TrimSpace(orientCommand(rawCmd))
	if cleanCmd == "" || orientEphemeralCommand(cleanCmd) || orientDestructiveRemedy(cleanCmd) {
		return storedRememberCandidate{}, false
	}
	errLine := squeezeTrim(search.SafeLine(p.Error), 90)
	if errLine == "" {
		return storedRememberCandidate{}, false
	}
	rawText := fmt.Sprintf("When encountering %q, resolve with %#q", errLine, cleanCmd)
	safe, _, err := repoSafeText(rawText)
	if err != nil || !usableRepoCandidateLine(safe) {
		return storedRememberCandidate{}, false
	}
	var files []string
	if rel := candidateErrorRelFile(root, p.Edit, p.Error); rel != "" {
		files = []string{rel}
	}
	sessions := 2
	if p.Repaired || p.SelfCorrected || p.Substitute {
		sessions = 1
	}
	at := p.When
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return storedRememberCandidate{
		ID:       sources.RepoEntryID("gotcha", safe),
		Project:  proj,
		Kind:     "gotcha",
		Text:     safe,
		Files:    files,
		Sessions: sessions,
		From:     repoFrom("deja", at),
		Source:   "session-end fix",
		At:       at,
	}, true
}

// candidateErrorRelFile extracts a clean repo-relative file path from either a
// recorded edit target or a leading `path:line` token in errText.
func candidateErrorRelFile(root, edit, errText string) string {
	if rel := cleanRepoRelFile(root, edit); rel != "" {
		return rel
	}
	fields := strings.Fields(errText)
	if len(fields) == 0 {
		return ""
	}
	tok := strings.Trim(fields[0], "\"'():,")
	if idx := strings.IndexByte(tok, ':'); idx > 1 {
		tok = tok[:idx]
	}
	rel := cleanRepoRelFile(root, tok)
	if rel == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		return rel
	}
	return ""
}

// mergeRememberCandidates combines existing sealed rows with newly captured
// candidates keyed by (Project, ID), retaining newest timestamps and richest file lists.
func mergeRememberCandidates(existing, incoming []storedRememberCandidate) []storedRememberCandidate {
	if len(incoming) == 0 {
		return existing
	}
	merged := make([]storedRememberCandidate, 0, len(existing)+len(incoming))
	byKey := make(map[string]int, len(existing)+len(incoming))
	keyFor := func(r storedRememberCandidate) string {
		return r.Project + "\x00" + r.ID
	}
	for _, r := range existing {
		k := keyFor(r)
		if _, dup := byKey[k]; dup {
			continue
		}
		byKey[k] = len(merged)
		merged = append(merged, r)
	}
	for _, r := range incoming {
		k := keyFor(r)
		if idx, ok := byKey[k]; ok {
			cur := merged[idx]
			if r.At.After(cur.At) {
				cur.At = r.At
			}
			cur.Sessions = max(cur.Sessions, r.Sessions)
			if len(r.Files) > 0 {
				cur.Files = sources.CleanRepoFiles(append(slices.Clone(cur.Files), r.Files...))
			}
			if cur.From == "" {
				cur.From = r.From
			}
			if cur.Session == "" {
				cur.Session = r.Session
			}
			merged[idx] = cur
			continue
		}
		byKey[k] = len(merged)
		merged = append(merged, r)
	}
	return merged
}
