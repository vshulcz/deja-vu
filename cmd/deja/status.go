package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
)

type statusReport struct {
	SchemaVersion   int      `json:"schema_version"`
	ActiveHarness   string   `json:"active_harness,omitempty"`
	Harness         string   `json:"harness,omitempty"`
	HooksWired      bool     `json:"hooks_wired"`
	MCPWired        bool     `json:"mcp_wired"`
	HookTargets     []string `json:"hook_targets"`
	MCPTargets      []string `json:"mcp_targets"`
	WiredTargets    []string `json:"wired_targets"`
	IndexExists     bool     `json:"index_exists"`
	IndexedSessions int      `json:"indexed_sessions"`
	InstallCommand  string   `json:"install_command,omitempty"`
}

func runStatus(dir string, args []string, stdout io.Writer) error {
	jsonOut := false
	harness := ""
	args = splitKnownValueFlags(args, "--harness")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--harness":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("status: --harness needs a source name")
			}
			harness = statusNormalizeHarnessInput(args[i+1])
			if err := checkHarness(&harness); err != nil {
				return err
			}
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return unknownFlag("status", args[i], statusFlags)
			}
			return fmt.Errorf("status takes no positional arguments — got %q", args[i])
		}
	}

	rep := collectStatus(dir, harness)
	rep.SchemaVersion = jsonout.Version
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	return printStatusReport(stdout, rep)
}

func collectStatus(dir, harness string) statusReport {
	hookTargets := statusHookTargets()
	mcpTargets := statusMCPTargets()
	wiredTargets := statusUnionTargets(hookTargets, mcpTargets)
	active := statusDetectActiveHarness()

	scopeHarness := harness
	if scopeHarness == "" {
		scopeHarness = active
	}
	hooksWired := len(hookTargets) > 0
	mcpWired := len(mcpTargets) > 0
	if scopeHarness != "" {
		target := statusWiringName(scopeHarness)
		hooksWired = slices.Contains(hookTargets, target)
		mcpWired = slices.Contains(mcpTargets, target)
	}

	rep := statusReport{
		ActiveHarness: active,
		Harness:       harness,
		HooksWired:    hooksWired,
		MCPWired:      mcpWired,
		HookTargets:   hookTargets,
		MCPTargets:    mcpTargets,
		WiredTargets:  wiredTargets,
	}
	if dir != "" && dirExists(dir) {
		rep.IndexExists = true
		if n, err := index.SessionCount(dir); err == nil {
			rep.IndexedSessions = n
		}
	}
	if !rep.HooksWired && !rep.MCPWired {
		rep.InstallCommand = "deja install --auto"
	}
	return rep
}

func statusHookTargets() []string {
	seen := map[string]bool{}
	if claudeHookWiringState().state != "missing" {
		seen["claude-code"] = true
	}
	if codexPluginInstalled() {
		seen["codex"] = true
	} else {
		codex := codexHookWiringState()
		for _, h := range codexHookWiring {
			if hookEventWired(codex.hooks, h.Event, h.Sub) {
				seen["codex"] = true
				break
			}
		}
	}
	for _, a := range autoWirings() {
		b, err := os.ReadFile(a.path())
		if !autoUnwired(a, b, err) || harnessPluginCarriesRecall(a.name) {
			seen[a.name] = true
		}
	}
	return statusSortedKeys(seen)
}

func statusMCPTargets() []string {
	seen := map[string]bool{}
	for _, c := range doctorMCPConfigs() {
		if c.wired(c.path) {
			seen[c.name] = true
		}
	}
	return statusSortedKeys(seen)
}

func statusUnionTargets(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		seen[v] = true
	}
	return statusSortedKeys(seen)
}

func statusSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func statusDetectActiveHarness() string {
	switch {
	case os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "":
		return "claude"
	case os.Getenv("CODEX_THREAD_ID") != "" || os.Getenv("CODEX_CI") != "" || os.Getenv("CODEX_SANDBOX") != "" || os.Getenv("CODEX_MANAGED_BY_NPM") != "":
		return "codex"
	case os.Getenv("GEMINI_CLI") != "":
		return "gemini"
	case os.Getenv("OPENCODE") != "":
		return "opencode"
	case os.Getenv("CURSOR_AGENT") != "":
		return "cursor"
	case os.Getenv("ANTIGRAVITY_AGENT") != "":
		return "antigravity"
	case os.Getenv("KIMI_CLI") != "":
		return "kimi"
	case os.Getenv("GIGACODE_PACKAGED_LAUNCH") != "" || os.Getenv("GIGACODE_CLI") != "":
		return "gigacode"
	case os.Getenv("QWEN_CODE") != "":
		return "qwen"
	default:
		return ""
	}
}

func statusNormalizeHarnessInput(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "claude-code":
		return "claude"
	case "gemini-cli":
		return "gemini"
	case "cursor-cli":
		return "cursor"
	case "gigacode-cli":
		return "gigacode"
	default:
		return raw
	}
}

func statusWiringName(harness string) string {
	switch statusNormalizeHarnessInput(harness) {
	case "claude":
		return "claude-code"
	default:
		return statusNormalizeHarnessInput(harness)
	}
}

func printStatusReport(w io.Writer, rep statusReport) error {
	if rep.Harness != "" {
		if _, err := fmt.Fprintf(w, "Harness: %s\n", rep.Harness); err != nil {
			return err
		}
	} else if rep.ActiveHarness != "" {
		if _, err := fmt.Fprintf(w, "Active harness: %s\n", rep.ActiveHarness); err != nil {
			return err
		}
	}
	hooksState := "not wired"
	if rep.HooksWired {
		hooksState = "wired"
	}
	mcpState := "not wired"
	if rep.MCPWired {
		mcpState = "wired"
	}
	if _, err := fmt.Fprintf(w, "Hooks: %s\nMCP:   %s\n", hooksState, mcpState); err != nil {
		return err
	}
	if len(rep.WiredTargets) > 0 {
		if _, err := fmt.Fprintf(w, "Wired targets: %s\n", strings.Join(rep.WiredTargets, ", ")); err != nil {
			return err
		}
	}
	if rep.IndexExists {
		if _, err := fmt.Fprintf(w, "Index: %d session(s)\n", rep.IndexedSessions); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, "Index: not built yet"); err != nil {
			return err
		}
	}
	if rep.InstallCommand != "" {
		if _, err := fmt.Fprintf(w, "Note: memory hooks and MCP are not wired — run `%s` to enable automatic recall.\n", rep.InstallCommand); err != nil {
			return err
		}
	}
	return nil
}
