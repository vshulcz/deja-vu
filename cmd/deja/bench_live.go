package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const liveBenchMaxFiles = 1200

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
	Repo             string             `json:"repo"`
	SourceFiles      int                `json:"source_files"`
	WorkedFiles      int                `json:"worked_files"`
	ActionableFiles  int                `json:"actionable_files"`
	ActionableRate   float64            `json:"actionable_rate"`
	Standing         int                `json:"standing"`
	RepoMemory       int                `json:"repo_memory"`
	PriorDecision    int                `json:"prior_decision"`
	RevertedApproach int                `json:"reverted_approach"`
	PastBreakage     int                `json:"past_breakage"`
	VerifiedCommand  int                `json:"verified_command"`
	TestTargetOnly   int                `json:"test_target_only"`
	Silent           int                `json:"silent"`
	EphemeralNoise   int                `json:"ephemeral_noise"`
	P50MS            float64            `json:"p50_ms"`
	P95MS            float64            `json:"p95_ms"`
	WallMS           float64            `json:"wall_ms"`
	Injections       liveInjectionAudit `json:"injections"`
}

// runBenchLive evaluates hook-tool recall on the current repository against the live index.
func runBenchLive(args []string) error {
	jsonOutput, err := parseBenchLiveArgs(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	report, err := measureLiveRepo(index.DefaultDir(), cwd)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printLiveBenchReport(os.Stdout, report)
	return nil
}

// parseBenchLiveArgs reads flags for deja bench live.
func parseBenchLiveArgs(args []string) (bool, error) {
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			return false, fmt.Errorf("bench: usage: bench live [--json]")
		}
	}
	return jsonOutput, nil
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
	projNames := liveBenchProjectNames(indexDir, root)
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
	report.WallMS = roundMS(time.Since(start))
	return report, nil
}

// liveBenchProjectNames resolves project identifiers matching root without mutating index state.
func liveBenchProjectNames(indexDir, root string) []string {
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
	case strings.Contains(line, breakageLabel):
		report.PastBreakage++
		actionable = true
	}
	if strings.Contains(line, " — verified here: ") {
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
		for _, tail := range []string{" — edited alongside ", " — callers in ", " — verified here: ", " — test target: "} {
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
			return nil
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
	if r.Injections.Total > 0 {
		_, _ = fmt.Fprintf(w, "injections log: %d recent (tool %d, dejavu %d, context %d)  median %d B  tool noise %d\n",
			r.Injections.Total, r.Injections.Tool, r.Injections.DejaVu, r.Injections.Context,
			r.Injections.MedianBytes, r.Injections.ToolNoise)
	}
}
