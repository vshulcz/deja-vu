package main

import (
	"maps"
	"reflect"
	"slices"
	"strings"
)

// FlagSpec describes a CLI flag accepted by a command or one of its subcommands.
type FlagSpec struct {
	Name       string
	Short      string
	TakesValue bool
}

// CommandSpec is the single metadata definition for a CLI command, linking its
// parser flags, help visibility, completion script presence, and bare-search guards.
type CommandSpec struct {
	Name             string
	Flags            []FlagSpec
	SubcommandFlags  []FlagSpec
	ToolHelp         bool
	HelpHidden       bool
	CompletionHidden bool
}

// FlagNames returns the declared long and short flag names for the command itself.
func (s CommandSpec) FlagNames() []string {
	out := make([]string, 0, len(s.Flags))
	for _, f := range s.Flags {
		if f.Name != "" {
			out = append(out, f.Name)
		} else if f.Short != "" {
			out = append(out, f.Short)
		}
	}
	return out
}

// ValueFlagNames returns every flag (long and short) across the command and its
// subcommands that expects a following argument value.
func (s CommandSpec) ValueFlagNames() []string {
	var out []string
	for _, f := range slices.Concat(s.Flags, s.SubcommandFlags) {
		if !f.TakesValue {
			continue
		}
		if f.Short != "" {
			out = append(out, f.Short)
		}
		if f.Name != "" {
			out = append(out, f.Name)
		}
	}
	return out
}

var commandSpecs = []CommandSpec{
	{Name: "aider", ToolHelp: true},
	{
		Name: "audit",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--secrets"},
			{Name: "--fail-on-secrets"},
		},
	},
	{
		Name: "bench",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--seed", TakesValue: true},
		},
		SubcommandFlags: []FlagSpec{
			{Name: "--delivery"},
		},
	},
	{
		Name: "blame",
		Flags: []FlagSpec{
			{Name: "--all"},
			{Name: "--all-projects"},
			{Name: "--json"},
			{Name: "--harness", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--attribution"},
			{Name: "--git-note"},
		},
	},
	{Name: "brief"},
	{Name: "check"},
	{Name: "completion"},
	{Name: "ctx"},
	{
		Name: "delete",
		Flags: []FlagSpec{
			{Name: "--dry-run"},
			{Name: "--yes"},
		},
	},
	{
		Name: "doctor",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--offline"},
			{Name: "--deep"},
			{Name: "--all"},
		},
	},
	{Name: "embed"},
	{
		Name: "files",
		Flags: []FlagSpec{
			{Name: "--project", TakesValue: true},
			{Name: "--all-projects"},
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
		},
	},
	{
		Name: "fix",
		Flags: []FlagSpec{
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
		},
	},
	{
		Name: "forget",
		Flags: []FlagSpec{
			{Name: "--list"},
			{Name: "--dry-run"},
			{Name: "--all-matches"},
			{Name: "--session", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--before", TakesValue: true},
			{Name: "--unforget", TakesValue: true},
		},
	},
	{
		Name: "friction",
		Flags: []FlagSpec{
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
			{Name: "--settle", TakesValue: true},
			{Name: "--unsettle", TakesValue: true},
		},
	},
	{Name: "goose", ToolHelp: true},
	{
		Name: "handoff",
		Flags: []FlagSpec{
			{Name: "--to", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--harness", TakesValue: true},
			{Name: "--exec"},
		},
	},
	{Name: "help", HelpHidden: true},
	{Name: "hook-antigravity"},
	{Name: "hook-codewhale", HelpHidden: true},
	{
		Name:       "hook-context",
		HelpHidden: true,
		Flags: []FlagSpec{
			{Name: "--plain"},
			{Name: "--once"},
			{Name: "--strict"},
			{Name: "--copilot"},
			{Name: "--notes"},
			{Name: "--junie"},
			{Name: "--mcode"},
		},
	},
	{Name: "hook-embed", HelpHidden: true},
	{
		Name: "hook-ext",
		Flags: []FlagSpec{
			{Name: "--host", TakesValue: true},
			{Name: "--op", TakesValue: true},
		},
	},
	{Name: "hook-goose", HelpHidden: true},
	{Name: "hook-goose-prompt", HelpHidden: true},
	{Name: "hook-mcp-call", HelpHidden: true},
	{Name: "hook-plan"},
	{Name: "hook-precompact", HelpHidden: true},
	{
		Name: "hook-prompt",
		Flags: []FlagSpec{
			{Name: "--plain"},
			{Name: "--copilot"},
			{Name: "--strict"},
			{Name: "--junie"},
			{Name: "--mcode"},
		},
	},
	{Name: "hook-refresh", HelpHidden: true},
	{
		Name:       "hook-session-end",
		HelpHidden: true,
		Flags: []FlagSpec{
			{Name: "--json"},
		},
	},
	{Name: "hook-stop", HelpHidden: true},
	{
		Name: "hook-tool",
		Flags: []FlagSpec{
			{Name: "--plain"},
			{Name: "--copilot"},
			{Name: "--crush"},
			{Name: "--junie"},
			{Name: "--mcode"},
		},
	},
	{
		Name: "hook-tool-after",
		Flags: []FlagSpec{
			{Name: "--plain"},
			{Name: "--copilot"},
			{Name: "--mcode"},
		},
	},
	{
		Name: "how",
		Flags: []FlagSpec{
			{Name: "--project", TakesValue: true},
			{Name: "--all-projects"},
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
		},
	},
	{
		Name: "index",
		Flags: []FlagSpec{
			{Name: "--rebuild"},
			{Name: "--quiet"},
		},
	},
	{
		Name: "init",
		Flags: []FlagSpec{
			{Name: "--encrypted"},
			{Name: "--passphrase-stdin"},
			{Name: "--auto-lock", TakesValue: true},
		},
	},
	{
		Name: "install",
		Flags: []FlagSpec{
			{Name: "--all"},
			{Name: "--auto"},
			{Name: "--no-guidance"},
			{Name: "--no-index"},
			{Name: "--no-mcp"},
			{Name: "--force"},
		},
	},
	{
		Name: "last",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--project", TakesValue: true},
			{Name: "--harness", TakesValue: true},
			{Name: "--from", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--role", TakesValue: true},
		},
	},
	{
		Name: "lock",
		Flags: []FlagSpec{
			{Name: "--passphrase-stdin"},
		},
	},
	{
		Name: "log",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--jsonl"},
			{Name: "--last"},
		},
	},
	{
		Name: "mcp",
		Flags: []FlagSpec{
			{Name: "--context-search"},
			{Name: "--http", TakesValue: true},
		},
	},
	{
		Name: "origin",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--harness", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--limit", TakesValue: true},
		},
	},
	{
		Name: "promote",
		Flags: []FlagSpec{
			{Name: "--state", TakesValue: true},
			{Name: "--note", TakesValue: true},
			{Name: "--to", TakesValue: true},
			{Name: "--tag", TakesValue: true},
			{Name: "--repo"},
		},
	},
	{Name: "reasonix-ext", HelpHidden: true, CompletionHidden: true},
	{
		Name: "recall",
		Flags: []FlagSpec{
			{Name: "--project", TakesValue: true},
			{Name: "--harness", TakesValue: true},
			{Name: "--limit", TakesValue: true},
			{Name: "--offset", TakesValue: true},
		},
	},
	{
		Name: "recap",
		Flags: []FlagSpec{
			{Name: "--since", TakesValue: true},
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
		},
	},
	{
		Name: "remember",
		Flags: []FlagSpec{
			{Name: "--project", TakesValue: true},
			{Name: "--tag", TakesValue: true},
			{Name: "--repo"},
		},
	},
	{
		Name: "repo",
		Flags: []FlagSpec{
			{Name: "--json"},
		},
		SubcommandFlags: []FlagSpec{
			{Name: "--limit", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--kind", TakesValue: true},
		},
	},
	{
		Name: "restore",
		Flags: []FlagSpec{
			{Name: "--force"},
			{Name: "--span", TakesValue: true},
			{Name: "--out", Short: "-o", TakesValue: true},
			{Name: "--all"},
		},
	},
	{
		Name: "resume",
		Flags: []FlagSpec{
			{Name: "--write-back"},
			{Name: "--exec"},
			{Name: "--app"},
		},
	},
	{
		Name: "review",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--harness", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--limit", TakesValue: true},
		},
	},
	{
		Name: "rules",
		SubcommandFlags: []FlagSpec{
			{Name: "--json"},
			{Name: "--limit", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--repo"},
		},
	},
	{
		Name: "search",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--re"},
			{Name: "--all"},
			{Name: "--no-embed"},
			{Name: "--rebuild"},
			{Name: "--harness", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--role", TakesValue: true},
			{Name: "--limit", TakesValue: true},
			{Name: "--session", TakesValue: true},
			{Name: "--sort", TakesValue: true},
		},
	},
	{
		Name: "secrets",
		Flags: []FlagSpec{
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
			{Name: "--scrub"},
			{Name: "--dry-run"},
		},
	},
	{Name: "share"},
	{
		Name: "shell",
		Flags: []FlagSpec{
			{Short: "-c", TakesValue: true},
			{Name: "--verify"},
			{Name: "--json"},
		},
	},
	{
		Name: "show",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--harness", TakesValue: true},
			{Name: "--offset", TakesValue: true},
			{Name: "--limit", TakesValue: true},
		},
	},
	{Name: "sources"},
	{
		Name: "stats",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--html", TakesValue: true},
			{Name: "--redaction"},
			{Name: "--impact"},
			{Name: "--card", TakesValue: true},
			{Name: "--year"},
			{Name: "--no-index"},
			{Name: "--harness", TakesValue: true},
			{Name: "--project", TakesValue: true},
			{Name: "--since", TakesValue: true},
			{Name: "--role", TakesValue: true},
		},
	},
	{
		Name: "status",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--harness", TakesValue: true},
		},
	},
	{Name: "statusline"},
	{
		Name: "sync",
		Flags: []FlagSpec{
			{Name: "--full"},
		},
		SubcommandFlags: []FlagSpec{
			{Name: "--include-imported"},
			{Name: "--peer", TakesValue: true},
			{Name: "--pull"},
			{Name: "--both"},
		},
	},
	{
		Name: "tests",
		Flags: []FlagSpec{
			{Name: "--limit", TakesValue: true},
			{Name: "--json"},
		},
	},
	{
		Name: "trail",
		Flags: []FlagSpec{
			{Name: "--json"},
			{Name: "--all-projects"},
			{Name: "--project", TakesValue: true},
			{Name: "--harness", TakesValue: true},
			{Name: "--limit", TakesValue: true},
		},
	},
	{
		Name: "uninstall",
		Flags: []FlagSpec{
			{Name: "--all"},
			{Name: "--auto"},
			{Name: "--no-guidance"},
		},
	},
	{
		Name: "unlock",
		Flags: []FlagSpec{
			{Name: "--passphrase-stdin"},
		},
	},
	{
		Name: "update",
		Flags: []FlagSpec{
			{Name: "--force"},
		},
	},
	{Name: "version"},
	{
		Name: "view",
		Flags: []FlagSpec{
			{Name: "--out", TakesValue: true},
			{Name: "--no-open"},
		},
	},
	{Name: "warmup"},
	{Name: "warmup-status", HelpHidden: true, CompletionHidden: true},
	{
		Name: "wip",
		Flags: []FlagSpec{
			{Name: "--json"},
		},
	},
}

// primaryFlagOwners fixes the message label for flags shared across several commands
// so bare-search hints point at the command most callers actually meant.
var primaryFlagOwners = map[string]string{
	"--offset":           "show",
	"--span":             "restore",
	"--out":              "restore",
	"--force":            "restore",
	"--tag":              "remember",
	"--repo":             "remember",
	"--deep":             "doctor",
	"--offline":          "doctor",
	"--dry-run":          "forget",
	"--all-matches":      "forget",
	"--list":             "forget",
	"--unforget":         "forget",
	"--before":           "forget",
	"--to":               "handoff",
	"--exec":             "resume",
	"--write-back":       "resume",
	"--app":              "resume",
	"--plain":            "hook-prompt",
	"--crush":            "hook-tool",
	"--junie":            "hook-prompt",
	"--no-open":          "view",
	"--full":             "sync export",
	"--include-imported": "sync export",
	"--peer":             "sync export",
	"--pull":             "sync ssh",
	"--both":             "sync ssh",
	"--from":             "last",
	"--last":             "log",
	"--jsonl":            "log",
	"--seed":             "bench",
	"--delivery":         "bench live",
	"--settle":           "friction",
	"--unsettle":         "friction",
	"--state":            "promote",
	"--note":             "promote",
	"--html":             "stats",
	"--redaction":        "stats",
	"--impact":           "stats",
	"--card":             "stats",
	"--year":             "stats",
	"--no-index":         "stats",
	"--attribution":      "blame",
	"--git-note":         "blame",
	"--all-projects":     "how",
	"--scrub":            "secrets",
	"--kind":             "repo add",
	"--yes":              "delete",
	"--quiet":            "index",
	"--context-search":   "mcp",
	"--http":             "mcp",
	"--secrets":          "audit",
	"--fail-on-secrets":  "audit",
	"--encrypted":        "init",
	"--passphrase-stdin": "unlock",
	"--auto-lock":        "init",
}

func commandSpecFor(name string) (CommandSpec, bool) {
	for _, spec := range commandSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return CommandSpec{}, false
}

func buildFlagsOfOtherCommands() map[string]string {
	out := make(map[string]string, len(primaryFlagOwners))
	maps.Copy(out, primaryFlagOwners)
	inSearch := make(map[string]bool, len(searchFlags))
	for _, f := range searchFlags {
		inSearch[f] = true
	}
	for _, spec := range commandSpecs {
		if spec.Name == "search" {
			continue
		}
		for _, f := range slices.Concat(spec.Flags, spec.SubcommandFlags) {
			if !strings.HasPrefix(f.Name, "--") {
				continue
			}
			if inSearch[f.Name] || out[f.Name] != "" {
				continue
			}
			out[f.Name] = spec.Name
		}
	}
	return out
}

func buildSearchValueFlags() map[string]bool {
	out := make(map[string]bool)
	spec, ok := commandSpecFor("search")
	if !ok {
		return out
	}
	for _, f := range spec.ValueFlagNames() {
		out[f] = true
	}
	return out
}

func buildCompletionHiddenCommands() map[string]bool {
	out := make(map[string]bool)
	for _, spec := range commandSpecs {
		if spec.CompletionHidden {
			out[spec.Name] = true
		}
	}
	return out
}

func buildHelpHiddenCommands() map[string]bool {
	out := make(map[string]bool)
	for _, spec := range commandSpecs {
		if spec.HelpHidden {
			out[spec.Name] = true
		}
	}
	return out
}

func registryCompletionCommands() []string {
	var out []string
	for _, spec := range commandSpecs {
		if strings.HasPrefix(spec.Name, "hook-") || spec.CompletionHidden || spec.ToolHelp {
			continue
		}
		out = append(out, spec.Name)
	}
	slices.Sort(out)
	return out
}

type mcpRecallArgs struct {
	Query   string    `json:"query"`
	Harness string    `json:"harness"`
	Project string    `json:"project"`
	Limit   mcpNumber `json:"limit"`
	Offset  mcpNumber `json:"offset"`
}

type mcpContextArgs struct {
	Query   string `json:"query"`
	Harness string `json:"harness"`
}

type mcpBlameArgs struct {
	Path    string    `json:"path"`
	Harness string    `json:"harness"`
	Project string    `json:"project"`
	Since   string    `json:"since"`
	Limit   mcpNumber `json:"limit"`
	All     bool      `json:"all"`
}

type mcpFixArgs struct {
	Error string    `json:"error"`
	Limit mcpNumber `json:"limit"`
}

type mcpHowArgs struct {
	What    string    `json:"what"`
	Project string    `json:"project"`
	Limit   mcpNumber `json:"limit"`
}

type mcpOrientArgs struct {
	Project   string    `json:"project"`
	Limit     mcpNumber `json:"limit"`
	SessionID string    `json:"session_id"`
	CWD       string    `json:"cwd"`
}

type mcpRememberArgs struct {
	Text    string   `json:"text"`
	Project string   `json:"project"`
	Tags    []string `json:"tags"`
}

type mcpHandoffArgs struct {
	Session string `json:"session"`
	Harness string `json:"harness"`
	Project string `json:"project"`
}

// mcpModeSpec is the single metadata record for each mode exposed on the MCP `deja` tool.
type mcpModeSpec struct {
	Mode           string
	Target         string
	Aliases        []string
	Summary        string
	QField         string
	AcceptsProject bool
	AcceptsHarness bool
	AcceptsLimit   bool
	IsWrite        bool
	ArgsType       reflect.Type
}

var mcpModeSpecs = []mcpModeSpec{
	{
		Mode:           "recall",
		Target:         "recall",
		Aliases:        []string{"search"},
		Summary:        "search past sessions (1-3 exact terms: path, error, flag, identifier).",
		QField:         "query",
		AcceptsProject: true,
		AcceptsHarness: true,
		AcceptsLimit:   true,
		ArgsType:       reflect.TypeFor[mcpRecallArgs](),
	},
	{
		Mode:           "context",
		Target:         "recall_context",
		Aliases:        []string{"digest", "recall_context"},
		Summary:        "full digest of one session when a recall hit needs detail.",
		QField:         "query",
		AcceptsHarness: true,
		ArgsType:       reflect.TypeFor[mcpContextArgs](),
	},
	{
		Mode:           "blame",
		Target:         "blame",
		Summary:        "why a file or path:line is shaped this way, before editing or deleting.",
		QField:         "path",
		AcceptsProject: true,
		AcceptsHarness: true,
		AcceptsLimit:   true,
		ArgsType:       reflect.TypeFor[mcpBlameArgs](),
	},
	{
		Mode:         "fix",
		Target:       "fix",
		Summary:      "what this machine ran after a failing output (pass verbatim).",
		QField:       "error",
		AcceptsLimit: true,
		ArgsType:     reflect.TypeFor[mcpFixArgs](),
	},
	{
		Mode:           "how",
		Target:         "how",
		Summary:        "real command and flags run here, before guessing.",
		QField:         "what",
		AcceptsProject: true,
		AcceptsLimit:   true,
		ArgsType:       reflect.TypeFor[mcpHowArgs](),
	},
	{
		Mode:           "orient",
		Target:         "orient",
		Summary:        "recent commands and files touched here.",
		AcceptsProject: true,
		AcceptsLimit:   true,
		ArgsType:       reflect.TypeFor[mcpOrientArgs](),
	},
	{
		Mode:           "remember",
		Target:         "remember",
		Summary:        "store one user-corrected choice or non-obvious gotcha.",
		QField:         "text",
		AcceptsProject: true,
		IsWrite:        true,
		ArgsType:       reflect.TypeFor[mcpRememberArgs](),
	},
	{
		Mode:           "handoff",
		Target:         "handoff",
		Summary:        "continue another session (q: id or harness).",
		QField:         "session",
		AcceptsProject: true,
		AcceptsHarness: true,
		ArgsType:       reflect.TypeFor[mcpHandoffArgs](),
	},
}

func declaredModeNames() []string {
	out := make([]string, len(mcpModeSpecs))
	for i, m := range mcpModeSpecs {
		out[i] = m.Mode
	}
	return out
}

func buildDispatcherModes() map[string]string {
	out := make(map[string]string, len(mcpModeSpecs)+4)
	for _, m := range mcpModeSpecs {
		out[m.Mode] = m.Target
		for _, alias := range m.Aliases {
			out[alias] = m.Target
		}
	}
	return out
}

func buildQField() map[string]string {
	out := make(map[string]string, len(mcpModeSpecs))
	for _, m := range mcpModeSpecs {
		out[m.Target] = m.QField
	}
	return out
}

func isMCPReadTarget(name string) bool {
	for _, m := range mcpModeSpecs {
		if m.Target == name {
			return !m.IsWrite
		}
	}
	return false
}
