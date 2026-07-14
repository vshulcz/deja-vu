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

func loadForSearch(o search.Options) []model.Session {
	var ss []model.Session
	if o.Harness == "" || o.Harness == "claude" {
		ss = append(ss, sources.LoadClaude()...)
	}
	if o.Harness == "" || o.Harness == "codex" {
		ss = append(ss, sources.LoadCodex()...)
	}
	if o.Harness == "" || o.Harness == "opencode" {
		if o.Regex {
			ss = append(ss, sources.LoadOpencodeRecent(200)...)
		} else {
			ss = append(ss, sources.LoadOpencodeMatching(o.Query)...)
		}
	}
	return ss
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	if args[0] == "sources" {
		printSources()
		return nil
	}
	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("show needs id-prefix")
		}
		ss := append(loadAll("claude"), loadAll("codex")...)
		ss = append(ss, sources.LoadOpencodePrefix(args[1])...)
		s, ok := search.FindByPrefix(ss, args[1])
		if !ok {
			return fmt.Errorf("no session matches %q", args[1])
		}
		search.PrintSession(os.Stdout, s)
		return nil
	}
	if args[0] == "ctx" {
		if len(args) < 2 {
			return fmt.Errorf("ctx needs query or id-prefix")
		}
		q := strings.Join(args[1:], " ")
		if !strings.Contains(q, " ") && len(q) >= 6 {
			ss := append(loadAll("claude"), loadAll("codex")...)
			ss = append(ss, sources.LoadOpencodePrefix(q)...)
			if s, ok := search.FindByPrefix(ss, q); ok {
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
		ss := append(loadAll("claude"), loadAll("codex")...)
		ss = append(ss, sources.LoadOpencodeRecent(n)...)
		for _, s := range search.Recent(ss, n) {
			fmt.Printf("[%s · %s · %s · %s]\n", s.Harness, s.Project, s.Updated.Format("2006-01-02"), s.ID)
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
		printNoMatches(os.Stderr, o.Query, len(ss))
	}
	search.Print(os.Stdout, hits, o)
	return nil
}

func printNoMatches(w io.Writer, q string, n int) {
	fmt.Fprintf(w, "deja: no matches for %q (searched %d sessions across claude/codex/opencode) — try fewer words or --re\n", q, n)
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
			if a == "--harness" {
				o.Harness = v
			} else if a == "--project" {
				o.Project = v
			} else if a == "--role" {
				o.Role = v
			} else {
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
		fmt.Printf("%s\t%s\tsessions=%d messages=%d size=%s\n", it.name, it.root, len(ss), msg, humanBytes(size))
	}
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, _ := sources.OpencodeCounts()
	fmt.Printf("opencode\t%s\tsessions=%d messages=%d size=%s\n", sources.OpencodeDB(), s, m, humanBytes(size))
}

func pathSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
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
	fmt.Println("usage: deja [--json] [--re] [--rebuild] [--harness name] [--project p] [--since 30d] [--role user] <query>\n       deja ctx <query|id-prefix>\n       deja show <id-prefix>\n       deja last [n]\n       deja sources")
}
