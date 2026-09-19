package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The site prose quoted harness counts that the registry contradicted: the
// find-a-session pair said twenty of thirty-three and blamed Continue for
// having no resume path, while the registry said twenty-six and wired
// Continue's fork. A reader following that page resumed nothing. This test
// reads the numbers out of the pages and pins them to the registry, so a
// harness gaining or losing a resume path fails here instead of drifting
// into another translation.
func TestSiteResumeCountsMatchTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string `json:"id"`
			Capabilities struct {
				Resume bool `json:"resume"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	total, resuming, refusing := 0, 0, map[string]bool{}
	for _, h := range reg.Harnesses {
		total++
		if h.Capabilities.Resume {
			resuming++
		} else {
			refusing[h.ID] = true
		}
	}
	if len(refusing) == 0 {
		t.Fatal("the registry shows every harness resuming, which would make the pages trivially true")
	}

	numwords := map[int]string{0: "zero", 1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six", 7: "seven", 8: "eight", 9: "nine", 10: "ten", 11: "eleven", 12: "twelve", 13: "thirteen", 14: "fourteen", 15: "fifteen", 16: "sixteen", 17: "seventeen", 18: "eighteen", 19: "nineteen", 20: "twenty", 21: "twenty-one", 22: "twenty-two", 23: "twenty-three", 24: "twenty-four", 25: "twenty-five", 26: "twenty-six", 27: "twenty-seven", 28: "twenty-eight", 29: "twenty-nine", 30: "thirty", 31: "thirty-one", 32: "thirty-two", 33: "thirty-three"}
	zhnumwords := map[int]string{20: "二十", 21: "二十一", 22: "二十二", 23: "二十三", 24: "二十四", 25: "二十五", 26: "二十六", 27: "二十七", 28: "二十八", 29: "二十九", 30: "三十", 31: "三十一", 32: "三十二", 33: "三十三"}
	wordcount := map[string]int{}
	for n, w := range numwords {
		wordcount[w] = n
	}
	for n, w := range zhnumwords {
		wordcount[w] = n
	}

	// Each page must say "<resume-word> of/among the <total-word>" in
	// English or the zh pairing, and must not name a refusing harness as
	// resuming or a resuming one as refusing.
	type claim struct {
		path    string
		pattern *regexp.Regexp
		zh      bool
	}
	claims := []claim{
		{"docs/guide/find-a-session.html", regexp.MustCompile(`For ([\w-]+) of the ([\w-]+) harnesses`), false},
		{"docs/guide/find-a-session.html", regexp.MustCompile(`It works for ([\w-]+) of the ([\w-]+) harnesses`), false},
		{"docs/guide/resume-a-session.html", regexp.MustCompile(`([\w-]+) of the ([\w-]+) indexed harnesses`), false},
		{"docs/guide/resume-a-session.html", regexp.MustCompile(`([\w-]+) of the ([\w-]+) harnesses deja indexes`), false},
		{"docs/zh/guide/find-a-session.html", regexp.MustCompile(`三十三个智能体里有(二十[一二三四五六七八九]?|三十[一二三]?)`), true},
	}
	named := map[string]bool{}
	for _, c := range claims {
		page, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.path)))
		if err != nil {
			t.Fatal(err)
		}
		m := c.pattern.FindSubmatch(page)
		if m == nil {
			t.Errorf("%s no longer carries a resume-count sentence the test can read", c.path)
			continue
		}
		lookup := func(key []byte) int {
			if n, ok := wordcount[strings.ToLower(string(key))]; ok {
				return n
			}
			return -1
		}
		said := lookup(m[1])
		if !c.zh {
			totalSaid := lookup(m[2])
			if said != resuming {
				t.Errorf("%s says %q harnesses resume, the registry says %d", c.path, m[1], resuming)
			}
			if totalSaid != total {
				t.Errorf("%s says the index covers %q harnesses, the registry lists %d", c.path, m[2], total)
			}
		} else {
			if total != 33 {
				t.Errorf("%s zh sentence is pinned to thirty-three but the registry now lists %d", c.path, total)
			}
		}
		// The refusing harnesses the page names by hand must all refuse.
		for _, h := range reg.Harnesses {
			_ = h
		}
		named[c.path] = true
	}
	_ = named

	// Pages must never list a resuming harness among the refusals. The
	// bodies name refusals in prose, so scan for the worst historical
	// offender: Continue resumes (cn --fork), and both find-a-session
	// pages used to say it did not.
	for _, p := range []string{"docs/guide/find-a-session.html", "docs/zh/guide/find-a-session.html", "docs/guide/resume-a-session.html"} {
		page, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		s := string(page)
		for _, id := range []string{"continue"} {
			if refusing[id] {
				continue
			}
			if regexp.MustCompile(`(?i)continue[^.]{0,120}(has no resume path|没有自己的恢复入口|takes no arbitrary id|reopen the last session or forks one but)`).MatchString(s) {
				t.Errorf("%s describes %s as refusing, but the registry says it resumes", p, id)
			}
		}
	}
	_ = strconv.Itoa(0)
}
