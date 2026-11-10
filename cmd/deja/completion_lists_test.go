package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

var completionTestShells = []string{"bash", "zsh", "fish", "powershell"}

func emittedCompletion(t *testing.T, shell string) string {
	t.Helper()
	out, err := captureRun(t, "completion", shell)
	if err != nil {
		t.Fatalf("completion %s: %v", shell, err)
	}
	return out
}

// Tab completion offered eleven harness names while deja read eighteen, in six
// hand-maintained copies of one list — hermes, goose, kimi, cline, roo,
// openclaw and zed were all missing. The lists are substituted from the
// registry now, and this is what keeps the next harness from repeating it.
func TestCompletionOffersEveryHarness(t *testing.T) {
	names := sources.HarnessNames()
	if len(names) < 10 {
		t.Fatalf("registry returned %d names, so this test proves nothing", len(names))
	}
	// The whole list, in registry order, not each name somewhere in the script:
	// every harness name also appears among the install targets, so looking for
	// them one at a time passes on a completion that offers none of them.
	want := strings.Join(names, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer the registry's harnesses", shell)
		}
	}
}

// The same for the agents `handoff --to` accepts: that list is a function, and
// the shells held a copy of it from when it was shorter.
func TestCompletionOffersEveryHandoffTarget(t *testing.T) {
	targets := handoffTargets()
	if len(targets) < 5 {
		t.Fatalf("handoffTargets returned %d, so this test proves nothing", len(targets))
	}
	want := strings.Join(targets, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every handoff target", shell)
		}
	}
}

// And nothing is left holding a placeholder: a substitution that stops matching
// would otherwise ship the marker to the reader's shell.
func TestCompletionSubstitutesEveryPlaceholder(t *testing.T) {
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); strings.Contains(script, "%HARNESSES%") ||
			strings.Contains(script, "%HANDOFF_TARGETS%") || strings.Contains(script, "%INSTALL_TARGETS%") ||
			strings.Contains(script, "%NOTE_STATES%") {
			t.Errorf("%s completion still carries a placeholder", shell)
		}
	}
}

// The roles list was the one hand-maintained copy left: seven of them across
// the three shells, all reading "user assistant tool" while `--role` accepted
// four more — tool-output, files, command and edit, the channels that record
// which files a session touched, which commands it ran and which edits it made
// (#1658). Substituted from knownRoles now, like the harnesses above.
// PowerShell kept two copies of the old list after that, because this test
// never looked at it. (#5017)
func TestCompletionOffersEveryRole(t *testing.T) {
	if len(knownRoles) < 5 {
		t.Fatalf("knownRoles holds %d entries, so this test proves nothing", len(knownRoles))
	}
	want := strings.Join(knownRoles, " ")
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		if !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every role deja accepts", shell)
		}
		// And no copy of the old short list may survive alongside it.
		if strings.Contains(script, "user assistant tool)") ||
			strings.Contains(script, "'user assistant tool'") ||
			strings.Contains(script, "'user', 'assistant', 'tool'") {
			t.Errorf("%s completion still holds a hand-written role list", shell)
		}
	}
}

func TestCompletionOffersHookFlags(t *testing.T) {
	want := map[string][]string{
		"bash": {
			`hook-prompt)` + "\n            COMPREPLY=( $(compgen -W \"--plain --copilot --strict --mcode\"",
			`hook-tool-after)` + "\n            COMPREPLY=( $(compgen -W \"--plain --copilot --mcode\"",
			`hook-session-end)` + "\n            COMPREPLY=( $(compgen -W \"--json\"",
		},
		"zsh": {
			`hook-prompt)` + "\n      _arguments '--plain[omit formatting]' '--copilot[answer in the Copilot CLI hook shape]' '--strict[skip low-confidence sessions]' '--mcode[answer in the MCode hook shape]'",
			`hook-tool-after)` + "\n      _arguments '--plain[omit formatting]' '--copilot[answer in the Copilot CLI hook shape]' '--mcode[answer in the MCode hook shape]'",
			`hook-session-end)` + "\n      _arguments '--json[print JSON]'",
		},
		"fish": {
			`__fish_seen_subcommand_from hook-prompt' -l plain`, `__fish_seen_subcommand_from hook-prompt' -l copilot`, `__fish_seen_subcommand_from hook-prompt' -l strict`, `__fish_seen_subcommand_from hook-prompt' -l mcode`,
			`__fish_seen_subcommand_from hook-tool-after' -l plain`, `__fish_seen_subcommand_from hook-tool-after' -l copilot`, `__fish_seen_subcommand_from hook-tool-after' -l mcode`,
			`__fish_seen_subcommand_from hook-session-end' -l json`,
		},
		"powershell": {
			`'hook-prompt' { @('--plain', '--copilot', '--strict', '--mcode') }`,
			`'hook-tool-after' { @('--plain', '--copilot', '--mcode') }`,
			`'hook-session-end' { @('--json') }`,
		},
	}
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		for _, commandEntry := range want[shell] {
			if !strings.Contains(script, commandEntry) {
				t.Errorf("%s completion does not dispatch hook flags through %s", shell, commandEntry)
			}
		}
	}
}

// The command list was the copy that outlived every other: `deja recap` and
// `deja tests` shipped in 0.21.0 and neither bash nor fish had ever heard of
// them, while the bash and fish scripts each carried the same list twice over
// and the PowerShell array carried it three times — merges landing beside each
// other. A command nobody can tab-complete is a command most people never find.
func TestEveryCommandIsOfferedByEveryShell(t *testing.T) {
	offered := map[string]bool{}
	for _, name := range completionCommands() {
		offered[name] = true
	}
	for name := range commands {
		// Flags the table answers to, hooks a harness calls, and the internals
		// nobody types: none of them is a command to complete.
		if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "hook-") || completionHiddenCommands[name] {
			continue
		}
		if !offered[name] {
			t.Errorf("`deja %s` is in the command table and in no completion script", name)
		}
	}
	// The reverse, minus the four run() answers before the map is consulted.
	handledEarly := map[string]bool{"search": true, "show": true, "last": true, "help": true}
	for name := range offered {
		if _, ok := commands[name]; !ok && !handledEarly[name] {
			t.Errorf("completion offers %q, which is not a command", name)
		}
	}
	want := strings.Join(completionCommands(), " ")
	for _, shell := range []string{"bash", "fish"} {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer the command list", shell)
		}
	}
}

// One list per shell, not three. The duplicates were harmless to the shell and
// fatal to the list: each copy drifted on its own.
func TestNoShellCarriesTheCommandListTwice(t *testing.T) {
	first := completionCommands()[0]
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		if n := strings.Count(script, first+" "); n > 2 {
			t.Errorf("%s completion mentions the head of the command list %d times", shell, n)
		}
	}
	ps := emittedCompletion(t, "powershell")
	if n := strings.Count(ps, "$commands = @("); n != 1 {
		t.Errorf("the powershell script declares $commands %d times", n)
	}
	// Counted in the array alone: recap has a case of its own now, which names
	// it once more further down (#5069).
	_, decl, _ := strings.Cut(ps, "$commands = @(")
	decl, _, _ = strings.Cut(decl, "\n")
	if n := strings.Count(decl, "'recap'"); n != 1 {
		t.Errorf("'recap' appears %d times in the powershell command list", n)
	}
}

// No shell had a case for restore, so bash, zsh and PowerShell fell through to
// the search flags — the first of which, --re, restore refuses — and fish
// offered nothing at all.
func TestCompletionHasARestoreBlock(t *testing.T) {
	for _, tc := range []struct{ shell, start, end string }{
		{"bash", "\n        restore)\n", "\n            ;;\n"},
		{"zsh", "\n    restore)\n", "\n      ;;\n"},
		{"fish", "__fish_seen_subcommand_from restore'", "\ncomplete -c deja -n '__fish_seen_subcommand_from secrets'"},
		{"powershell", "\n            'restore' {\n", "\n            }\n"},
	} {
		script := emittedCompletion(t, tc.shell)
		_, block, ok := strings.Cut(script, tc.start)
		if !ok {
			t.Errorf("%s completion has no restore block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		// fish names a long flag "-l span" where the others spell "--span".
		block = strings.ReplaceAll(block, "-l ", "--")
		for _, flag := range []string{"--span", "--force", "--all", "out"} {
			if !strings.Contains(block, flag) {
				t.Errorf("%s restore block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
	}
}

// zsh keeps its command list by hand, with a description beside each name, so
// the joined-list check above cannot see it — and `deja recall` was missing
// from it while bash and fish offered it.
func TestZshOffersEveryCommand(t *testing.T) {
	script := emittedCompletion(t, "zsh")
	_, block, ok := strings.Cut(script, "\n  commands=(\n")
	if !ok {
		t.Fatal("zsh completion has no commands=( block")
	}
	block, _, _ = strings.Cut(block, "\n  )\n")
	for _, name := range completionCommands() {
		if !strings.Contains(block, "'"+name+":") {
			t.Errorf("zsh completion does not offer `deja %s`", name)
		}
	}
}

// recall fell through to the search flags in bash, zsh and PowerShell, most of
// which it refuses, and fish offered nothing for it.
func TestCompletionHasARecallBlock(t *testing.T) {
	for _, tc := range []struct{ shell, start, end, valueGuard string }{
		{"bash", "\n        recall)\n", "\n            ;;\n", "elif [[ \"$prev\" == \"--project\" || \"$prev\" == \"--limit\" || \"$prev\" == \"--offset\" ]]; then\n                COMPREPLY=()"},
		{"zsh", "\n    recall)\n", "\n      ;;\n", ""},
		{"fish", "__fish_seen_subcommand_from recall;", "\ncomplete -c deja -n '__fish_seen_subcommand_from remember'", ""},
		{"powershell", "\n            'recall' {\n", "\n            }\n", "elseif ($previous -in @('--project', '--limit', '--offset')) { @() }"},
	} {
		script := emittedCompletion(t, tc.shell)
		_, block, ok := strings.Cut(script, tc.start)
		if !ok {
			t.Errorf("%s completion has no recall block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		if tc.valueGuard != "" {
			if !strings.Contains(block, tc.valueGuard) {
				t.Errorf("%s recall block offers flags while completing a project, limit, or offset:\n%s", tc.shell, block)
			}
			block = strings.Replace(block, tc.valueGuard, "", 1)
		}
		block = strings.ReplaceAll(block, "-l ", "--")
		for _, flag := range recallFlags {
			if !strings.Contains(block, flag) {
				t.Errorf("%s recall block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
		for _, flag := range []string{"--re", "--role", "--json", "--since"} {
			if strings.Contains(block, flag+" ") || strings.Contains(block, flag+"'") || strings.Contains(block, flag+"=") || strings.Contains(block, flag+"[") {
				t.Errorf("%s recall block offers %s, which recall refuses:\n%s", tc.shell, flag, block)
			}
		}
		if !strings.Contains(block, sources.HarnessNames()[0]) && !strings.Contains(block, "harnesses") {
			t.Errorf("%s recall block does not complete --harness:\n%s", tc.shell, block)
		}
	}
}

// show in bash and PowerShell only guarded --harness, so the count after
// --offset or --limit was completed with show's own flags (#5940).
func TestCompletionShowBlockLeavesCountsAlone(t *testing.T) {
	for _, tc := range []struct{ shell, start, end, valueGuard string }{
		{"bash", "\n        show)\n", "\n            ;;\n", "elif [[ \"$prev\" == \"--offset\" || \"$prev\" == \"--limit\" ]]; then\n                COMPREPLY=()"},
		{"powershell", "\n            'show' {\n", "\n            }\n", "elseif ($previous -in @('--offset', '--limit')) { @() }"},
	} {
		_, block, ok := strings.Cut(emittedCompletion(t, tc.shell), tc.start)
		if !ok {
			t.Errorf("%s completion has no show block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		if !strings.Contains(block, tc.valueGuard) {
			t.Errorf("%s show block offers flags while completing an offset or limit:\n%s", tc.shell, block)
		}
		block = strings.Replace(block, tc.valueGuard, "", 1)
		for _, flag := range showFlags {
			if !strings.Contains(block, flag) {
				t.Errorf("%s show block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
	}
}

// The value guards must also be present when a contributor lacks either shell.
func TestCompletionLastAndStatsLeaveValuesAlone(t *testing.T) {
	for _, tc := range []struct{ shell, command, valueGuard string }{
		{"bash", "last", `elif [[ "$prev" == "--project" || "$prev" == "--since" || "$prev" == "--from" ]]; then
                COMPREPLY=()`},
		{"bash", "stats", `elif [[ "$prev" == "--project" || "$prev" == "--since" ]]; then
                COMPREPLY=()`},
		{"powershell", "last", `elseif ($previous -in @('--project', '--since', '--from')) { @() }`},
		{"powershell", "stats", `elseif ($previous -in @('--project', '--since')) { @() }`},
	} {
		t.Run(tc.shell+"/"+tc.command, func(t *testing.T) {
			block := shellCommandBlock(tc.shell, emittedCompletion(t, tc.shell), tc.command)
			if !strings.Contains(block, tc.valueGuard) {
				t.Errorf("%s %s completion offers flags where a value is expected:\n%s", tc.shell, tc.command, block)
			}
		})
	}
}

// friction shared one case with fix and tests in every shell, so Tab never
// offered --settle or --unsettle, which `deja help friction` documents. Its
// case is its own now; fix keeps the two flags it takes, and the wall id after
// --settle or --unsettle is not completed (#5640).
func TestCompletionHasAFrictionBlock(t *testing.T) {
	for _, tc := range []struct {
		shell, friction, fix, end string
		// takesValue is how the shell says the flag's value is not completed.
		takesValue []string
	}{
		{"bash", "\n        friction)\n", "\n        fix|tests)\n", "\n            ;;\n",
			[]string{`"$prev" == "--settle" || "$prev" == "--unsettle" ]]; then` + "\n                COMPREPLY=()"}},
		{"zsh", "\n    friction)\n", "\n    fix|tests)\n", "\n      ;;\n",
			[]string{"'--settle=[mark a wall fixed by the id its row prints]:wall id:'", "'--unsettle=[take a --settle back]:wall id:'"}},
		{"fish", "", "", "",
			[]string{"-l settle -x", "-l unsettle -x"}},
		{"powershell", "\n            'friction' {\n", "\n            { $_ -in @('fix', 'tests') } {\n", "\n            }\n",
			[]string{"if ($previous -in @('--limit', '--settle', '--unsettle')) { @() }"}},
	} {
		script := emittedCompletion(t, tc.shell)
		block := func(start, name string) string {
			if tc.shell == "fish" {
				return fishLinesFor(script, name)
			}
			_, b, ok := strings.Cut(script, start)
			if !ok {
				t.Errorf("%s completion has no %s block", tc.shell, name)
				return ""
			}
			b, _, _ = strings.Cut(b, tc.end)
			return b
		}
		friction, fix := block(tc.friction, "friction"), block(tc.fix, "fix")
		// fish names a long flag "-l settle" where the others spell "--settle".
		flags := func(b string) string { return strings.ReplaceAll(b, "-l ", "--") }
		for _, flag := range []string{"--limit", "--json", "--settle", "--unsettle"} {
			if !strings.Contains(flags(friction), flag) {
				t.Errorf("%s friction block does not offer %s:\n%s", tc.shell, flag, friction)
			}
		}
		for _, flag := range []string{"--limit", "--json"} {
			if !strings.Contains(flags(fix), flag) {
				t.Errorf("%s fix block no longer offers %s:\n%s", tc.shell, flag, fix)
			}
		}
		for _, flag := range []string{"--settle", "--unsettle"} {
			if strings.Contains(flags(fix), flag) {
				t.Errorf("%s fix block offers %s, which only friction takes:\n%s", tc.shell, flag, fix)
			}
		}
		for _, want := range tc.takesValue {
			if !strings.Contains(friction, want) {
				t.Errorf("%s friction block completes the wall id after --settle; want %q in:\n%s", tc.shell, want, friction)
			}
		}
	}
}

// fishLinesFor is the fish script's lines that apply to one command: fish has
// no case block, each flag is a complete line whose first condition lists the
// commands it is for.
func fishLinesFor(script, name string) string {
	const prefix = "complete -c deja -n '__fish_seen_subcommand_from "
	var lines []string
	for line := range strings.SplitSeq(script, "\n") {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		cmds, _, _ := strings.Cut(rest, "'")
		cmds, _, _ = strings.Cut(cmds, ";")
		for c := range strings.FieldsSeq(cmds) {
			if c == name {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func shellCommandBlock(shell, script, cmd string) string {
	switch shell {
	case "fish":
		return strings.ReplaceAll(fishLinesFor(script, cmd), "-l ", "--")
	case "bash":
		_, body, ok := strings.Cut(script, `    case "$command" in`+"\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n    esac\n")
		return caseBranchFor(body, "        ", "            ;;", cmd, false)
	case "zsh":
		_, body, ok := strings.Cut(script, `  case "$words[2]" in`+"\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n  esac\n")
		return caseBranchFor(body, "    ", "      ;;", cmd, false)
	case "powershell":
		_, body, ok := strings.Cut(script, "        $candidates = switch ($command) {\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n        }\n")
		b := powershellBranchFor(body, cmd)
		if strings.Contains(b, "$defaultOptions") {
			if _, def, ok := strings.Cut(script, "$defaultOptions = @("); ok {
				def, _, _ = strings.Cut(def, ")")
				b += " " + def
			}
		}
		return b
	}
	return ""
}

func caseBranchFor(body, indent, endMarker, cmd string, _ bool) string {
	lines := strings.Split(body, "\n")
	var defaultBlock string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, indent) || strings.HasPrefix(line, indent+" ") {
			continue
		}
		head, ok := strings.CutSuffix(strings.TrimPrefix(line, indent), ")")
		if !ok {
			continue
		}
		var blockLines []string
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == endMarker {
				i = j
				break
			}
			blockLines = append(blockLines, lines[j])
		}
		block := strings.Join(blockLines, "\n")
		if head == "*" {
			defaultBlock = block
			continue
		}
		for c := range strings.SplitSeq(head, "|") {
			if c == cmd {
				return block
			}
		}
	}
	return defaultBlock
}

func powershellBranchFor(body, cmd string) string {
	var defaultBlock string
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "default {") {
			defaultBlock = line
			continue
		}
		if strings.HasPrefix(trimmed, "'"+cmd+"' {") ||
			(strings.HasPrefix(trimmed, "{ $_ -in @(") && strings.Contains(trimmed, "'"+cmd+"'")) {
			var blockLines []string
			_, rest, _ := strings.Cut(body, line)
			blockLines = append(blockLines, line)
			if !strings.HasSuffix(trimmed, "}") {
				for l := range strings.SplitSeq(rest, "\n") {
					if l == "" {
						continue
					}
					blockLines = append(blockLines, l)
					if l == "            }" {
						break
					}
				}
			}
			return strings.Join(blockLines, "\n")
		}
	}
	return defaultBlock
}

func TestEveryHelpEntryFlagIsOfferedByCompletion(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}
	for _, entry := range helpEntries {
		if entry.toolHelp ||
			(strings.HasPrefix(entry.name, "hook-") && entry.name != "hook-context") {
			continue
		}
		usageText := strings.Join(entry.usage, " ")
		var flags []string
		for tok := range strings.FieldsSeq(usageText) {
			tok = strings.Trim(tok, "[]|")
			for part := range strings.SplitSeq(tok, "|") {
				if strings.HasPrefix(part, "--") && len(part) > 2 {
					flags = append(flags, part)
				}
			}
		}
		if len(flags) == 0 {
			continue
		}
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], entry.name)
			// install_targets / $installTargets holds --all and --auto for install and uninstall.
			if entry.name == "install" || entry.name == "uninstall" {
				block += " --all --auto"
			}
			for _, flag := range flags {
				if !strings.Contains(block, flag) {
					t.Errorf("%s completion for %q does not offer %s (documented in %q):\n%s",
						shell, entry.name, flag, usageText, block)
				}
			}
		}
	}
}

func TestEveryValueFlagIsGuardedInEveryShell(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}

	valueFlagsByCmd := []struct {
		cmd   string
		flags []string
	}{
		{"blame", []string{"--harness", "--project", "--since"}},
		{"origin", []string{"--harness", "--project", "--limit"}},
		{"review", []string{"--harness", "--project", "--limit"}},
		{"bench", []string{"--seed"}},
		{"shell", []string{"-c"}},
		{"forget", []string{"--session", "--project", "--before", "--unforget"}},
		{"handoff", []string{"--to"}},
		{"last", []string{"--from", "--harness", "--project", "--since", "--role"}},
		{"recall", []string{"--project", "--harness", "--limit", "--offset"}},
		{"remember", []string{"--project", "--tag"}},
		{"repo", []string{"--limit", "--since", "--kind"}},
		{"rules", []string{"--limit", "--since", "--project"}},
		{"restore", []string{"--span", "-o", "--out"}},
		{"secrets", []string{"--limit"}},
		{"stats", []string{"--html", "--card", "--harness", "--project", "--since", "--role"}},
		{"sync", []string{"--peer"}},
		{"show", []string{"--harness", "--offset", "--limit"}},
		{"view", []string{"--out"}},
		{"recap", []string{"--since", "--limit"}},
		{"files", []string{"--project", "--limit"}},
		{"how", []string{"--project", "--limit"}},
		{"promote", []string{"--state", "--note", "--tag", "--to"}},
		{"friction", []string{"--limit", "--settle", "--unsettle"}},
		{"fix", []string{"--limit"}},
		{"tests", []string{"--limit"}},
		{"mcp", []string{"--http"}},
		{"search", []string{"--harness", "--project", "--since", "--role", "--session", "--limit", "--sort"}},
	}

	for _, tc := range valueFlagsByCmd {
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], tc.cmd)
			for _, flag := range tc.flags {
				switch shell {
				case "bash":
					// Value-taking flags must either be guarded in a `$prev` branch or,
					// for stats --html/--card, remain guarded by _arguments in zsh/fish.
					if tc.cmd == "stats" && (flag == "--html" || flag == "--card") {
						continue
					}
					if !strings.Contains(block, `"`+flag+`"`) {
						t.Errorf("bash %s block does not guard value flag %s in $prev check:\n%s", tc.cmd, flag, block)
					}
				case "powershell":
					if tc.cmd == "stats" && (flag == "--html" || flag == "--card") {
						continue
					}
					if !strings.Contains(block, "$previous") || !strings.Contains(block, "'"+flag+"'") {
						t.Errorf("powershell %s block does not guard value flag %s in $previous check:\n%s", tc.cmd, flag, block)
					}
				case "zsh":
					want := flag + "="
					if flag == "-c" {
						want = "-c["
					}
					if !strings.Contains(block, want) {
						t.Errorf("zsh %s block does not declare value spec %s:\n%s", tc.cmd, want, block)
					}
				case "fish":
					raw := fishLinesFor(scripts["fish"], tc.cmd)
					fishFlag := "-l " + strings.TrimPrefix(flag, "--")
					switch flag {
					case "-c":
						fishFlag = "-s c"
					case "-o":
						fishFlag = "-s o"
					}
					var matched bool
					for line := range strings.SplitSeq(raw, "\n") {
						if strings.Contains(line, fishFlag) && (strings.Contains(line, " -r") || strings.Contains(line, " -x")) {
							matched = true
							break
						}
					}
					if !matched {
						t.Errorf("fish %s block does not mark %s with -r or -x:\n%s", tc.cmd, flag, raw)
					}
				}
			}
		}
	}

	for _, shell := range completionTestShells {
		block := shellCommandBlock(shell, scripts[shell], "repo")
		for _, sub := range []string{"candidates", "add", "rm", "init", "sync", "on", "off"} {
			if !strings.Contains(block, sub) {
				t.Errorf("%s repo block does not offer subcommand %q:\n%s", shell, sub, block)
			}
		}
	}
}
