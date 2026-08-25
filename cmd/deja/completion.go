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
    command="${COMP_WORDS[1]}"
    action="${COMP_WORDS[2]}"

    local commands="blame bench check completion ctx doctor embed files fix forget friction handoff help how index install last log mcp promote remember restore resume search share show sources stats statusline sync uninstall update version view warmup"
    local harnesses="%HARNESSES%"
    local install_targets="%INSTALL_TARGETS% --all --auto"

    if (( COMP_CWORD == 1 )); then
        COMPREPLY=( $(compgen -W "$commands --version -version --json --re --all --no-embed --harness --project --since --role --session --rebuild" -- "$cur") )
        return
    fi

    case "$command" in
        blame)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "--all --json --harness --project --since" -- "$cur") )
            else
                COMPREPLY=( $(compgen -f -- "$cur") )
            fi
            ;;
        bench)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W "recall context" -- "$cur") )
            elif [[ "$action" == "recall" ]]; then
                COMPREPLY=( $(compgen -W "--json" -- "$cur") )
            elif [[ "$action" == "context" ]]; then
                COMPREPLY=( $(compgen -W "--json --seed" -- "$cur") )
            fi
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish powershell pwsh" -- "$cur") )
            ;;
        doctor)
            COMPREPLY=( $(compgen -W "--json --offline --deep" -- "$cur") )
            ;;
        forget)
            COMPREPLY=( $(compgen -W "--list --dry-run --session --project --before --unforget" -- "$cur") )
            ;;
        handoff)
            if [[ "$prev" == "--to" ]]; then
                COMPREPLY=( $(compgen -W "%HANDOFF_TARGETS%" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--to --exec" -- "$cur") )
            fi
            ;;
        hook-context)
            COMPREPLY=( $(compgen -W "--plain" -- "$cur") )
            ;;
        index)
            COMPREPLY=( $(compgen -W "--rebuild -rebuild" -- "$cur") )
            ;;
        install|uninstall)
            COMPREPLY=( $(compgen -W "$install_targets --no-guidance" -- "$cur") )
            ;;
        last)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            elif [[ "$prev" == "--role" ]]; then
                COMPREPLY=( $(compgen -W "user assistant tool" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--harness --project --since --role" -- "$cur") )
            fi
            ;;
        remember)
            COMPREPLY=( $(compgen -W "--project" -- "$cur") )
            ;;
        resume)
            COMPREPLY=( $(compgen -W "--exec" -- "$cur") )
            ;;
        stats)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --impact --html --redaction --card --harness --project --since --role" -- "$cur") )
            fi
            ;;
        sync)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W "export import ssh" -- "$cur") )
            elif [[ "$action" == "export" ]]; then
                COMPREPLY=( $(compgen -W "--full" -- "$cur") )
            elif [[ "$action" == "ssh" ]]; then
                COMPREPLY=( $(compgen -W "--pull --full" -- "$cur") )
            else
                COMPREPLY=( $(compgen -d -- "$cur") )
            fi
            ;;
        check|ctx|embed|hook-precompact|hook-prompt|mcp|share|show|sources|statusline|update|version|warmup)
            COMPREPLY=()
            ;;
        *)
            if [[ "$prev" == "--harness" ]]; then
                COMPREPLY=( $(compgen -W "$harnesses" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "--json --re --all --no-embed --harness --project --since --role --session --rebuild" -- "$cur") )
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
    'how:commands this machine actually ran for a thing'
    'doctor:diagnose local stores and wiring'
    'promote:distill a session into a curated note'
    'embed:build the semantic sidecar'
    'forget:remove indexed sessions'
    'handoff:continue a session in another agent'
    'help:print the command reference'
    'index:build or refresh the index'
    'install:wire deja into an agent'
    'last:list recent sessions'
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
      _arguments '--all[include all matching sessions]' '--json[print JSON]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '1:path:_files'
      ;;
    bench)
      if (( CURRENT == 3 )); then
        _values 'benchmark' recall context
      elif [[ "$words[3]" == "recall" ]]; then
        _arguments '--json[print JSON]'
      else
        _arguments '--json[print JSON]' '--seed=[benchmark seed]:seed:'
      fi
      ;;
    completion)
      _values 'shell' bash zsh fish powershell pwsh
      ;;
    doctor)
      _arguments '--json[print JSON]' '--offline[skip version check]' '--deep[verify index against sources]'
      ;;
    forget)
      _arguments '--list[list tombstones]' '--dry-run[show changes without applying]' '--session=[session ID prefix]:session:' '--project=[project substring]:project:' '--before=[duration or date]:time:' '--unforget=[tombstone ID]:ID:'
      ;;
    handoff)
      _arguments '--to=[target agent]:agent:(%HANDOFF_TARGETS%)' '--exec[launch the target agent]' '1:session ID prefix:'
      ;;
    hook-context)
      _arguments '--plain[omit formatting]'
      ;;
    index)
      _arguments '--rebuild[force a full rebuild]' '-rebuild[force a full rebuild]'
      ;;
    install|uninstall)
      _arguments '--no-guidance[skip guidance files]' "1:target:($install_targets)"
      ;;
    last)
      _arguments '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(user assistant tool)' '1:count:'
      ;;
    remember)
      _arguments '--project=[note project]:project:' '1:text:'
      ;;
    resume)
      _arguments '--exec[launch the native harness]' '1:session ID prefix:'
      ;;
    stats)
      _arguments '--json[print JSON]' '--impact[measured impact report]' '--html=[write HTML timeline]:path:_files' '--redaction[include redaction facts]' '--card=[write SVG card]:path:_files' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(user assistant tool)'
      ;;
    sync)
      if (( CURRENT == 3 )); then
        _values 'sync action' export import ssh
      elif [[ "$words[3]" == "export" ]]; then
        _arguments '--full[export all records]' '1:directory:_files -/'
      elif [[ "$words[3]" == "import" ]]; then
        _arguments '1:directory:_files -/'
      else
        _arguments '--pull[pull from the remote]' '--full[transfer all records]' '1:host:'
      fi
      ;;
    check|ctx|embed|hook-precompact|hook-prompt|mcp|share|show|sources|statusline|update|version|warmup)
      ;;
    *)
      _arguments '--json[print JSON]' '--re[interpret query as a regular expression]' '--all[include all results]' '--no-embed[skip semantic reranking]' '--harness=[filter by harness]:harness:($harnesses)' '--project=[filter by project]:project:' '--since=[filter by age]:duration:' '--role=[filter by role]:role:(user assistant tool)' '--session=[only one session]:id:' '--rebuild[force a full rebuild]'
      ;;
  esac
}

compdef _deja deja
`

const fishCompletion = `function __deja_needs_command
    test (count (commandline -opc)) -eq 1
end

complete -c deja -n '__deja_needs_command' -a 'blame bench check completion ctx doctor embed files fix forget friction handoff help how index install last log mcp promote remember restore resume search share show sources stats statusline sync uninstall update version view warmup'
complete -c deja -n '__deja_needs_command' -l json -d 'Print JSON'
complete -c deja -n '__deja_needs_command' -l re -d 'Interpret query as a regular expression'
complete -c deja -n '__deja_needs_command' -l all -d 'Include all results'
complete -c deja -n '__deja_needs_command' -l no-embed -d 'Skip semantic reranking'
complete -c deja -n '__deja_needs_command' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__deja_needs_command' -l project -r
complete -c deja -n '__deja_needs_command' -l since -r
complete -c deja -n '__deja_needs_command' -l role -r -a 'user assistant tool'
complete -c deja -n '__deja_needs_command' -l session -r
complete -c deja -n '__deja_needs_command' -l rebuild

complete -c deja -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell pwsh'
complete -c deja -n '__fish_seen_subcommand_from blame' -l all
complete -c deja -n '__fish_seen_subcommand_from blame' -l json
complete -c deja -n '__fish_seen_subcommand_from blame' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from blame' -l project -r
complete -c deja -n '__fish_seen_subcommand_from blame' -l since -r
complete -c deja -n '__fish_seen_subcommand_from blame' -F
complete -c deja -n '__fish_seen_subcommand_from bench; and not __fish_seen_subcommand_from recall context' -a 'recall context'
complete -c deja -n '__fish_seen_subcommand_from recall' -l json
complete -c deja -n '__fish_seen_subcommand_from context' -l json
complete -c deja -n '__fish_seen_subcommand_from context' -l seed -r
complete -c deja -n '__fish_seen_subcommand_from doctor' -l json
complete -c deja -n '__fish_seen_subcommand_from doctor' -l offline
complete -c deja -n '__fish_seen_subcommand_from doctor' -l deep
complete -c deja -n '__fish_seen_subcommand_from forget' -l list
complete -c deja -n '__fish_seen_subcommand_from forget' -l dry-run
complete -c deja -n '__fish_seen_subcommand_from forget' -l session -r
complete -c deja -n '__fish_seen_subcommand_from forget' -l project -r
complete -c deja -n '__fish_seen_subcommand_from forget' -l before -r
complete -c deja -n '__fish_seen_subcommand_from forget' -l unforget -r
complete -c deja -n '__fish_seen_subcommand_from handoff' -l to -r -a '%HANDOFF_TARGETS%'
complete -c deja -n '__fish_seen_subcommand_from handoff' -l exec
complete -c deja -n '__fish_seen_subcommand_from hook-context' -l plain
complete -c deja -n '__fish_seen_subcommand_from index' -l rebuild
complete -c deja -n '__fish_seen_subcommand_from install uninstall' -a '%INSTALL_TARGETS% --all --auto'
complete -c deja -n '__fish_seen_subcommand_from install uninstall' -l no-guidance
complete -c deja -n '__fish_seen_subcommand_from last' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from last' -l project -r
complete -c deja -n '__fish_seen_subcommand_from last' -l since -r
complete -c deja -n '__fish_seen_subcommand_from last' -l role -r -a 'user assistant tool'
complete -c deja -n '__fish_seen_subcommand_from remember' -l project -r
complete -c deja -n '__fish_seen_subcommand_from resume' -l exec
complete -c deja -n '__fish_seen_subcommand_from stats' -l json
complete -c deja -n '__fish_seen_subcommand_from stats' -l impact
complete -c deja -n '__fish_seen_subcommand_from stats' -l html -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l redaction
complete -c deja -n '__fish_seen_subcommand_from stats' -l card -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l harness -r -a '%HARNESSES%'
complete -c deja -n '__fish_seen_subcommand_from stats' -l project -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l since -r
complete -c deja -n '__fish_seen_subcommand_from stats' -l role -r -a 'user assistant tool'
complete -c deja -n '__fish_seen_subcommand_from sync; and not __fish_seen_subcommand_from export import ssh' -a 'export import ssh'
complete -c deja -n '__fish_seen_subcommand_from export' -l full
complete -c deja -n '__fish_seen_subcommand_from export' -F
complete -c deja -n '__fish_seen_subcommand_from import' -F
complete -c deja -n '__fish_seen_subcommand_from ssh' -l pull
complete -c deja -n '__fish_seen_subcommand_from ssh' -l full
`

const powershellCompletion = `# PowerShell completion for deja
Register-ArgumentCompleter -Native -CommandName deja -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $commands = @(
        'blame', 'bench', 'check', 'completion', 'ctx', 'doctor', 'embed',
        'files', 'fix', 'forget', 'friction', 'handoff', 'help', 'how',
        'index', 'install', 'last', 'log', 'mcp', 'promote', 'remember',
        'restore', 'resume', 'search', 'share', 'show', 'sources', 'stats',
        'statusline', 'sync', 'uninstall', 'update', 'version', 'view', 'warmup'
    )
    $harnesses = @('%HARNESSES%' -split ' ' | Where-Object { $_ })
    $installTargets = @('%INSTALL_TARGETS%' -split ' ' | Where-Object { $_ }) + @('--all', '--auto')
    $handoffTargets = @('%HANDOFF_TARGETS%' -split ' ' | Where-Object { $_ })
    $defaultOptions = @(
        '--json', '--re', '--all', '--no-embed', '--harness', '--project',
        '--since', '--role', '--session', '--rebuild'
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
                else { @('--all', '--json', '--harness', '--project', '--since') }
            }
            'bench' {
                if ($argumentPosition -eq 1) { @('recall', 'context') }
                elseif ($action -eq 'recall') { @('--json') }
                else { @('--json', '--seed') }
            }
            'completion' { @('bash', 'zsh', 'fish', 'powershell', 'pwsh') }
            'doctor' { @('--json', '--offline', '--deep') }
            'forget' { @('--list', '--dry-run', '--session', '--project', '--before', '--unforget') }
            'handoff' {
                if ($previous -eq '--to') { $handoffTargets }
                else { @('--to', '--exec') }
            }
            'hook-context' { @('--plain') }
            'index' { @('--rebuild', '-rebuild') }
            { $_ -in @('install', 'uninstall') } { $installTargets + @('--no-guidance') }
            'last' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { @('user', 'assistant', 'tool') }
                else { @('--harness', '--project', '--since', '--role') }
            }
            'remember' { @('--project') }
            'resume' { @('--exec') }
            'stats' {
                if ($previous -eq '--harness') { $harnesses }
                elseif ($previous -eq '--role') { @('user', 'assistant', 'tool') }
                else { @('--json', '--impact', '--html', '--redaction', '--card', '--harness', '--project', '--since', '--role') }
            }
            'sync' {
                if ($argumentPosition -eq 1) { @('export', 'import', 'ssh') }
                elseif ($action -eq 'export') { @('--full') }
                elseif ($action -eq 'ssh') { @('--pull', '--full') }
                else { @() }
            }
            { $_ -in @('check', 'ctx', 'embed', 'hook-precompact', 'hook-prompt', 'mcp', 'share', 'show', 'sources', 'statusline', 'update', 'version', 'warmup') } { @() }
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
