package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// runCompletion writes a shell-specific script so the binary stays dependency-free.
func runCompletion(args []string) error {
	if len(args) > 1 {
		// One shell was given; "needs bash, ..." read as if none was (#5077).
		return fmt.Errorf("completion takes one shell, got %q and %q", args[0], args[1])
	}
	if len(args) != 1 {
		return errors.New("completion needs bash, zsh, fish, or powershell")
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		return fmt.Errorf("unknown shell %q; want bash, zsh, fish, powershell, or pwsh", args[0])
	}
	// Every list a shell offers is substituted rather than duplicated per shell:
	// three hand-maintained copies is how this one fell seven harnesses behind.
	// The harness names went the same way — six copies of a list written when
	// there were eleven, so tab completion stopped at copilot while deja read
	// hermes, goose, kimi, cline, roo, openclaw and zed as well.
	script = strings.ReplaceAll(script, "%INSTALL_TARGETS%", strings.Join(installTargetNames(), " "))
	script = strings.ReplaceAll(script, "%HARNESSES%", strings.Join(sources.HarnessNames(), " "))
	script = strings.ReplaceAll(script, "%HANDOFF_TARGETS%", strings.Join(handoffTargets(), " "))
	// The last hand-written list: seven copies of "user assistant tool" while
	// --role accepted four more (#1658).
	script = strings.ReplaceAll(script, "%ROLES%", strings.Join(knownRoles, " "))
	// The promote states come from the map promote checks against, sorted
	// because a map has no order of its own (#5066).
	states := make([]string, 0, len(sources.NoteStates))
	for s := range sources.NoteStates {
		states = append(states, s)
	}
	sort.Strings(states)
	script = strings.ReplaceAll(script, "%NOTE_STATES%", strings.Join(states, " "))
	// And the command list itself, which was the last copy left: `deja recap`
	// and `deja tests` shipped in 0.21.0 and bash and fish had never heard of
	// either, while the PowerShell array had grown three overlapping copies of
	// itself from merges landing beside each other.
	script = strings.ReplaceAll(script, "%COMMANDS%", strings.Join(completionCommands(), " "))
	script = strings.ReplaceAll(script, "%COMMANDS_QUOTED%", "'"+strings.Join(completionCommands(), "', '")+"'")
	_, err := fmt.Fprint(os.Stdout, script)
	return err
}

var completionScripts = map[string]string{
	"bash":       bashCompletion,
	"zsh":        zshCompletion,
	"fish":       fishCompletion,
	"powershell": powershellCompletion,
	"pwsh":       powershellCompletion,
}

const bashCompletion = `# bash completion for deja
_deja_completion() {
    local cur prev command action
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if (( COMP_CWORD > 0 )); then
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi
    # Defaulted, like prev above: a shell with set -u is one people run, and
    # reading these bare made the first Tab after "deja " print
    # "COMP_WORDS[2]: unbound variable" instead of the candidates (#1656).
    command="${COMP_WORDS[1]-}"
    action="${COMP_WORDS[2]-}"

    local commands="%COMMANDS%"
    local harnesses="%HARNESSES%"
    local install_targets="%INSTALL_TARGETS% --all --auto"

    if (( COMP_CWORD == 1 )); then
        COMPREPLY=( $(compgen -W "$commands --version -version --json --re --all --no-embed --harness --project --since --role --session --rebuild --limit --sort" -- "$cur") )
        return
    fi

    case "$command" in
        blame)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "--all --all-projects --json --harness --project --since --attribution --git-note" -- "$cur") )
            else
                COMPREPLY=( $(compgen -f -- "$cur") )
            fi
            ;;
        bench)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W "recall context prompt block ingest read" -- "$cur") )
            elif [[ "$action" == "recall" || "$action" == "context" || "$action" == "prompt"|| "$action" == "block" || "$action" == "ingest" || "$action" == "read" ]]; then
                COMPREPLY=( $(compgen -W "--json --seed" -- "$cur") )
            fi
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish powershell pwsh" -- "$cur") )
            ;;
        doctor)
            COMPREPLY=( $(compgen -W "--json --offline --deep --all" -- "$cur") )
            ;;
        delete)
            COMPREPLY=( $(compgen -W "--dry-run --yes" -- "$cur") )
            ;;
        forget)
            COMPREPLY=( $(compgen -W "--list --dry-run --session --project --before --unforget --all-matches" -- "$cur") )
            ;;
        handoff)
            if [[ "$prev" == "--to" ]]; then
                COMPREPLY=( $(compgen -W "%HANDOFF_TARGETS%" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--to --exec" -- "$cur") )
            fi
            ;;
        hook-context)
            COMPREPLY=( $(compgen -W "--plain --once --copilot --notes" -- "$cur") )
            ;;
        index)
            COMPREPLY=( $(compgen -W "--rebuild -rebuild --quiet -quiet" -- "$cur") )
            ;;
        install)
            COMPREPLY=( $(compgen -W "$install_targets --no-guidance --no-index --force" -- "$cur") )
            ;;
        uninstall)
            COMPREPLY=( $(compgen -W "$install_targets --no-guidance" -- "$cur") )
            ;;
        last)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$prev" == "--role" ]]; then
                COMPREPLY=( $(compgen -W "%ROLES%" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --from --harness --project --since --role" -- "$cur") )
            fi
            ;;
        recall)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--project --harness --limit --offset" -- "$cur") )
            fi
            ;;
        remember)
            COMPREPLY=( $(compgen -W "--project --tag" -- "$cur") )
            ;;
        rules)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W "sync status candidates" -- "$cur") )
            elif [[ "$action" == "candidates" && "$prev" != "--limit" && "$prev" != "--since" ]]; then
                COMPREPLY=( $(compgen -W "--json --limit --since" -- "$cur") )
            else
                COMPREPLY=()
            fi
            ;;
        resume)
            COMPREPLY=( $(compgen -W "--exec --write-back --app" -- "$cur") )
            ;;
        restore)
            if [[ "$prev" == "-o" || "$prev" == "--out" ]]; then
                COMPREPLY=( $(compgen -f -- "$cur") )
            elif [[ "$prev" == "--span" ]]; then
                COMPREPLY=()
            elif [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "--span -o --out --force --all" -- "$cur") )
            else
                COMPREPLY=( $(compgen -f -- "$cur") )
            fi
            ;;
        secrets)
            if [[ "$prev" == "--limit" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -W "--limit --json --scrub --dry-run" -- "$cur") )
            fi
            ;;
        stats)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --impact --year --html --redaction --card --harness --project --since --role" -- "$cur") )
            fi
            ;;
        sync)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W "export import ssh forget" -- "$cur") )
            elif [[ "$action" == "export" ]]; then
                if [[ "$prev" == "--peer" ]]; then
                    COMPREPLY=()
                else
                    COMPREPLY=( $(compgen -W "--full --include-imported --peer" -- "$cur") )
                fi
            elif [[ "$action" == "ssh" ]]; then
                COMPREPLY=( $(compgen -W "--pull --full --both" -- "$cur") )
            elif [[ "$action" == "forget" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -d -- "$cur") )
            fi
            ;;
        show)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --harness --offset --limit" -- "$cur") )
            fi
            ;;
        update)
            COMPREPLY=( $(compgen -W "--force" -- "$cur") )
            ;;
        wip)
            COMPREPLY=( $(compgen -W "--json" -- "$cur") )
            ;;
        log)
            COMPREPLY=( $(compgen -W "--last --json" -- "$cur") )
            ;;
        view)
            if [[ "$prev" == "--out" ]]; then
                COMPREPLY=( $(compgen -f -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--out --no-open" -- "$cur") )
            fi
            ;;
        recap)
            if [[ "$prev" == "--since" || "$prev" == "--limit" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -W "--since --limit --json" -- "$cur") )
            fi
            ;;
        files|how)
            if [[ "$prev" == "--project" || "$prev" == "--limit" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -W "--project --all-projects --limit --json" -- "$cur") )
            fi
            ;;
        promote)
            if [[ "$prev" == "--state" ]]; then
                COMPREPLY=( $(compgen -W "%NOTE_STATES%" -- "$cur") )
            elif [[ "$prev" == "--to" ]]; then
                COMPREPLY=( $(compgen -f -- "$cur") )
            elif [[ "$prev" == "--note" || "$prev" == "--tag" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -W "--state --note --tag --to" -- "$cur") )
            fi
            ;;
        fix|friction|tests)
            if [[ "$prev" == "--limit" ]]; then
                COMPREPLY=()
            else
                COMPREPLY=( $(compgen -W "--limit --json" -- "$cur") )
            fi
            ;;
        brief|check|ctx|embed|hook-precompact|hook-prompt|mcp|share|sources|statusline|version|warmup)
            COMPREPLY=()
            ;;
        *)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$prev" == "--sort" ]]; then
                COMPREPLY=( $(compgen -W "relevance date" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --re --all --no-embed --harness --project --since --role --session --rebuild --limit --sort" -- "$cur") )
            fi
            ;;
    esac
}

complete -F _deja_completion deja
`

const zshCompletion = `#compdef deja

_deja() {
  local -a commands harnesses install_targets
  commands=(
    'blame:find sessions that discussed a file'
    'bench:run benchmarks'
    'check:read a plan from stdin and print factual co-occurrences'
    'completion:generate shell completion'
    'ctx:print a compact context digest'
    'files:which files the work on a topic touched'
    'fix:what was run after this error before'
    'friction:errors this machine keeps hitting across sessions'
    'secrets:credentials your agent transcripts are carrying'
    'tests:your build and test runs, week by week'
    'recap:what the last week settled, with receipts'
    'recall:the page the MCP recall tool gives an agent'
    'how:commands this machine actually ran for a thing'
    'delete:erase a session from its agent and from deja'
    'doctor:diagnose local stores and wiring'
    'promote:distill a session into a curated note'
    'rules:copy your rules file into every agent, or list your corrections'
    'embed:build the semantic sidecar'
    'forget:remove indexed sessions'
    'handoff:continue a session in another agent'
    'help:print the command reference'
    'index:build or refresh the index'
    'install:wire deja into an agent'
    'last:list recent sessions'
    'brief:recent sessions and what deja served, as text'
    'log:show what deja served to agents'
    'mcp:serve the MCP protocol'
    'remember:store a durable note'
    'restore:hand back a span an agent replaced'
    'resume:reopen a session'
    'search:search your history explicitly'
    'share:print a sanitized session digest'
    'show:print a session'
    'sources:list discovered stores'
    'stats:print usage statistics'
    'view:browse your memory in one local HTML page'
    'wip:what the last session in this project was doing'
    'statusline:print status bar data'
    'sync:move memory between machines'
    'uninstall:remove deja agent wiring'
    'update:update a standalone install'
    'version:print the version'
    'warmup:build or refresh the index'
  )
  harnesses=(%HARNESSES%)
  install_targets=(%INSTALL_TARGETS% --all --auto)

  if (( CURRENT == 2 )); then
    _describe -t commands 'deja command' commands
    return
  fi

  case "$words[2]" in
    blame)
      _arguments '--attribution[show line attribution]' '--git-note[write attribution as a git note; implies --attribution]' '--all[include all matching sessions]' '--all-projects[include other projects]' '--json[print JSON]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '1:path:_files'
      ;;
    bench)
      if (( CURRENT == 3 )); then
        _values 'benchmark' recall context prompt block ingest read
      else
        _arguments '--json[print JSON]' '--seed=[benchmark seed]:seed:'
      fi
      ;;
    completion)
      _values 'shell' bash zsh fish powershell pwsh
      ;;
    doctor)
      _arguments '--json[print JSON]' '--offline[skip version check]' '--deep[verify index against sources]' '--all[list every store, missing ones included]'
      ;;
    delete)
      _arguments '--dry-run[list the files, remove nothing]' '--yes[do not ask]' '1:session ID prefix:'
      ;;
    forget)
      _arguments '--list[list tombstones]' '--dry-run[show changes without applying]' '--session=[session ID prefix]:session:' '--project=[project substring]:project:' '--before=[duration or date]:time:' '--unforget=[tombstone ID]:ID:' '--all-matches[act on every match]'
      ;;
    handoff)
      _arguments '--to=[target agent]:agent:(%HANDOFF_TARGETS%)' '--exec[launch the target agent]' '1:session ID prefix:'
      ;;
    hook-context)
      _arguments '--plain[omit formatting]' '--once[one digest per session]' '--copilot[answer in the Copilot CLI hook shape]' '--notes[only the notes meant for the person]'
      ;;
    index)
      _arguments '--rebuild[force a full rebuild]' '-rebuild[force a full rebuild]' '--quiet[say nothing when it worked]' '-quiet[say nothing when it worked]'
      ;;
    install)
      _arguments '--no-guidance[skip guidance files]' '--no-index[skip indexing]' '--force[replace edited guidance files]' "1:target:($install_targets)"
      ;;
    uninstall)
      _arguments '--no-guidance[skip guidance files]' "1:target:($install_targets)"
      ;;
    last)
      _arguments '--json[print JSON]' '--from=[which machine]:origin:(machine local)' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(%ROLES%)' '1:count:'
      ;;
    recall)
      _arguments '--project=[project name]:project:' '--harness=[filter by harness]:harness:($harnesses)' '--limit=[maximum sessions]:count:' '--offset=[sessions to skip]:count:' '*:query:'
      ;;
    remember)
      _arguments '--project=[note project]:project:' '*--tag=[tag the note, repeatable]:tag:' '1:text:'
      ;;
    rules)
      if (( CURRENT == 3 )); then
        _values 'rules action' sync status candidates
      elif [[ "$words[3]" == "candidates" ]]; then
        _arguments '--json[print JSON]' '--limit=[maximum candidates]:count:' '--since=[filter by age]:duration:'
      fi
      ;;
    resume)
      _arguments '--exec[launch the native harness]' '--write-back[write a deleted transcript back from the index]' '--app[open a Codex session in the ChatGPT app]' '1:session ID prefix:'
      ;;
    restore)
      _arguments '--span[which recorded span to restore]:number:' '(--out)-o[write the span to a file]:file:_files' '(-o)--out[write the span to a file]:file:_files' '--force[overwrite an existing -o file]' '--all[list every recorded span]' '1:path:_files'
      ;;
    secrets)
      _arguments '--limit=[maximum findings]:count:' '--json[print JSON]' '--scrub[rewrite files to remove secrets]' '--dry-run[show what --scrub would change]'
      ;;
    stats)
      _arguments '--json[print JSON]' '--impact[measured impact report]' '--year[your last twelve months in one screen]' '--html=[write HTML timeline]:path:_files' '--redaction[include redaction facts]' '--card=[write SVG card]:path:_files' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(%ROLES%)'
      ;;
    sync)
      if (( CURRENT == 3 )); then
        _values 'sync action' export import ssh forget
      elif [[ "$words[3]" == "export" ]]; then
        _arguments '--full[export all records]' '--include-imported[include records received from other machines]' '--peer=[destination peer name]:peer:' '1:directory:_files -/'
      elif [[ "$words[3]" == "import" ]]; then
        _arguments '1:directory:_files -/'
      elif [[ "$words[3]" == "forget" ]]; then
        _arguments '1:host:'
      else
        _arguments '--pull[pull from the remote]' '--full[transfer all records]' '--both[transfer in both directions]' '1:host:'
      fi
      ;;
    show)
      _arguments '--json[print JSON]' '--harness=[filter by harness]:harness:($harnesses)' '--offset=[skip leading messages]:count:' '--limit=[cap messages printed]:count:' '1:session ID prefix:'
      ;;
    update)
      _arguments '--force[write over a binary a package manager owns]'
      ;;
    wip)
      _arguments '--json[print JSON]'
      ;;
    log)
      _arguments '--last[print the most recent injected digest]' '--json[print JSON]' '1:count:'
      ;;
    view)
      _arguments '--out[write the page to a file]:file:_files' '--no-open[do not open the browser]'
      ;;
    recap)
      _arguments '--since[how far back, a duration like 30d (default 7d)]:duration:' '--limit[maximum sessions]:count:' '--json[print JSON]'
      ;;
    files|how)
      _arguments '--project[project name]:project:' '--all-projects[search every project, not just this one]' '--limit[maximum results]:count:' '--json[print JSON]' '*:topic:'
      ;;
    promote)
      if [[ "$words[CURRENT-1]" == "--state" ]]; then
        _values 'state' %NOTE_STATES%
      else
        _arguments '--state[mark the note]:state:(%NOTE_STATES%)' '--note[the note text]:text:' '*--tag[tag the note, repeatable]:tag:' '--to[also append the note to a Markdown file]:file:_files' '1:session ID prefix:'
      fi
      ;;
    fix|friction|tests)
      _arguments '--limit[max results]:count:' '--json[print JSON]'
      ;;
    brief|check|ctx|embed|hook-precompact|hook-prompt|mcp|share|sources|statusline|version|warmup)
      ;;
    *)
      _arguments '--json[print JSON]' '--re[interpret query as a regular expression]' '--all[include all results]' '--no-embed[skip semantic reranking]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(%ROLES%)' '--session=[only one session]:id:' '--rebuild[force a full rebuild]' '--limit=[max sessions to return (1-100)]:count:' '--sort=[order of the results]:order:(relevance date)'
      ;;
  esac
}

compdef _deja deja
`

const fishCompletion = `function __deja_needs_command
    test (count (commandline -opc)) -eq 1
end

complete -c deja -n '__deja_needs_command' -a '%COMMANDS%'
complete -c deja -n '__deja_needs_command' -l json -d 'Print JSON'
complete -c deja -n '__deja_needs_command' -l re -d 'Interpret query as a regular expression'
complete -c deja -n '__deja_needs_command' -l all -d 'Include all results'
complete -c deja -n '__deja_needs_command' -l no-embed -d 'Skip semantic reranking'
complete -c deja -n '__deja_needs_command' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__deja_needs_command' -l project -r
complete -c deja -n '__deja_needs_command' -l since -r
complete -c deja -n '__deja_needs_command' -l role -r -a '%ROLES%'
complete -c deja -n '__deja_needs_command' -l session -r
complete -c deja -n '__deja_needs_command' -l rebuild
complete -c deja -n '__deja_needs_command' -l limit -r -d 'Max sessions to return (1-100)'
complete -c deja -n '__fish_seen_subcommand_from search' -l limit -r -d 'Max sessions to return (1-100)'
complete -c deja -n '__deja_needs_command' -l sort -x -a 'relevance date' -d 'Order of the results'
complete -c deja -n '__fish_seen_subcommand_from search' -l sort -x -a 'relevance date' -d 'Order of the results'

complete -c deja -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell pwsh'
complete -c deja -n '__fish_seen_subcommand_from blame' -l attribution -d 'Show line attribution'
complete -c deja -n '__fish_seen_subcommand_from blame' -l git-note -d 'Write attribution as a git note; implies --attribution'
complete -c deja -n '__fish_seen_subcommand_from blame' -l all
complete -c deja -n '__fish_seen_subcommand_from blame' -l all-projects
complete -c deja -n '__fish_seen_subcommand_from blame' -l json
complete -c deja -n '__fish_seen_subcommand_from blame' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from blame' -l project -r
complete -c deja -n '__fish_seen_subcommand_from blame' -l since -r
complete -c deja -n '__fish_seen_subcommand_from blame' -F
complete -c deja -n '__fish_seen_subcommand_from bench; and not __fish_seen_subcommand_from recall context prompt block ingest read' -a 'recall context prompt block ingest read'
complete -c deja -n '__fish_seen_subcommand_from bench; and __fish_seen_subcommand_from recall context prompt block ingest read' -l json
complete -c deja -n '__fish_seen_subcommand_from bench; and __fish_seen_subcommand_from recall context prompt block ingest read' -l seed -r
complete -c deja -n '__fish_seen_subcommand_from doctor' -l json
complete -c deja -n '__fish_seen_subcommand_from doctor' -l offline
complete -c deja -n '__fish_seen_subcommand_from doctor' -l deep
complete -c deja -n '__fish_seen_subcommand_from doctor' -l all
complete -c deja -n '__fish_seen_subcommand_from delete' -l dry-run -d 'List the files, remove nothing'
complete -c deja -n '__fish_seen_subcommand_from delete' -l yes -d 'Do not ask'
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l list
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l dry-run
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l session -r
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l project -r
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l before -r
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l unforget -r
complete -c deja -n '__fish_seen_subcommand_from forget; and not __fish_seen_subcommand_from sync' -l all-matches
complete -c deja -n '__fish_seen_subcommand_from handoff' -l to -r -a '%HANDOFF_TARGETS%'
complete -c deja -n '__fish_seen_subcommand_from handoff' -l exec
complete -c deja -n '__fish_seen_subcommand_from hook-context' -l plain
complete -c deja -n '__fish_seen_subcommand_from hook-context' -l once
complete -c deja -n '__fish_seen_subcommand_from hook-context' -l notes
complete -c deja -n '__fish_seen_subcommand_from index' -l rebuild
complete -c deja -n '__fish_seen_subcommand_from index' -l quiet -d 'Say nothing when it worked'
complete -c deja -n '__fish_seen_subcommand_from install uninstall' -a '%INSTALL_TARGETS% --all --auto'
complete -c deja -n '__fish_seen_subcommand_from install uninstall' -l no-guidance
complete -c deja -n '__fish_seen_subcommand_from install' -l no-index -d 'Skip indexing'
complete -c deja -n '__fish_seen_subcommand_from install' -l force -d 'Replace edited guidance files'
complete -c deja -n '__fish_seen_subcommand_from show' -l json
complete -c deja -n '__fish_seen_subcommand_from show' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from show' -l offset -r
complete -c deja -n '__fish_seen_subcommand_from show' -l limit -r
complete -c deja -n '__fish_seen_subcommand_from last' -l json
complete -c deja -n '__fish_seen_subcommand_from last' -l from -r
complete -c deja -n '__fish_seen_subcommand_from last' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from last' -l project -r
complete -c deja -n '__fish_seen_subcommand_from last' -l since -r
complete -c deja -n '__fish_seen_subcommand_from last' -l role -r -a '%ROLES%'
complete -c deja -n '__fish_seen_subcommand_from recall; and not __fish_seen_subcommand_from bench' -l project -r
complete -c deja -n '__fish_seen_subcommand_from recall; and not __fish_seen_subcommand_from bench' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from recall; and not __fish_seen_subcommand_from bench' -l limit -r
complete -c deja -n '__fish_seen_subcommand_from recall; and not __fish_seen_subcommand_from bench' -l offset -r
complete -c deja -n '__fish_seen_subcommand_from remember' -l project -r
complete -c deja -n '__fish_seen_subcommand_from remember' -l tag -r
complete -c deja -n '__fish_seen_subcommand_from rules; and not __fish_seen_subcommand_from sync status candidates' -f -a 'sync status candidates'
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l json
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l limit -r
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l since -r
complete -c deja -n '__fish_seen_subcommand_from resume' -l exec
complete -c deja -n '__fish_seen_subcommand_from resume' -l write-back -d 'Write a deleted transcript back from the index'
complete -c deja -n '__fish_seen_subcommand_from resume' -l app -d 'Open a Codex session in the ChatGPT app'
complete -c deja -n '__fish_seen_subcommand_from restore' -l span -x -d 'Which recorded span to restore'
complete -c deja -n '__fish_seen_subcommand_from restore' -s o -l out -r -F -d 'Write the span to a file'
complete -c deja -n '__fish_seen_subcommand_from restore' -l force -d 'Overwrite an existing -o file'
complete -c deja -n '__fish_seen_subcommand_from restore' -l all -d 'List every recorded span'
complete -c deja -n '__fish_seen_subcommand_from restore' -F
complete -c deja -n '__fish_seen_subcommand_from secrets' -l limit -r
complete -c deja -n '__fish_seen_subcommand_from secrets' -l json
complete -c deja -n '__fish_seen_subcommand_from secrets' -l scrub -d 'Rewrite files to remove secrets'
complete -c deja -n '__fish_seen_subcommand_from secrets' -l dry-run -d 'Show what --scrub would change'
complete -c deja -n '__fish_seen_subcommand_from stats' -l json
complete -c deja -n '__fish_seen_subcommand_from stats' -l impact
complete -c deja -n '__fish_seen_subcommand_from stats' -l year
complete -c deja -n '__fish_seen_subcommand_from stats' -l html -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l redaction
complete -c deja -n '__fish_seen_subcommand_from stats' -l card -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from stats' -l project -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l since -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l role -r -a '%ROLES%'
complete -c deja -n '__fish_seen_subcommand_from update' -l force -d 'Write over a binary a package manager owns'
complete -c deja -n '__fish_seen_subcommand_from wip' -l json
complete -c deja -n '__fish_seen_subcommand_from log' -l last -d 'Print the most recent injected digest'
complete -c deja -n '__fish_seen_subcommand_from log' -l json
complete -c deja -n '__fish_seen_subcommand_from view' -l out -r -F -d 'Write the page to a file'
complete -c deja -n '__fish_seen_subcommand_from view' -l no-open -d 'Do not open the browser'
complete -c deja -n '__fish_seen_subcommand_from fix friction tests' -l limit -x -d 'Max results'
complete -c deja -n '__fish_seen_subcommand_from fix friction tests' -l json -d 'Print JSON'
complete -c deja -n '__fish_seen_subcommand_from recap' -l since -x -d 'How far back, a duration like 30d (default 7d)'
complete -c deja -n '__fish_seen_subcommand_from recap' -l limit -x -d 'Maximum sessions'
complete -c deja -n '__fish_seen_subcommand_from recap' -l json
complete -c deja -n '__fish_seen_subcommand_from files how' -l project -x
complete -c deja -n '__fish_seen_subcommand_from files how' -l all-projects -d 'Search every project, not just this one'
complete -c deja -n '__fish_seen_subcommand_from files how' -l limit -x
complete -c deja -n '__fish_seen_subcommand_from files how' -l json
complete -c deja -n '__fish_seen_subcommand_from promote' -l state -x -a '%NOTE_STATES%' -d 'Mark the note'
complete -c deja -n '__fish_seen_subcommand_from promote' -l note -x -d 'The note text'
complete -c deja -n '__fish_seen_subcommand_from promote' -l tag -x -d 'Tag the note, repeatable'
complete -c deja -n '__fish_seen_subcommand_from promote' -l to -r -F -d 'Also append the note to a Markdown file'
complete -c deja -n '__fish_seen_subcommand_from sync; and not __fish_seen_subcommand_from rules; and not __fish_seen_subcommand_from export import ssh forget' -a 'export import ssh forget'
complete -c deja -n '__fish_seen_subcommand_from sync; and __fish_seen_subcommand_from export' -l include-imported
complete -c deja -n '__fish_seen_subcommand_from sync; and __fish_seen_subcommand_from export' -l peer -r
complete -c deja -n '__fish_seen_subcommand_from export' -l full
complete -c deja -n '__fish_seen_subcommand_from export' -F
complete -c deja -n '__fish_seen_subcommand_from import' -F
complete -c deja -n '__fish_seen_subcommand_from ssh' -l pull
complete -c deja -n '__fish_seen_subcommand_from ssh' -l full
complete -c deja -n '__fish_seen_subcommand_from sync; and __fish_seen_subcommand_from ssh' -l both
`

const powershellCompletion = `# PowerShell completion for deja
Register-ArgumentCompleter -Native -CommandName deja -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $commands = @(%COMMANDS_QUOTED%)
    $harnesses = @('%HARNESSES%' -split ' ' | Where-Object { $_ })
    $roles = @('%ROLES%' -split ' ' | Where-Object { $_ })
    $noteStates = @('%NOTE_STATES%' -split ' ' | Where-Object { $_ })
    $installTargets = @('%INSTALL_TARGETS%' -split ' ' | Where-Object { $_ }) + @('--all', '--auto')
    $handoffTargets = @('%HANDOFF_TARGETS%' -split ' ' | Where-Object { $_ })
    $defaultOptions = @(
        '--json', '--re', '--all', '--no-embed', '--harness', '--project',
        '--since', '--role', '--session', '--rebuild', '--limit', '--sort'
    )

    $elements = @($commandAst.CommandElements | ForEach-Object { $_.Extent.Text })
    $hasCurrentWord = -not [string]::IsNullOrEmpty($wordToComplete)
    $completingCommand = $elements.Count -le 1 -or ($elements.Count -eq 2 -and $hasCurrentWord)

    if ($completingCommand) {
        $candidates = $commands + @('--version', '-version') + $defaultOptions
    } else {
        $command = $elements[1]
        $action = if ($elements.Count -gt 2) { $elements[2] } else { '' }
        $argumentPosition = if ($hasCurrentWord) { $elements.Count - 2 } else { $elements.Count - 1 }
        $previous = if ($hasCurrentWord -and $elements.Count -gt 1) {
            $elements[$elements.Count - 2]
        } elseif ($elements.Count -gt 0) {
            $elements[$elements.Count - 1]
        } else {
            ''
        }

        $candidates = switch ($command) {
            'blame' {
                if ($previous -eq '--harness') { $harnesses }
                else { @('--all', '--all-projects', '--json', '--harness', '--project', '--since', '--attribution', '--git-note') }
            }
            'bench' {
                if ($argumentPosition -eq 1) { @('recall', 'context', 'prompt', 'block', 'ingest', 'read') }
                else { @('--json', '--seed') }
            }
            'completion' { @('bash', 'zsh', 'fish', 'powershell', 'pwsh') }
            'doctor' { @('--json', '--offline', '--deep', '--all') }
            'delete' { @('--dry-run', '--yes') }
            'forget' { @('--list', '--dry-run', '--session', '--project', '--before', '--unforget', '--all-matches') }
            'handoff' {
                if ($previous -eq '--to') { $handoffTargets }
                else { @('--to', '--exec') }
            }
            'hook-context' { @('--plain', '--once', '--copilot', '--notes') }
            'index' { @('--rebuild', '-rebuild', '--quiet', '-quiet') }
            'install' { $installTargets + @('--no-guidance', '--no-index', '--force') }
            'uninstall' { $installTargets + @('--no-guidance') }
            'last' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { $roles }
                else { @('--json', '--from', '--harness', '--project', '--since', '--role') }
            }
            'recall' {
                if ($previous -eq '--harness') { $harnesses }
                else { @('--project', '--harness', '--limit', '--offset') }
            }
            'remember' { @('--project', '--tag') }
            'rules' {
                if ($argumentPosition -eq 1) { @('sync', 'status', 'candidates') }
                elseif ($action -eq 'candidates' -and $previous -notin @('--limit', '--since')) { @('--json', '--limit', '--since') }
                else { @() }
            }
            'resume' { @('--exec', '--write-back', '--app') }
            'restore' {
                if ($previous -in @('-o', '--out', '--span') -or -not $wordToComplete.StartsWith('-')) { @() }
                else { @('--span', '-o', '--out', '--force', '--all') }
            }
            'secrets' {
                if ($previous -eq '--limit') { @() }
                else { @('--limit', '--json', '--scrub', '--dry-run') }
            }
            'stats' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { $roles }
                else { @('--json', '--impact', '--year', '--html', '--redaction', '--card', '--harness', '--project', '--since', '--role') }
            }
            'sync' {
                if ($argumentPosition -eq 1) { @('export', 'import', 'ssh', 'forget') }
                elseif ($action -eq 'export' -and $previous -ne '--peer') { @('--full', '--include-imported', '--peer') }
                elseif ($action -eq 'ssh') { @('--pull', '--full', '--both') }
                else { @() }
            }
            'show' {
                if ($previous -eq '--harness') { $harnesses }
                else { @('--json', '--harness', '--offset', '--limit') }
            }
            'update' { @('--force') }
            'wip' { @('--json') }
            'log' { @('--last', '--json') }
            'view' {
                if ($previous -eq '--out') { @() }
                else { @('--out', '--no-open') }
            }
            'recap' {
                if ($previous -in @('--since', '--limit')) { @() }
                else { @('--since', '--limit', '--json') }
            }
            { $_ -in @('files', 'how') } {
                if ($previous -in @('--project', '--limit')) { @() }
                else { @('--project', '--all-projects', '--limit', '--json') }
            }
            'promote' {
                if ($previous -eq '--state') { $noteStates }
                elseif ($previous -in @('--note', '--tag', '--to')) { @() }
                else { @('--state', '--note', '--tag', '--to') }
            }
            { $_ -in @('fix', 'friction', 'tests') } {
                if ($previous -eq '--limit') { @() }
                else { @('--limit', '--json') }
            }
            { $_ -in @('brief', 'check', 'ctx', 'embed', 'hook-precompact', 'hook-prompt', 'mcp', 'share', 'sources', 'statusline', 'version', 'warmup') } { @() }
            default { if ($previous -eq '--sort') { @('relevance', 'date') } else { $defaultOptions } }
        }
    }

    $candidates |
        Where-Object {
            $_ -and $_.StartsWith($wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)
        } |
        Sort-Object -Unique |
        ForEach-Object {
            [System.Management.Automation.CompletionResult]::new(
                $_, $_, [System.Management.Automation.CompletionResultType]::ParameterValue, $_
            )
        }
}
`

// completionCommands is what the shells offer, in one place instead of a copy
// per shell: `deja recap` and `deja tests` shipped in 0.21.0 and bash and fish
// had never heard of either, while the PowerShell array had grown three
// overlapping copies of itself from merges landing beside each other.
//
// Written out rather than read from the command table, because that table holds
// this function and Go will not have the cycle. What keeps the two in step is
// TestEveryCommandIsOfferedByEveryShell, which fails the moment a command is
// added to one and not the other.
func completionCommands() []string {
	return []string{
		"bench", "blame", "brief", "check", "completion", "ctx", "delete", "doctor", "embed",
		"files", "fix", "forget", "friction", "handoff", "help", "how", "index",
		"install", "last", "log", "mcp", "promote", "recall", "recap", "remember", "restore",
		"resume", "rules", "search", "secrets", "share", "show", "sources", "stats",
		"statusline", "sync", "tests", "uninstall", "update", "version", "view",
		"warmup", "wip",
	}
}

// completionHiddenCommands are the ones deja runs for itself. Leaving a command
// out is a decision recorded here rather than a name missing from three scripts.
var completionHiddenCommands = map[string]bool{
	"warmup-status": true,
	// Reasonix starts it as the plugin's runtime and talks JSON-RPC to it.
	"reasonix-ext": true,
}
