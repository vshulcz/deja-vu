package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Chinese numerals the count can be written with. Separate from countWords
// because none of the checks around this one can read them: the word check
// scans for "twenty-five", the digit check wants digits, and both skip
// README.zh.md and docs/zh entirely. That README shipped 二十五 — the count from
// eight harnesses ago — while every English document said thirty-three, and its
// lede named only the first twenty-five agents.
var zhCountWords = map[int]string{
	15: "十五", 16: "十六", 17: "十七", 18: "十八", 19: "十九",
	20: "二十", 21: "二十一", 22: "二十二", 23: "二十三", 24: "二十四",
	25: "二十五", 26: "二十六", 27: "二十七", 28: "二十八", 29: "二十九",
	30: "三十", 31: "三十一", 32: "三十二", 33: "三十三", 34: "三十四",
	35: "三十五", 36: "三十六",
}

// A bare numeral scan does not work here: 三十条任务链 and 十三个项目 count other
// things and are meant to stay. The counted noun is what turns a numeral into a
// claim about harnesses, so these are the shapes to pin.
var zhHarnessClaims = []*regexp.Regexp{
	regexp.MustCompile(`([一二三四五六七八九十]+)个(?:编程)?(?:智能体|工具)`),
	regexp.MustCompile(`今天是([一二三四五六七八九十]+)个`),
}

func TestChineseDocsCountTheHarnessesTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	want, ok := zhCountWords[n]
	if !ok {
		t.Fatalf("registry has %d harnesses and this test has no Chinese numeral for it; add one", n)
	}

	files := []string{"README.zh.md"}
	err := filepath.WalkDir(filepath.Join(root, "docs", "zh"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("docs/zh: %v", err)
	}

	claims := 0
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(b)
		for _, re := range zhHarnessClaims {
			for _, m := range re.FindAllStringSubmatch(text, -1) {
				if !knownZhNumeral(m[1]) {
					continue
				}
				claims++
				if m[1] != want {
					t.Errorf("%s says %q; the registry has %d (%s)", name, m[0], n, want)
				}
			}
		}
		// The lede names the agents one by one, and adding a harness never
		// touches that sentence. A correct count over a short list is worse
		// than no count.
		if name == "README.zh.md" {
			list := zhNamedList(text)
			if list == "" {
				t.Error("README.zh.md no longer names the harnesses; if that is on purpose, this check goes with it")
			} else if got := strings.Count(list, "·") + 1; got != n {
				t.Errorf("README.zh.md names %d harnesses; the registry has %d", got, n)
			}
		}
	}
	if claims == 0 {
		t.Fatal("no Chinese document says how many harnesses there are; if that is on purpose, this test goes with it")
	}
}

// knownZhNumeral reports whether a numeral is one of the counts this test can
// be about, so 十三个项目 and other unrelated counts are left alone.
func knownZhNumeral(s string) bool {
	for _, w := range zhCountWords {
		if w == s {
			return true
		}
	}
	return false
}

// zhNamedList returns the paragraph that lists the harnesses by name — the one
// closed by the full stop Chinese uses, right after Zed.
func zhNamedList(text string) string {
	const end = "Zed。"
	i := strings.LastIndex(text, end)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(text[:i], "\n\n")
	if start < 0 {
		return ""
	}
	return text[start+2 : i+len(end)]
}
