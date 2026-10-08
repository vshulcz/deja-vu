package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// runCompletion writes a shell-specific script so the binary stays dependency-free.
func runCompletion(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("completion needs bash, zsh, fish, or powershell")
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
        COMPREPLY=( $(compgen -W "$commands --version -version --json --re --all --no-embed --harness --project --since --role --session --rebuild --quiet --limit" -- "$cur") )
        return
    fi

    case "$command" in
        blame)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "--all --json --harness --project --since --attribution --git-note" -- "$cur") )
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
            COMPREPLY=( $(compgen -W "--exec" -- "$cur") )
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
        check|ctx|embed|hook-precompact|hook-prompt|mcp|share|sources|statusline|update|version|warmup)
            COMPREPLY=()
            ;;
        *)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --re --all --no-embed --harness --project --since --role --session --rebuild --quiet --limit" -- "$cur") )
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
    'how:commands this machine actually ran for a thing'
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
    'brief:the screen bare deja prints on a terminal'
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
      _arguments '--attribution[show line attribution]' '--git-note[write attribution as a git note; implies --attribution]' '--all[include all matching sessions]' '--json[print JSON]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '1:path:_files'
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
      _arguments '--exec[launch the native harness]' '1:session ID prefix:'
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
    check|ctx|embed|hook-precompact|hook-prompt|mcp|share|sources|statusline|update|version|warmup)
      ;;
    *)
      _arguments '--json[print JSON]' '--re[interpret query as a regular expression]' '--all[include all results]' '--no-embed[skip semantic reranking]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(%ROLES%)' '--session=[only one session]:id:' '--rebuild[force a full rebuild]' '--quiet[say nothing when it worked]' '--limit=[max sessions to return (1-100)]:count:'
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

complete -c deja -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell pwsh'
complete -c deja -n '__fish_seen_subcommand_from blame' -l attribution -d 'Show line attribution'
complete -c deja -n '__fish_seen_subcommand_from blame' -l git-note -d 'Write attribution as a git note; implies --attribution'
complete -c deja -n '__fish_seen_subcommand_from blame' -l all
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
complete -c deja -n '__fish_seen_subcommand_from remember' -l project -r
complete -c deja -n '__fish_seen_subcommand_from remember' -l tag -r
complete -c deja -n '__fish_seen_subcommand_from rules; and not __fish_seen_subcommand_from sync status candidates' -f -a 'sync status candidates'
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l json
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l limit -r
complete -c deja -n '__fish_seen_subcommand_from rules; and __fish_seen_subcommand_from candidates' -l since -r
complete -c deja -n '__fish_seen_subcommand_from resume' -l exec
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
    $installTargets = @('%INSTALL_TARGETS%' -split ' ' | Where-Object { $_ }) + @('--all', '--auto')
    $handoffTargets = @('%HANDOFF_TARGETS%' -split ' ' | Where-Object { $_ })
    $defaultOptions = @(
        '--json', '--re', '--all', '--no-embed', '--harness', '--project',
        '--since', '--role', '--session', '--rebuild', '--limit'
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
                else { @('--all', '--json', '--harness', '--project', '--since', '--attribution', '--git-note') }
            }
            'bench' {
                if ($argumentPosition -eq 1) { @('recall', 'context', 'prompt', 'block', 'ingest', 'read') }
                else { @('--json', '--seed') }
            }
            'completion' { @('bash', 'zsh', 'fish', 'powershell', 'pwsh') }
            'doctor' { @('--json', '--offline', '--deep', '--all') }
            'forget' { @('--list', '--dry-run', '--session', '--project', '--before', '--unforget', '--all-matches') }
            'handoff' {
                if ($previous -eq '--to') { $handoffTargets }
                else { @('--to', '--exec') }
            }
            'hook-context' { @('--plain', '--once', '--copilot', '--notes') }
            'index' { @('--rebuild', '-rebuild') }
            'install' { $installTargets + @('--no-guidance', '--no-index', '--force') }
            'uninstall' { $installTargets + @('--no-guidance') }
            'last' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { @('user', 'assistant', 'tool') }
                else { @('--json', '--from', '--harness', '--project', '--since', '--role') }
            }
            'remember' { @('--project', '--tag') }
            'rules' {
                if ($argumentPosition -eq 1) { @('sync', 'status', 'candidates') }
                elseif ($action -eq 'candidates' -and $previous -notin @('--limit', '--since')) { @('--json', '--limit', '--since') }
                else { @() }
            }
            'resume' { @('--exec') }
            'secrets' {
                if ($previous -eq '--limit') { @() }
                else { @('--limit', '--json', '--scrub', '--dry-run') }
            }
            'stats' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { @('user', 'assistant', 'tool') }
                else { @('--json', '--impact', '--html', '--redaction', '--card', '--harness', '--project', '--since', '--role') }
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
            { $_ -in @('check', 'ctx', 'embed', 'hook-precompact', 'hook-prompt', 'mcp', 'share', 'sources', 'statusline', 'update', 'version', 'warmup') } { @() }
            default { $defaultOptions }
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
		"bench", "blame", "brief", "check", "completion", "ctx", "doctor", "embed",
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
