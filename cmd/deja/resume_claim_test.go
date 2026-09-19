package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// How many harnesses `deja resume` can reopen is written in prose on three
// pages and was checked by nothing. It went stale in every direction at once:
// `find-a-session.html` said "twenty of the thirty-three … the remaining five"
// — which do not add up — and listed Continue among the five although
// `cn --fork <id>` is wired in resume.go, while `resume-a-session.html` said
// "twenty of the twenty-five indexed harnesses", two counts old. A translator
// then carried the same numbers into Chinese, which is how it surfaced (#3778).
//
// The registry is the source: `capabilities.resume` is tied to the code by
// TestCapabilityRegistryMatchesCode, so pinning the prose to it pins it to the
// commands deja actually prints.
func TestTheResumeClaimMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string `json:"id"`
			DisplayName  string `json:"display_name"`
			Capabilities struct {
				Resume bool `json:"resume"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	var without []string
	resumes := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		name := h.DisplayName
		if name == "" {
			name = h.ID
		}
		if h.Capabilities.Resume {
			resumes++
			continue
		}
		without = append(without, name)
	}
	if resumes == 0 || len(without) == 0 {
		t.Fatal("the registry says every harness resumes, or none does; this test is reading the wrong field")
	}
	sort.Slice(without, func(i, j int) bool {
		return strings.ToLower(without[i]) < strings.ToLower(without[j])
	})

	wantCount := countWord(t, resumes)
	// The pages say it in words, and the sentence around it differs on each.
	claim := regexp.MustCompile(`(?i)([a-z-]+) of the ([a-z-]+) (?:indexed )?harnesses`)

	for _, name := range []string{
		"docs/guide/find-a-session.html",
		"docs/guide/resume-a-session.html",
	} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(raw)
		found := claim.FindAllStringSubmatch(text, -1)
		if len(found) == 0 {
			t.Errorf("%s no longer says how many harnesses resume; if that is on purpose, take it out of this list", name)
		}
		for _, m := range found {
			if strings.EqualFold(m[1], wantCount) {
				continue
			}
			t.Errorf("%s says %q; the registry has %d harnesses that resume (%s)", name, m[0], resumes, wantCount)
		}
		// And the exceptions, which is the half that was wrong rather than
		// merely old: a harness listed as unable to resume when it can sends
		// the reader to another tool for nothing.
		for _, missing := range without {
			if !strings.Contains(text, missing) {
				t.Errorf("%s does not name %s, which cannot be resumed", name, missing)
			}
		}
		for _, h := range reg.Harnesses {
			if !h.Capabilities.Resume || h.DisplayName == "" {
				continue
			}
			// Roo Code and Kilo Code are named in these paragraphs on purpose:
			// half of each store resumes and the other half reopens in the
			// editor, and the pages say so.
			if h.ID == "roo" || h.ID == "kilocode" {
				continue
			}
			if strings.Contains(text, "— "+h.DisplayName) || strings.Contains(text, ", "+h.DisplayName+" and ") {
				t.Errorf("%s lists %s among the harnesses without a resume path; the registry says it has one", name, h.DisplayName)
			}
		}
	}
}
