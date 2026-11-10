package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

var (
	activeDejaDirMu sync.RWMutex
	activeDejaDir   string
)

func setActiveDejaDir(dir string) {
	activeDejaDirMu.Lock()
	defer activeDejaDirMu.Unlock()
	activeDejaDir = dir
}

func getActiveDejaDir() string {
	activeDejaDirMu.RLock()
	defer activeDejaDirMu.RUnlock()
	return activeDejaDir
}

// `deja repo` is project memory committed in the repository: a few picked
// decisions, dropped approaches and traps in <root>/.deja/memory.jsonl, which
// everyone who clones the repository gets in recall. deja proposes and writes
// entries only when asked; the person reviews the diff and commits it.

var (
	repoFlags     = []string{"--json"}
	repoAddFlags  = []string{"--kind"}
	repoInitFlags = []string{"--limit"}
	repoRmFlags   = []string{}
)

const repoFileShown = sources.RepoMemoryDir + "/" + sources.RepoMemoryName

func runRepo(dir string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "candidates":
			return runRepoCandidates(dir, os.Stdout, args[1:])
		case "add":
			return runRepoAdd(dir, os.Stdout, args[1:])
		case "gotcha", "rejected", "decision":
			return runRepoAdd(dir, os.Stdout, append([]string{"--kind", args[0]}, args[1:]...))
		case "init", "sync":
			return runRepoSync(dir, os.Stdout, args[0], args[1:])
		case "off", "disable":
			return runRepoToggle(os.Stdout, args[0], args[1:], true)
		case "on", "enable":
			return runRepoToggle(os.Stdout, args[0], args[1:], false)
		case "rm", "remove", "delete":
			return runRepoRemove(os.Stdout, args[1:])
		}
	}
	return runRepoList(dir, os.Stdout, args)
}

func runRepoToggle(w io.Writer, sub string, args []string, off bool) error {
	if len(args) > 0 {
		return fmt.Errorf("repo %s takes no arguments — got %q", sub, args[0])
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	if err := sources.SetRepoAutoDisabled(root, off); err != nil {
		return err
	}
	if off {
		fmt.Fprintf(w, "automatic %s updates are off for %s — `deja repo on` turns them back on\n", repoFileShown, search.SafeLine(sources.ClaudeProjectName(root)))
	} else {
		fmt.Fprintf(w, "automatic %s updates are on for %s\n", repoFileShown, search.SafeLine(sources.ClaudeProjectName(root)))
	}
	return nil
}

// repoCheckout is the repository the command is about: the one the process
// stands in.
func repoCheckout() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		return "", errors.New("repo: not inside a git repository — repo memory lives in the repository's .deja/ directory")
	}
	return root, nil
}

func runRepoList(dir string, w io.Writer, args []string) error {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return unknownFlag("repo", a, repoFlags)
		}
		asJSON = true
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	autoEvolveRepoMemory(dir, root)
	entries, bad, err := readRepoMemoryAndNotes(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sources.RememberRepoCheckout(root)
	if asJSON {
		if entries == nil {
			entries = []sources.RepoEntry{}
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(entries)
	}
	if len(entries) == 0 && bad == 0 {
		if sources.RepoAutoDisabled(root) {
			fmt.Fprintf(w, "no %s in this repository (automatic updates are off — `deja repo on` or `deja repo sync`)\n", repoFileShown)
			return nil
		}
		fmt.Fprintf(w, "no %s in this repository yet — `deja repo sync` builds it automatically, or `deja repo candidates` lists entries\n", repoFileShown)
		return nil
	}
	guarded := map[string]bool{}
	for _, e := range entries {
		for _, f := range e.Files {
			guarded[f] = true
		}
	}
	extra := ""
	if len(guarded) > 0 {
		extra = fmt.Sprintf(" · %d file%s guarded", len(guarded), pluralS(len(guarded)))
	}
	if sources.RepoAutoDisabled(root) {
		extra += " · auto-sync off"
	}
	fmt.Fprintf(w, "%d entr%s in %s%s\n", len(entries), pluralY(len(entries)), repoFileShown, extra)
	for i, e := range entries {
		fmt.Fprintf(w, "%2d. %s\n", i+1, repoEntryRow(e))
	}
	if bad > 0 {
		fmt.Fprintf(w, "%d line%s deja cannot read — a merge conflict or a hand edit; fix or delete %s before adding more\n", bad, pluralS(bad), pluralThem(bad))
	}
	if line := repoPolicyLine(sources.RepoMemoryProject(root)); line != "" {
		fmt.Fprintln(w, line)
	}
	return nil
}

func repoEntryRow(e sources.RepoEntry) string {
	var meta []string
	if len(e.Files) > 0 {
		shown := e.Files
		if len(shown) > 2 {
			shown = shown[:2]
		}
		fileStr := strings.Join(shown, ", ")
		if len(e.Files) > 2 {
			fileStr += fmt.Sprintf(" +%d", len(e.Files)-2)
		}
		meta = append(meta, fileStr)
	}
	if e.Commit != "" {
		meta = append(meta, "@"+search.SafeLine(e.Commit))
	}
	if e.Sessions > 1 {
		meta = append(meta, fmt.Sprintf("%dx", e.Sessions))
	}
	if e.From != "" {
		meta = append(meta, search.SafeLine(e.From))
	}
	tail := ""
	if len(meta) > 0 {
		tail = "  (" + strings.Join(meta, " · ") + ")"
	}
	return fmt.Sprintf("%s %-8s %s%s", search.SafeLine(e.ID), e.Kind, search.SafeLine(e.Text), tail)
}

// repoPolicyLine says when the trust policy keeps these entries off a path, so
// a list that shows entries is not read as "the agent gets these".
func repoPolicyLine(project string) string {
	pol := policy.Load()
	var off []string
	for _, a := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
		if !pol.Allows(a, project) {
			off = append(off, a)
		}
	}
	if len(off) == 0 {
		return ""
	}
	return "your policy keeps repo memory out of " + strings.Join(off, ", ") + " (" + policy.Path() + ")"
}

func runRepoAdd(dir string, w io.Writer, args []string) error {
	kind := ""
	var words []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--kind":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("repo add: --kind needs decision, rejected or gotcha")
			}
			i++
			kind = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(a, "--kind="):
			val := strings.TrimSpace(strings.TrimPrefix(a, "--kind="))
			if val == "" {
				return errors.New("repo add: --kind needs decision, rejected or gotcha")
			}
			kind = strings.ToLower(val)
		case a == "--":
			words = append(words, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			return unknownFlag("repo add", a, repoAddFlags)
		default:
			words = append(words, a)
		}
	}
	if kind != "" && !sources.RepoKinds[kind] {
		return fmt.Errorf("repo add: --kind is decision, rejected or gotcha, got %q", kind)
	}
	if len(words) == 0 || strings.TrimSpace(strings.Join(words, " ")) == "" {
		return errors.New(`repo add: name candidate numbers or ids from ` + "`deja repo candidates`" + `, or write one: deja repo add "the text"`)
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	var entries []sources.RepoEntry
	switch {
	case kind != "":
		entries = append(entries, sources.RepoEntry{Kind: kind, Text: strings.Join(words, " ")})
	case allRepoCandidateSelectors(words):
		picked, err := pickRepoCandidates(dir, root, words)
		if err != nil {
			return err
		}
		entries = picked
	default:
		text := strings.Join(words, " ")
		entries = append(entries, sources.RepoEntry{Kind: inferRepoKind(text), Text: text})
	}
	return writeRepoEntries(w, root, entries)
}

func runRepoSync(dir string, w io.Writer, sub string, args []string) error {
	limit := 15
	if sub == "init" {
		limit = 5
	}
	args = splitKnownValueFlags(args, "--limit")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return fmt.Errorf("repo %s: --limit needs value", sub)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("repo %s: --limit wants a positive number, got %q", sub, args[i])
			}
			limit = n
		default:
			return unknownFlag("repo "+sub, args[i], repoInitFlags)
		}
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	if err = index.Ensure(dir, "", false, noteIndexNarration(dir)); err != nil {
		return ensureError(dir, err)
	}
	cands, err := collectRepoCandidates(dir, root, 0, 0)
	if err != nil {
		return err
	}
	var incoming []sources.RepoEntry
	for _, c := range cands {
		if c.Refused != "" || (sub == "sync" && !autoSyncEligible(c)) {
			continue
		}
		incoming = append(incoming, repoEntryFromCandidate(c))
		if len(incoming) >= limit {
			break
		}
	}
	if sub == "init" {
		var fresh []sources.RepoEntry
		for _, c := range cands {
			if c.InFile || c.Refused != "" {
				continue
			}
			fresh = append(fresh, repoEntryFromCandidate(c))
			if len(fresh) >= limit {
				break
			}
		}
		if len(fresh) == 0 {
			fmt.Fprintf(w, "no new candidates for %s to initialize %s\n", search.SafeLine(sources.ClaudeProjectName(root)), repoFileShown)
			return nil
		}
		return writeRepoEntries(w, root, fresh)
	}
	added, updated, pruned, err := sources.EvolveRepoEntries(root, incoming, 50)
	if err != nil {
		return fmt.Errorf("repo sync: %w", err)
	}
	pruned += pruneFilteredRepoEntries(root)
	if added == 0 && updated == 0 && pruned == 0 {
		fmt.Fprintf(w, "%s is up to date\n", repoFileShown)
		return nil
	}
	fmt.Fprintf(w, "synced %s: +%d added, %d reinforced, %d stale pruned\n", repoFileShown, added, updated, pruned)
	return nil
}

func repoEntryFromCandidate(c repoCandidate) sources.RepoEntry {
	day := c.at.UTC().Format("2006-01-02")
	if c.at.IsZero() {
		day = time.Now().UTC().Format("2006-01-02")
	}
	return sources.RepoEntry{
		ID:       c.ID,
		Kind:     c.Kind,
		Text:     c.Text,
		Files:    c.Files,
		Commit:   c.Commit,
		Sessions: c.Sessions,
		Date:     day,
		From:     c.From,
	}
}

// autoEvolveRepoMemory quietly evolves .deja/memory.jsonl from the local index
// unless disabled via `deja repo off`, DEJA_REPO=off, or policy. Under `go test`
// binaries it only evolves when .deja/memory.jsonl already exists (or when
// DEJA_REPO=on is explicitly set).
func autoEvolveRepoMemory(dir, cwd string) {
	if redact.Disabled() {
		return
	}
	root := sources.RepoRoot(cwd)
	if root == "" || sources.RepoAutoDisabled(root) {
		return
	}
	if !policy.Load().Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return
	}
	inTestBinary := strings.HasSuffix(strings.TrimSuffix(os.Args[0], ".exe"), ".test")
	if inTestBinary && strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_REPO"))) != "on" {
		if !fileThere(sources.RepoMemoryPath(root)) {
			return
		}
	}
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		return
	}
	cands, err := collectRepoCandidates(dir, root, 0, 20)
	if err != nil {
		return
	}
	var incoming []sources.RepoEntry
	for _, c := range cands {
		if c.Refused != "" || !autoSyncEligible(c) {
			continue
		}
		incoming = append(incoming, repoEntryFromCandidate(c))
		if len(incoming) >= 12 {
			break
		}
	}
	_, _, _, _ = sources.EvolveRepoEntries(root, incoming, 50)
	_ = pruneFilteredRepoEntries(root)
}

func pruneFilteredRepoEntries(root string) int {
	entries, bad, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || bad > 0 {
		return 0
	}
	var drop []string
	bestCoChange := map[string]int{}
	for i, e := range entries {
		if stem, ok := coChangeEntryStem(e.Text); ok {
			if prev, exists := bestCoChange[stem]; !exists || e.Text > entries[prev].Text {
				if exists {
					drop = append(drop, entries[prev].ID)
				}
				bestCoChange[stem] = i
			} else {
				drop = append(drop, e.ID)
			}
			continue
		}
		if e.From != "" && !strings.HasPrefix(e.From, "deja") && !usableRepoCandidateLine(e.Text) {
			drop = append(drop, e.ID)
		}
	}
	if len(drop) == 0 {
		return 0
	}
	removed, _ := sources.RemoveRepoEntries(root, drop)
	return len(removed)
}

func coChangeEntryStem(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, "Editing ")
	if !ok {
		return "", false
	}
	a, after, ok := strings.Cut(rest, " requires updating ")
	if !ok {
		return "", false
	}
	b, _, ok := strings.Cut(after, " alongside it (")
	if !ok || a == "" || b == "" {
		return "", false
	}
	if a > b {
		a, b = b, a
	}
	return a + "<->" + b, true
}

func autoSyncEligible(c repoCandidate) bool {
	if c.tier == 0 {
		return true
	}
	return len(c.Files) > 0
}

func runRepoRemove(w io.Writer, args []string) error {
	var ids []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return unknownFlag("repo rm", a, repoRmFlags)
		}
		if strings.TrimSpace(a) == "" {
			return errors.New("repo rm: name one or more entry numbers or ids from `deja repo`")
		}
		ids = append(ids, a)
	}
	if len(ids) == 0 {
		return errors.New("repo rm: name one or more entry numbers or ids from `deja repo`")
	}
	root, err := repoCheckout()
	if err != nil {
		return err
	}
	if existing, bad, readErr := sources.ReadRepoMemory(sources.RepoMemoryPath(root)); readErr == nil && bad == 0 {
		for i, raw := range ids {
			tok := strings.TrimPrefix(strings.TrimSpace(raw), "#")
			if n, convErr := strconv.Atoi(tok); convErr == nil && n >= 1 && n <= len(existing) {
				ids[i] = existing[n-1].ID
			}
		}
	}
	removed, err := sources.RemoveRepoEntries(root, ids)
	if errors.Is(err, sources.ErrRepoMemoryMalformed) {
		return fmt.Errorf("repo rm: %s has a line deja cannot read — fix or delete it first", repoFileShown)
	}
	if err != nil {
		return fmt.Errorf("repo rm: %w", err)
	}
	if len(removed) == 0 {
		return fmt.Errorf("repo rm: no matching id in %s — `deja repo` lists them", repoFileShown)
	}
	fmt.Fprintf(w, "removed %d entr%s from %s\n", len(removed), pluralY(len(removed)), repoFileShown)
	for _, e := range removed {
		fmt.Fprintln(w, "  "+repoEntryRow(e))
	}
	return nil
}

// pickRepoCandidates resolves candidate numbers or ids from the candidates list.
func pickRepoCandidates(dir, root string, ids []string) ([]sources.RepoEntry, error) {
	if err := index.Ensure(dir, "", false, noteIndexNarration(dir)); err != nil {
		return nil, ensureError(dir, err)
	}
	cands, err := collectRepoCandidates(dir, root, 0, 0)
	if err != nil {
		return nil, err
	}
	byID := map[string]repoCandidate{}
	for _, c := range cands {
		byID[c.ID] = c
	}
	var out []sources.RepoEntry
	for _, id := range ids {
		id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "#")
		var (
			c  repoCandidate
			ok bool
		)
		if n, convErr := strconv.Atoi(id); convErr == nil && n >= 1 && n <= len(cands) {
			c, ok = cands[n-1], true
		} else {
			c, ok = byID[id]
		}
		if !ok {
			return nil, fmt.Errorf("repo add: %q is not a candidate id — `deja repo candidates` lists them, and --kind writes your own text", search.SafeLine(id))
		}
		if c.Refused != "" {
			return nil, fmt.Errorf("repo add: %s is refused: %s", c.ID, c.Refused)
		}
		out = append(out, repoEntryFromCandidate(c))
	}
	return out, nil
}

func allRepoCandidateSelectors(words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		tok := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(w)), "#")
		if tok == "" || strings.ContainsAny(tok, " \t\n") {
			return false
		}
		if n, err := strconv.Atoi(tok); err == nil && n > 0 && n <= 999 {
			continue
		}
		if len(tok) >= 6 && len(tok) <= 12 && isHexToken(tok) {
			continue
		}
		return false
	}
	return true
}

func isHexToken(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}

// inferRepoKind classifies prose into rejected, gotcha or decision when the
// caller did not name --kind.
func inferRepoKind(text string) string {
	if _, ok := index.GiveUpLine(text); ok {
		return "rejected"
	}
	low := " " + strings.ToLower(strings.TrimSpace(text)) + " "
	for _, kw := range []string{
		" rejected ", " reverted ", " dropped ", " abandoned ",
		" отказались ", " отбросили ", " откатили ", " не подошёл ", " не подошел ",
	} {
		if strings.Contains(low, kw) {
			return "rejected"
		}
	}
	for _, kw := range []string{
		" never ", " do not ", " don't ", " must not ", " cannot ", " can't ",
		" deadlock", " gotcha", " caveat", " breaks ", " fails when ", " fails if ",
		" никогда ", " нельзя ", " не надо ", " не запускать ", " ломает ", " падает при ",
	} {
		if strings.Contains(low, kw) {
			return "gotcha"
		}
	}
	return "decision"
}

// readRepoMemoryAndNotes reads .deja/memory.jsonl and merges any repo-scoped
// notes from .deja/notes.jsonl so `remember --repo` and `promote --repo` work
// seamlessly alongside `deja repo` without duplicating index records.
func readRepoMemoryAndNotes(root string) ([]sources.RepoEntry, int, error) {
	entries, bad, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, bad, err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.ID] = true
	}
	if np := sources.RepoNotesFile(root); np != "" && np != sources.NotesFile() {
		if ss, nerr := sources.ParseNotesFile(np); nerr == nil {
			for _, s := range ss {
				for _, m := range s.Messages {
					safe, _, serr := repoSafeText(m.Text)
					if serr != nil {
						continue
					}
					kind := inferRepoKind(safe)
					if strings.HasPrefix(s.Title, "[rejected]") {
						kind = "rejected"
					}
					id := sources.RepoEntryID(kind, safe)
					if have[id] {
						continue
					}
					have[id] = true
					entries = append(entries, sources.RepoEntry{
						ID:   id,
						Kind: kind,
						Text: safe,
						From: repoFrom("deja", m.Time),
						Date: m.Time.UTC().Format("2006-01-02"),
					})
				}
			}
		}
	}
	var livePaths []string
	if root != "" {
		livePaths = append(livePaths,
			filepath.Join(root, ".deja", "live_rules.jsonl"),
			filepath.Join(root, "live_rules.jsonl"),
		)
	}
	if activeDir := getActiveDejaDir(); activeDir != "" {
		livePaths = append(livePaths, filepath.Join(activeDir, "live_rules.jsonl"))
	}
	seenLivePath := map[string]bool{}
	for _, lp := range livePaths {
		if seenLivePath[lp] {
			continue
		}
		seenLivePath[lp] = true
		for _, le := range readLiveRulesFile(lp) {
			id := le.ID
			if id == "" {
				id = sources.RepoEntryID(le.Kind, le.Text)
			}
			if have[id] {
				continue
			}
			have[id] = true
			le.ID = id
			entries = append(entries, le)
		}
	}
	return entries, bad, nil
}

type liveRuleRecord struct {
	sources.RepoEntry
	Symbols []string
}

func readLiveRulesFile(p string) []sources.RepoEntry {
	recs := readLiveRuleRecords(p)
	out := make([]sources.RepoEntry, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.RepoEntry)
	}
	return out
}

func readLiveRuleRecords(p string) []liveRuleRecord {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []liveRuleRecord
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r struct {
			ID      string   `json:"id"`
			Kind    string   `json:"kind"`
			Text    string   `json:"text"`
			Files   []string `json:"files"`
			Paths   []string `json:"paths"`
			Symbols []string `json:"symbols"`
			Date    string   `json:"date"`
			From    string   `json:"from"`
		}
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		if r.Kind == "" {
			r.Kind = "rule"
		}
		if r.Text == "" {
			continue
		}
		files := r.Files
		if len(files) == 0 {
			files = r.Paths
		}
		out = append(out, liveRuleRecord{
			RepoEntry: sources.RepoEntry{
				ID:    r.ID,
				Kind:  r.Kind,
				Text:  r.Text,
				Files: files,
				Date:  r.Date,
				From:  r.From,
			},
			Symbols: r.Symbols,
		})
	}
	return out
}

func appendLiveRule(targetPath string, entry sources.RepoEntry, syms []string) {
	if targetPath == "" || entry.Text == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return
	}
	existing := readLiveRuleRecords(targetPath)
	var kept []liveRuleRecord
	for _, e := range existing {
		if e.ID != entry.ID && !strings.EqualFold(strings.TrimSpace(e.Text), strings.TrimSpace(entry.Text)) {
			kept = append(kept, e)
		}
	}
	kept = append(kept, liveRuleRecord{RepoEntry: entry, Symbols: syms})
	if len(kept) > 20 {
		kept = kept[len(kept)-20:]
	}
	var b bytes.Buffer
	for _, e := range kept {
		data := struct {
			ID      string   `json:"id"`
			Kind    string   `json:"kind"`
			Text    string   `json:"text"`
			Files   []string `json:"files,omitempty"`
			Symbols []string `json:"symbols,omitempty"`
			Date    string   `json:"date"`
			From    string   `json:"from,omitempty"`
		}{
			ID:      e.ID,
			Kind:    e.Kind,
			Text:    e.Text,
			Files:   e.Files,
			Symbols: e.Symbols,
			Date:    e.Date,
			From:    e.From,
		}
		raw, err := json.Marshal(data)
		if err == nil {
			b.Write(raw)
			b.WriteByte('\n')
		}
	}
	tmp := fmt.Sprintf("%s.tmp-%d-%d", targetPath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err == nil {
		_ = os.Rename(tmp, targetPath)
	}
}

// writeRepoEntries redacts each entry, refuses one that still holds a secret,
// writes the rest and says what to do next. Nothing is staged.
func writeRepoEntries(w io.Writer, root string, entries []sources.RepoEntry) error {
	masked := 0
	for i := range entries {
		text, n, err := repoSafeText(entries[i].Text)
		if err != nil {
			return fmt.Errorf("repo add: %w", err)
		}
		masked += n
		entries[i].Text = text
		entries[i].ID = sources.RepoEntryID(entries[i].Kind, text)
		if len(entries[i].Files) == 0 {
			entries[i].Files = sources.CleanRepoFiles(extractMentionedRepoFiles(root, text, nil))
		}
		if entries[i].Date == "" {
			entries[i].Date = time.Now().UTC().Format("2006-01-02")
		}
	}
	dirBefore := fileThere(filepath.Join(root, sources.RepoMemoryDir))
	added, err := sources.AddRepoEntries(root, entries)
	if errors.Is(err, sources.ErrRepoMemoryMalformed) {
		return fmt.Errorf("repo add: %s has a line deja cannot read (a merge conflict or a hand edit) — fix or delete it first; `deja repo` counts them", repoFileShown)
	}
	if err != nil {
		return fmt.Errorf("repo add: %w", err)
	}
	if len(added) == 0 {
		fmt.Fprintf(w, "already in %s\n", repoFileShown)
		return nil
	}
	fmt.Fprintf(w, "added %d entr%s to %s\n", len(added), pluralY(len(added)), repoFileShown)
	for _, e := range added {
		fmt.Fprintln(w, "  "+repoEntryRow(e))
	}
	if !dirBefore {
		fmt.Fprintf(w, "wrote %s/.gitattributes so parallel additions merge line by line, and a short %s/README.md for whoever opens the folder\n", sources.RepoMemoryDir, sources.RepoMemoryDir)
	}
	if masked > 0 {
		fmt.Fprintf(w, "%d secret%s or private detail%s masked. pattern redaction is a floor — read the diff before you commit\n", masked, pluralS(masked), pluralS(masked))
	}
	fmt.Fprintf(w, "nothing is committed: review with `git diff %s/` and commit it like code\n", sources.RepoMemoryDir)
	return nil
}

func fileThere(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// repoSafeText is an entry as it may be committed: the secret rules the
// index and every export use, then the outbound pass for home paths, emails,
// internal hosts and private addresses. It is read once more afterwards, and
// text that still matches a secret rule is refused rather than written.
func repoSafeText(text string) (string, int, error) {
	if redact.Disabled() {
		return "", 0, errors.New("redaction is off (DEJA_NO_REDACT=1), and nothing is written into a repository without it")
	}
	clean := sources.CleanRepoText(text)
	if clean == "" {
		return "", 0, errors.New("the entry is empty")
	}
	out, secrets := redact.Text(clean)
	out, private := redact.Outbound(out)
	out = sources.CleanRepoText(out)
	if _, left := redact.Text(out); len(left) > 0 {
		return "", 0, fmt.Errorf("the entry still matches a secret rule (%s) after redaction — write it without the value", strings.Join(sortedKinds(left), ", "))
	}
	if n := len([]rune(out)); n > sources.RepoTextMax {
		return "", 0, fmt.Errorf("the entry is %d characters, over %d — shorten it and add it with --kind", n, sources.RepoTextMax)
	}
	return out, countAll(secrets) + countAll(private), nil
}

func countAll(c redact.Counts) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

func sortedKinds(c redact.Counts) []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// repoRememberKind reads the MCP remember tool's repo target: "repo:" alone
// files a decision, "repo:rejected" and "repo:gotcha" the other kinds.
func repoRememberKind(project string) (string, bool, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(project), model.RepoProjectPrefix)
	if !ok {
		return "", false, nil
	}
	kind := strings.ToLower(strings.TrimSpace(rest))
	if kind == "" {
		kind = "decision"
	}
	if !sources.RepoKinds[kind] {
		return "", true, argErrorf("project %q: after repo: comes decision, rejected or gotcha", search.SafeLine(project))
	}
	return kind, true, nil
}

// mcpRememberRepo writes one entry for the MCP remember tool. The agent was
// asked to put it in the repository; the reply says it is not committed.
func mcpRememberRepo(kind, text string) (string, error) {
	root, err := repoCheckout()
	if err != nil {
		return "", argErrorf("%v", err)
	}
	var b strings.Builder
	if err := writeRepoEntries(&b, root, []sources.RepoEntry{{Kind: kind, Text: text}}); err != nil {
		return "", argErrorf("%v", err)
	}
	return strings.TrimSpace(b.String()) + "\nTell the user it is written but not committed, and to review the diff.", nil
}

// repoMemoryBlock is what the session-start digest says about the
// repository's own entries: read from the file rather than the index, so a
// fresh clone has them before deja has indexed anything there. Dropped
// approaches and traps lead, since they are what an agent repeats.
func repoMemoryBlock(cwd string, pol policy.Policy, taskFiles ...string) string {
	const maxRows, budget = 8, 900
	root := sources.RepoRoot(cwd)
	if root == "" {
		return ""
	}
	if !pol.Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return ""
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return ""
	}
	sources.RememberRepoCheckout(root)
	rank := map[string]int{"rejected": 0, "gotcha": 1, "decision": 2}
	sort.SliceStable(entries, func(i, j int) bool {
		if len(taskFiles) > 0 {
			mi := repoEntryMatchesAnyEntryFile(root, entries[i], taskFiles)
			mj := repoEntryMatchesAnyEntryFile(root, entries[j], taskFiles)
			if mi != mj {
				return mi
			}
		}
		if rank[entries[i].Kind] != rank[entries[j].Kind] {
			return rank[entries[i].Kind] < rank[entries[j].Kind]
		}
		if entries[i].Sessions != entries[j].Sessions {
			return entries[i].Sessions > entries[j].Sessions
		}
		return entries[i].Date > entries[j].Date
	})
	var b strings.Builder
	b.WriteString("from this repository's " + repoFileShown + " (committed by the people working on it):\n")
	head := b.Len()
	usedRunes := utf8.RuneCountInString(b.String())
	for i, e := range entries {
		if i >= maxRows {
			break
		}
		shortEntry := e
		if r := []rune(shortEntry.Text); len(r) > 180 {
			shortEntry.Text = strings.TrimSpace(string(r[:179])) + "…"
		}
		row := "  · " + search.SafeLine(sources.RepoEntryBody(shortEntry)) + "\n"
		rowRunes := utf8.RuneCountInString(row)
		if usedRunes+rowRunes > budget && i > 0 {
			break
		}
		b.WriteString(row)
		usedRunes += rowRunes
	}
	if b.Len() == head {
		return ""
	}
	if more := len(entries) - strings.Count(b.String()[head:], "\n"); more > 0 {
		b.WriteString("  " + strconv.Itoa(more) + " more — recall finds them by topic\n")
	}
	return b.String()
}

// joinBlocks puts two newline-ended blocks one after the other.
func joinBlocks(a, b string) string {
	if a == "" {
		return b
	}
	return strings.TrimRight(a, "\n") + "\n" + b
}

// repoMemoryStamp identifies the checkout's file for the session-start cache,
// so an entry added or deleted shows at the next start rather than after the
// cache runs out. Empty when there is no file, which leaves the gate as it was.
func repoMemoryStamp(cwd string) string {
	root := sources.RepoRoot(cwd)
	if root == "" {
		return ""
	}
	fi, err := os.Stat(sources.RepoMemoryPath(root))
	if err != nil {
		return ""
	}
	return "|repo:" + strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

func repoEntryMatchesAnyEntryFile(root string, e sources.RepoEntry, taskFiles []string) bool {
	for _, f := range taskFiles {
		if f != "" && (repoEntryHasFile(root, f, e.Files) || repoEntryMentionsFile(root, f, e.Text)) {
			return true
		}
	}
	return false
}
