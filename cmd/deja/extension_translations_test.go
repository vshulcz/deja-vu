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

func countRuneRange(s string, lo, hi rune) int {
	n := 0
	for _, r := range s {
		if r >= lo && r <= hi {
			n++
		}
	}
	return n
}

func countJapaneseChars(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0x3040 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF) {
			n++
		}
	}
	return n
}

func countHangulChars(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x1100 && r <= 0x11FF) {
			n++
		}
	}
	return n
}

// Every extension under extensions/ must carry all 11 non-English docs linked
// both ways, reference the demo asset, repo memory, and handoff, avoid SVG
// assets on VS Code, keep `--profile` on dsh install commands, and hold real
// translated text in each target script.
func TestExtensionDocsCoverAllElevenLocales(t *testing.T) {
	locales := []string{"zh", "zh-TW", "ja", "ko", "es", "pt", "fr", "de", "ru", "tr", "hi"}
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "extensions"))
	if err != nil {
		t.Fatal(err)
	}

	packages := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		readmeBytes, err := os.ReadFile(filepath.Join(root, "extensions", name, "README.md"))
		if err != nil {
			continue
		}
		packages++
		readme := string(readmeBytes)

		for _, lang := range locales {
			wantURL := "https://github.com/vshulcz/deja-vu/blob/main/extensions/" + name + "/docs/" + lang + ".md"
			if !strings.Contains(readme, wantURL) {
				t.Errorf("extensions/%s/README.md missing switcher link %s", name, wantURL)
			}

			docBytes, err := os.ReadFile(filepath.Join(root, "extensions", name, "docs", lang+".md"))
			if err != nil {
				t.Errorf("extensions/%s/docs/%s.md missing: %v", name, lang, err)
				continue
			}
			doc := string(docBytes)

			if !strings.Contains(doc, "[English](../README.md)") {
				t.Errorf("extensions/%s/docs/%s.md missing [English](../README.md)", name, lang)
			}
			for _, other := range locales {
				if other == lang {
					continue
				}
				if !strings.Contains(doc, "("+other+".md)") {
					t.Errorf("extensions/%s/docs/%s.md missing sibling link (%s.md)", name, lang, other)
				}
			}
			for _, req := range []string{
				"assets/demo.gif",
				".deja/memory.jsonl",
				"deja handoff",
				"https://github.com/vshulcz/deja-vu",
			} {
				if !strings.Contains(doc, req) {
					t.Errorf("extensions/%s/docs/%s.md missing %q", name, lang, req)
				}
			}
			if name == "vscode" && strings.Contains(strings.ToLower(doc), ".svg") {
				t.Errorf("extensions/vscode/docs/%s.md contains disallowed .svg reference", lang)
			}
			if name == "dsh" {
				for line := range strings.SplitSeq(doc, "\n") {
					if strings.Contains(line, "dsh plugin") && strings.Contains(line, "add") && !strings.Contains(line, "--profile") {
						t.Errorf("extensions/dsh/docs/%s.md: %q misses --profile", lang, strings.TrimSpace(line))
					}
				}
			}

			switch lang {
			case "zh", "zh-TW":
				if n := countCJK(doc); n < 200 {
					t.Errorf("extensions/%s/docs/%s.md has %d CJK chars (< 200)", name, lang, n)
				}
			case "ja":
				if n := countJapaneseChars(doc); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Japanese chars (< 150)", name, lang, n)
				}
			case "ko":
				if n := countHangulChars(doc); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Hangul chars (< 150)", name, lang, n)
				}
			case "ru":
				if n := countRuneRange(doc, 0x0400, 0x04FF); n < 200 {
					t.Errorf("extensions/%s/docs/%s.md has %d Cyrillic chars (< 200)", name, lang, n)
				}
			case "hi":
				if n := countRuneRange(doc, 0x0900, 0x097F); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Devanagari chars (< 150)", name, lang, n)
				}
			}
		}
	}

	if packages != 16 {
		t.Fatalf("expected 16 extension packages under extensions/, got %d", packages)
	}
}
