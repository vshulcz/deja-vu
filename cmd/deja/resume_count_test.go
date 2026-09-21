package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Three pages say how many harnesses `deja resume` can hand a session back to,
// and one of them names the ones it cannot. Both halves come out of
// registry.json, which a test already checks against the code — but the prose
// was typed, so a harness landing with no resume path of its own would leave
// three pages claiming one more than there is.
func TestTheResumeCountFollowsTheRegistry(t *testing.T) {
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
	resumable := 0
	for _, h := range reg.Harnesses {
		if h.Capabilities.Resume {
			resumable++
			continue
		}
		without = append(without, h.DisplayName)
	}
	if resumable == 0 || len(without) == 0 {
		t.Fatal("registry.json has no resume capabilities — this checks nothing")
	}
	total := countWord(t, len(reg.Harnesses))
	yes := countWord(t, resumable)

	// "twenty-seven of the thirty-four harnesses", in either voice and
	// wherever it sits — body text or the FAQ block in the page head.
	claim := regexp.MustCompile(`(?i)([a-z-]+) of the ([a-z-]+) (?:indexed )?harnesses`)
	pages := []string{
		"docs/guide/find-a-session.html",
		"docs/guide/resume-a-session.html",
		"docs/guide/lost-context.html",
	}
	for _, page := range pages {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		found := claim.FindAllStringSubmatch(text, -1)
		if len(found) == 0 {
			t.Errorf("%s no longer says how many harnesses resume — the phrasing changed and this stopped checking it", page)
			continue
		}
		for _, m := range found {
			if got := strings.ToLower(m[1]); got != yes {
				t.Errorf("%s says %q of the harnesses resume; the registry has %d, so it is %q",
					page, got, resumable, yes)
			}
			if got := strings.ToLower(m[2]); got != total {
				t.Errorf("%s counts %q harnesses; the registry has %d, so it is %q",
					page, got, len(reg.Harnesses), total)
			}
		}
	}

	// And the page that names the ones without a resume path names all of them
	// and nothing else.
	page := "docs/guide/find-a-session.html"
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
	if err != nil {
		t.Fatal(err)
	}
	rest := regexp.MustCompile(`remaining ([a-z-]+) — ([^—]+) — have no resume path`)
	m := rest.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("%s no longer lists the harnesses without a resume path", page)
	}
	if m[1] != spelledCount(len(without)) {
		t.Errorf("%s says %q have no resume path; the registry says %d, so it is %q",
			page, m[1], len(without), spelledCount(len(without)))
	}
	named := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' })
	var listed []string
	for _, n := range named {
		for _, part := range strings.Split(n, " and ") {
			if p := strings.TrimSpace(part); p != "" {
				listed = append(listed, p)
			}
		}
	}
	want := map[string]bool{}
	for _, n := range without {
		want[n] = true
	}
	for _, n := range listed {
		if !want[n] {
			t.Errorf("%s names %q as having no resume path; the registry does not", page, n)
			continue
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("%s does not name %s, which has no resume path", page, n)
	}
}
