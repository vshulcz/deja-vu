package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const (
	liveBenchMaxFiles = 1200
	liveBenchMaxTasks = 60
)

// liveLocalizationAudit measures temporal Turn-0 task localization replay on real sessions.
type liveLocalizationAudit struct {
	Tasks             int     `json:"tasks"`
	Localized         int     `json:"localized"`
	LocalizedRate     float64 `json:"localized_rate"`
	ImplicitTasks     int     `json:"implicit_tasks"`
	ImplicitLocalized int     `json:"implicit_localized"`
	ImplicitRate      float64 `json:"implicit_rate"`
	WithGuardrail     int     `json:"with_guardrail"`
	WithTestCmd       int     `json:"with_test_cmd"`
	MedianBytes       int     `json:"median_bytes"`
	P50MS             float64 `json:"p50_ms"`
	P95MS             float64 `json:"p95_ms"`
}

// liveInjectionAudit summarizes historical snapshots recorded in .injections.jsonl.
type liveInjectionAudit struct {
	Total       int `json:"total"`
	Tool        int `json:"tool"`
	DejaVu      int `json:"dejavu"`
	Context     int `json:"context"`
	MedianBytes int `json:"median_bytes"`
	ToolNoise   int `json:"tool_noise"`
}

// liveBenchReport summarizes read-only point-of-action recall across the current repository.
type liveBenchReport struct {
	Repo             string                `json:"repo"`
	SourceFiles      int                   `json:"source_files"`
	WorkedFiles      int                   `json:"worked_files"`
	ActionableFiles  int                   `json:"actionable_files"`
	ActionableRate   float64               `json:"actionable_rate"`
	Standing         int                   `json:"standing"`
	RepoMemory       int                   `json:"repo_memory"`
	PriorDecision    int                   `json:"prior_decision"`
	RevertedApproach int                   `json:"reverted_approach"`
	PastBreakage     int                   `json:"past_breakage"`
	VerifiedCommand  int                   `json:"verified_command"`
	TestTargetOnly   int                   `json:"test_target_only"`
	Silent           int                   `json:"silent"`
	EphemeralNoise   int                   `json:"ephemeral_noise"`
	P50MS            float64               `json:"p50_ms"`
	P95MS            float64               `json:"p95_ms"`
	WallMS           float64               `json:"wall_ms"`
	Localization     liveLocalizationAudit `json:"localization"`
	Injections       liveInjectionAudit    `json:"injections"`
}

// liveBenchOptions holds parsed CLI flags for deja bench live.
type liveBenchOptions struct {
	jsonOutput bool
	delivery   bool
}

// liveHarnessCatalogSummary summarizes memory delivery coverage across all supported harnesses.
type liveHarnessCatalogSummary struct {
	Catalog          int `json:"catalog"`
	Detected         int `json:"detected"`
	AutoWired        int `json:"auto_wired"`
	MCPConfigured    int `json:"mcp_configured"`
	GuidanceWritten  int `json:"guidance_written"`
	WithSessions     int `json:"with_sessions"`
	ImportedSessions int `json:"imported_sessions,omitempty"`
}

// liveDeliveryWiring summarizes read-only installation wiring state for one active harness.
type liveDeliveryWiring struct {
	Harness  string `json:"harness"`
	State    string `json:"state"`
	MCP      string `json:"mcp,omitempty"`
	Guidance string `json:"guidance,omitempty"`
	Sessions int    `json:"sessions,omitempty"`
	Detected bool   `json:"detected,omitempty"`
}

// liveDeliveryReport summarizes delivery effectiveness across harnesses and retained telemetry.
type liveDeliveryReport struct {
	Repo                     string                    `json:"repo"`
	Harnesses                liveHarnessCatalogSummary `json:"harnesses"`
	Wirings                  []liveDeliveryWiring      `json:"wirings"`
	Recalls                  int                       `json:"recalls"`
	EmptyResultRate          float64                   `json:"empty_result_rate"`
	Injections               int                       `json:"injections"`
	DejaVuMoments            int                       `json:"dejavu_moments"`
	ToolLines                int                       `json:"tool_lines"`
	FixesServed              int                       `json:"fixes_served"`
	PushToPullRatio          float64                   `json:"push_to_pull_ratio"`
	Compaction               *usage.CompactionSummary  `json:"compaction,omitempty"`
	Snapshots                liveInjectionAudit        `json:"snapshots"`
	DuplicateDigestSnapshots int                       `json:"duplicate_digest_snapshots"`
	WallMS                   float64                   `json:"wall_ms"`
}

// runBenchLive evaluates hook-tool recall on the current repository against the live index.
func runBenchLive(args []string) error {
	opts, err := parseBenchLiveArgs(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if opts.delivery {
		report := measureLiveDelivery(index.DefaultDir(), cwd)
		if opts.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		printLiveDeliveryReport(os.Stdout, report)
		return nil
	}
	report, err := measureLiveRepo(index.DefaultDir(), cwd)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printLiveBenchReport(os.Stdout, report)
	return nil
}

// parseBenchLiveArgs reads flags for deja bench live.
func parseBenchLiveArgs(args []string) (liveBenchOptions, error) {
	var opts liveBenchOptions
	for _, arg := range args {
		switch arg {
		case "--json":
			opts.jsonOutput = true
		case "--delivery":
			opts.delivery = true
		default:
			return liveBenchOptions{}, errors.New("bench: usage: bench live [--delivery] [--json]")
		}
	}
	return opts, nil
}

// measureLiveRepo runs read-only hook-tool evaluation across non-test source files in cwd.
func measureLiveRepo(indexDir, cwd string) (liveBenchReport, error) {
	start := time.Now()
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	files, err := collectLiveBenchFiles(root, liveBenchMaxFiles)
	if err != nil {
		return liveBenchReport{}, err
	}
	pol := policy.Load()
	projNames := liveBenchProjectNames(root)
	ready := planIndexReady(indexDir)

	report := liveBenchReport{
		Repo:        filepath.Base(root),
		SourceFiles: len(files),
		Injections:  auditLiveInjections(indexDir),
	}
	if len(files) == 0 {
		report.WallMS = roundMS(time.Since(start))
		return report, nil
	}

	durations := make([]float64, 0, len(files))
	for _, path := range files {
		sessions := 0
		if ready {
			for _, meta := range index.FileSessions(indexDir, path) {
				if !pol.Allows(policy.ActivationAuto, meta.Project) {
					continue
				}
				if !fileMetaInScope(meta, path, nil) && !fileMetaInScope(meta, path, projNames) {
					continue
				}
				if !fileMetaMatchesRepoSubpath(meta, root, path) {
					continue
				}
				sessions++
			}
		}
		if sessions >= toolHookMinFileSessionsWithDecision {
			report.WorkedFiles++
		}

		t0 := time.Now()
		line := fileHookLineOutsideWithEdit(indexDir, root, path, "", nil)
		durations = append(durations, float64(time.Since(t0).Microseconds())/1000.0)

		classifyLiveHookLine(&report, line)
	}

	if report.WorkedFiles > 0 {
		report.ActionableRate = math.Round((float64(report.ActionableFiles)/float64(report.WorkedFiles))*1000) / 1000
	} else if report.ActionableFiles > 0 {
		report.ActionableRate = math.Round((float64(report.ActionableFiles)/float64(report.SourceFiles))*1000) / 1000
	}
	slices.Sort(durations)
	report.P50MS = percentileMS(durations, 0.50)
	report.P95MS = percentileMS(durations, 0.95)
	if ready {
		report.Localization = measureLiveLocalization(indexDir, root, projNames, pol)
	}
	report.WallMS = roundMS(time.Since(start))
	return report, nil
}

// liveBenchProjectNames resolves project identifiers matching root without mutating index state.
func liveBenchProjectNames(root string) []string {
	names := digest.ProjectNameCandidates(root)
	if len(names) == 0 {
		if plain := sources.PlainProjectName(root); plain != "" {
			names = append(names, plain)
		}
	}
	return names
}

// classifyLiveHookLine categorizes one fileHookLineOutsideWithEdit result into report counters.
func classifyLiveHookLine(report *liveBenchReport, line string) {
	if strings.TrimSpace(line) == "" {
		report.Silent++
		return
	}
	actionable := false
	switch {
	case strings.Contains(line, standingLabel):
		report.Standing++
		actionable = true
	case strings.Contains(line, repoDecisionLabel):
		report.RepoMemory++
		actionable = true
	case strings.Contains(line, decisionLabel) || strings.Contains(line, modelOrderLabel):
		report.PriorDecision++
		actionable = true
		if body := extractLiveDecisionBody(line); body != "" {
			if (len([]rune(body)) >= 45 && !usableRepoCandidateLine(body)) || isEphemeralCompletionReport(body) {
				report.EphemeralNoise++
			}
		}
	case strings.Contains(line, revertedLabel):
		report.RevertedApproach++
		actionable = true
		if body := extractLiveDecisionBody(line); body != "" {
			if (len([]rune(body)) >= 45 && !usableReversalLine(body)) || isEphemeralCompletionReport(body) || !hasReversalRationale(body) {
				report.EphemeralNoise++
			}
		}
	case strings.Contains(line, breakageLabel):
		report.PastBreakage++
		actionable = true
	}
	if strings.Contains(line, ranLabel) || strings.Contains(line, failLabel) || strings.Contains(line, " — verified here: ") {
		report.VerifiedCommand++
		actionable = true
	}
	if actionable {
		report.ActionableFiles++
		return
	}
	if strings.Contains(line, " — test target: ") {
		report.TestTargetOnly++
		return
	}
	// Co-edited / cross-file caller reminders with no decision/breakage/test-target.
	report.ActionableFiles++
}

// extractLiveDecisionBody strips trailing co-edit or verification segments from a decision line.
func extractLiveDecisionBody(line string) string {
	for _, label := range []string{decisionLabel, modelOrderLabel, revertedLabel} {
		_, rest, ok := strings.Cut(line, label)
		if !ok {
			continue
		}
		for _, tail := range []string{" — usually edited alongside ", " — edited alongside ", " — callers in ", ranLabel, failLabel, " — verified here: ", " — test target: "} {
			if before, _, cut := strings.Cut(rest, tail); cut {
				rest = before
			}
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// collectLiveBenchFiles walks root deterministically for non-test source files.
func collectLiveBenchFiles(root string, limit int) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable entries skipped during best-effort walk
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || liveSkippedDir(name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= limit {
			return fs.SkipAll
		}
		if !isLiveBenchSourceFile(path, name) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// liveSkippedDir reports whether a directory holds third-party, build, or fixture files.
func liveSkippedDir(name string) bool {
	switch name {
	case "vendor", "node_modules", "dist", "build", "target", "testdata", "__pycache__", "out", "coverage":
		return true
	}
	return false
}

// isLiveBenchSourceFile reports whether name is a non-test code file evaluated by bench live.
func isLiveBenchSourceFile(path, name string) bool {
	if isTestFilePath(path) {
		return false
	}
	low := strings.ToLower(name)
	for _, suf := range []string{".pb.go", "_gen.go", ".generated.ts", ".min.js", ".d.ts"} {
		if strings.HasSuffix(low, suf) {
			return false
		}
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs":
		return true
	}
	return false
}

// auditLiveInjections inspects historical snapshots in .injections.jsonl without writing anything.
func auditLiveInjections(indexDir string) liveInjectionAudit {
	snaps := usage.Snapshots(indexDir, 0)
	if len(snaps) == 0 {
		return liveInjectionAudit{}
	}
	var audit liveInjectionAudit
	audit.Total = len(snaps)
	sizes := make([]int, 0, len(snaps))
	for _, s := range snaps {
		sizes = append(sizes, s.Bytes)
		switch s.Kind {
		case "tool":
			audit.Tool++
			if body := extractLiveDecisionBody(s.Digest); body != "" {
				if (len([]rune(body)) >= 45 && !usableRepoCandidateLine(body)) || isEphemeralCompletionReport(body) {
					audit.ToolNoise++
				}
			}
		case "dejavu":
			audit.DejaVu++
		case "context":
			audit.Context++
		}
	}
	slices.Sort(sizes)
	audit.MedianBytes = sizes[len(sizes)/2]
	return audit
}

// percentileMS returns the p-th quantile (0..1) rounded to 0.01 ms.
func percentileMS(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	idx = max(0, min(idx, len(sorted)-1))
	return math.Round(sorted[idx]*100) / 100
}

// roundMS rounds a duration to 0.01 ms.
func roundMS(d time.Duration) float64 {
	return math.Round(float64(d.Microseconds())/10.0) / 100.0
}

// printLiveBenchReport formats liveBenchReport for terminal output.
func printLiveBenchReport(w io.Writer, r liveBenchReport) {
	_, _ = fmt.Fprintf(w, "repo: %s  source files: %d  worked (>=2 sessions): %d\n", r.Repo, r.SourceFiles, r.WorkedFiles)
	_, _ = fmt.Fprintf(w, "actionable recall: %d files (%.1f%% of worked)  ephemeral noise: %d\n",
		r.ActionableFiles, r.ActionableRate*100, r.EphemeralNoise)
	_, _ = fmt.Fprintf(w, "  standing notes:     %d\n", r.Standing)
	_, _ = fmt.Fprintf(w, "  repo memory:        %d\n", r.RepoMemory)
	_, _ = fmt.Fprintf(w, "  prior decisions:    %d\n", r.PriorDecision)
	_, _ = fmt.Fprintf(w, "  reverted approaches:%d\n", r.RevertedApproach)
	_, _ = fmt.Fprintf(w, "  past breakages:     %d\n", r.PastBreakage)
	_, _ = fmt.Fprintf(w, "  verified commands:  %d\n", r.VerifiedCommand)
	_, _ = fmt.Fprintf(w, "  test target only:   %d\n", r.TestTargetOnly)
	_, _ = fmt.Fprintf(w, "  silent:             %d\n", r.Silent)
	_, _ = fmt.Fprintf(w, "latency: p50 %.2f ms  p95 %.2f ms  wall %.0f ms\n", r.P50MS, r.P95MS, r.WallMS)
	if r.Localization.Tasks > 0 {
		_, _ = fmt.Fprintf(w, "turn-0 localization: %d/%d tasks (%.1f%%)  implicit: %d/%d (%.1f%%)  with guardrail: %d  with test cmd: %d  median %d B  p50 %.2f ms\n",
			r.Localization.Localized, r.Localization.Tasks, r.Localization.LocalizedRate*100,
			r.Localization.ImplicitLocalized, r.Localization.ImplicitTasks, r.Localization.ImplicitRate*100,
			r.Localization.WithGuardrail, r.Localization.WithTestCmd, r.Localization.MedianBytes, r.Localization.P50MS)
	}
	if r.Injections.Total > 0 {
		_, _ = fmt.Fprintf(w, "injections log: %d recent (tool %d, dejavu %d, context %d)  median %d B  tool noise %d\n",
			r.Injections.Total, r.Injections.Tool, r.Injections.DejaVu, r.Injections.Context,
			r.Injections.MedianBytes, r.Injections.ToolNoise)
	}
}

// measureLiveDelivery collects read-only wiring and delivery telemetry metrics without mutating indexDir.
func measureLiveDelivery(indexDir, cwd string) liveDeliveryReport {
	start := time.Now()
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	tot := usage.Totals(indexDir)
	snaps := usage.Snapshots(indexDir, 0)
	dupCount := 0
	for _, s := range snaps {
		if hasDuplicateDigestLines(s.Digest) {
			dupCount++
		}
	}
	push := tot.Injections
	ratio := 0.0
	if tot.Recalls > 0 {
		ratio = math.Round((float64(push)/float64(tot.Recalls))*100) / 100
	}
	harnesses, wirings := collectLiveDeliveryWirings(indexDir)
	return liveDeliveryReport{
		Repo:                     filepath.Base(root),
		Harnesses:                harnesses,
		Wirings:                  wirings,
		Recalls:                  tot.Recalls,
		EmptyResultRate:          math.Round(tot.EmptyResultRate*1000) / 1000,
		Injections:               tot.Injections,
		DejaVuMoments:            tot.DejaVuMoments,
		ToolLines:                tot.ToolLines,
		FixesServed:              tot.FixesServed,
		PushToPullRatio:          ratio,
		Compaction:               tot.Compaction,
		Snapshots:                auditLiveInjections(indexDir),
		DuplicateDigestSnapshots: dupCount,
		WallMS:                   roundMS(time.Since(start)),
	}
}

func canonicalBenchHarness(name string) string {
	switch name {
	case "", "deja":
		return ""
	case "codex-hook":
		return "codex"
	case "claude":
		return "claude-code"
	case "copilot-chat":
		return "vscode"
	case "trae-ide":
		return "trae"
	default:
		return name
	}
}

func normalizeBenchAutoState(harness, state string, switchedOff, binaryMissing, isSharedSkill bool) string {
	if switchedOff || state == "disabled" {
		return "switched-off"
	}
	if state == "" || state == "absent" {
		return "missing"
	}
	if binaryMissing && (state == "wired" || state == "plugin") {
		return "binary-missing"
	}
	if state == "out of date" {
		return "partial"
	}
	if state == "wired" {
		if isSharedSkill {
			return "guidance-only"
		}
		switch harness {
		case "aider":
			return "context-file"
		case "roo":
			return "guidance-only"
		}
	}
	return state
}

func normalizeBenchMCPState(state string, binaryMissing, switchedOff, pluginMissing bool) string {
	if switchedOff || state == "switched-off" || state == "disabled" {
		return "switched-off"
	}
	if pluginMissing || state == "plugin-missing" {
		return "plugin-missing"
	}
	if binaryMissing && (state == "ok" || state == "configured" || state == "installed" || state == "wired") {
		return "binary-missing"
	}
	switch state {
	case "", "missing", "absent", "config-missing", "not-wired", "not configured", "none":
		return "missing"
	case "ok", "configured", "installed", "wired":
		return "configured"
	case "out of date":
		return "stale"
	default:
		return state
	}
}

func statExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

type benchHarnessSignals struct {
	catalog    map[string]bool
	detected   map[string]bool
	autoStates map[string]string
	mcpStates  map[string]string
	sessions   map[string]int
	imported   int
}

func detectLocalHarnessRoots(sig *benchHarnessSignals) {
	for raw := range existingTargetChecks() {
		if name := canonicalBenchHarness(raw); name != "" {
			sig.catalog[name] = true
		}
	}
	for _, raw := range existingTargets() {
		if name := canonicalBenchHarness(raw); name != "" {
			sig.catalog[name] = true
			sig.detected[name] = true
		}
	}
	if slices.ContainsFunc(sources.ClaudeRoots(), statExists) {
		sig.detected["claude-code"] = true
	}
	if slices.ContainsFunc(sources.CodexRoots(), statExists) {
		sig.detected["codex"] = true
	}
	for _, pair := range []struct {
		name string
		path string
	}{
		{"gemini", sources.GeminiRoot()},
		{"opencode", sources.OpencodeDB()},
		{"codearts", sources.CodeArtsRoot()},
		{"doubao", sources.DoubaoRoot()},
		{"qoder", sources.QoderRoot()},
	} {
		if statExists(pair.path) {
			sig.detected[pair.name] = true
		}
	}
	for _, pair := range []struct {
		name string
		ok   bool
	}{
		{"kimi", kimiPluginInstalled()},
		{"grok", grokPluginInstalled()},
		{"codex", codexPluginInstalled()},
	} {
		if pair.ok {
			sig.detected[pair.name] = true
		}
	}
}

func collectDoctorHarnessSignals(sig *benchHarnessSignals, indexDir string) {
	shared := sharedSkillPath()
	for _, a := range collectDoctorAutoRecall() {
		name := canonicalBenchHarness(a.Name)
		if name == "" {
			continue
		}
		sig.catalog[name] = true
		isSharedSkill := shared != "" && a.Path == shared
		st := normalizeBenchAutoState(name, a.State, a.SwitchedOff, a.BinaryMissing, isSharedSkill)
		if prev, ok := sig.autoStates[name]; !ok || prev == "missing" || prev == "guidance-only" {
			sig.autoStates[name] = st
		}
		if st != "missing" && !isSharedSkill {
			sig.detected[name] = true
		}
	}
	for _, m := range collectDoctorMCP() {
		name := canonicalBenchHarness(m.Name)
		if name == "" {
			continue
		}
		sig.catalog[name] = true
		st := normalizeBenchMCPState(m.State, m.BinaryMissing, m.SwitchedOff, m.PluginMissing)
		if prev, ok := sig.mcpStates[name]; !ok || prev == "missing" {
			sig.mcpStates[name] = st
		}
		if m.State != "config-missing" && m.State != "" && m.State != "missing" {
			sig.detected[name] = true
		}
	}
	for raw, count := range index.HarnessSessionCounts(indexDir) {
		if count <= 0 {
			continue
		}
		if name := canonicalBenchHarness(raw); name != "" && sig.catalog[name] {
			sig.sessions[name] += count
			continue
		}
		sig.imported += count
	}
}

func collectLiveDeliveryWirings(indexDir string) (liveHarnessCatalogSummary, []liveDeliveryWiring) {
	sig := benchHarnessSignals{
		catalog: map[string]bool{
			"claude-code": true,
			"codex":       true,
			"gemini":      true,
			"opencode":    true,
			"codearts":    true,
			"qoder":       true,
			"kimi":        true,
			"grok":        true,
		},
		detected:   map[string]bool{},
		autoStates: map[string]string{},
		mcpStates:  map[string]string{},
		sessions:   map[string]int{},
	}
	detectLocalHarnessRoots(&sig)
	collectDoctorHarnessSignals(&sig, indexDir)

	summary := liveHarnessCatalogSummary{
		Catalog:          len(sig.catalog),
		ImportedSessions: sig.imported,
	}
	names := make([]string, 0, len(sig.catalog))
	for name := range sig.catalog {
		names = append(names, name)
	}
	slices.Sort(names)

	var out []liveDeliveryWiring
	for _, name := range names {
		isDetected := sig.detected[name]
		autoSt := sig.autoStates[name]
		if autoSt == "" {
			autoSt = "missing"
		}
		mcpSt := sig.mcpStates[name]
		sessCount := sig.sessions[name]
		autoActive := autoSt != "missing" && autoSt != "guidance-only"
		mcpActive := mcpSt != "" && mcpSt != "missing"
		if !isDetected && sessCount == 0 && !autoActive && !mcpActive {
			continue
		}

		var guidance string
		switch guidanceStatus(name) {
		case "written":
			guidance = "written"
			summary.GuidanceWritten++
		case "missing", "absent":
			guidance = "missing"
		}

		if isDetected {
			summary.Detected++
		}
		if autoSt == "wired" || autoSt == "plugin" {
			summary.AutoWired++
		}
		if mcpSt == "configured" {
			summary.MCPConfigured++
		}
		if sessCount > 0 {
			summary.WithSessions++
		}

		w := liveDeliveryWiring{
			Harness:  name,
			State:    autoSt,
			Guidance: guidance,
			Sessions: sessCount,
			Detected: isDetected,
		}
		if mcpSt != "" && mcpSt != "missing" {
			w.MCP = mcpSt
		} else if mcpSt == "missing" && isDetected {
			w.MCP = "missing"
		}
		out = append(out, w)
	}

	slices.SortFunc(out, func(a, b liveDeliveryWiring) int {
		if c := cmp.Compare(b.Sessions, a.Sessions); c != 0 {
			return c
		}
		return cmp.Compare(a.Harness, b.Harness)
	})
	return summary, out
}

func hasDuplicateDigestLines(digestText string) bool {
	if digestText == "" {
		return false
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(digestText, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 24 {
			continue
		}
		if strings.HasPrefix(trimmed, "<deja-") || strings.HasPrefix(trimmed, "</deja-") {
			continue
		}
		if strings.Trim(trimmed, "-= ") == "" {
			continue
		}
		if seen[trimmed] {
			return true
		}
		seen[trimmed] = true
	}
	return false
}

// printLiveDeliveryReport formats liveDeliveryReport for terminal output.
func printLiveDeliveryReport(w io.Writer, r liveDeliveryReport) {
	_, _ = fmt.Fprintf(w, "repo: %s  wall %.0f ms\n", r.Repo, r.WallMS)
	_, _ = fmt.Fprintf(w, "harnesses: %d catalog  %d detected  %d auto-wired  %d mcp  %d guidance  %d with sessions\n",
		r.Harnesses.Catalog, r.Harnesses.Detected, r.Harnesses.AutoWired, r.Harnesses.MCPConfigured,
		r.Harnesses.GuidanceWritten, r.Harnesses.WithSessions)
	if r.Harnesses.ImportedSessions > 0 {
		_, _ = fmt.Fprintf(w, "imported: %d sessions\n", r.Harnesses.ImportedSessions)
	}
	if len(r.Wirings) == 0 {
		_, _ = fmt.Fprintln(w, "wirings: none")
	} else {
		parts := make([]string, 0, len(r.Wirings))
		for _, hw := range r.Wirings {
			var meta []string
			if hw.MCP != "" {
				meta = append(meta, "mcp:"+hw.MCP)
			}
			if hw.Guidance != "" {
				meta = append(meta, "guidance:"+hw.Guidance)
			}
			if hw.Sessions > 0 {
				meta = append(meta, fmt.Sprintf("%ds", hw.Sessions))
			}
			if !hw.Detected && hw.Sessions > 0 {
				meta = append(meta, "sessions-only")
			}
			item := hw.Harness + "=" + hw.State
			if len(meta) > 0 {
				item += "(" + strings.Join(meta, ",") + ")"
			}
			parts = append(parts, item)
		}
		_, _ = fmt.Fprintf(w, "wirings: %s\n", strings.Join(parts, "  "))
	}
	_, _ = fmt.Fprintf(w, "pull: %d recalls (empty %.1f%%)  fixes served: %d\n",
		r.Recalls, r.EmptyResultRate*100, r.FixesServed)
	_, _ = fmt.Fprintf(w, "push: %d total (%d dejavu, %d tool lines)  push/pull %.2f\n",
		r.Injections, r.DejaVuMoments, r.ToolLines, r.PushToPullRatio)
	if r.Compaction != nil {
		_, _ = fmt.Fprintf(w, "compaction: %d captures  %d measured  %d pending  %d unmeasured  median %.1f  p75 %d\n",
			r.Compaction.Captures, r.Compaction.Measured, r.Compaction.Pending, r.Compaction.Unmeasured,
			r.Compaction.MedianActions, r.Compaction.P75Actions)
	}
	_, _ = fmt.Fprintf(w, "snapshots: %d recent (tool %d, dejavu %d, context %d)  median %d B  tool noise %d  duplicate lines %d\n",
		r.Snapshots.Total, r.Snapshots.Tool, r.Snapshots.DejaVu, r.Snapshots.Context,
		r.Snapshots.MedianBytes, r.Snapshots.ToolNoise, r.DuplicateDigestSnapshots)
}

// measureLiveLocalization replays real opening user task prompts from past
// repository sessions against strictly-older index state and checks whether
// Turn-0 prompt context names at least one file the session ended up editing.
func measureLiveLocalization(indexDir, root string, projNames []string, pol policy.Policy) liveLocalizationAudit {
	metas, err := index.AllMeta(indexDir)
	if err != nil || len(metas) == 0 {
		return liveLocalizationAudit{}
	}
	slices.SortFunc(metas, func(a, b index.SessionMeta) int {
		return b.Updated.Compare(a.Updated)
	})
	var audit liveLocalizationAudit
	var durations []float64
	var byteSizes []int
	seenTitle := map[string]bool{}
	seenAsked := map[string]bool{}
	for _, meta := range metas {
		if audit.Tasks >= liveBenchMaxTasks {
			break
		}
		if meta.AgentTitle || meta.Kind == model.KindSubagent || meta.Kind == model.KindSubagentFork || meta.Kind == "cron" || meta.Started.IsZero() || !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !slices.Contains(projNames, meta.Project) {
			continue
		}
		titleNorm := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(meta.Title))), " ")
		if titleNorm != "" && seenTitle[titleNorm] {
			continue
		}
		targets := liveExistingEditedTargets(root, meta.Touched)
		if len(targets) == 0 || len(targets) > 8 {
			continue
		}
		if titleNorm != "" {
			seenTitle[titleNorm] = true
		}
		asked := liveOpeningPrompt(indexDir, meta)
		if utf8.RuneCountInString(asked) < 20 || index.TitleIsBorrowed(asked) || digest.IsAgentArtifact(asked) {
			continue
		}
		normAsked := strings.Join(strings.Fields(strings.ToLower(asked)), " ")
		if seenAsked[normAsked] {
			continue
		}
		seenAsked[normAsked] = true
		skip := make(map[string]bool, 32)
		skip[meta.ID] = true
		for _, other := range metas {
			if other.ID == meta.ID || !other.Started.Before(meta.Started) || other.Updated.After(meta.Started) {
				skip[other.ID] = true
			}
		}
		implicit := !livePromptNamesAnyTarget(asked, targets)
		audit.Tasks++
		if implicit {
			audit.ImplicitTasks++
		}
		t0 := time.Now()
		out := evalPromptTurn0ReadOnly(indexDir, root, asked, projNames, skip)
		durations = append(durations, float64(time.Since(t0).Microseconds())/1000.0)
		if strings.TrimSpace(out) != "" {
			byteSizes = append(byteSizes, len(out))
		}
		if liveOutputNamesAnyTarget(out, targets) {
			audit.Localized++
			if implicit {
				audit.ImplicitLocalized++
			}
			if liveOutputHasGuardrail(out) {
				audit.WithGuardrail++
			}
			if liveOutputHasTestRecipe(out) {
				audit.WithTestCmd++
			}
		}
	}
	if audit.Tasks > 0 {
		audit.LocalizedRate = math.Round((float64(audit.Localized)/float64(audit.Tasks))*1000) / 1000
	}
	if audit.ImplicitTasks > 0 {
		audit.ImplicitRate = math.Round((float64(audit.ImplicitLocalized)/float64(audit.ImplicitTasks))*1000) / 1000
	}
	slices.Sort(durations)
	audit.P50MS = percentileMS(durations, 0.50)
	audit.P95MS = percentileMS(durations, 0.95)
	if len(byteSizes) > 0 {
		slices.Sort(byteSizes)
		audit.MedianBytes = byteSizes[len(byteSizes)/2]
	}
	return audit
}

func liveOpeningPrompt(indexDir string, meta index.SessionMeta) string {
	if sess, ok, err := index.FindByIdentityInRoles(indexDir, meta.Harness, meta.ID, map[string]bool{model.RoleUser: true}); err == nil && ok {
		for _, m := range sess.Messages {
			txt := strings.TrimSpace(prompt.Unwrap(digest.StripHarnessBlocks(m.Text)))
			if utf8.RuneCountInString(txt) < 12 || index.TitleIsBorrowed(txt) || digest.IsAgentArtifact(txt) {
				continue
			}
			return clipLiveOpeningPrompt(txt)
		}
	}
	return clipLiveOpeningPrompt(strings.TrimSpace(meta.Title))
}

func clipLiveOpeningPrompt(s string) string {
	runes := []rune(s)
	if len(runes) <= 2400 {
		return s
	}
	return string(runes[:1600]) + "\n" + string(runes[len(runes)-800:])
}

func liveExistingEditedTargets(root string, touched []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range touched {
		rel := orientPath(raw, root)
		if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		abs := filepath.Join(root, rel)
		if !isLiveBenchSourceFile(abs, filepath.Base(abs)) || !fileThere(abs) {
			continue
		}
		slashRel := filepath.ToSlash(rel)
		if !seen[slashRel] {
			seen[slashRel] = true
			out = append(out, slashRel)
		}
	}
	return out
}

func livePromptNamesAnyTarget(asked string, targets []string) bool {
	low := strings.ToLower(asked)
	for _, rel := range targets {
		base := strings.ToLower(filepath.Base(rel))
		if base != "" && strings.Contains(low, base) {
			return true
		}
	}
	return false
}

func liveOutputNamesAnyTarget(out string, targets []string) bool {
	if strings.TrimSpace(out) == "" {
		return false
	}
	low := strings.ToLower(out)
	for _, rel := range targets {
		slashRel := strings.ToLower(filepath.ToSlash(rel))
		if slashRel != "" && strings.Contains(low, slashRel) {
			return true
		}
		base := strings.ToLower(filepath.Base(slashRel))
		if len(base) >= 9 && strings.Contains(low, base) {
			return true
		}
	}
	return false
}

func liveOutputHasGuardrail(out string) bool {
	for _, marker := range []string{standingLabel, repoDecisionLabel, decisionLabel, modelOrderLabel, revertedLabel, breakageLabel} {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}

func liveOutputHasTestRecipe(out string) bool {
	for _, marker := range []string{"go test", "pytest", "cargo test", "npm test", "test target:", "verified"} {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}

// evalPromptTurn0ReadOnly executes the Turn-0 prompt recall pipeline without
// writing to .seen, .injections.jsonl, or .deja/memory.jsonl.
func evalPromptTurn0ReadOnly(dir, cwd, asked string, names []string, skip map[string]bool) string {
	asked = prompt.Unwrap(digest.StripHarnessBlocks(asked))
	nudge := promptDirectContextIn(dir, cwd, "", asked, true, skip)
	terms := prompt.Terms(focusPromptForTaskRecall(asked))
	if !promptTermsWorthAsking(terms) {
		return nudge
	}
	sel := selectPromptRankedCandidates(dir, cwd, asked, names, terms, skip, skip, nil, false)
	if len(sel.ranked) == 0 {
		return nudge
	}
	if len(sel.ss) == 0 {
		return attachRankedConsensusFallback(dir, cwd, "", asked, nudge, sel.ranked, sel.matched, sel.strong, nil, skip, true)
	}
	ss := sel.ss
	if len(names) > 0 || !projectMatchesAnyTerm(ss[0].Project, terms) {
		ss = search.LeadWithConclusion(ss, terms)
	}
	for i := range ss {
		index.MarkUserRules(dir, &ss[i], names)
	}
	ss, rejectedWarning := orderForInjection(ss)
	if sel.worthDigest {
		if full, ok := promptDigestBodyWithLead(dir, cwd, "", false, ss, sel.confident, terms, sel.idfOf, asked, nudge, rejectedWarning, sel.ranked, sel.matched, sel.strong, skip); ok {
			return full
		}
		return attachRankedConsensusFallback(dir, cwd, "", asked, nudge, sel.ranked, sel.matched, sel.strong, nil, skip, true)
	}
	return buildWeakPromptPointerBody(dir, cwd, asked, ss, terms, nudge, sel.ranked, sel.matched, sel.strong, nil, skip)
}
