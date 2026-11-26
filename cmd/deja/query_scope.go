package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// QueryScopeInput describes raw caller inputs used to resolve project, harness,
// and live-session exclusions consistently across CLI and MCP read paths.
type QueryScopeInput struct {
	IndexDir           string
	CWD                string
	Project            string
	Harness            string
	AllProjects        bool
	RespectGlobalEnv   bool
	IncludeLiveLineage bool
	Activation         string
}

// QueryScope holds a normalized project/harness/live-session filter.
type QueryScope struct {
	CWD             string
	ProjectDir      string
	ExplicitProject string
	Harness         string
	Activation      string
	Projects        []string
	ExcludeIDs      map[string]bool
	ExcludeUpTo     map[string]time.Time
	Global          bool
}

func appendUniqueScopeProject(dst []string, items ...string) []string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || slices.Contains(dst, item) {
			continue
		}
		dst = append(dst, item)
	}
	return dst
}

func hasWindowsDrivePrefix(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	return isAlpha && s[1] == ':' && (s[2] == '/' || s[2] == '\\')
}

func pathProjectCandidatesFromString(raw string) []string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if hasWindowsDrivePrefix(cleaned) {
		cleaned = cleaned[2:]
	}
	cleaned = strings.TrimPrefix(cleaned, "//")
	var segs []string
	for part := range strings.SplitSeq(cleaned, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		segs = append(segs, part)
	}
	if len(segs) == 0 {
		return nil
	}
	var out []string
	if len(segs) >= 2 {
		out = append(out, segs[len(segs)-2]+"/"+segs[len(segs)-1])
	}
	out = append(out, segs[len(segs)-1])
	return out
}

func resolveProjectInput(project, fallbackCWD string) (string, []string) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", nil
	}
	fallbackCWD = strings.TrimSpace(fallbackCWD)
	expanded := project
	expandedHome := false
	if project == "~" || strings.HasPrefix(project, "~/") || strings.HasPrefix(project, "~\\") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if project == "~" {
				expanded = home
			} else {
				rel := strings.ReplaceAll(project[2:], "\\", "/")
				expanded = filepath.Join(home, filepath.FromSlash(rel))
			}
			expandedHome = true
		}
	}
	if hasWindowsDrivePrefix(expanded) || strings.HasPrefix(expanded, "\\\\") {
		var dir string
		var names []string
		if runtime.GOOS == "windows" || filepath.IsAbs(expanded) {
			dir = filepath.Clean(expanded)
			names = appendUniqueScopeProject(names, projectNamesForDir(dir)...)
		}
		names = appendUniqueScopeProject(names, pathProjectCandidatesFromString(expanded)...)
		names = appendUniqueScopeProject(names, expanded, project)
		return dir, names
	}
	isRelDot := expanded == "." || expanded == ".." ||
		strings.HasPrefix(expanded, "./") || strings.HasPrefix(expanded, "../") ||
		strings.HasPrefix(expanded, ".\\") || strings.HasPrefix(expanded, "..\\")
	isAbsFS := filepath.IsAbs(expanded) || strings.HasPrefix(expanded, "/")
	isExistingSubdir := false
	if !expandedHome && !isRelDot && !isAbsFS && fallbackCWD != "" && strings.ContainsAny(expanded, "/\\") {
		cand := filepath.Join(fallbackCWD, filepath.FromSlash(strings.ReplaceAll(expanded, "\\", "/")))
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			expanded = cand
			isExistingSubdir = true
		}
	}
	if !expandedHome && !isRelDot && !isAbsFS && !isExistingSubdir {
		return "", []string{project}
	}
	if !filepath.IsAbs(expanded) && fallbackCWD != "" {
		expanded = filepath.Join(fallbackCWD, filepath.FromSlash(strings.ReplaceAll(expanded, "\\", "/")))
	}
	dir := filepath.Clean(expanded)
	names := projectNamesForDir(dir)
	names = appendUniqueScopeProject(names, sources.ClaudeProjectName(dir), dir, project)
	return dir, names
}

func normalizeProjectName(project, fallbackCWD string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return ""
	}
	dir, names := resolveProjectInput(project, fallbackCWD)
	if dir != "" {
		if p := sources.ClaudeProjectName(dir); p != "" {
			return p
		}
		if len(names) > 0 {
			return names[0]
		}
	}
	return project
}

func projectNamesForDir(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	names := digest.ProjectNameCandidates(cwd)
	if label := digest.ProjectLabel(cwd); label != "" {
		names = append([]string{label}, slices.DeleteFunc(names, func(name string) bool { return name == label })...)
	}
	return names
}

func resolveQueryScope(in QueryScopeInput) (QueryScope, error) {
	harness := strings.TrimSpace(in.Harness)
	if err := checkHarness(&harness); err != nil {
		return QueryScope{}, err
	}
	project := strings.TrimSpace(in.Project)
	global := in.AllProjects || (in.RespectGlobalEnv && project == "" && os.Getenv("DEJA_RECALL_GLOBAL") == "1")
	var projectDir string
	var projects []string
	if !global {
		if project != "" {
			projectDir, projects = resolveProjectInput(project, in.CWD)
		} else {
			projects = projectNamesForDir(in.CWD)
		}
	}
	scope := QueryScope{
		CWD:             in.CWD,
		ProjectDir:      projectDir,
		ExplicitProject: project,
		Harness:         harness,
		Activation:      in.Activation,
		Projects:        projects,
		Global:          global,
	}
	if in.IncludeLiveLineage && in.IndexDir != "" {
		scope.ExcludeIDs, scope.ExcludeUpTo = liveLineageCut(in.IndexDir)
	}
	return scope, nil
}

// CanWidenToMachine reports whether an empty implicit project scope may fall back
// to machine-wide results without surprising an explicit --project filter.
func (s QueryScope) CanWidenToMachine() bool {
	return !s.Global && s.ExplicitProject == "" && len(s.Projects) > 0
}

// Name returns a human-readable project label for headings and JSON output.
func (s QueryScope) Name() string {
	return howScopeName(s.Projects)
}

// MatchesProject reports whether recordProject belongs in the resolved scope.
func (s QueryScope) MatchesProject(recordProject string) bool {
	return howProjectMatches(recordProject, s.Projects)
}

// ApplyToSearch copies resolved project, harness, and live-lineage filters into o.
func (s QueryScope) ApplyToSearch(o *search.Options) {
	if o == nil {
		return
	}
	if s.Harness != "" {
		o.Harness = s.Harness
	}
	if len(s.Projects) > 0 {
		o.Projects = slices.Clone(s.Projects)
	}
	if len(s.ExcludeIDs) > 0 {
		o.ExcludeSessions = s.ExcludeIDs
	}
	if len(s.ExcludeUpTo) > 0 {
		o.ExcludeUpTo = s.ExcludeUpTo
	}
}
