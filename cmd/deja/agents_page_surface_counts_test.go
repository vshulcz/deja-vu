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

// The agents page named the harnesses wired per prompt and before an action,
// and the lists went stale as targets gained hooks: five were wired before an
// action, the page said, while thirty-one were. It now counts them, and the
// counts are held to the registry the capability-drift test holds to the code.
func TestAgentsPageCountsTheWiredSurfaces(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			Surfaces map[string]struct {
				Status string `json:"status"`
			} `json:"surfaces"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	count := func(surface, status string) int {
		n := 0
		for _, h := range reg.Harnesses {
			if h.Surfaces[surface].Status == status {
				n++
			}
		}
		return n
	}
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "agents.html"))
	if err != nil {
		t.Fatal(err)
	}
	claim := regexp.MustCompile(`in (\d+) of the (\d+) harnesses[^.;]*?in part in (\d+) more`)
	for _, c := range []struct{ item, surface string }{
		{"<b>On each prompt</b>", "prompt"},
		{"<b>Before an edit or a command</b>", "pre_tool"},
	} {
		at := strings.Index(string(page), c.item)
		if at < 0 {
			t.Fatalf("agents.html lost the %q item", c.item)
		}
		li := string(page[at:])
		li = li[:strings.Index(li, "</li>")]
		m := claim.FindStringSubmatch(li)
		if m == nil {
			t.Fatalf("%s: no \"in N of the M harnesses … in part in K more\" sentence:\n%s", c.surface, li)
		}
		got := []string{m[1], m[2], m[3]}
		want := []string{strconv.Itoa(count(c.surface, "yes")), strconv.Itoa(len(reg.Harnesses)), strconv.Itoa(count(c.surface, "partial"))}
		if strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("%s: page says %s (wired/harnesses/partial), registry says %s", c.surface, strings.Join(got, "/"), strings.Join(want, "/"))
		}
	}
}
