package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Chinese numerals for 10-99, which is the whole range a harness count will
// live in.
func chineseTens(n int) string {
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	if n < 10 || n > 99 {
		return ""
	}
	tens, unit := n/10, n%10
	out := "十"
	if tens > 1 {
		out = digits[tens] + "十"
	}
	if unit > 0 {
		out += digits[unit]
	}
	return out
}

// The English pages have two tests counting the registry for them; the Chinese
// ones had none, and a third of this repository's readers arrive on them. A
// count that is three harnesses stale is the same defect in either language,
// and harder to notice in the one you do not read.
//
// The rule is narrow on purpose: a count near the current one is the drift
// (thirty-four written when the registry holds thirty-five), while a different
// tens is some other number the page is entitled to say — forty sessions on
// one topic, eleven tools in the comparison.
func TestTheChinesePagesCountTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	current := chineseTens(n) + "个"
	if current == "个" {
		t.Fatalf("no Chinese numeral for %d harnesses", n)
	}

	var stale []string
	for d := -3; d <= 3; d++ {
		if d == 0 {
			continue
		}
		if s := chineseTens(n + d); s != "" {
			stale = append(stale, s+"个")
		}
	}

	pages := []string{filepath.Join(root, "docs/readme/README.zh.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs", "zh"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".html") {
			pages = append(pages, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 10 {
		t.Fatalf("only %d Chinese pages found — this test has lost its subject", len(pages))
	}

	saysTheCount := 0
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		rel, _ := filepath.Rel(root, page)
		rel = filepath.ToSlash(rel)
		if strings.Contains(text, current) {
			saysTheCount++
		}
		for _, s := range stale {
			if strings.Contains(text, s) {
				t.Errorf("%s says %q; the registry has %d, so it is %q", rel, s, n, current)
			}
		}
	}
	if saysTheCount == 0 {
		t.Errorf("no Chinese page states the harness count (%q) — the phrasing changed and this test stopped seeing it", current)
	}
}
