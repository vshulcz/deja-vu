package cjkfold

import "testing"

// The two generated tables are parallel: foldTrad[i] folds to foldSimp[i].
// Rune() silently skips any Traditional entry past the end of foldSimp, so an
// unequal length would drop mappings without any error. Guard the invariant.
func TestFoldTablesAligned(t *testing.T) {
	trad := []rune(foldTrad)
	simp := []rune(foldSimp)
	if len(trad) != len(simp) {
		t.Fatalf("foldTrad has %d runes, foldSimp has %d — tables misaligned", len(trad), len(simp))
	}
}

func TestRuneFolds(t *testing.T) {
	pairs := map[rune]rune{
		'國': '国', '學': '学', '車': '车', '馬': '马',
		'龍': '龙', '離': '离', '書': '书', '讀': '读',
		'語': '语', '漢': '汉',
	}
	for trad, want := range pairs {
		if got := Rune(trad); got != want {
			t.Errorf("Rune(%q) = %q, want %q", trad, got, want)
		}
	}
}

func TestRuneLeavesOthers(t *testing.T) {
	// Already-simplified, Latin, Cyrillic, Hiragana and Katakana runes fold to
	// themselves.
	for _, r := range []rune{'国', 'a', 'я', 'あ', 'ア', '5', '国'} {
		if got := Rune(r); got != r {
			t.Errorf("Rune(%q) = %q, want unchanged", r, got)
		}
	}
}

func TestString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"讀書", "读书"},
		{"漢語", "汉语"},
		{"hello", "hello"},
		{"смысл", "смысл"},
		{"mix 車 and text", "mix 车 and text"},
		{"", ""},
	}
	for _, c := range cases {
		if got := String(c.in); got != c.want {
			t.Errorf("String(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStringUnchangedReturnsInput(t *testing.T) {
	// Nothing to fold: the same string comes back.
	in := "only latin and 国 already simplified"
	if got := String(in); got != in {
		t.Errorf("String(%q) = %q, want unchanged", in, got)
	}
}

func TestHasCJK(t *testing.T) {
	yes := []string{"漢", "書と本", "アイス", "한국어", "mixed 語 here"}
	for _, s := range yes {
		if !HasCJK(s) {
			t.Errorf("HasCJK(%q) = false, want true", s)
		}
	}
	no := []string{"", "hello world", "смысл", "12345", "a.b,c"}
	for _, s := range no {
		if HasCJK(s) {
			t.Errorf("HasCJK(%q) = true, want false", s)
		}
	}
}
