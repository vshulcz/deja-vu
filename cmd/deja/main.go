package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "deja:", err)
		os.Exit(1)
	}
}

func loadAll(h string) []model.Session {
	var ss []model.Session
	if h == "" || h == "claude" {
		ss = append(ss, sources.LoadClaude()...)
	}
	if h == "" || h == "codex" {
		ss = append(ss, sources.LoadCodex()...)
	}
	if h == "" || h == "opencode" {
		ss = append(ss, sources.LoadOpencode()...)
	}
	if h == "" || h == "aider" {
		ss = append(ss, sources.LoadAider()...)
	}
	if h == "" || h == "gemini" {
		ss = append(ss, sources.LoadGemini()...)
	}
	if h == "" || h == "cursor" {
		ss = append(ss, sources.LoadCursor()...)
	}
	if h == "" || h == "antigravity" {
		ss = append(ss, sources.LoadAntigravity()...)
	}
	if h == "" || h == "grok" {
		ss = append(ss, sources.LoadGrok()...)
	}
	if h == "" || h == "qwen" {
		ss = append(ss, sources.LoadQwen()...)
	}
	return ss
}

func loadFileSources() []model.Session {
	var ss []model.Session
	for _, harness := range []string{"claude", "codex", "aider", "gemini", "cursor", "antigravity", "grok", "qwen"} {
		ss = append(ss, loadAll(harness)...)
	}
	return ss
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-version" {
		fmt.Fprintf(os.Stdout, "deja %s\n", version)
		return nil
	}
	if args[0] == "sources" {
		printSources()
		return nil
	}
	if args[0] == "completion" {
		return runCompletion(args[1:])
	}
	if args[0] == "doctor" {
		return runDoctor(os.Stdout, args[1:], doctorLookup)
	}
	if args[0] == "warmup" {
		prepareFirstIndexGreeting()
		if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err != nil {
			return err
		}
		maybeFirstIndexGreeting()
		return nil
	}
	if args[0] == "index" {
		force := false
		for _, a := range args[1:] {
			if a == "--rebuild" || a == "-rebuild" {
				force = true
				continue
			}
			return fmt.Errorf("index: unknown flag %q", a)
		}
		prepareFirstIndexGreeting()
		if err := index.Ensure(index.DefaultDir(), "", force, os.Stderr); err != nil {
			return err
		}
		clearWarmupSentinel()
		maybeFirstIndexGreeting()
		return nil
	}
	if args[0] == "embed" {
		return runEmbed(args[1:])
	}
	if args[0] == "bench" {
		return runBench(args[1:])
	}
	if args[0] == "statusline" {
		return runStatusline(os.Stdin, os.Stdout)
	}
	if args[0] == "stats" {
		return runStats(args[1:])
	}
	if args[0] == "remember" {
		return runRemember(args[1:])
	}
	if args[0] == "forget" {
		return runForget(args[1:])
	}
	if args[0] == "mcp" {
		return serveMCP(os.Stdin, os.Stdout)
	}
	if args[0] == "hook-prompt" {
		return runHookPrompt(os.Stdin, os.Stdout)
	}
	if args[0] == "hook-context" {
		plain := len(args) > 1 && args[1] == "--plain"
		_ = runHookContext(plain)
		return nil
	}
	if args[0] == "hook-precompact" {
		runHookPrecompact()
		return nil
	}
	if args[0] == "install" {
		return runInstall(args[1:], false)
	}
	if args[0] == "uninstall" {
		return runInstall(args[1:], true)
	}
	if args[0] == "update" {
		return runUpdate(args[1:], os.Stdout)
	}
	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("show needs id-prefix")
		}
		s, ok, err := findByPrefix(args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no session matches %q", args[1])
		}
		search.PrintSession(os.Stdout, s)
		return nil
	}
	if args[0] == "share" {
		return runShare(args[1:], os.Stdout)
	}
	if args[0] == "resume" {
		return runResume(args[1:], os.Stdout)
	}
	if args[0] == "handoff" {
		return runHandoff(args[1:], os.Stdout)
	}
	if args[0] == "sync" {
		return runSync(args[1:])
	}
	if args[0] == "ctx" {
		if len(args) < 2 {
			return fmt.Errorf("ctx needs query or id-prefix")
		}
		q := strings.Join(args[1:], " ")
		if !strings.Contains(q, " ") && len(q) >= 6 {
			s, ok, err := findByPrefix(q)
			if err != nil {
				return err
			}
			if ok {
				search.PrintContext(os.Stdout, s, "")
				return nil
			}
		}
		o := search.Options{Query: q, All: true}
		if err := index.EnsureForSearch(index.DefaultDir(), o, false, os.Stderr); err != nil {
			return err
		}
		ss, err := index.SearchWithRecovery(index.DefaultDir(), o, os.Stderr)
		if err != nil {
			return err
		}
		hits, err := search.Run(ss, o)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			return fmt.Errorf("no session matches %q", q)
		}
		search.PrintContext(os.Stdout, hits[0].Session, q)
		return nil
	}
	if args[0] == "blame" {
		return runBlame(args[1:])
	}
	if args[0] == "last" {
		n, o, err := parseLast(args[1:])
		if err != nil {
			return err
		}
		ss, err := recentMatching(n, o)
		if err != nil {
			return err
		}
		for _, s := range ss {
			fmt.Printf("[%s · %s · %s · %s]", s.Harness, s.Project, s.Updated.Format("2006-01-02"), s.ID)
			title := s.Title
			if title == "" {
				title = firstUserTitle(s)
			}
			if title != "" {
				fmt.Printf(" %s", title)
			}
			fmt.Println()
		}
		return nil
	}
	force := false
	var filtered []string
	for _, a := range args {
		if a == "--rebuild" || a == "-rebuild" {
			force = true
			continue
		}
		filtered = append(filtered, a)
	}
	o, err := parseSearch(filtered)
	if err != nil {
		return err
	}
	prepareFirstIndexGreeting()
	if err := index.EnsureForSearch(index.DefaultDir(), o, force, os.Stderr); err != nil {
		return fmt.Errorf("ensure: %w", err)
	}
	maybeFirstIndexGreeting()
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, os.Stderr)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	ss := result.Sessions
	o.Tier = result.Tier
	if result.Stemmed {
		printStemmed(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		printFuzzy(os.Stderr, result.Variants)
		o.Fuzzy = true
		o.FuzzyVariants = result.Variants
	}
	if result.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	if !o.NoEmbed && os.Getenv("DEJA_EMBED") != "off" {
		hits = maybeRerank(hits, o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(hits, o, os.Stderr)
	o.Semantic = semantic
	if len(hits) == 0 {
		printNoMatches(os.Stderr, o.Query, len(ss))
	}
	search.Print(os.Stdout, hits, o)
	return nil
}

func printStemmed(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying word forms: %s -> %s\n", token, variant)
			}
		}
	}
}

func printNoMatches(w io.Writer, q string, n int) {
	fmt.Fprintf(w, "deja: no matches in %d indexed sessions — try fewer words or --re (query %q)\n", n, q)
}

func clearWarmupSentinel() {
	if path := os.Getenv("DEJA_WARMUP_SENTINEL"); path != "" {
		_ = os.Remove(path)
	}
}

func printFuzzy(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying close spellings: %s -> %s\n", token, variant)
			}
		}
	}
}

func findByPrefix(p string) (model.Session, bool, error) {
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err == nil {
		if s, ok, err := index.FindByPrefix(index.DefaultDir(), p); err == nil {
			return s, ok, nil
		}
	}
	ss := loadFileSources()
	ss = append(ss, sources.LoadOpencodePrefix(p)...)
	s, ok := search.FindByPrefix(ss, p)
	return s, ok, nil
}

func recent(n int) ([]model.Session, error) {
	return recentMatching(n, search.Options{})
}

func recentMatching(n int, o search.Options) ([]model.Session, error) {
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err == nil {
		if ss, err := index.RecentMatching(index.DefaultDir(), n, o); err == nil {
			return ss, nil
		}
	}
	ss := filterRecentSources(loadFileSources(), o)
	if o.Harness == "" || o.Harness == "opencode" {
		ss = append(ss, filterRecentSources(sources.LoadOpencodeRecent(n), o)...)
	}
	return search.Recent(ss, n), nil
}

func parseLast(args []string) (int, search.Options, error) {
	n := 10
	seenN := false
	o := search.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--harness", "--project":
			if i+1 >= len(args) {
				return n, o, fmt.Errorf("%s needs value", a)
			}
			i++
			if a == "--harness" {
				o.Harness = args[i]
			} else {
				o.Project = args[i]
			}
		default:
			if strings.HasPrefix(a, "-") {
				return n, o, fmt.Errorf("last: unknown flag %q", a)
			}
			if !seenN {
				if x, err := strconv.Atoi(a); err == nil {
					n = x
					seenN = true
				}
			}
		}
	}
	return n, o, nil
}

func filterRecentSources(ss []model.Session, o search.Options) []model.Session {
	if o.Harness == "" && o.Project == "" {
		return ss
	}
	out := ss[:0]
	project := strings.ToLower(o.Project)
	for _, s := range ss {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if project != "" && !strings.Contains(strings.ToLower(s.Project), project) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func firstUserTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		t := strings.Join(strings.Fields(msg.Text), " ")
		r := []rune(t)
		if len(r) > 60 {
			return strings.TrimSpace(string(r[:60])) + "…"
		}
		return t
	}
	return ""
}

func parseSearch(args []string) (search.Options, error) {
	o := search.Options{}
	var q []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			o.JSON = true
		case "--re":
			o.Regex = true
		case "--all":
			o.All = true
		case "--no-embed":
			o.NoEmbed = true
		case "--harness", "--project", "--since", "--role":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			default:
				d, err := parseDur(v)
				if err != nil {
					return o, err
				}
				o.Since = d
			}
		default:
			q = append(q, a)
		}
	}
	o.Query = strings.Join(q, " ")
	if o.Query == "" {
		return o, fmt.Errorf("query required")
	}
	return o, nil
}

func parseBlame(args []string) (string, search.BlameOptions, bool, error) {
	o := search.BlameOptions{}
	jsonOutput := false
	var path string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOutput = true
		case "--all":
			o.All = true
		case "--harness", "--project", "--since":
			if i+1 >= len(args) {
				return "", o, false, fmt.Errorf("%s needs value", a)
			}
			i++
			switch a {
			case "--harness":
				o.Harness = args[i]
			case "--project":
				o.Project = args[i]
			case "--since":
				d, err := parseDur(args[i])
				if err != nil {
					return "", o, false, err
				}
				o.Since = d
			}
		default:
			if strings.HasPrefix(a, "-") {
				return "", o, false, fmt.Errorf("blame: unknown flag %q", a)
			}
			if path != "" {
				return "", o, false, fmt.Errorf("blame accepts one path")
			}
			path = a
		}
	}
	if path == "" {
		return "", o, false, fmt.Errorf("blame needs path")
	}
	return path, o, jsonOutput, nil
}

func runBlame(args []string) error {
	path, o, jsonOutput, err := parseBlame(args)
	if err != nil {
		return err
	}
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return err
	}
	hits, err := findBlameHits(target, o, os.Stderr)
	if err != nil {
		return fmt.Errorf("blame search: %w", err)
	}
	if jsonOutput {
		search.PrintBlame(os.Stdout, hits, true)
		return nil
	}
	if len(hits) == 0 {
		fmt.Fprintf(os.Stderr, "deja: no sessions mention %s; run deja index if the index is stale\n", target.Base)
		return nil
	}
	search.PrintBlame(os.Stdout, hits, false)
	return nil
}

func findBlameHits(target search.BlameTarget, o search.BlameOptions, progress io.Writer) ([]search.BlameHit, error) {
	query := search.Options{Query: target.Stem, Harness: o.Harness, Project: o.Project, Since: o.Since, All: true}
	if err := index.EnsureForSearch(index.DefaultDir(), query, false, progress); err != nil {
		return nil, err
	}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), query, progress)
	if err != nil {
		return nil, err
	}
	return search.Blame(result.Sessions, target, o), nil
}
func parseDur(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		return time.Duration(n) * 24 * time.Hour, err
	}
	return time.ParseDuration(s)
}

func printSources() {
	redactions := map[string]int{}
	if stats, err := index.Redactions(index.DefaultDir()); err == nil {
		redactions = stats.Files
	}
	antigravityRoots := sources.AntigravityRoots()
	antigravityLocation := strings.Join(antigravityRoots, string(os.PathListSeparator))
	if antigravityLocation == "" {
		antigravityLocation = filepath.Join(sources.Home(), ".gemini", "antigravity*")
	}
	items := []struct {
		name, location string
		roots          []string
		load           func() []model.Session
	}{
		{"claude", sources.ClaudeRoot(), []string{sources.ClaudeRoot()}, sources.LoadClaude},
		{"codex", sources.CodexRoot(), []string{sources.CodexRoot()}, sources.LoadCodex},
		{"gemini", sources.GeminiRoot(), []string{filepath.Join(sources.GeminiRoot(), "tmp")}, sources.LoadGemini},
		{"cursor", strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, string(os.PathListSeparator)), []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, sources.LoadCursor},
		{"antigravity", antigravityLocation, antigravityRoots, sources.LoadAntigravity},
		{"grok", sources.GrokRoot(), []string{sources.GrokRoot()}, sources.LoadGrok},
		{"qwen", filepath.Join(sources.QwenRoot(), "projects"), []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.LoadQwen},
		{"copilot", sources.CopilotRoot(), []string{sources.CopilotRoot()}, sources.LoadCopilot},
		{"deja", sources.NotesFile(), []string{sources.NotesFile()}, sources.LoadNotes},
	}
	for _, it := range items {
		var size int64
		redacted := 0
		for _, root := range it.roots {
			size += pathSize(root)
			redacted += redactionsUnder(redactions, root)
		}
		raw := it.load()
		ss := sources.FilterSessions(raw)
		excluded := len(raw) - len(ss)
		msg := 0
		for _, s := range ss {
			msg += len(s.Messages)
		}
		note := ""
		if it.name == "cursor" && len(sources.CursorDBs()) > 0 && !sources.SQLite3Available() {
			note = "\t(sqlite3 CLI not found — Cursor IDE sessions unavailable)"
		}
		if n := len(sources.ExclusionPatterns()); n > 0 {
			note += fmt.Sprintf("\texcluded-patterns=%d", n)
		}
		if excluded > 0 {
			note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
		}
		fmt.Printf("%s\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", it.name, it.location, len(ss), msg, humanBytes(size), redacted, note)
	}
	aiderFiles := sources.AiderFiles()
	var aiderSize int64
	aiderRedactions := 0
	for _, p := range aiderFiles {
		if fi, err := os.Stat(p); err == nil {
			aiderSize += fi.Size()
		}
		aiderRedactions += redactions[p]
	}
	rawAiderSessions := sources.LoadAider()
	aiderSessions := sources.FilterSessions(rawAiderSessions)
	aiderMessages := 0
	for _, s := range aiderSessions {
		aiderMessages += len(s.Messages)
	}
	aiderLocation := filepath.Join(sources.Home(), ".aider.chat.history.md")
	if roots := os.Getenv("DEJA_AIDER_ROOTS"); roots != "" {
		aiderLocation += string(os.PathListSeparator) + roots
	}
	note := ""
	if n := len(sources.ExclusionPatterns()); n > 0 {
		note = fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	if excluded := len(rawAiderSessions) - len(aiderSessions); excluded > 0 {
		note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
	}
	fmt.Printf("aider\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", aiderLocation, len(aiderSessions), aiderMessages, humanBytes(aiderSize), aiderRedactions, note)
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, _ := sources.OpencodeCounts()
	note = ""
	if size > 0 && !sources.SQLite3Available() {
		note = "\t(sqlite3 CLI not found — opencode sessions unavailable)"
	}
	if n := len(sources.ExclusionPatterns()); n > 0 {
		note += fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	fmt.Printf("opencode\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", sources.OpencodeDB(), s, m, humanBytes(size), redactions[sources.OpencodeDB()], note)
}

func runForget(args []string) error {
	var o index.ForgetOptions
	list := false
	unforget := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list":
			list = true
		case "--dry-run":
			o.DryRun = true
		case "--session", "--project", "--before", "--unforget":
			if i+1 >= len(args) {
				return fmt.Errorf("forget: %s needs value", args[i])
			}
			i++
			switch args[i-1] {
			case "--session":
				o.Session = args[i]
			case "--project":
				o.Project = args[i]
			case "--unforget":
				unforget = args[i]
			case "--before":
				if d, err := parseDur(args[i]); err == nil {
					o.Before = time.Now().Add(-d)
				} else if t, e := parseForgetDate(args[i]); e == nil {
					o.Before = t
				} else {
					return fmt.Errorf("forget: invalid before %q", args[i])
				}
			}
		default:
			return fmt.Errorf("forget: unknown flag %q", args[i])
		}
	}
	if !list && unforget == "" && o.Session == "" && o.Project == "" && o.Before.IsZero() {
		return fmt.Errorf("forget: selector required")
	}
	if list {
		for _, key := range index.Tombstones() {
			fmt.Fprintln(os.Stdout, key)
		}
		return nil
	}
	if unforget != "" {
		return index.Unforget(unforget)
	}
	result, err := index.Forget(index.DefaultDir(), o)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "sessions dropped: %d\nmessages dropped: %d\ntombstones added: %d\n", result.Sessions, result.Messages, result.Tombstones)
	return nil
}

func parseForgetDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}

func redactionsUnder(files map[string]int, root string) int {
	total := 0
	for p, n := range files {
		if p == root || strings.HasPrefix(p, root+string(os.PathSeparator)) {
			total += n
		}
	}
	return total
}

func pathSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink == 0 && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
func printUsage() {
	fmt.Println(`deja - persistent memory for coding agents

Usage:
  deja [flags] <query>
  deja show <id-prefix>
  deja share <id-prefix>
  deja resume <id-prefix> [--exec]
  deja handoff [--to <agent>] [id-prefix] [--exec]
  deja hook-prompt   (UserPromptSubmit hook: relevance recall per prompt)
  deja ctx <query|id-prefix>
  deja blame <path> [--all] [--json] [--project name] [--harness name] [--since 30d]
  deja sync export <dir> [--full]
  deja sync import <dir>
  deja sync ssh <host> [--pull] [--full]
  deja last [n] [--project name] [--harness name]
  deja sources
  deja completion <bash|zsh|fish>
  deja forget --session <id-prefix> [--project <substring>] [--before <duration|date>] [--dry-run]
  deja forget --list | --unforget <id>
  deja doctor [--json]
  deja warmup
  deja index [--rebuild]
  deja embed
  deja bench recall [--json]
  deja statusline
  deja stats [--json] [--card [path]] [--html [path]]
	deja remember "text" [--project name]
  deja mcp
  deja version
  deja update
  deja install <claude-code|codex|opencode|cursor|gemini|antigravity|grok|qwen|statusline|--all|--auto>
  deja uninstall <claude-code|codex|opencode|cursor|gemini|antigravity|grok|qwen|statusline|--all|--auto>

Examples:
  deja "jwt refresh token bug"
  deja '"connection pool exhausted"'
  deja "exhaustd"  # zero exact results try close spellings
  deja --harness claude --since 30d "panic in indexer"
  deja last 20 --harness codex
  deja last --project api-gateway
  deja --re "timeout|deadline exceeded"
  deja ctx "schema migration rollback" > deja-context.md
  deja install --all

See README.md for the full CLI reference.`)
}
