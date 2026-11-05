package model

import "testing"

func TestRepoProjectSourceAndBareName(t *testing.T) {
	s := Session{Project: RepoProjectPrefix + "acme/api"}
	s.SetSource("workstation")
	if s.Source.Origin != "repo" || s.Source.Instance != "" {
		t.Errorf("source = %+v", s.Source)
	}
	for in, want := range map[string]string{
		"repo:acme/api":     "acme/api",
		"imported:acme/api": "acme/api",
		"acme/api":          "acme/api",
	} {
		if got := BareProject(in); got != want {
			t.Errorf("BareProject(%q) = %q, want %q", in, got, want)
		}
	}
}
