package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	return ss
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
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
	if args[0] == "warmup" {
		return index.Ensure(index.DefaultDir(), "", false, os.Stderr)
	}
	if args[0] == "stats" {
		return runStats(args[1:])
	}
	if args[0] == "mcp" {
		return serveMCP(os.Stdin, os.Stdout)
	}
	if args[0] == "hook-context" {
		_ = runHookContext()
		return nil
	}
	if args[0] == "install" {
		return runInstall(args[1:], false)
	}
	if args[0] == "uninstall" {
		return runInstall(args[1:], true)
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
		ss, err := index.Search(index.DefaultDir(), o)
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
	if args[0] == "last" {
		n := 10
		if len(args) > 1 {
			if x, err := strconv.Atoi(args[1]); err == nil {
				n = x
			}
		}
		ss, err := recent(n)
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
	if err := index.EnsureForSearch(index.DefaultDir(), o, force, os.Stderr); err != nil {
		return fmt.Errorf("ensure: %w", err)
	}
	ss, err := index.Search(index.DefaultDir(), o)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	if len(hits) == 0 {
		printNoMatches(os.Stderr, o.Query, len(ss))
	}
	search.Print(os.Stdout, hits, o)
	return nil
}

func printNoMatches(w io.Writer, q string, n int) {
	fmt.Fprintf(w, "deja: no matches for %q (searched %d sessions across claude/codex/opencode) — try fewer words or --re\n", q, n)
}

func findByPrefix(p string) (model.Session, bool, error) {
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err == nil {
		if s, ok, err := index.FindByPrefix(index.DefaultDir(), p); err == nil {
			return s, ok, nil
		}
	}
	ss := append(loadAll("claude"), loadAll("codex")...)
	ss = append(ss, sources.LoadOpencodePrefix(p)...)
	s, ok := search.FindByPrefix(ss, p)
	return s, ok, nil
}

func recent(n int) ([]model.Session, error) {
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err == nil {
		if ss, err := index.Recent(index.DefaultDir(), n); err == nil {
			return ss, nil
		}
	}
	ss := append(loadAll("claude"), loadAll("codex")...)
	ss = append(ss, sources.LoadOpencodeRecent(n)...)
	return search.Recent(ss, n), nil
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
	items := []struct {
		name, root string
		load       func() []model.Session
	}{{"claude", sources.ClaudeRoot(), sources.LoadClaude}, {"codex", sources.CodexRoot(), sources.LoadCodex}}
	for _, it := range items {
		size := pathSize(it.root)
		ss := it.load()
		msg := 0
		for _, s := range ss {
			msg += len(s.Messages)
		}
		fmt.Printf("%s\t%s\tsessions=%d messages=%d size=%s redacted=%d\n", it.name, it.root, len(ss), msg, humanBytes(size), redactionsUnder(redactions, it.root))
	}
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, _ := sources.OpencodeCounts()
	fmt.Printf("opencode\t%s\tsessions=%d messages=%d size=%s redacted=%d\n", sources.OpencodeDB(), s, m, humanBytes(size), redactions[sources.OpencodeDB()])
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
func usage() {
	fmt.Println(`deja - persistent memory for coding agents

Usage:
  deja [flags] <query>
  deja show <id-prefix>
  deja share <id-prefix>
  deja ctx <query|id-prefix>
  deja sync export <dir> [--full]
  deja sync import <dir>
  deja last [n]
  deja sources
  deja warmup
  deja stats [--json]
  deja mcp
  deja version
  deja install <claude-code|codex|opencode|--all>
  deja uninstall <claude-code|codex|opencode|--all>

Examples:
  deja "jwt refresh token bug"
  deja --harness claude --since 30d "panic in indexer"
  deja --re "timeout|deadline exceeded"
  deja ctx "schema migration rollback" > /tmp/deja-context.md
  deja install --all

See README.md for the full CLI reference.`)
}
