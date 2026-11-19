package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/docsdate"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// "capped at 1,536 bytes per prompt and about 4 KB per tool call" is on
// twenty-odd pages, once in the prose and once inside the FAQ block, and every
// copy is hand-written. The numbers are right today; the reason to pin them is
// the four claims that were not (#3779, #3781): a number nobody checks is a
// number that drifts, and here it would drift on every per-harness page at
// once.
func TestTheInjectionBudgetsThePagesQuoteAreTheOnesInTheCode(t *testing.T) {
	root := filepath.Join("..", "..")
	wantPrompt := fmt.Sprintf("%d,%03d bytes per prompt", promptHookBudget/1000, promptHookBudget%1000)
	wantTool := fmt.Sprintf("about %d KB per tool call", recallMCPBudget/1024)

	var pages []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext == ".html" || ext == ".txt" || ext == ".md" {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	prompt := regexp.MustCompile(`[\d,]+ bytes per prompt`)
	tool := regexp.MustCompile(`about [\d.]+ [KM]B per tool call`)
	quoting := 0
	for _, path := range pages {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		for _, got := range prompt.FindAllString(text, -1) {
			quoting++
			if got != wantPrompt {
				t.Errorf("%s says %q; the hook budget is %d bytes (%q)", name, got, promptHookBudget, wantPrompt)
			}
		}
		for _, got := range tool.FindAllString(text, -1) {
			if !strings.EqualFold(got, wantTool) {
				t.Errorf("%s says %q; the tool-call budget is %d bytes (%q)", name, got, recallMCPBudget, wantTool)
			}
		}
	}
	if quoting < 5 {
		t.Fatalf("only %d pages quote the prompt budget; this test is looking for the wrong phrase", quoting)
	}
}

// The commands page is checked in one direction — every real command has a row
// (#3356) — and nothing checked the other: a document can tell a reader to run
// something the binary would refuse. `deja install dsh-auto` was exactly that
// until #3869, printed on a guide page and rejected by the dispatcher.
//
// The first word after `deja` is a command position only when it is not a
// query: an unknown word is a search by design, so `deja connection pool` is a
// legitimate example and no test can tell it from a typo. A flag after it
// settles that — `deja stats --card` is an invocation, not a query — so this
// takes every documented invocation carrying a flag and requires the
// dispatcher to know its command.
func TestEveryFlaggedInvocationInTheDocsNamesARealCommand(t *testing.T) {
	root := filepath.Join("..", "..")
	files := shippedDocFiles(t, root)
	if len(files) < 80 {
		t.Fatalf("found %d documents to read — the walk is wrong", len(files))
	}

	found := map[string]bool{}
	var offenders []string
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		for _, name := range flaggedCommandNames(string(b)) {
			found[name] = true
			if dispatchKnows(name) {
				continue
			}
			offenders = append(offenders, name+" in "+filepath.ToSlash(rel))
		}
	}
	// The floor is the part that keeps this from passing because the walk
	// stopped finding anything: 21 commands are documented with a flag today.
	if len(found) < 15 {
		t.Fatalf("only %d commands are documented with a flag (%v) — the pattern is wrong", len(found), sortedNames(found))
	}
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("a document says to run `deja %s`, which the dispatcher does not know", o)
	}
}

// shippedDocFiles is every document a reader or an agent acts on: the site, the
// guides, the READMEs, and the skill and command files deja installs into a
// harness. The changelog is not one of them — it quotes output and mistyped
// commands on purpose, which is the opposite of an instruction.
func shippedDocFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"docs", "claude-plugin", "codex-plugin", "npm", "."} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", ".claude":
					return filepath.SkipDir
				}
				// The repository root is read for its own files only; the
				// directories that matter have their own entry above.
				if dir == "." && p != base {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Base(p) == "CHANGELOG.md" {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".md", ".html", ".txt":
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// A code span, a `<code>` element, a shell prompt or the start of a line in a
// block, followed within three words by a flag.
var flaggedDeja = regexp.MustCompile("(?m)(?:<code>|`|\\$ |^[ \t]*)deja ([a-z][a-z0-9-]*)((?: +[^\\s<`]+){0,3})")

func flaggedCommandNames(body string) []string {
	var out []string
	for _, m := range flaggedDeja.FindAllStringSubmatch(body, -1) {
		if !strings.Contains(m[2], "--") {
			continue
		}
		out = append(out, m[1])
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// compare.html names ten other projects and used to link two of them (#3854).
// Pinning the pair means a column cannot quietly end up pointing at a
// neighbouring project the way CASS did on the day-zero page (#3845) — several of
// these names are taken more than once on GitHub.
func TestCompareLinksEveryProjectItNames(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "compare.html"))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"Mem0":           "mem0ai/mem0",
		"Letta":          "letta-ai/letta",
		"memU":           "NevaMind-AI/memU",
		"Memori":         "MemoriLabs/Memori",
		"claude-mem":     "thedotmack/claude-mem",
		"agentmemory":    "rohitg00/agentmemory",
		"MemPalace":      "MemPalace/mempalace",
		"cognee":         "topoteretes/cognee",
		"Graphiti / Zep": "getzep/graphiti",
		"Hindsight":      "vectorize-io/hindsight",
		"ai-memory":      "akitaonrails/ai-memory",
		"basic-memory":   "basicmachines-co/basic-memory",

		"cass":            "Dicklesworthstone/coding_agent_session_search",
		"Agent Sessions":  "jazzyalex/agent-sessions",
		"agent-historian": "adlternative/agent-historian",
		"casr":            "Dicklesworthstone/cross_agent_session_resumer",
		// The column was "Agent Session Viewer", archived in February before
		// the table was written; this is its Go rewrite (#3865).
		"agentsview": "kenn-io/agentsview",
	}

	rows := regexp.MustCompile(`(?s)<tr[^>]*><th[^>]*></th><th[^>]*class="us"[^>]*>deja-vu</th>(.*?)</tr>`).FindAllSubmatch(page, -1)
	if len(rows) != 3 {
		t.Fatalf("found %d comparison-table header rows, want 3", len(rows))
	}

	cell := regexp.MustCompile(`(?s)<th[^>]*>(.*?)</th>`)
	href := regexp.MustCompile(`<a href="https://github\.com/([^"]+)"[^>]*>(.*?)</a>`)
	seen := map[string]bool{}
	for _, row := range rows {
		for _, c := range cell.FindAllSubmatch(row[1], -1) {
			got := href.FindSubmatch(c[1])
			if got == nil {
				t.Errorf("column %q does not link a github repository", strings.TrimSpace(string(c[1])))
				continue
			}
			name := strings.TrimSpace(string(got[2]))
			repo := strings.Trim(string(got[1]), "/")
			seen[name] = true
			if w, ok := want[name]; !ok {
				t.Errorf("column %q is new here; add the repository it belongs to", name)
			} else if repo != w {
				t.Errorf("%s links %s, want %s", name, repo, w)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the tables no longer have a %s column", name)
		}
	}
}

// compare.html put our own harness count in a cell of its own, with the noun
// ("Agents read", "Coding agents") in the row label next to it, so
// TestPagesCountHarnessesInDigitsCorrectly — which matches a number followed by
// "agents" or "harnesses" in one run of text — could never see it. It said 24
// and 25 while the registry held 34, on the page where the column beside ours
// says "25 providers" (#3855).
func TestCompareCountsTheHarnessesTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	page, err := os.ReadFile(filepath.Join(root, "docs", "guide", "compare.html"))
	if err != nil {
		t.Fatal(err)
	}

	rows := regexp.MustCompile(`<tr><td>(Agents read|Coding agents)</td><td class="us">(\d+)`).FindAllSubmatch(page, -1)
	if len(rows) != 2 {
		t.Fatalf("found %d rows stating deja's own agent count, want 2", len(rows))
	}
	for _, row := range rows {
		got, err := strconv.Atoi(string(row[2]))
		if err != nil {
			t.Fatal(err)
		}
		if got != n {
			t.Errorf("the %q row says %d; the registry has %d harnesses", row[1], got, n)
		}
	}
}

// The prose under the third table quotes funes and ctx figures from the
// day-zero page. When day-zero moved to funes 1.3.2 and ctx 1.4.12 (#3858) the
// copies here kept the old versions' numbers, including a funes lead at rank
// five that the new measurement does not have (#3865). The quotes are read
// against the day-zero table rather than hard-coded.
func TestCompareQuotesTheDayZeroTable(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	day, cmp := read("day-zero.html"), read("compare.html")

	// columns: deja, ctx
	cells := func(label string) []string {
		m := regexp.MustCompile(`<tr><td>` + regexp.QuoteMeta(label) + `</td><td class="us y">(\d+)</td><td>(\d+)</td>`).FindStringSubmatch(day)
		if m == nil {
			t.Fatalf("day-zero.html has no %q row in the expected shape", label)
		}
		return m[1:]
	}
	hit1, found := cells("hit@1 / 100"), cells("found@50 / 100")

	for _, want := range []string{
		"hit@1 " + hit1[1] + ", found@50 " + found[1],
	} {
		if !strings.Contains(cmp, want) {
			t.Errorf("compare.html should say %q, from the day-zero table", want)
		}
	}
}

// The benchmarks page and the day-zero page publish hit@1 for the same dataset
// four and a half times apart — per-question haystacks against all 19,195
// sessions at once — and each now names the other's figure so the gap reads as
// a setup and not a contradiction (#3851). Those quotes are copies, so they are
// checked against the numbers they copy.
func TestDayZeroAndBenchmarksQuoteEachOther(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	bench, day := read("benchmarks.html"), read("day-zero.html")

	lme := regexp.MustCompile(`<b>LongMemEval-S</b> \(session-level retrieval[^)]*\): <b>hit@1 ([\d.]+)%</b>`).FindStringSubmatch(bench)
	if lme == nil {
		t.Fatal("benchmarks.html no longer states LongMemEval-S hit@1")
	}
	if !regexp.MustCompile(`deja's hit@1 is ` + regexp.QuoteMeta(lme[1]) + `%`).MatchString(day) {
		t.Errorf("day-zero.html should quote the benchmark's hit@1 of %s%%", lme[1])
	}

	row := regexp.MustCompile(`<tr><td>hit@1 / 100</td><td class="us y">(\d+)</td>`).FindStringSubmatch(day)
	if row == nil {
		t.Fatal("day-zero.html no longer has deja's hit@1 cell")
	}
	if !regexp.MustCompile(`lands at ` + row[1] + `% hit@1`).MatchString(bench) {
		t.Errorf("benchmarks.html should quote day zero's hit@1 of %s%%", row[1])
	}

	// The corpus is LongMemEval's assistant chats in coding-agent file layouts,
	// not coding work (#3860).
	if regexp.MustCompile(`coding-agent sessions`).MatchString(day) {
		t.Error(`day-zero.html calls its corpus "coding-agent sessions"; it is general chat in coding-agent file layouts`)
	}
}

// The day-zero table asks a reader to trust six rows of numbers about other
// people's software, so each of those tools names the repository that was
// measured (#3839). Several of the names are taken more than once on GitHub —
// there is more than one "ctx" and more than one "funes" — and the row is only
// checkable if it says which one.
func TestDayZeroLinksEveryToolItCompares(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "guide", "day-zero.html")
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	row := regexp.MustCompile(`(?s)<tr[^>]*><th[^>]*></th><th[^>]*class="us"[^>]*>deja-vu</th>(.*?)</tr>`).FindSubmatch(page)
	if row == nil {
		t.Fatalf("no comparison-table header row in %s", path)
	}
	cells := regexp.MustCompile(`(?s)<th[^>]*>(.*?)</th>`).FindAllSubmatch(row[1], -1)
	if len(cells) < 6 {
		t.Fatalf("header names %d tools besides deja-vu, want at least 6", len(cells))
	}

	// The repository each name belongs to, checked against what the page says
	// about it: CASS was linked to cass_memory_system, another project by the
	// same author, while the version and issue the footnote cites — 0.7.1 and
	// #441 — only exist in coding_agent_session_search.
	want := map[string]string{
		"ctx":         "ctxrs/ctx",
		"CASS":        "Dicklesworthstone/coding_agent_session_search",
		"agentsview":  "kenn-io/agentsview",
		"agentmemory": "rohitg00/agentmemory",
		"MemPalace":   "MemPalace/mempalace",
		"claude-mem":  "thedotmack/claude-mem",
	}

	href := regexp.MustCompile(`<a href="(https://github\.com/[^"]+)"[^>]*>([^<]+)</a>`)
	seen := map[string]bool{}
	for _, cell := range cells {
		got := href.FindSubmatch(cell[1])
		if got == nil {
			t.Errorf("column %q does not link a github repository", strings.TrimSpace(string(cell[1])))
			continue
		}
		name := strings.TrimSpace(string(got[2]))
		repo := strings.Trim(strings.TrimPrefix(string(got[1]), "https://github.com/"), "/")
		if strings.Count(repo, "/") != 1 {
			t.Errorf("%s is not an owner/repo url", got[1])
			continue
		}
		seen[name] = true
		if w, ok := want[name]; !ok {
			t.Errorf("column %q is new here; add the repository it belongs to", name)
		} else if repo != w {
			t.Errorf("%s links %s, want %s", name, repo, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the table no longer has a %s column", name)
		}
	}
}

// The day-zero page hands the reader one command and says it produces the deja
// row. It did not: without -corpus the pile is the haystacks of the scored
// questions alone — 4,604 sessions, not the 19,195 the page is about, and every
// number in the column moves (#3847). Two files carry that command, and both
// claim 19,195 sessions under it.
func TestTheDayZeroCommandLaysDownTheCorpusThePageReports(t *testing.T) {
	root := filepath.Join("..", "..")
	carriers := []string{
		filepath.Join("docs", "guide", "day-zero.html"),
		filepath.Join("scripts", "day0compare", "README.md"),
	}
	invocation := regexp.MustCompile(`go run \./scripts/day0bench[^<\n]*`)

	for _, rel := range carriers {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		cmd := invocation.Find(body)
		if cmd == nil {
			t.Errorf("%s no longer shows a day0bench command", rel)
			continue
		}
		for _, flag := range []string{"-data ", "-limit 100", "-corpus 500", "-keep "} {
			if !strings.Contains(string(cmd), flag) {
				t.Errorf("%s documents a command missing %q: %s", rel, flag, cmd)
			}
		}
	}

	page, err := os.ReadFile(filepath.Join(root, "docs", "guide", "day-zero.html"))
	if err != nil {
		t.Fatal(err)
	}

	// LongMemEval-S is 500 questions, so -corpus 500 is the whole set and the
	// only value that reaches the session count the page states everywhere.
	if !strings.Contains(string(page), "19,195 sessions") {
		t.Error("the page no longer says 19,195 sessions; the -corpus value above needs rechecking")
	}
}

// docs/feed.xml is what WebSub publishes: Google takes no ping and no IndexNow,
// and the feed is the only push it documents. A feed that disagrees with the
// sitemap, or names a page that is not served, announces a 404 — and the failure
// is silent, because a hub reports success for fetching the feed either way.
func TestFeedAgreesWithTheSitemap(t *testing.T) {
	root := filepath.Join("..", "..")

	var sm struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
		} `xml:"url"`
	}
	read(t, filepath.Join(root, "docs", "sitemap.xml"), &sm)

	var feed struct {
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Updated string `xml:"updated"`
		Entries []struct {
			Title   string `xml:"title"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
		} `xml:"entry"`
	}
	read(t, filepath.Join(root, "docs", "feed.xml"), &feed)

	if len(feed.Entries) == 0 {
		t.Fatal("docs/feed.xml has no entries — run `go run ./scripts/genfeed`")
	}

	// A hub needs both: rel=self to know what it is fetching, rel=hub to be
	// the hub at all. Without either, publishing is a no-op that reports success.
	rel := map[string]string{}
	for _, l := range feed.Links {
		rel[l.Rel] = l.Href
	}
	if rel["self"] != "https://vshulcz.github.io/deja-vu/feed.xml" {
		t.Errorf("rel=self is %q, and a hub fetches that URL", rel["self"])
	}
	if rel["hub"] == "" {
		t.Error("the feed names no hub, so nothing can be published from it")
	}

	day := map[string]string{}
	for _, u := range sm.URLs {
		day[strings.TrimSpace(u.Loc)] = strings.TrimSpace(u.Lastmod)
	}
	for _, e := range feed.Entries {
		want, listed := day[e.ID]
		if !listed {
			t.Errorf("%s is in the feed and not in the sitemap, so it may not be served at all", e.ID)
			continue
		}
		if e.Updated != want+"T00:00:00Z" {
			t.Errorf("%s is dated %s in the feed and %s in the sitemap", e.ID, e.Updated, want)
		}
		if e.Title == "" {
			t.Errorf("%s has no title in the feed", e.ID)
		}
		page := strings.TrimPrefix(e.ID, "https://vshulcz.github.io/deja-vu/")
		if page == "" || strings.HasSuffix(page, "/") {
			page += "index.html"
		}
		if _, err := os.Stat(filepath.Join(root, "docs", filepath.FromSlash(page))); err != nil {
			t.Errorf("%s is in the feed and docs/%s is not there", e.ID, page)
		}
	}

	// Newest first is the whole point of the feed; a subscriber reads the top.
	for i := 1; i < len(feed.Entries); i++ {
		if feed.Entries[i-1].Updated < feed.Entries[i].Updated {
			t.Errorf("entry %d is older than the one after it — run `go run ./scripts/genfeed`", i-1)
			break
		}
	}
	if feed.Updated != feed.Entries[0].Updated {
		t.Errorf("the feed says it changed %s and its newest entry says %s", feed.Updated, feed.Entries[0].Updated)
	}

	// The page has to point at the feed or nothing discovers it.
	b, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `type="application/atom+xml"`) {
		t.Error("docs/index.html does not link the feed, so only something told about it finds it")
	}
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The check above runs feed → sitemap, so a feed missing a page passes it: the
// entries it does carry are all fine. That is how docs/feed.xml went a release
// without a page the sitemap had listed for two days, while still naming a page
// that had been deleted — nothing read the sitemap side. This runs the other
// direction, which is genfeed's whole contract: the feed is the newest forty
// pages the repository serves, newest first, ties broken by URL.
func TestTheFeedIsTheNewestPagesTheSitemapLists(t *testing.T) {
	root := filepath.Join("..", "..")

	var sm struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
		} `xml:"url"`
	}
	read(t, filepath.Join(root, "docs", "sitemap.xml"), &sm)

	type page struct{ loc, day string }
	var served []page
	for _, u := range sm.URLs {
		loc, day := strings.TrimSpace(u.Loc), strings.TrimSpace(u.Lastmod)
		if loc == "" || day == "" {
			continue
		}
		// genfeed keeps a URL only when the file behind it is a page with a
		// title; a redirect stub or a URL with nothing on disk is not one.
		rel := strings.TrimPrefix(loc, "https://vshulcz.github.io/deja-vu/")
		if rel == "" || strings.HasSuffix(rel, "/") {
			rel += "index.html"
		}
		b, err := os.ReadFile(filepath.Join(root, "docs", filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		// The same test genfeed applies: the title's content, not the tag.
		// An empty <title></title> is a page it drops and this would keep.
		m := feedTitle.FindSubmatch(b)
		if len(m) < 2 || strings.TrimSpace(string(m[1])) == "" {
			continue
		}
		served = append(served, page{loc, day})
	}
	sort.SliceStable(served, func(i, j int) bool {
		if served[i].day != served[j].day {
			return served[i].day > served[j].day
		}
		return served[i].loc < served[j].loc
	})
	const entries = 40
	if len(served) > entries {
		served = served[:entries]
	}

	var feed struct {
		Entries []struct {
			ID string `xml:"id"`
		} `xml:"entry"`
	}
	read(t, filepath.Join(root, "docs", "feed.xml"), &feed)

	if len(feed.Entries) != len(served) {
		t.Fatalf("the feed carries %d entries and the sitemap serves %d — run `go run ./scripts/genfeed`",
			len(feed.Entries), len(served))
	}
	for i, want := range served {
		if got := feed.Entries[i].ID; got != want.loc {
			t.Errorf("entry %d is %s and the sitemap puts %s (%s) there — run `go run ./scripts/genfeed`",
				i, got, want.loc, want.day)
		}
	}
}

// feedTitle is scripts/genfeed's own title expression.
var feedTitle = regexp.MustCompile(`(?s)<title>(.*?)</title>`)

// A translated page is only a translation to a search engine if both pages say
// so: hreflang declared on one side alone is discarded, and the two pages then
// compete as duplicates of each other. The Chinese pages carried the pair from
// the start; the English ones did not, so the first three translations pointed
// at counterparts that never pointed back.
//
// The same check covers the sitemap, because a page no sitemap lists is not
// offered to anyone at all.
func TestChinesePagesArePairedWithTheirEnglishOnes(t *testing.T) {
	root := filepath.Join("..", "..")
	const site = "https://vshulcz.github.io/deja-vu/"

	sitemap, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dir      string
		hreflang string
	}{
		{"zh", "zh-Hans"},
		{"ru", "ru"},
	} {
		var pages []string
		err = filepath.Walk(filepath.Join(root, "docs", tc.dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
				return err
			}
			pages = append(pages, p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) == 0 {
			t.Fatalf("no %s pages under docs/%s — this test has lost its subject", tc.dir, tc.dir)
		}

		for _, loc := range pages {
			rel := filepath.ToSlash(strings.TrimPrefix(loc, filepath.Join(root, "docs")+string(filepath.Separator)))
			en := filepath.Join(root, "docs", filepath.FromSlash(strings.TrimPrefix(rel, tc.dir+"/")))

			locURL := site + rel
			enURL := site + strings.TrimPrefix(rel, tc.dir+"/")

			locBody, err := os.ReadFile(loc)
			if err != nil {
				t.Fatal(err)
			}
			enBody, err := os.ReadFile(en)
			if err != nil {
				t.Errorf("%s has no English counterpart at %s: %v", rel, en, err)
				continue
			}

			for _, want := range []struct{ file, link string }{
				{loc, `hreflang="` + tc.hreflang + `" href="` + locURL + `"`},
				{loc, `hreflang="en" href="` + enURL + `"`},
				{en, `hreflang="` + tc.hreflang + `" href="` + locURL + `"`},
				{en, `hreflang="en" href="` + enURL + `"`},
			} {
				body := locBody
				if want.file == en {
					body = enBody
				}
				if !strings.Contains(string(body), want.link) {
					t.Errorf("%s does not declare %s — hreflang has to be on both pages or it is ignored",
						strings.TrimPrefix(want.file, root+string(filepath.Separator)), want.link)
				}
			}

			for _, u := range []string{locURL, enURL} {
				if !strings.Contains(string(sitemap), "<loc>"+u+"</loc>") {
					t.Errorf("docs/sitemap.xml does not list %s", u)
				}
			}
		}
	}
}

// Every image a page asks for has to be in the published tree. The site is
// served from docs/, and the repository keeps a second copy of the demo assets
// at the root for the README — so an asset added for a page and committed only
// to the root one renders as a broken image on the site and nowhere else. That
// is what happened to the blame recording: `assets/blame.gif` existed, the page
// asked for `../assets/blame.gif`, and nothing failed.
func TestEveryImageThePagesAskForExists(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	img := regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // the test walks the repo checkout
		if err != nil {
			return err
		}
		for _, m := range img.FindAllStringSubmatch(string(b), -1) {
			src := m[1]
			// Remote and inline images are somebody else's problem.
			if strings.Contains(src, "://") || strings.HasPrefix(src, "data:") {
				continue
			}
			src, _, _ = strings.Cut(src, "?")
			src, _, _ = strings.Cut(src, "#")
			var want string
			if after0, ok := strings.CutPrefix(src, "/"); ok {
				// Absolute on the published site, which is served under /deja-vu/.
				want = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(after0, "deja-vu/")))
			} else {
				want = filepath.Join(filepath.Dir(path), filepath.FromSlash(src))
			}
			checked++
			if _, err := os.Stat(want); err != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s asks for %s, which is not in the published tree", filepath.ToSlash(rel), src)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no page image was checked, so this test proves nothing")
	}
}

// Two pages sat in the sitemap with nothing on the site linking to them (#3840,
// #3841): one was left out of a sidebar copied onto 58 pages, the other was a
// utility the pages documenting its flag never mentioned. A sitemap entry that
// no page links is the weakest thing a sitemap can hold, and nothing noticed
// either for weeks, so the crawl is a test.
func TestEverySitemapPageIsReachableFromTheIndex(t *testing.T) {
	docs := filepath.Join("..", "..", "docs")
	const site = "https://vshulcz.github.io/deja-vu/"

	sitemap, err := os.ReadFile(filepath.Join(docs, "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	rel := func(u string) string {
		p := strings.TrimPrefix(u, site)
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		return p
	}
	var listed []string
	for _, m := range regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(string(sitemap), -1) {
		if strings.HasPrefix(m[1], site) {
			listed = append(listed, rel(m[1]))
		}
	}
	if len(listed) < 100 {
		t.Fatalf("sitemap lists %d pages under %s, want at least 100", len(listed), site)
	}

	// Only <a> links count. Two zh pages were reachable here only through the
	// hreflang <link> of their English twins, and Google listed them as having
	// no incoming link at all.
	href := regexp.MustCompile(`<a\b[^>]*\bhref="([^"]+)"`)
	links := func(page string) []string {
		body, err := os.ReadFile(filepath.Join(docs, page))
		if err != nil {
			return nil
		}
		var out []string
		for _, m := range href.FindAllStringSubmatch(string(body), -1) {
			h := m[1]
			if strings.HasPrefix(h, site) {
				out = append(out, rel(h))
				continue
			}
			if strings.Contains(h, "://") || strings.HasPrefix(h, "#") || strings.HasPrefix(h, "//") ||
				strings.HasPrefix(h, "mailto:") || strings.HasPrefix(h, "data:") {
				continue
			}
			h = strings.SplitN(strings.SplitN(h, "#", 2)[0], "?", 2)[0]
			if h == "" {
				continue
			}
			if strings.HasSuffix(h, "/") {
				h += "index.html"
			}
			out = append(out, filepath.ToSlash(filepath.Join(filepath.Dir(page), h)))
		}
		return out
	}

	seen := map[string]bool{"index.html": true}
	for queue := []string{"index.html"}; len(queue) > 0; {
		page := queue[0]
		queue = queue[1:]
		for _, next := range links(page) {
			if seen[next] || filepath.Ext(next) != ".html" {
				continue
			}
			if _, err := os.Stat(filepath.Join(docs, next)); err != nil {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}

	var orphans []string
	for _, page := range listed {
		if !seen[page] {
			orphans = append(orphans, page)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d of the %d sitemap pages are linked from nowhere: %s",
			len(orphans), len(listed), strings.Join(orphans, ", "))
	}
}

// The guide pages share one sidebar, one head shape and one sitemap. Each is
// hand-written HTML, so the way they drift is silent: a page that is not in the
// sitemap is a page search engines never fetch, a page whose sidebar forgets a
// sibling is a page readers cannot leave, and a page marking two entries as
// current is one nobody notices until it looks wrong in a screenshot.
func TestGuidePagesShareTheirNavigation(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var pages []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			pages = append(pages, e.Name())
		}
	}
	if len(pages) < 8 {
		t.Fatalf("only %d guide pages found; the glob is wrong", len(pages))
	}

	sitemap, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}

	link := regexp.MustCompile(`href="([a-z0-9-]+\.html)"`)
	known := map[string]bool{}
	for _, p := range pages {
		known[p] = true
	}

	for _, name := range pages {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		page := string(b)

		if n := strings.Count(page, `aria-current="page"`); n != 1 {
			t.Errorf("%s marks %d sidebar entries as current, want exactly one", name, n)
		}
		selfCanonical := `<link rel="canonical" href="https://vshulcz.github.io/deja-vu/guide/` + name + `">`
		loc := `<loc>https://vshulcz.github.io/deja-vu/guide/` + name + `</loc>`
		inSitemap := strings.Contains(string(sitemap), loc)
		switch {
		case !strings.Contains(page, `<link rel="canonical"`):
			t.Errorf("%s has no canonical link", name)
		case strings.Contains(page, selfCanonical) && !inSitemap:
			t.Errorf("%s is not in docs/sitemap.xml, so it is a page nobody is sent to", name)
		case !strings.Contains(page, selfCanonical) && inSitemap:
			t.Errorf("%s is canonicalized elsewhere and must not be listed in docs/sitemap.xml", name)
		}
		for _, m := range link.FindAllStringSubmatch(page, -1) {
			if !known[m[1]] {
				t.Errorf("%s links to %s, which is not a guide page", name, m[1])
			}
		}
		// Every page has to reach the problem pages, which are the ones people
		// arrive on: a sidebar that lists only the product pages sends a
		// first-time reader back to the search results.
		for _, sibling := range []string{
			"forgetting.html",
			"where-sessions-are-stored.html",
			"after-compaction.html",
			"repeated-mistakes.html",
			"switching-agents.html",
			"export-conversations.html",
			"sync-across-machines.html",
			"token-cost.html",
			"rules-files.html",
			"auditing-agents.html",
			"find-a-session.html",
			"parallel-sessions.html",
			"memory-for-opencode.html",
			"memory-for-dsh.html",
			"memory-for-kimi.html",
			"memory-for-zed.html",
			"memory-for-grok.html",
			// The four biggest audiences arrive on these, so a sidebar that
			// omits one strands the reader who came from a search for it.
			"memory-for-claude-code.html",
			"memory-for-codex.html",
			"memory-for-cursor.html",
			"memory-for-copilot.html",
		} {
			if name == sibling {
				continue
			}
			if !strings.Contains(page, `href="`+sibling+`"`) {
				t.Errorf("%s does not link to %s", name, sibling)
			}
		}
	}
}

// sitemap.txt is the same list as sitemap.xml, one URL per line. Search Console
// reported the XML as "could not be processed" with zero URLs found, twice,
// while the file validated against the sitemaps.org schema and Googlebot's own
// user agent fetched it whole. A text sitemap has nothing to parse, so it is
// the second way in — and it goes stale the moment someone adds a page to only
// one of the two.
func TestTextSitemapMatchesTheXMLOne(t *testing.T) {
	root := filepath.Join("..", "..")
	xml, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	txt, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.txt"))
	if err != nil {
		t.Fatal(err)
	}
	locs := regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(string(xml), -1)
	want := make([]string, 0, len(locs))
	for _, m := range locs {
		want = append(want, m[1])
	}
	got := strings.Split(strings.TrimSpace(string(txt)), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("docs/sitemap.txt lists %d URLs and docs/sitemap.xml %d, or in a different order — regenerate the text one from the XML", len(got), len(want))
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "https://vshulcz.github.io/deja-vu/") || strings.ContainsAny(u, " \t") {
			t.Errorf("docs/sitemap.txt has a line that is not one of our URLs: %q", u)
		}
	}
}

// lastmod is what a crawler schedules the next fetch from, so a date that stops
// following the file turns into a page nobody re-reads. It used to be written
// once, when the entry first appeared, and 102 of the 107 URLs advertised a day
// up to nine weeks before their page last changed. `go run ./scripts/genregistry`
// takes the date from git now, and this holds it there.
//
// The comparison allows a week of slack: a squash merge re-dates the commit that
// carries the page, so an exact match would fail on main the day after a docs
// change lands.
func TestSitemapLastmodFollowsThePages(t *testing.T) {
	root := filepath.Join("..", "..")
	if out, err := exec.CommandContext(t.Context(), "git", "-C", root, "rev-parse", "--is-shallow-repository").Output(); err != nil {
		t.Skipf("no git history to compare against: %v", err)
	} else if strings.TrimSpace(string(out)) == "true" {
		t.Skip("shallow clone: every file looks like it changed in the tip commit")
	}

	b, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`<loc>https://vshulcz\.github\.io/deja-vu/([^<]*)</loc>(?:<lastmod>([^<]*)</lastmod>)?`)
	found := entry.FindAllStringSubmatch(string(b), -1)
	if len(found) < 100 {
		t.Fatalf("only %d URLs matched in docs/sitemap.xml; the pattern is wrong", len(found))
	}

	dater, err := docsdate.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dater.Close() }()

	const slack = 7 * 24 * time.Hour
	for _, m := range found {
		loc, lastmod := m[1], m[2]
		named := loc
		if named == "" {
			named = "/"
		}
		served := loc
		if served == "" || strings.HasSuffix(served, "/") {
			served += "index.html"
		}
		// git wants forward slashes in a pathspec on every platform; only the
		// stat below goes through the OS separator.
		rel := "docs/" + served
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			continue // served from somewhere this test cannot see
		}
		if lastmod == "" {
			t.Errorf("%s has no lastmod — run `go run ./scripts/genregistry`", named)
			continue
		}
		said, err := time.Parse("2006-01-02", lastmod)
		if err != nil {
			t.Errorf("%s has lastmod %q, which is not a date", named, lastmod)
			continue
		}
		// The day the page's content last changed, the way genregistry dates
		// it: a commit that only rewrote the sidebar does not count.
		day, err := dater.Day(rel)
		if err != nil {
			t.Fatalf("dating %s: %v", loc, err)
		}
		if day == "" {
			continue
		}
		changed, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatalf("docsdate returned %q for %s", day, loc)
		}
		// Both directions: a date behind the file is the drift this catches, and
		// a date ahead of it is the same lie told the other way round — a page
		// that says it changed tomorrow is one a crawler is told to come back
		// for and finds nothing new.
		if drift := changed.Sub(said); drift > slack || drift < -slack {
			t.Errorf("%s says lastmod %s but last changed %s — run `go run ./scripts/genregistry`", named, lastmod, day)
		}
	}
}

// A URL listed twice is two entries that can disagree about the page, and
// merges that both added the same pages left nineteen of them that way.
func TestSitemapListsEachPageOnce(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, m := range regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(string(b), -1) {
		seen[m[1]]++
	}
	for loc, n := range seen {
		if n > 1 {
			t.Errorf("%s is in docs/sitemap.xml %d times — run `go run ./scripts/genregistry`", loc, n)
		}
	}
}

// The page's structured data and the sitemap answer the same question, so
// they give the same day; dateModified used to be typed once and left there.
func TestGuideDateModifiedIsTheSitemapDay(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	lastmod := map[string]string{}
	entry := regexp.MustCompile(`<loc>https://vshulcz\.github\.io/deja-vu/([^<]*)</loc><lastmod>([^<]*)</lastmod>`)
	for _, m := range entry.FindAllStringSubmatch(string(b), -1) {
		lastmod[m[1]] = m[2]
	}
	modified := regexp.MustCompile(`"dateModified": ?"([^"]*)"`)
	published := regexp.MustCompile(`"datePublished": ?"([^"]*)"`)
	for _, glob := range []string{"guide/*.html", "zh/guide/*.html"} {
		pages, err := filepath.Glob(filepath.Join(root, "docs", filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pages {
			page, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			m := modified.FindSubmatch(page)
			if m == nil {
				continue
			}
			rel, _ := filepath.Rel(filepath.Join(root, "docs"), p)
			rel = filepath.ToSlash(rel)
			want, ok := lastmod[rel]
			if !ok {
				continue
			}
			if pub := published.FindSubmatch(page); len(pub) > 1 && string(pub[1]) > want {
				want = string(pub[1])
			}
			if got := string(m[1]); got != want {
				t.Errorf("%s: dateModified %s, sitemap lastmod %s — run `go run ./scripts/genregistry`", rel, got, want)
			}
		}
	}
}

// What a result page shows is not what the page says: a title is cut around 60
// characters and a description around 160, and the " — deja-vu" suffix spends
// part of that budget. Every per-agent page once carried the same 78-95
// character title — "X memory: recall of past sessions, from every agent on the
// machine" — so the clause that says what the page is for was the clause that
// got cut, on twenty-two pages at once.
//
// The second rule is that a page tells a crawler one title, not two: sixteen of
// them had an og:title that said something else.
const (
	titleBudget = 60
	descBudget  = 160
	siteSuffix  = " — deja-vu"
)

var (
	titleRE = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRE  = regexp.MustCompile(`(?s)<meta name="description" content="(.*?)">`)
	ogTRE   = regexp.MustCompile(`(?s)<meta property="og:title" content="(.*?)">`)
	ogDRE   = regexp.MustCompile(`(?s)<meta property="og:description" content="(.*?)">`)
	twTRE   = regexp.MustCompile(`(?s)<meta name="twitter:title" content="(.*?)">`)
	twDRE   = regexp.MustCompile(`(?s)<meta name="twitter:description" content="(.*?)">`)
)

func TestGuideSnippetsFitWhatSearchShows(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 40 {
		t.Fatalf("found %d guide pages, expected the whole guide", len(pages))
	}

	// Third rule: a title belongs to one page. getting-started.html and
	// search.html both said "Search your Claude Code and Codex history", so
	// one of them was named after the other's subject — and two pages under
	// one title is what a doorway page looks like from outside.
	titleOwner := map[string]string{}

	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		name := filepath.Base(p)

		title := first(titleRE, s)
		desc := first(descRE, s)
		if title == "" || desc == "" {
			t.Errorf("%s: no title or no description", name)
			continue
		}
		if n := utf8.RuneCountInString(title); n > titleBudget {
			t.Errorf("%s: title is %d characters, over %d — search cuts it: %q",
				name, n, titleBudget, title)
		}
		if n := utf8.RuneCountInString(desc); n > descBudget {
			t.Errorf("%s: description is %d characters, over %d", name, n, descBudget)
		}

		if other, ok := titleOwner[title]; ok {
			t.Errorf("%s and %s share the title %q — one of them is named after the other's subject",
				other, name, title)
		} else {
			titleOwner[title] = name
		}

		// og:title may carry the title with or without the site suffix; what it
		// may not do is name a different page.
		stem := strings.TrimSuffix(title, siteSuffix)
		for _, got := range []struct{ what, value string }{
			{"og:title", first(ogTRE, s)},
			{"twitter:title", first(twTRE, s)},
		} {
			if got.value != "" && got.value != stem && got.value != title {
				t.Errorf("%s: %s says %q, the title says %q", name, got.what, got.value, stem)
			}
		}
		for _, got := range []struct{ what, value string }{
			{"og:description", first(ogDRE, s)},
			{"twitter:description", first(twDRE, s)},
		} {
			if got.value != "" && got.value != desc {
				t.Errorf("%s: %s does not match the description", name, got.what)
			}
		}
	}
}

// A result page shows a date for a how-to, and a reader choosing between
// results uses it; none of the fifty guide pages carried one, so there was
// nothing to show. And the site declared no name of its own, which is why a
// result was labelled vshulcz.github.io — somebody's personal page rather than
// the project's documentation.
func TestGuidePagesSayWhenAndWhoseTheyAre(t *testing.T) {
	root := filepath.Join("..", "..")
	pages, err := filepath.Glob(filepath.Join(root, "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	date := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	for _, p := range pages {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		name := filepath.Base(p)
		for _, field := range []string{"datePublished", "dateModified"} {
			m := regexp.MustCompile(`"` + field + `": ?"([^"]*)"`).FindSubmatch(b)
			if m == nil {
				t.Errorf("%s has no %s, so search has no date to show", name, field)
				continue
			}
			got := string(m[1])
			if !date.MatchString(got) {
				t.Errorf("%s: %s is %q, want YYYY-MM-DD", name, field, got)
			}
			if got > today {
				t.Errorf("%s: %s is %s, which is in the future", name, field, got)
			}
		}
	}

	home, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"@type": "WebSite"`, `"name": "deja-vu"`,
		`"url": "https://vshulcz.github.io/deja-vu/"`} {
		if !strings.Contains(string(home), want) {
			t.Errorf("docs/index.html does not declare %s", want)
		}
	}
}

func first(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// Every guide and registry page closes with the same line, so a reader who
// arrives at one of them directly still finds the way back to the project.
// Four of the seven delete-* pages carried it and three did not, which is how
// this started: a page added later is the one that quietly differs.
//
// Placement is the other half. The line lives inside <article>, which is the
// content column of the .doc grid; below the grid it stretches the full width
// of the page and stops lining up with the text above it.
func TestEveryGuideAndRegistryPageEndsWithTheStarLine(t *testing.T) {
	const marker = `class="star-cta"`
	root := filepath.Join("..", "..", "docs")
	checked := 0

	for _, dir := range []string{"guide", "registry"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
				continue
			}
			page := filepath.ToSlash(filepath.Join(dir, e.Name()))
			b, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			checked++

			switch n := strings.Count(text, marker); n {
			case 1:
			case 0:
				t.Errorf("%s does not ask the reader for a star", page)
				continue
			default:
				t.Errorf("%s asks for a star %d times", page, n)
				continue
			}
			closing := strings.Index(text, "</article>")
			if closing < 0 {
				t.Errorf("%s has no </article>, so the star line has no column to sit in", page)
				continue
			}
			if strings.Index(text, marker) > closing {
				t.Errorf("%s puts the star line after </article>, outside the content column", page)
			}
		}
	}

	// Unstyled, the line renders as a paragraph of body text in the middle of
	// the page furniture.
	css, err := os.ReadFile(filepath.Join(root, "assets", "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".star-cta{") {
		t.Error("site.css has no .star-cta rule, so the line is unstyled on every guide and registry page")
	}

	if checked < 80 {
		t.Fatalf("only %d pages were checked, so this test proves little", checked)
	}
}

// A broken JSON-LD block is invisible: the page renders, the browser ignores it,
// and the search engine drops the structured data without telling anyone. One
// page shipped with a quoted command inside a JSON string, which is exactly the
// shape that survives review.
func TestGuidePagesCarryValidStructuredData(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no guide pages found: %v", err)
	}
	block := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		found := 0
		for _, m := range block.FindAllStringSubmatch(string(b), -1) {
			found++
			var decoded any
			if err := json.Unmarshal([]byte(m[1]), &decoded); err != nil {
				t.Errorf("%s: JSON-LD does not parse — %v", filepath.Base(page), err)
			}
		}
		if found == 0 && !strings.HasSuffix(page, "index.html") {
			t.Errorf("%s: no structured data at all", filepath.Base(page))
		}
	}
}

// Valid JSON is not the same as JSON about this page. These pages are written
// by copying a neighbour, and the two blocks that name the harness are the two
// nobody rereads: four new pages shipped with another harness's breadcrumb and
// its three FAQ answers, which is what Google would have published as the rich
// result for a page about something else.
func TestGuideStructuredDataDescribesItsOwnPage(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no guide pages found: %v", err)
	}
	// Every harness that has a page of its own. A question on one page naming
	// another page's harness is the copy that was never finished — a question
	// naming none of them is just a general one, which is fine.
	var harnesses []string
	for _, page := range pages {
		if rest, ok := strings.CutPrefix(strings.TrimSuffix(filepath.Base(page), ".html"), "memory-for-"); ok {
			first, _, _ := strings.Cut(rest, "-")
			harnesses = append(harnesses, first)
		}
	}

	block := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	for _, page := range pages {
		name := filepath.Base(page)
		harness, ok := strings.CutPrefix(strings.TrimSuffix(name, ".html"), "memory-for-")
		if !ok {
			continue // only the per-harness pages carry a harness name to get wrong
		}
		// claude-code and the like: the first segment is the word both the
		// breadcrumb and the questions use.
		harness, _, _ = strings.Cut(harness, "-")

		// A brand written with a space is the same name as its one-word id:
		// "Kilo Code" is kilocode and "Cherry Studio" is cherrystudio, so the
		// comparison folds the separators away. Spelled out, the check said a
		// page about Kilo Code did not name Kilo Code.
		fold := func(s string) string {
			return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(s))
		}
		foreign := func(text string) string {
			low := fold(text)
			if strings.Contains(low, harness) {
				return ""
			}
			for _, other := range harnesses {
				if other != harness && strings.Contains(low, other) {
					return other
				}
			}
			return ""
		}

		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		for _, m := range block.FindAllStringSubmatch(string(b), -1) {
			var doc struct {
				Type            string `json:"@type"`
				ItemListElement []struct {
					Position int    `json:"position"`
					Name     string `json:"name"`
				} `json:"itemListElement"`
				MainEntity []struct {
					Name string `json:"name"`
				} `json:"mainEntity"`
			}
			if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
				continue // the test above already reports a parse failure
			}
			switch doc.Type {
			case "BreadcrumbList":
				for _, item := range doc.ItemListElement {
					if item.Position != 2 {
						continue
					}
					if !strings.Contains(fold(item.Name), harness) {
						t.Errorf("%s: breadcrumb reads %q, which does not name %s — the page was copied and this block came with it", name, item.Name, harness)
					}
				}
			case "FAQPage":
				for _, q := range doc.MainEntity {
					if other := foreign(q.Name); other != "" {
						t.Errorf("%s: FAQ asks %q, which is about %s", name, q.Name, other)
					}
				}
			}
		}
	}
}

// install.sh takes darwin and linux and fails everything else, so a Windows
// reader following the front page gets "unsupported OS" and no next step. The
// release zip is that next step and neither document said so (#1320).
func TestDocsNameTheWindowsInstall(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"README.md", filepath.Join("docs", "guide", "getting-started.html")} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))
		if !strings.Contains(text, "windows_amd64.zip") {
			t.Errorf("%s does not name the windows download", name)
		}
		if !strings.Contains(text, "unsupported os") {
			t.Errorf("%s does not say what the install script does on windows", name)
		}
		// deja-vu is in ScoopInstaller/Main, so `scoop install deja-vu` needs no
		// bucket added first. A page that names only the zip sends a quarter of
		// our downloads through manual PATH editing for no reason.
		if !strings.Contains(text, "scoop install deja-vu") {
			t.Errorf("%s does not name the scoop install, which is the short path on windows", name)
		}
		// And winget, which ships with Windows: the package has been in
		// microsoft/winget-pkgs since 0.21.0 (#428125), and a reader without
		// Scoop should not be sent to the zip.
		if !strings.Contains(text, "winget install vshulcz.deja-vu") {
			t.Errorf("%s does not name the winget install", name)
		}
	}
}

// The translations carry their own Windows paragraph, so each one has to name
// both package managers too.
func TestTranslatedReadmesNameBothWindowsInstalls(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			t.Fatal(err)
		}
		for _, cmd := range []string{"scoop install deja-vu", "winget install vshulcz.deja-vu"} {
			if !strings.Contains(string(b), cmd) {
				t.Errorf("%s does not name %q", l.file, cmd)
			}
		}
	}
}

// And that the binary alone is a finished install for searching: the reporter's
// setup is a zip, no `deja install`, and every MCP target reading `not-wired`,
// which the docs described nowhere.
func TestDocsSayInstallIsOptional(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"README.md", filepath.Join("docs", "guide", "getting-started.html")} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))
		if !strings.Contains(text, "not-wired") {
			t.Errorf("%s does not mention what doctor reports without deja install", name)
		}
		if !strings.Contains(text, "optional") {
			t.Errorf("%s does not say deja install is optional", name)
		}
	}
}

// The claim itself, checked against the script rather than trusted: if
// install.sh ever grows a windows branch, these paragraphs are wrong.
func TestInstallScriptStillRefusesWindows(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	if !strings.Contains(script, "darwin|linux") {
		t.Error("install.sh no longer takes only darwin and linux; the docs say it does")
	}
	if strings.Contains(script, "mingw") || strings.Contains(script, "msys") {
		t.Error("install.sh looks like it handles windows now; the docs say it does not")
	}
}
