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

// How many harnesses `deja resume` can reopen is written in prose and was
// checked by nothing, so it went stale in every direction at once. Four pages,
// three different numbers, none of them right:
//
//	find-a-session.html   "twenty of the thirty-three … the remaining five"
//	resume-a-session.html "Twenty of the twenty-five indexed harnesses"
//	lost-context.html     "Nineteen of thirty-three" and "twenty of the twenty-five do"
//
// and the list of exceptions named Continue, which resumes with `cn --fork`.
// A contributor then translated one of those sentences into Chinese, faithfully
// (#3778), which is how it surfaced.
//
// The registry is the source: `capabilities.resume` is tied to the code by
// TestCapabilityRegistryMatchesCode, so pinning the prose to it pins it to the
// commands deja actually prints.
type harnessResume struct {
	total   int
	resumes int
	without []string
	names   map[string]bool // display name -> resumes
}

func readHarnessResume(t *testing.T, root string) harnessResume {
	t.Helper()
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
	out := harnessResume{names: map[string]bool{}}
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		out.total++
		name := h.DisplayName
		if name == "" {
			name = h.ID
		}
		out.names[name] = h.Capabilities.Resume
		if h.Capabilities.Resume {
			out.resumes++
			continue
		}
		out.without = append(out.without, name)
	}
	if out.resumes == 0 || len(out.without) == 0 {
		t.Fatal("the registry says every harness resumes, or none does; this test is reading the wrong field")
	}
	sort.Slice(out.without, func(i, j int) bool {
		return strings.ToLower(out.without[i]) < strings.ToLower(out.without[j])
	})
	return out
}

// numberWord reports whether a word is one of the counts these documents can
// be about, so "transcripts of thirty-three agents" is read as a claim about
// the total and not as a subset of something.
func numberWord(w string) bool {
	w = strings.ToLower(w)
	for _, known := range countWords {
		if known == w {
			return true
		}
	}
	return false
}

func TestTheResumeClaimMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	reg := readHarnessResume(t, root)
	wantTotal := countWord(t, reg.total)
	wantResumes := countWord(t, reg.resumes)

	// Every page, because the claim turned up on one nobody thought of. Both
	// spellings too: "of the thirty-three harnesses" and "of thirty-three
	// harnesses" — the second is how the one on lost-context.html hid.
	claim := regexp.MustCompile(`(?i)([a-z-]+) of (?:the )?([a-z-]+) (?:indexed )?(harnesses|agents)`)

	var pages []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".html", ".txt", ".md":
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 20 {
		t.Fatalf("only %d pages found; the walk is wrong", len(pages))
	}

	// A total with no noun after it: "twenty of the twenty-five do" sat on
	// lost-context.html under the sentence about resuming, and the claim above
	// cannot see it because there is nothing for it to be twenty-five of.
	// Only where the number stands for the harnesses themselves: "of thirty
	// task chains" is a bench corpus and is meant to stay thirty.
	bare := regexp.MustCompile(`(?i)of (?:the )?([a-z-]+)\s*(?:do|does|support|can|indexed|harnesses|agents|[.,;)]|—)`)

	claims := 0
	for _, path := range pages {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		for _, m := range claim.FindAllStringSubmatchIndex(text, -1) {
			whole := text[m[0]:m[1]]
			first, second := text[m[2]:m[3]], text[m[4]:m[5]]
			if !numberWord(second) {
				// "one of the registry entries" and other prose that happens
				// to fit the shape.
				continue
			}
			claims++
			if !strings.EqualFold(second, wantTotal) {
				t.Errorf("%s says %q; the registry has %d harnesses (%s)", name, whole, reg.total, wantTotal)
			}
			if !numberWord(first) {
				continue // a claim about all of them, not a subset
			}
			// A subset claim in a sentence about resuming is the resume count.
			lo := m[0] - 200
			if lo < 0 {
				lo = 0
			}
			hi := m[1] + 200
			if hi > len(text) {
				hi = len(text)
			}
			if !strings.Contains(strings.ToLower(text[lo:hi]), "resume") {
				continue
			}
			if !strings.EqualFold(first, wantResumes) {
				t.Errorf("%s says %q; %d harnesses resume (%s)", name, whole, reg.resumes, wantResumes)
			}
		}
		for _, m := range bare.FindAllStringSubmatch(text, -1) {
			if !numberWord(m[1]) || strings.EqualFold(m[1], wantTotal) {
				continue
			}
			// CHANGELOG keeps saying what was true at each release; so does a
			// line quoting an older measurement by its own count.
			t.Errorf("%s says %q, and there are %d harnesses (%s)", name, m[0], reg.total, wantTotal)
		}
	}
	if claims == 0 {
		t.Fatal("no page states how many harnesses there are; if that is on purpose, this test goes with it")
	}

	// And the exceptions, which is the half that was wrong rather than merely
	// old: a harness listed as unable to resume when it can sends the reader
	// to another tool for nothing.
	for _, page := range []string{"docs/guide/find-a-session.html", "docs/guide/resume-a-session.html"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		text := string(raw)
		for _, missing := range reg.without {
			if !strings.Contains(text, missing) {
				t.Errorf("%s does not name %s, which cannot be resumed", page, missing)
			}
		}
		for display, resumes := range reg.names {
			if !resumes {
				continue
			}
			// Roo Code and Kilo Code are named in those paragraphs on purpose:
			// half of each store resumes and the other half reopens in the
			// editor, and the pages say so.
			if display == "Roo Code" || display == "Kilo Code" {
				continue
			}
			if strings.Contains(text, "— "+display) || strings.Contains(text, ", "+display+" and ") {
				t.Errorf("%s lists %s among the harnesses without a resume path; the registry says it has one", page, display)
			}
		}
	}
}
