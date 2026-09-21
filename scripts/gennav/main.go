// Command gennav writes the sidebar of every page under docs/guide.
//
// The sidebar used to be written into each page by hand. By September it was
// 84 links in one column, and the 68 copies had drifted into three versions.
// Here it is defined once, grouped, and each group folds: a page opens the
// group it belongs to and leaves the rest closed.
//
// A page on disk that no group names is an error, so a new guide page cannot
// ship without a way to reach it.
//
// Run from the repository root:
//
//	go run ./scripts/gennav
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type link struct{ href, label string }

type group struct {
	title string
	fold  bool // a <details> that opens only on its own pages
	links []link
}

// The first group is the reading path guide.js counts ("5/8"); keep the two
// lists in step.
var groups = []group{
	{title: "Guide", links: []link{
		{"getting-started.html", "Getting started"},
		{"agents.html", "Agents &amp; MCP"},
		{"search.html", "Search"},
		{"commands.html", "CLI reference"},
		{"harnesses.html", "Harnesses"},
		{"privacy.html", "Privacy"},
	}},
	{title: "Why agents forget", fold: true, links: []link{
		{"forgetting.html", "Why agents forget"},
		{"does-my-agent-remember.html", "Does my agent remember?"},
		{"lost-context.html", "Lost context"},
		{"context-window-full.html", "Context window full"},
		{"after-compaction.html", "After compaction"},
		{"repeated-mistakes.html", "Repeated mistakes"},
		{"switching-agents.html", "Switching agents"},
		{"parallel-sessions.html", "Parallel sessions"},
		{"rules-files.html", "When CLAUDE.md grows"},
		{"token-cost.html", "What memory costs"},
	}},
	{title: "Session history", fold: true, links: []link{
		{"where-sessions-are-stored.html", "Where history lives"},
		{"find-a-session.html", "Finding a session"},
		{"resume-a-session.html", "Resuming a session"},
		{"export-conversations.html", "Exporting sessions"},
		{"sync-across-machines.html", "Across machines"},
		{"auditing-agents.html", "Auditing an agent"},
		{"recover-a-deleted-session.html", "Recover a deleted session"},
	}},
	{title: "Deleting sessions", fold: true, links: []link{
		{"session-files-on-disk.html", "Claude Code"},
		{"delete-codex-sessions.html", "Codex"},
		{"delete-cursor-chat-history.html", "Cursor"},
		{"delete-copilot-chat-history.html", "Copilot Chat"},
		{"delete-gemini-cli-history.html", "Gemini CLI"},
		{"delete-opencode-session-history.html", "opencode"},
		{"delete-cline-task-history.html", "Cline"},
		{"delete-kilo-code-task-history.html", "Kilo Code"},
	}},
	{title: "Per-agent guides", fold: true, links: []link{
		{"memory-for-aider.html", "aider"},
		{"memory-for-amp.html", "Amp"},
		{"memory-for-antigravity.html", "Antigravity"},
		{"memory-for-cherrystudio.html", "Cherry Studio"},
		{"memory-for-claude-code.html", "Claude Code"},
		{"memory-for-cline.html", "Cline"},
		{"memory-for-codewhale.html", "CodeWhale"},
		{"memory-for-codex.html", "Codex"},
		{"memory-for-commandcode.html", "Command Code"},
		{"memory-for-continue.html", "Continue"},
		{"memory-for-copilot.html", "Copilot CLI"},
		{"memory-for-crush.html", "Crush"},
		{"memory-for-cursor.html", "Cursor"},
		{"memory-for-dsh.html", "dsh"},
		{"memory-for-gjc.html", "gajae-code"},
		{"memory-for-gemini.html", "Gemini CLI"},
		{"memory-for-goose.html", "Goose"},
		{"memory-for-grok.html", "Grok Build"},
		{"memory-for-hermes.html", "Hermes"},
		{"memory-for-kilocode.html", "Kilo Code"},
		{"memory-for-kimchi.html", "Kimchi Coding"},
		{"memory-for-kimi.html", "Kimi Code"},
		{"memory-for-kiro.html", "Kiro"},
		{"memory-for-omp.html", "omp"},
		{"memory-for-openclaw.html", "OpenClaw"},
		{"memory-for-opencode.html", "opencode"},
		{"memory-for-pi.html", "pi"},
		{"memory-for-prime.html", "prime-agent"},
		{"memory-for-qwen.html", "Qwen Code"},
		{"memory-for-roo.html", "Roo Code"},
		{"memory-for-senpi.html", "Senpi"},
		{"memory-for-copilot-chat.html", "VS Code Copilot Chat"},
		{"memory-for-zcode.html", "ZCode"},
		{"memory-for-zed.html", "Zed"},
	}},
	{title: "Evidence", links: []link{
		{"benchmarks.html", "Benchmarks"},
		{"compare.html", "Compare"},
		{"day-zero.html", "Day zero"},
	}},
	{title: "Reference", links: []link{
		{"https://github.com/vshulcz/deja-vu/blob/main/docs/ARCHITECTURE.md", "Architecture"},
		{"https://github.com/vshulcz/deja-vu/blob/main/docs/SECURITY-MODEL.md", "Security model"},
		{"../registry/README.html", "Format registry"},
	}},
}

const tail = `<a class="sidecat" href="../"><img src="../assets/icon-live.svg" width="46" height="46" alt=""><span>it remembers, so you do not have to</span></a>`

var aside = regexp.MustCompile(`(?s)<aside>.*?</aside>`)

func render(page string) string {
	var b strings.Builder
	b.WriteString(`<aside><a href="../">← deja-vu</a>`)
	for _, g := range groups {
		here := false
		for _, l := range g.links {
			here = here || l.href == page
		}
		if g.fold {
			open := ""
			if here {
				open = " open"
			}
			fmt.Fprintf(&b, "\n<details class=\"grpfold\"%s><summary>%s <span class=\"n\">%d</span></summary>", open, g.title, len(g.links))
		} else {
			fmt.Fprintf(&b, "\n<div class=\"grp\">%s</div>", g.title)
		}
		for _, l := range g.links {
			cur := ""
			if l.href == page {
				cur = ` aria-current="page"`
			}
			fmt.Fprintf(&b, "\n<a href=\"%s\"%s>%s</a>", l.href, cur, l.label)
		}
		if g.fold {
			b.WriteString("\n</details>")
		}
	}
	b.WriteString(tail + `</aside>`)
	return b.String()
}

func main() {
	dir := filepath.Join("docs", "guide")
	named := map[string]bool{}
	for _, g := range groups {
		for _, l := range g.links {
			if named[l.href] {
				fail("%s is in the sidebar twice", l.href)
			}
			named[l.href] = true
		}
	}
	pages, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		fail("%v", err)
	}
	sort.Strings(pages)
	var missing []string
	for _, p := range pages {
		name := filepath.Base(p)
		if !named[name] {
			missing = append(missing, name)
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			fail("%v", err)
		}
		if !aside.Match(b) {
			fail("%s has no <aside> to replace", p)
		}
		out := aside.ReplaceAllLiteral(b, []byte(render(name)))
		if err := os.WriteFile(p, out, 0o644); err != nil {
			fail("%v", err)
		}
	}
	for href := range named {
		if strings.HasSuffix(href, ".html") && !strings.Contains(href, "/") {
			if _, err := os.Stat(filepath.Join(dir, href)); err != nil {
				missing = append(missing, href+" (named, not on disk)")
			}
		}
	}
	if len(missing) > 0 {
		fail("not in the sidebar groups in scripts/gennav: %s", strings.Join(missing, ", "))
	}
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "gennav: "+f+"\n", a...)
	os.Exit(1)
}
