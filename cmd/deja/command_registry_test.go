package main

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestCommandRegistryCoversEveryCommandAndHelpEntry(t *testing.T) {
	dispatched := map[string]bool{
		"search": true,
		"show":   true,
		"last":   true,
		"aider":  true,
		"goose":  true,
	}
	for name := range commands {
		if strings.HasPrefix(name, "-") {
			continue
		}
		dispatched[name] = true
	}

	seen := map[string]bool{}
	for _, spec := range commandSpecs {
		if spec.Name == "" {
			t.Fatal("commandSpecs contains an entry with empty Name")
		}
		if seen[spec.Name] {
			t.Errorf("commandSpecs contains duplicate entry %q", spec.Name)
		}
		seen[spec.Name] = true
		if !dispatched[spec.Name] {
			t.Errorf("commandSpecs has orphan entry %q not in commands or dispatchKnows", spec.Name)
		}
	}
	for name := range dispatched {
		if _, ok := commandSpecFor(name); !ok {
			t.Errorf("dispatched command %q missing from commandSpecs", name)
		}
	}

	for _, entry := range helpEntries {
		spec, ok := commandSpecFor(entry.name)
		if !ok {
			t.Errorf("helpEntries %q missing from commandSpecs", entry.name)
			continue
		}
		if entry.toolHelp != spec.ToolHelp {
			t.Errorf("commandSpecFor(%q).ToolHelp = %v, want %v from helpEntries", entry.name, spec.ToolHelp, entry.toolHelp)
		}
	}
	for _, spec := range commandSpecs {
		if !spec.HelpHidden {
			if _, ok := helpEntryFor(spec.Name); !ok {
				t.Errorf("non-hidden command %q missing from helpEntries", spec.Name)
			}
		}
		if got := helpHidden[spec.Name]; got != spec.HelpHidden {
			t.Errorf("helpHidden[%q] = %v, want commandSpec.HelpHidden = %v", spec.Name, got, spec.HelpHidden)
		}
		if got := completionHiddenCommands[spec.Name]; got != spec.CompletionHidden {
			t.Errorf("completionHiddenCommands[%q] = %v, want commandSpec.CompletionHidden = %v", spec.Name, got, spec.CompletionHidden)
		}
	}

	var wantCompletion []string
	for _, spec := range commandSpecs {
		if strings.HasPrefix(spec.Name, "hook-") || spec.CompletionHidden || spec.ToolHelp {
			continue
		}
		wantCompletion = append(wantCompletion, spec.Name)
	}
	slices.Sort(wantCompletion)
	gotCompletion := completionCommands()
	if !slices.Equal(gotCompletion, wantCompletion) {
		t.Errorf("completionCommands() mismatch:\n got: %v\nwant: %v", gotCompletion, wantCompletion)
	}
}

func TestCommandRegistryFlagListsAndBareSearchGuards(t *testing.T) {
	searchSpec, ok := commandSpecFor("search")
	if !ok {
		t.Fatal("commandSpecFor(\"search\") missing")
	}
	if got := searchSpec.FlagNames(); !slices.Equal(got, searchFlags) {
		t.Errorf("searchSpec.FlagNames() = %v, want %v", got, searchFlags)
	}
	for _, flag := range searchSpec.ValueFlagNames() {
		if !searchValueFlags[flag] {
			t.Errorf("searchValueFlags missing %q from searchSpec.ValueFlagNames()", flag)
		}
	}
	for flag := range searchValueFlags {
		if !slices.Contains(searchSpec.ValueFlagNames(), flag) {
			t.Errorf("searchValueFlags has extra %q not in searchSpec.ValueFlagNames()", flag)
		}
	}

	inSearch := map[string]bool{}
	for _, f := range searchFlags {
		inSearch[f] = true
	}
	for _, spec := range commandSpecs {
		if spec.Name == "search" {
			continue
		}
		all := slices.Concat(spec.Flags, spec.SubcommandFlags)
		for _, f := range all {
			if !strings.HasPrefix(f.Name, "--") {
				continue
			}
			if !inSearch[f.Name] && flagsOfOtherCommands[f.Name] == "" {
				t.Errorf("flag %q from command %q missing from flagsOfOtherCommands", f.Name, spec.Name)
			}
		}
	}
	if flagsOfOtherCommands["--host"] != "hook-ext" {
		t.Errorf("flagsOfOtherCommands[\"--host\"] = %q, want \"hook-ext\"", flagsOfOtherCommands["--host"])
	}
	if flagsOfOtherCommands["--op"] != "hook-ext" {
		t.Errorf("flagsOfOtherCommands[\"--op\"] = %q, want \"hook-ext\"", flagsOfOtherCommands["--op"])
	}
	for _, args := range [][]string{{"--op", "prompt"}, {"--host", "claude", "query"}} {
		if _, err := parseSearch(args); err == nil || !strings.Contains(err.Error(), "is a flag of `deja hook-ext`") {
			t.Errorf("parseSearch(%v) error = %v, want bare-search guard naming `deja hook-ext`", args, err)
		}
	}

	if _, _, err := parseHookExtFlags([]string{"--bogus"}); err == nil {
		t.Fatal("parseHookExtFlags([--bogus]) succeeded, want unknownFlag error")
	} else if msg := err.Error(); !strings.Contains(msg, `hook-ext: unknown flag "--bogus"`) || !strings.Contains(msg, "--help") {
		t.Errorf("parseHookExtFlags([--bogus]) error = %q, want canonical unknownFlag help hint", msg)
	}
	if _, _, err := parseHookExtFlags([]string{"--hots"}); err == nil || !strings.Contains(err.Error(), "did you mean --host?") {
		t.Errorf("parseHookExtFlags([--hots]) error = %v, want suggestion for --host", err)
	}
	if _, _, err := parseHookExtFlags([]string{"--opp"}); err == nil || !strings.Contains(err.Error(), "did you mean --op?") {
		t.Errorf("parseHookExtFlags([--opp]) error = %v, want suggestion for --op", err)
	}
}

func TestCompletionParityAcrossAllFourShells(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}
	scripts := map[string]string{}
	for _, sh := range shells {
		scripts[sh] = emittedCompletion(t, sh)
	}

	for _, entry := range helpEntries {
		if entry.toolHelp {
			continue
		}
		var flags []string
		for tok := range strings.FieldsSeq(strings.Join(entry.usage, " ")) {
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
		for _, sh := range shells {
			block := shellCommandBlock(sh, scripts[sh], entry.name)
			if entry.name == "install" || entry.name == "uninstall" {
				block += " --all --auto"
			}
			for _, flag := range flags {
				if !strings.Contains(block, flag) {
					t.Errorf("%s completion for %q missing documented flag %s", sh, entry.name, flag)
				}
			}
		}
	}

	zeroOrDedicated := []string{
		"aider", "brief", "check", "ctx", "embed", "goose", "help",
		"hook-antigravity", "hook-codewhale", "hook-goose", "hook-goose-prompt",
		"hook-mcp-call", "hook-plan", "hook-precompact", "hook-refresh", "hook-stop",
		"reasonix-ext", "share", "sources", "statusline", "version", "warmup", "warmup-status",
	}
	for _, cmd := range zeroOrDedicated {
		for _, sh := range shells {
			block := shellCommandBlock(sh, scripts[sh], cmd)
			if sh == "fish" {
				block = fishLinesFor(scripts[sh], cmd)
			}
			for _, leaked := range []string{"--re", "--no-embed", "--sort"} {
				if strings.Contains(block, leaked) {
					t.Errorf("%s completion for %q leaked search flag %s:\n%s", sh, cmd, leaked, block)
				}
			}
		}
	}

	hookExtSpec, ok := commandSpecFor("hook-ext")
	if !ok {
		t.Fatal("hook-ext missing from commandSpecs")
	}
	if !slices.Equal(hookExtSpec.ValueFlagNames(), []string{"--host", "--op"}) {
		t.Errorf("hook-ext ValueFlagNames() = %v, want [--host --op]", hookExtSpec.ValueFlagNames())
	}
	for _, flag := range hookExtSpec.ValueFlagNames() {
		bashBlock := shellCommandBlock("bash", scripts["bash"], "hook-ext")
		if !strings.Contains(bashBlock, `"`+flag+`"`) {
			t.Errorf("bash hook-ext missing value guard for %s:\n%s", flag, bashBlock)
		}
		psBlock := shellCommandBlock("powershell", scripts["powershell"], "hook-ext")
		if !strings.Contains(psBlock, "'"+flag+"'") || !strings.Contains(psBlock, "$previous") {
			t.Errorf("powershell hook-ext missing value guard for %s:\n%s", flag, psBlock)
		}
		zshBlock := shellCommandBlock("zsh", scripts["zsh"], "hook-ext")
		if !strings.Contains(zshBlock, flag+"=") {
			t.Errorf("zsh hook-ext missing value spec %s=:\n%s", flag, zshBlock)
		}
		fishBlock := fishLinesFor(scripts["fish"], "hook-ext")
		if !strings.Contains(fishBlock, "-l "+strings.TrimPrefix(flag, "--")) || (!strings.Contains(fishBlock, " -r") && !strings.Contains(fishBlock, " -x")) {
			t.Errorf("fish hook-ext missing -r/-x value flag for %s:\n%s", flag, fishBlock)
		}
	}
}

func hasJSONField(t reflect.Type, name string) bool {
	if t == nil || name == "" {
		return false
	}
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag == name {
			return true
		}
	}
	return false
}

func TestMCPRegistryParityWithToolsListAndHandlers(t *testing.T) {
	canonical := make([]string, 0, len(mcpModeSpecs))
	for _, spec := range mcpModeSpecs {
		canonical = append(canonical, spec.Mode)
	}
	if got := declaredModes(); !slices.Equal(got, canonical) {
		t.Errorf("declaredModes() = %v, want %v", got, canonical)
	}

	desc := dejaToolDescription()
	for _, spec := range mcpModeSpecs {
		if !strings.Contains(desc, "- "+spec.Mode+": "+spec.Summary) {
			t.Errorf("dejaToolDescription() missing mode bullet for %q", spec.Mode)
		}
		if dispatcherModes[spec.Mode] != spec.Target {
			t.Errorf("dispatcherModes[%q] = %q, want %q", spec.Mode, dispatcherModes[spec.Mode], spec.Target)
		}
		for _, alias := range spec.Aliases {
			if dispatcherModes[alias] != spec.Target {
				t.Errorf("dispatcherModes[alias %q] = %q, want %q", alias, dispatcherModes[alias], spec.Target)
			}
		}
		if gotQ, ok := qField[spec.Target]; !ok || gotQ != spec.QField {
			t.Errorf("qField[%q] = (%q, %v), want (%q, true)", spec.Target, gotQ, ok, spec.QField)
		}
		if spec.ArgsType == nil || spec.ArgsType.Kind() != reflect.Struct {
			t.Errorf("mcpModeSpec[%q].ArgsType must be a struct, got %v", spec.Mode, spec.ArgsType)
			continue
		}
		if spec.QField != "" && !hasJSONField(spec.ArgsType, spec.QField) {
			t.Errorf("%s lacks json tag %q required by QField", spec.ArgsType.Name(), spec.QField)
		}
		if hasJSONField(spec.ArgsType, "project") != spec.AcceptsProject {
			t.Errorf("%s AcceptsProject=%v disagrees with json:project field", spec.Mode, spec.AcceptsProject)
		}
		if hasJSONField(spec.ArgsType, "harness") != spec.AcceptsHarness {
			t.Errorf("%s AcceptsHarness=%v disagrees with json:harness field", spec.Mode, spec.AcceptsHarness)
		}
		if hasJSONField(spec.ArgsType, "limit") != spec.AcceptsLimit {
			t.Errorf("%s AcceptsLimit=%v disagrees with json:limit field", spec.Mode, spec.AcceptsLimit)
		}
		if !spec.IsWrite && !isMCPReadTarget(spec.Target) {
			t.Errorf("read mode target %q not covered by isMCPReadTarget", spec.Target)
		}
		if spec.IsWrite && isMCPReadTarget(spec.Target) {
			t.Errorf("write mode target %q should not be marked read target", spec.Target)
		}
	}
}

func TestQueryScopeParityAcrossCLIAndMCP(t *testing.T) {
	explicit, err := resolveQueryScope(QueryScopeInput{
		CWD:        "/Users/alex/work/alpha",
		Project:    "beta",
		Harness:    "claude",
		Activation: policy.ActivationSearch,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope explicit failed: %v", err)
	}
	if !slices.Equal(explicit.Projects, []string{"beta"}) || explicit.CanWidenToMachine() {
		t.Errorf("explicit scope = %+v, want Projects=[beta] non-widenable", explicit)
	}
	if explicit.Name() != "beta" || !explicit.MatchesProject("beta") || explicit.MatchesProject("alpha") {
		t.Errorf("explicit scope helpers unexpected: Name=%q Matches(beta)=%v Matches(alpha)=%v",
			explicit.Name(), explicit.MatchesProject("beta"), explicit.MatchesProject("alpha"))
	}

	implicit, err := resolveQueryScope(QueryScopeInput{
		CWD:        "/Users/alex/work/alpha",
		Activation: policy.ActivationMCP,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope implicit failed: %v", err)
	}
	if !implicit.CanWidenToMachine() || len(implicit.Projects) == 0 || !implicit.MatchesProject("/Users/alex/work/alpha") {
		t.Errorf("implicit scope unexpected: %+v", implicit)
	}

	var opts search.Options
	implicit.ApplyToSearch(&opts)
	if !slices.Equal(opts.Projects, implicit.Projects) {
		t.Errorf("ApplyToSearch Projects = %v, want %v", opts.Projects, implicit.Projects)
	}

	t.Setenv("DEJA_RECALL_GLOBAL", "1")
	globalEnv, err := resolveQueryScope(QueryScopeInput{
		CWD:              "/Users/alex/work/alpha",
		RespectGlobalEnv: true,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope DEJA_RECALL_GLOBAL failed: %v", err)
	}
	if !globalEnv.Global || len(globalEnv.Projects) != 0 || globalEnv.Name() != "" || !globalEnv.MatchesProject("anything") {
		t.Errorf("globalEnv scope unexpected: %+v", globalEnv)
	}

	explicitOverGlobal, err := resolveQueryScope(QueryScopeInput{
		CWD:              "/Users/alex/work/alpha",
		Project:          "gamma",
		RespectGlobalEnv: true,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope explicit over DEJA_RECALL_GLOBAL failed: %v", err)
	}
	if explicitOverGlobal.Global || !slices.Equal(explicitOverGlobal.Projects, []string{"gamma"}) {
		t.Errorf("explicit project should win over DEJA_RECALL_GLOBAL, got %+v", explicitOverGlobal)
	}

	if _, badErr := resolveQueryScope(QueryScopeInput{Harness: "not-a-real-harness"}); badErr == nil {
		t.Fatal("resolveQueryScope with unknown harness succeeded, want error")
	}

	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	markSessionLive(dir, "live-session-1")
	live, err := resolveQueryScope(QueryScopeInput{
		IndexDir:           dir,
		CWD:                "/Users/alex/work/alpha",
		IncludeLiveLineage: true,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope live lineage failed: %v", err)
	}
	if !live.ExcludeIDs["live-session-1"] {
		t.Errorf("live ExcludeIDs = %v, want live-session-1 excluded", live.ExcludeIDs)
	}
	var liveOpts search.Options
	live.ApplyToSearch(&liveOpts)
	if !liveOpts.ExcludeSessions["live-session-1"] {
		t.Errorf("ApplyToSearch ExcludeSessions = %v, want live-session-1", liveOpts.ExcludeSessions)
	}
}
