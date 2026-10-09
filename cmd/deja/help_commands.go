package main

import (
	"fmt"
	"os"
	"strings"
)

// helpEntry is one command as `deja help` lists it and as `deja <cmd> --help`
// and `deja help <cmd>` explain it. The page used to be one hand-written block
// of usage lines: eleven commands had no description anywhere, the hook
// commands sat between handoff and view, and a command's own --help was its
// usage line alone (#4626).
type helpEntry struct {
	name string
	// usage is every form the command takes, each starting with "deja ".
	usage []string
	// desc is the one line `deja help` prints beside the name. It is kept
	// short enough that the row fits 80 columns without wrapping.
	desc string
	// more is said under the description on the command's own page.
	more string
	// examples are lines a reader can run as they are.
	examples []string
	// group is the heading the command is listed under.
	group string
	// toolHelp: `deja <cmd> --help` belongs to the program the command starts,
	// so only `deja help <cmd>` answers it.
	toolHelp bool
}

const (
	groupSearch = "Search and read"
	groupAsk    = "Ask about past work"
	groupNotes  = "Notes and rules"
	groupSetup  = "Setup"
	groupAgents = "For agents and hooks (deja install wires these)"
)

var helpGroups = []string{groupSearch, groupAsk, groupNotes, groupSetup, groupAgents}

// helpNameWidth is the name column: the longest command name listed.
const helpNameWidth = 16

var helpEntries = []helpEntry{
	{name: "search", group: groupSearch,
		usage:    []string{"deja search [flags] <query>"},
		desc:     "same as deja <query>, for a query that starts with -",
		examples: []string{`deja search -- "--force-with-lease"`, `deja search --harness codex --since 30d "stale connection"`}},
	{name: "show", group: groupSearch,
		usage:    []string{"deja show <id-prefix> [--json] [--harness name] [--offset n] [--limit n]"},
		desc:     "read one session, message by message",
		more:     "The id is the one a search hit, deja last or deja log prints; a unique prefix is enough. --harness picks one when the prefix matches sessions from two agents.",
		examples: []string{"deja show 01a00feb", "deja show 01a00feb --offset 40 --limit 20"}},
	{name: "last", group: groupSearch,
		usage:    []string{"deja last [n] [--json] [--project name] [--harness name] [--from machine|local] [--since duration] [--role user|assistant|tool|files|command|edit|summary]"},
		desc:     "recent sessions, newest first",
		more:     "--from names a machine the sessions were synced from, or local for this one.",
		examples: []string{"deja last 20 --harness codex", "deja last --since 7d --role user"}},
	{name: "ctx", group: groupSearch,
		usage:    []string{"deja ctx <query|id-prefix>"},
		desc:     "a context digest to paste or pipe into an agent",
		more:     "A query collects the matching sessions; an id prints that one session's digest.",
		examples: []string{`deja ctx "schema migration rollback" > deja-context.md`, "deja ctx 01a00feb"}},
	{name: "recall", group: groupSearch,
		usage:    []string{"deja recall <words> [--project name] [--harness name] [--limit n]"},
		desc:     "the page the MCP recall tool gives an agent",
		more:     "About 4 KB, this project first. For skills and scripts that reach deja through the shell rather than MCP.",
		examples: []string{`deja recall "pgbouncer prepared statements"`}},
	{name: "share", group: groupSearch,
		usage:    []string{"deja share <id-prefix>"},
		desc:     "a redacted digest of one session, to paste elsewhere",
		examples: []string{"deja share 01a00feb > session.md"}},
	{name: "log", group: groupSearch,
		usage:    []string{"deja log [n] [--last] [--json]"},
		desc:     "what deja recalled and injected, newest first",
		more:     "--last prints the most recent injected digest exactly as the agent got it.",
		examples: []string{"deja log 20", "deja log --last"}},
	{name: "view", group: groupSearch,
		usage:    []string{"deja view [--no-open] [--out path]"},
		desc:     "browse sessions, recalls and notes in one HTML page",
		more:     "The page is written locally and opened in the browser; nothing leaves the machine.",
		examples: []string{"deja view", "deja view --no-open"}},
	{name: "brief", group: groupSearch,
		usage:    []string{"deja brief"},
		desc:     "the screen bare deja prints on a terminal",
		more:     "Today's sessions, what deja served, recent work and a search to try. Under its own name so it can be piped or paged.",
		examples: []string{"deja brief | less"}},

	{name: "blame", group: groupAsk,
		usage: []string{
			"deja blame <path>[:line] [--all] [--all-projects] [--json] [--project name] [--harness name] [--since 30d]",
			"deja blame <path>:<line> --attribution [--json] [--git-note]",
		},
		desc:     "the sessions behind a file, or behind one line of it",
		more:     "With a line: the commit that last changed it and the session whose edit wrote it. --attribution prints that answer alone; --git-note stores it as a git note.",
		examples: []string{"deja blame src/server.go", "deja blame src/server.go:42 --attribution"}},
	{name: "files", group: groupAsk,
		usage:    []string{"deja files <topic> [--project name] [--all-projects] [--limit n] [--json]"},
		desc:     "files opened or edited while a topic was worked on",
		more:     "Answers from the project of the working directory; --all-projects asks the whole machine.",
		examples: []string{`deja files "rate limiter"`, "deja files auth --all-projects"}},
	{name: "how", group: groupAsk,
		usage:    []string{"deja how <what> [--project name] [--all-projects] [--limit n] [--json]"},
		desc:     "the commands this project actually ran for something",
		more:     "Ordered by how many sessions ran each one. Answers from the project of the working directory; --all-projects asks the whole machine.",
		examples: []string{`deja how "go test"`, "deja how docker compose --all-projects"}},
	{name: "fix", group: groupAsk,
		usage:    []string{`deja fix "<error text>" [--limit n] [--json]`},
		desc:     "what was run after this error before",
		more:     "Paste the error or pipe the failing output in. This is what your own history did, not a guaranteed fix.",
		examples: []string{`deja fix "connection refused"`, "go test ./... 2>&1 | deja fix"}},
	{name: "friction", group: groupAsk,
		usage:    []string{"deja friction [--limit n] [--json]"},
		desc:     "errors that keep coming back across sessions",
		examples: []string{"deja friction", "deja friction --limit 5"}},
	{name: "tests", group: groupAsk,
		usage:    []string{"deja tests [--limit n] [--json]"},
		desc:     "your build and test runs, week by week",
		more:     "Then the tests that failed on more than one day.",
		examples: []string{"deja tests", "deja tests --json"}},
	{name: "recap", group: groupAsk,
		usage:    []string{"deja recap [--since 7d] [--limit n] [--json]"},
		desc:     "what the week settled, with the session behind it",
		more:     "Quoted from the sessions and grouped by project. Every line goes through a second redaction pass, since it is written to be pasted.",
		examples: []string{"deja recap", "deja recap --since 30d"}},
	{name: "wip", group: groupAsk,
		usage:    []string{"deja wip [--json]"},
		desc:     "what the last session in this project was doing",
		more:     "The task, what it settled, the files in flight and the last command, read from the transcript.",
		examples: []string{"deja wip", "deja wip --json"}},
	{name: "handoff", group: groupAsk,
		usage:    []string{"deja handoff [--to <agent>] [id-prefix] [--exec]"},
		desc:     "what the next agent needs to carry on",
		more:     "The problem, what was tried, what was settled and where it stopped. --to phrases it for one agent; --exec opens it there.",
		examples: []string{"deja handoff", "deja handoff --to codex --exec"}},
	{name: "resume", group: groupAsk,
		usage:    []string{"deja resume <id-prefix> [--write-back] [--exec]"},
		desc:     "the command that reopens a session in its agent",
		more:     "--exec runs it. --write-back first restores a transcript the agent has since deleted.",
		examples: []string{"deja resume 01a00feb", "deja resume 01a00feb --exec"}},
	{name: "restore", group: groupAsk,
		usage:    []string{"deja restore <path> [--span n] [-o|--out file] [--force]"},
		desc:     "text an agent replaced in a file, to get it back",
		more:     "Lists the replaced spans, newest first; --span prints one and -o writes it. An existing file is kept unless --force.",
		examples: []string{"deja restore src/server.go", "deja restore src/server.go --span 2 -o server.go.old"}},
	{name: "secrets", group: groupAsk,
		usage:    []string{"deja secrets [--limit n] [--json] [--scrub [--dry-run]]"},
		desc:     "credentials your agent transcripts are carrying",
		more:     "Never the value: deja's copy is redacted. --scrub rewrites the transcripts that still hold one and keeps the original beside each file.",
		examples: []string{"deja secrets", "deja secrets --scrub --dry-run"}},
	{name: "stats", group: groupAsk,
		usage: []string{
			"deja stats [--json] [--impact] [--year] [--redaction] [--card [path]]",
			"    [--html [path]] [--project name] [--harness name] [--since 30d] [--role name]",
		},
		desc:     "totals, projects and activity, as text, card or page",
		examples: []string{"deja stats", "deja stats --since 30d --card"}},

	{name: "remember", group: groupNotes,
		usage:    []string{`deja remember "text" [--project name] [--tag name]`},
		desc:     "store a note that recall ranks above transcripts",
		examples: []string{`deja remember "staging deploys need the VPN" --project api --tag deploy`}},
	{name: "promote", group: groupNotes,
		usage:    []string{`deja promote <id-prefix> [--state accepted|rejected|superseded|stale] [--note "text"] [--tag name] [--to path]`},
		desc:     "turn a session into a curated note with its evidence",
		more:     "--to also appends the note to a Markdown file in the repo.",
		examples: []string{`deja promote 01a00feb --state accepted --note "pgx v5 fixed the pool leak"`}},
	{name: "forget", group: groupNotes,
		usage: []string{
			"deja forget --session <id-prefix> [--project <substring>] [--before <duration|date>] [--dry-run] [--all-matches]",
			"deja forget --list | --unforget <id>",
		},
		desc:     "remove sessions from the index",
		examples: []string{"deja forget --session 01a00feb --dry-run", "deja forget --list"}},
	{name: "rules", group: groupNotes,
		usage: []string{
			"deja rules [sync]",
			"deja rules candidates [--json] [--limit n] [--since 90d]",
		},
		desc:     "one rules file, copied into every agent's rules",
		more:     "The file is ~/.config/deja/rules.md. sync writes it into each agent's global rules file as a marked block. candidates lists the turns where you corrected an agent, for your agent to group into rules.",
		examples: []string{"deja rules", "deja rules sync", "deja rules candidates --since 180d"}},

	{name: "install", group: groupSetup,
		usage:    []string{"deja install <target>... | --all | --auto [--no-guidance] [--no-index] [--force]"},
		desc:     "wire recall, hooks and guidance into your agents",
		more:     "--all wires MCP recall into every agent found on this machine; --auto adds the hooks and plugins each one takes.",
		examples: []string{"deja install --auto", "deja install claude-code codex"}},
	{name: "uninstall", group: groupSetup,
		usage:    []string{"deja uninstall <target>... | --all | --auto"},
		desc:     "remove deja's wiring from an agent",
		examples: []string{"deja uninstall claude-code", "deja uninstall --all"}},
	{name: "doctor", group: groupSetup,
		usage:    []string{"deja doctor [--json] [--deep] [--offline] [--all]"},
		desc:     "check stores, wiring, index and version",
		more:     "--deep re-reads a sample of the sources to prove the index against them and exits non-zero on drift.",
		examples: []string{"deja doctor", "deja doctor --deep"}},
	{name: "sources", group: groupSetup,
		usage:    []string{"deja sources"},
		desc:     "where deja looks for each agent's history",
		examples: []string{"deja sources"}},
	{name: "index", group: groupSetup,
		usage:    []string{"deja index [--rebuild] [--quiet]"},
		desc:     "read new agent history into the index",
		more:     "--rebuild reads everything again; --quiet says nothing unless something failed.",
		examples: []string{"deja index", "deja index --rebuild"}},
	{name: "warmup", group: groupSetup,
		usage:    []string{"deja warmup"},
		desc:     "build or refresh the index without searching",
		more:     "For a shell profile or a provisioning script.",
		examples: []string{"deja warmup"}},
	{name: "embed", group: groupSetup,
		usage:    []string{"deja embed"},
		desc:     "build the semantic search sidecar from an endpoint",
		more:     "DEJA_EMBED_URL names the embedding endpoint and DEJA_EMBED_MODEL the model. This sends session text to that endpoint.",
		examples: []string{"DEJA_EMBED_URL=http://localhost:11434/v1/embeddings deja embed"}},
	{name: "sync", group: groupSetup,
		usage: []string{
			"deja sync",
			"deja sync export <dir> [--full] [--include-imported] [--peer name]",
			"deja sync import <dir>",
			"deja sync ssh <host> [--pull] [--both] [--full]",
			"deja sync forget <host>",
		},
		desc:     "move redacted memory between machines",
		more:     "Bare deja sync exchanges with every machine deja knows, both ways.",
		examples: []string{"deja sync ssh laptop --both", "deja sync export /Volumes/usb/deja"}},
	{name: "update", group: groupSetup,
		usage:    []string{"deja update [--force]"},
		desc:     "update a standalone install",
		more:     "Homebrew and npm installs update through their package manager. --force writes over a binary a package manager owns.",
		examples: []string{"deja update"}},
	{name: "completion", group: groupSetup,
		usage:    []string{"deja completion <bash|zsh|fish|powershell>"},
		desc:     "print a completion script for your shell",
		examples: []string{"source <(deja completion bash)", "deja completion fish > ~/.config/fish/completions/deja.fish"}},
	{name: "version", group: groupSetup,
		usage:    []string{"deja version"},
		desc:     "print the version",
		examples: []string{"deja version"}},
	{name: "bench", group: groupSetup,
		usage:    []string{"deja bench recall|context|prompt|block|ingest|read [--json] [--seed n]"},
		desc:     "reproducible benchmarks of recall and indexing",
		examples: []string{"deja bench recall", "deja bench ingest --json"}},
	{name: "aider", group: groupSetup, toolHelp: true,
		usage:    []string{"deja aider [aider flags]"},
		desc:     "start aider with this project's history as context",
		more:     "Everything after aider is aider's own, --help included.",
		examples: []string{"deja aider --model sonnet"}},
	{name: "goose", group: groupSetup, toolHelp: true,
		usage:    []string{"deja goose [goose flags]"},
		desc:     "start Goose with recall re-read every turn",
		more:     "Everything after goose is Goose's own, --help included.",
		examples: []string{"deja goose session"}},

	{name: "help",
		usage:    []string{"deja help [command]"},
		desc:     "every command, or one command's flags and examples",
		more:     "deja <command> --help says the same.",
		examples: []string{"deja help blame", "deja blame --help"}},

	{name: "mcp", group: groupAgents,
		usage:    []string{"deja mcp"},
		desc:     "the MCP server, on stdio",
		more:     "deja install wires it into each agent; run it by hand only to debug a client.",
		examples: []string{"deja mcp < requests.jsonl"}},
	{name: "statusline", group: groupAgents,
		usage:    []string{"deja statusline"},
		desc:     "one line for an agent's status bar",
		more:     "Today's sessions and what deja served; with the host's session payload on stdin, also what earlier sessions decided about the file in hand.",
		examples: []string{"deja install statusline"}},
	{name: "check", group: groupAgents,
		usage:    []string{"deja check -"},
		desc:     "what history says about a plan read from stdin",
		examples: []string{"deja check - < plan.md"}},
	{name: "hook-prompt", group: groupAgents,
		usage:    []string{"deja hook-prompt [--plain] [--junie]"},
		desc:     "per prompt: recall for what was asked",
		examples: []string{"deja hook-prompt --plain < payload.json"}},
	{name: "hook-context", group: groupAgents,
		usage:    []string{"deja hook-context [--plain] [--once] [--strict] [--copilot] [--notes] [--junie]"},
		desc:     "session start: the project digest, once",
		examples: []string{"deja hook-context --plain < payload.json"}},
	{name: "hook-antigravity", group: groupAgents,
		usage:    []string{"deja hook-antigravity"},
		desc:     "Antigravity: the digest on the first turn",
		examples: []string{"deja hook-antigravity < payload.json"}},
	{name: "hook-plan", group: groupAgents,
		usage:    []string{"deja hook-plan"},
		desc:     "before a plan runs: what history says about it",
		examples: []string{"deja hook-plan < payload.json"}},
	{name: "hook-tool", group: groupAgents,
		usage:    []string{"deja hook-tool [--plain] [--crush] [--junie]"},
		desc:     "before a command or edit: what it already has",
		examples: []string{"deja hook-tool --plain < payload.json"}},
	{name: "hook-tool-after", group: groupAgents,
		usage:    []string{"deja hook-tool-after [--plain] [--copilot]"},
		desc:     "after a failed command: what followed it before",
		examples: []string{"deja hook-tool-after --plain < payload.json"}},
}

func helpEntryFor(name string) (helpEntry, bool) {
	for _, e := range helpEntries {
		if e.name == name {
			return e, true
		}
	}
	return helpEntry{}, false
}

// searchFlagRows are the flags of the bare query, in the same column as the
// command descriptions.
var searchFlagRows = [][2]string{
	{"--harness <name>", "only sessions from one agent (claude, codex, ...)"},
	{"--project <name>", "only sessions from one project"},
	{"--since <duration>", "only sessions newer than e.g. 30d, 12h"},
	{"--role <name>", "only turns from one role: user, assistant, tool"},
	{"", "(tool output), files, command, edit"},
	{"--session <id>", "only one session, by the id a hit prints"},
	{"--limit <1-100>", "max sessions to return (default 15)"},
	{"--all", "every match, no cap"},
	{"--re", "treat the query as a regular expression"},
	{"--json", "machine-readable output"},
	{"--no-embed", "skip the semantic (embedding) tier"},
}

// helpRow lays out one name and its description on the shared column.
func helpRow(name, desc string) string {
	return strings.TrimRight(fmt.Sprintf("  %-*s  %s", helpNameWidth+5, name, desc), " ")
}

func searchFlagsBlock() string {
	var b strings.Builder
	for _, r := range searchFlagRows {
		b.WriteString(helpRow(r[0], r[1]) + "\n")
	}
	return b.String()
}

// usageText is the page `deja help` prints.
func usageText() string {
	var b strings.Builder
	b.WriteString("deja - persistent memory for coding agents\n\n")
	b.WriteString("Usage:\n")
	b.WriteString(helpRow("deja <query>", "search your past agent sessions") + "\n")
	b.WriteString(helpRow("deja <command>", "one of the commands below") + "\n")
	b.WriteString(helpRow("deja help <command>", "a command's flags and examples") + "\n")
	for _, g := range helpGroups {
		b.WriteString("\n" + g + ":\n")
		for _, e := range helpEntries {
			if e.group == g {
				b.WriteString(helpRow("deja "+e.name, e.desc) + "\n")
			}
		}
	}
	b.WriteString("\nSearch flags (deja [flags] <query>):\n")
	b.WriteString(searchFlagsBlock())
	b.WriteString(`
Examples:
  deja "jwt refresh token bug"
  deja '"connection pool exhausted"'
  deja --harness claude --since 30d "panic in indexer"
  deja --all "connection pool"
  deja last 20 --harness codex
  deja install --auto
`)
	return b.String()
}

// bareDejaPointer is what a bare deja prints when stdout is not a terminal.
func bareDejaPointer() string {
	return "deja - persistent memory for coding agents\n\n" +
		helpRow("deja <query>", "search your past agent sessions") + "\n" +
		helpRow("deja brief", "the screen bare deja prints on a terminal") + "\n" +
		helpRow("deja help", "every command, with a line on each") + "\n"
}

// helpForCommand answers `deja <cmd> --help` and `deja help <cmd>`: the usage,
// what the command is for, and examples. Every command rejected `--help` as an
// unknown flag once, and a couple did worse: `deja statusline --help` printed a
// statusline and `deja mcp --help` started the server and hung the terminal
// (#1111). An empty answer means the word is not a command.
func helpForCommand(name string) string {
	e, ok := helpEntryFor(name)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, u := range e.usage {
		b.WriteString("  " + u + "\n")
	}
	if name == "install" || name == "uninstall" {
		b.WriteString("    targets:\n" + wrapTargets(installTargetNames(), "      ", usageWidth()) + "\n")
	}
	b.WriteString("\n" + wrapProse(sentence(e.desc)+" "+e.more, proseWidth()) + "\n")
	if name == "handoff" {
		b.WriteString("\n" + wrapProse("--to takes: "+strings.Join(handoffTargets(), ", "), proseWidth()) + "\n")
	}
	if name == "search" {
		b.WriteString("\nFlags:\n" + searchFlagsBlock())
	}
	if len(e.examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, x := range e.examples {
			b.WriteString("  " + x + "\n")
		}
	}
	return b.String()
}

// sentence turns a help row's description into the opening of a paragraph.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	s = string(r)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// proseWidth is the terminal's width up to 80: a paragraph is easier to read
// at that measure however wide the window is.
func proseWidth() int {
	return min(usageWidth(), 80)
}

// wrapProse fills words into lines no wider than width, never breaking a word.
func wrapProse(s string, width int) string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		if cur != "" && len([]rune(cur))+1+len([]rune(w)) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// cmdHelp is `deja help [command]`.
func cmdHelp(_ string, rest []string) error {
	if len(rest) == 0 {
		printUsage()
		return nil
	}
	h := helpForCommand(rest[0])
	if h == "" {
		return fmt.Errorf("help: %q is not a command — `deja help` lists them", rest[0])
	}
	fmt.Print(wrapUsage(h, printableWidth(os.Stdout)))
	return nil
}
