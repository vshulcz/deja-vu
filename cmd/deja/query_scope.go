package main

import (
	"os"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/search"
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
	ExplicitProject string
	Harness         string
	Activation      string
	Projects        []string
	ExcludeIDs      map[string]bool
	ExcludeUpTo     map[string]time.Time
	Global          bool
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
	var projects []string
	if !global {
		if project != "" {
			projects = []string{project}
		} else {
			projects = projectNamesForDir(in.CWD)
		}
	}
	scope := QueryScope{
		CWD:             in.CWD,
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
