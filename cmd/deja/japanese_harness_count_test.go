package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The Japanese README states the harness count in digits with a Japanese
// counted noun, which no other check here can see: the English word check
// scans for "thirty-four", the digit checks run over a fixed file list that
// this page is not on, and the Chinese ones look for 三十四. README.zh.md
// shipped a count eight harnesses stale before it got its own check (#3458);
// the same drift is likelier in a page none of the maintainers reads.
var jaHarnessClaims = []*regexp.Regexp{
	// "34 のエージェント" and "34 のコーディングエージェント": the counted noun is
	// what turns a numeral into a claim about harnesses. A bare numeral scan
	// would catch 43 回のコンパクション and the ten seconds of an install.
	regexp.MustCompile(`([0-9]+)\s*の(?:コーディング)?エージェント`),
}

func TestTheJapanesePageCountsTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	page := filepath.Join(root, "docs/readme/README.ja.md")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("README.ja.md: %v", err)
	}
	text := string(b)

	found := 0
	for _, re := range jaHarnessClaims {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			got, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			found++
			if got != n {
				t.Errorf("README.ja.md says %q; the registry has %d harnesses", m[0], n)
			}
		}
	}
	if found == 0 {
		t.Error("README.ja.md no longer states the harness count; if the phrasing changed on purpose, this check changes with it")
	}
}
