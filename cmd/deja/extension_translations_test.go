package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each package under extensions/ is the page someone reads inside the harness
// they already use, and for six of the seven it existed only in English while
// dsh had a Chinese one (extensions/dsh/docs/zh.md). The count inside those
// pages is already guarded — TestChineseDocsCountTheHarnessesTheRegistryHas
// walks extensions/ for Chinese pages — but nothing said the page had to exist,
// or that the two halves had to point at each other. A translation reachable
// from nowhere is the same as no translation.
func TestEveryExtensionHasAChinesePageLinkedBothWays(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "extensions"))
	if err != nil {
		t.Fatal(err)
	}

	pairs := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		english := filepath.Join(root, "extensions", name, "README.md")
		if _, err := os.Stat(english); err != nil {
			continue // a package without a README is a different problem
		}
		pairs++

		zhPath := filepath.Join(root, "extensions", name, "docs", "zh.md")
		zh, err := os.ReadFile(zhPath)
		if err != nil {
			t.Errorf("extensions/%s/ has a README and no Chinese page: %v", name, err)
			continue
		}

		// The English page links the Chinese one by absolute URL, because it is
		// also published to npm and to plugin directories, where a relative
		// path resolves against their site and not against this repository.
		en, err := os.ReadFile(english)
		if err != nil {
			t.Fatalf("extensions/%s/README.md: %v", name, err)
		}
		want := "https://github.com/vshulcz/deja-vu/blob/main/extensions/" + name + "/docs/zh.md"
		if !strings.Contains(string(en), want) {
			t.Errorf("extensions/%s/README.md does not link its Chinese page — want %s", name, want)
		}
		if !strings.Contains(string(zh), "[English](../README.md)") {
			t.Errorf("extensions/%s/docs/zh.md does not link back to the English page", name)
		}

		// A file that is named zh and reads as English is the failure this
		// catches: the count guard beside it only fires on a Chinese numeral,
		// so a copied English page passes it in silence.
		if cjk := countCJK(string(zh)); cjk < 200 {
			t.Errorf("extensions/%s/docs/zh.md has %d Chinese characters — it reads as a copy of the English page", name, cjk)
		}
	}

	if pairs < 7 {
		t.Fatalf("found %d packages with a README under extensions/ — the listing is not being read", pairs)
	}
}

func countCJK(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			n++
		}
	}
	return n
}
