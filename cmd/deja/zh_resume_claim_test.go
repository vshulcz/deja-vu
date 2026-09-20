package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Chinese half of the resume claim, which the English check cannot see: it
// scans for "twenty-six of the thirty-three", and the translated page states
// the same thing as 三十三个智能体里有二十六个. That sentence went stale once
// already — it was faithfully translated from the English page while the
// English page was wrong — and the numeral check beside this one pins only the
// total, because 二十六个 has no counted noun after it.
//
// The shape of this check comes from @theluckystrike's PR #3783.
func TestChineseResumeClaimMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	reg := readHarnessResume(t, root)

	wantTotal, wantResumes := zhNumeral(reg.total), zhNumeral(reg.resumes)
	wantWithout := zhNumeral(len(reg.without))
	if wantTotal == "" || wantResumes == "" || wantWithout == "" {
		t.Fatalf("no Chinese numeral for %d/%d/%d", reg.total, reg.resumes, len(reg.without))
	}

	// 三十三个智能体里有二十六个 — "of the thirty-three agents, twenty-six".
	pair := regexp.MustCompile(`([一二三四五六七八九十]+)个智能体里有([一二三四五六七八九十]+)个`)
	// 剩下七个——aider、…、Zed——没有自己的恢复入口 — the ones that cannot.
	rest := regexp.MustCompile(`剩下([一二三四五六七八九十]+)个——([^—]*)——`)

	pairs, lists := 0, 0
	err := filepath.WalkDir(filepath.Join(root, "docs", "zh"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(b)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		for _, m := range pair.FindAllStringSubmatch(text, -1) {
			if !isZhNumeral(m[1]) || !isZhNumeral(m[2]) {
				continue
			}
			pairs++
			if m[1] != wantTotal {
				t.Errorf("%s says %q; the registry has %d harnesses (%s)", name, m[0], reg.total, wantTotal)
			}
			if m[2] != wantResumes {
				t.Errorf("%s says %q; %d harnesses resume (%s)", name, m[0], reg.resumes, wantResumes)
			}
		}

		for _, m := range rest.FindAllStringSubmatch(text, -1) {
			if !isZhNumeral(m[1]) {
				continue
			}
			lists++
			if m[1] != wantWithout {
				t.Errorf("%s says 剩下%s个; %d harnesses have no resume path (%s)",
					name, m[1], len(reg.without), wantWithout)
			}
			// The names stay in Latin script in the translated sentence, so they
			// are comparable to the registry's display names — but only as whole
			// list items: "pi" is inside "VS Code Copilot Chat", and a substring
			// match reads the sentence as naming a harness that resumes. The
			// list is separated by 、 and closed with 和.
			named := map[string]bool{}
			for _, item := range strings.Split(strings.ReplaceAll(m[2], "和", "、"), "、") {
				if item = strings.TrimSpace(item); item != "" {
					named[item] = true
				}
			}
			// This is the half that was wrong rather than merely old: Continue
			// was listed here as unable to resume while `cn --fork` reopens it.
			for _, missing := range reg.without {
				if !named[missing] {
					t.Errorf("%s does not name %s among the harnesses without a resume path", name, missing)
				}
			}
			for display, resumes := range reg.names {
				if !resumes || display == "Roo Code" || display == "Kilo Code" {
					continue
				}
				if named[display] {
					t.Errorf("%s lists %s among the harnesses without a resume path; the registry says it has one", name, display)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pairs == 0 {
		t.Fatal("no Chinese page states how many harnesses resume; if that is on purpose, this test goes with it")
	}
	if lists == 0 {
		t.Fatal("no Chinese page lists the harnesses that cannot resume; if that is on purpose, this check goes with it")
	}
}

// zhNumeral writes a count the way these pages do: 七, 十, 十一, 二十六, 三十三.
// zhCountWords next door starts at fifteen, because the harness count will
// never be seven again — but the number of harnesses that refuse to resume is
// small, and it is the other half of the same sentence.
func zhNumeral(n int) string {
	digits := []rune("一二三四五六七八九")
	switch {
	case n < 1 || n > 99:
		return ""
	case n < 10:
		return string(digits[n-1])
	case n < 20:
		if n == 10 {
			return "十"
		}
		return "十" + string(digits[n-10-1])
	}
	out := string(digits[n/10-1]) + "十"
	if n%10 != 0 {
		out += string(digits[n%10-1])
	}
	return out
}

// isZhNumeral keeps the scans off numerals that count something else, the way
// the English checks skip words that are not counts.
func isZhNumeral(s string) bool {
	for n := 1; n <= 99; n++ {
		if zhNumeral(n) == s {
			return true
		}
	}
	return false
}
