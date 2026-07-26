#!/usr/bin/env python3
"""Generate internal/index/cjk_fold.go for deja-vu from the Unihan database.

Traditional-to-Simplified folding lets a query written in one script find
content written in the other. Unihan's kSimplifiedVariant is the authoritative
mapping; we keep only entries with exactly one target that differs from the
source, so the fold stays deterministic (the reverse direction is genuinely
ambiguous — Simplified 干 covers Traditional 乾/幹/干 — which is why folding
only ever runs Traditional to Simplified).

Usage:
    curl -sSLO https://www.unicode.org/Public/UCD/latest/ucd/Unihan.zip
    unzip -o Unihan.zip Unihan_Variants.txt
    python3 gen_cjk_fold.py Unihan_Variants.txt > cjk_fold.go
"""

import sys

HEADER = '''package index

// Code generated from the Unihan database (kSimplifiedVariant); DO NOT EDIT.
// Regenerate with tools/gen_cjk_fold.py — see that file for the exact source
// and the filtering rule.
//
// CJK bigrams are sequences of raw codepoints, so 距離 and 距离 are unrelated
// index keys and a query written in one script cannot reach content written in
// the other. Folding Traditional to Simplified on both the index and the query
// side makes the two meet. The direction matters: Traditional to Simplified is
// effectively many-to-one and therefore deterministic, while the reverse is
// ambiguous (Simplified 干 stands for Traditional 乾, 幹 and 干).
//
// Only pairs whose target differs from the source are listed, and only where
// Unihan gives a single simplification, so the table is a pure function.

import "sync"

// foldTrad and foldSimp are parallel rune sequences: foldTrad[i] folds to
// foldSimp[i]. Two strings cost a fraction of a map literal in source size and
// build into the map lazily, so a run that never touches CJK never pays for it.
const foldTrad = "%s"

const foldSimp = "%s"

var (
\tfoldOnce  sync.Once
\tfoldTable map[rune]rune
)

func foldCJKRune(r rune) rune {
\tfoldOnce.Do(func() {
\t\ttrad := []rune(foldTrad)
\t\tsimp := []rune(foldSimp)
\t\tfoldTable = make(map[rune]rune, len(trad))
\t\tfor i, t := range trad {
\t\t\tif i < len(simp) {
\t\t\t\tfoldTable[t] = simp[i]
\t\t\t}
\t\t}
\t})
\tif s, ok := foldTable[r]; ok {
\t\treturn s
\t}
\treturn r
}

// foldCJK folds every Traditional rune in s to its Simplified form and leaves
// everything else — Latin, Cyrillic, Hiragana, Katakana, Hangul — untouched.
func foldCJK(s string) string {
\tvar changed bool
\trunes := []rune(s)
\tfor i, r := range runes {
\t\tif f := foldCJKRune(r); f != r {
\t\t\trunes[i] = f
\t\t\tchanged = true
\t\t}
\t}
\tif !changed {
\t\treturn s
\t}
\treturn string(runes)
}
'''


def main() -> None:
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    pairs = {}
    with open(sys.argv[1], encoding="utf-8") as f:
        for line in f:
            if line.startswith("#") or "\t" not in line:
                continue
            parts = line.rstrip("\n").split("\t")
            if len(parts) < 3 or parts[1] != "kSimplifiedVariant":
                continue
            src = chr(int(parts[0][2:], 16))
            targets = [chr(int(v[2:], 16)) for v in parts[2].split()
                       if v.startswith("U+")]
            distinct = [t for t in targets if t != src]
            if len(distinct) == 1:
                pairs[src] = distinct[0]

    trad = "".join(sorted(pairs))
    simp = "".join(pairs[t] for t in sorted(pairs))
    assert len(trad) == len(simp) == len(pairs)
    sys.stdout.write(HEADER % (trad, simp))
    print(f"// {len(pairs)} pairs", file=sys.stderr)


if __name__ == "__main__":
    main()
