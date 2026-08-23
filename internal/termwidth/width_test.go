package termwidth

import "testing"

func TestColumns(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"retry queue", 11},
		{"重试队列", 8},
		{"リトライ", 8},
		{"한글", 4},
		{"ｆｕｌｌ", 8},
		{"🙂", 2},
		{"поток", 5},
		{"queue 队列", 10},
	} {
		if got := Columns(tc.in); got != tc.want {
			t.Errorf("Columns(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// A cut lands on a character boundary, never inside one, and never past the
// budget — half a wide character would be a broken cell either way.
func TestCutStopsAtTheBudget(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"retry queue", 5, "retry"},
		{"retry queue", 40, "retry queue"},
		{"重试队列", 4, "重试"},
		{"重试队列", 5, "重试"},
		{"重试队列", 0, ""},
		{"a重b", 2, "a"},
	} {
		got := Cut(tc.in, tc.width)
		if got != tc.want {
			t.Errorf("Cut(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
		if Columns(got) > tc.width {
			t.Errorf("Cut(%q, %d) prints %d columns", tc.in, tc.width, Columns(got))
		}
	}
}

func TestCutRightKeepsTheTail(t *testing.T) {
	if got := CutRight("work/数据平台/消费者重平衡", 12); got != "消费者重平衡" {
		t.Errorf("CutRight = %q, want the last six characters", got)
	}
	if got := Columns(CutRight("work/数据平台/消费者重平衡", 11)); got > 11 {
		t.Errorf("CutRight returned %d columns for a budget of 11", got)
	}
	// A wide rune that would straddle the budget is dropped whole rather than
	// half-printed.
	if got := CutRight("平衡", 1); got != "" {
		t.Errorf("CutRight = %q, want nothing to fit in one column", got)
	}
	if got := CutRight("abc", 10); got != "abc" {
		t.Errorf("CutRight shortened a string that fits: %q", got)
	}
}

// The table stopped at 1F64F, so a rocket counted one column and every line
// carrying one ran a column past the edge — at 60 and again at 80, which is
// what a single mismeasured rune looks like (#1594).
func TestRuneColumnsCoversTheWideEmojiBlocks(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		name string
	}{
		{'🚀', "rocket"},
		{'🚗', "car"},
		{'🛑', "stop sign"},
		{'🟠', "orange circle"},
		{'🟩', "green square"},
		{'🩺', "stethoscope"},
		{'🪟', "window"},
		{'🫠', "melting face"},
		{'🔥', "fire"},
		{'语', "CJK"},
		{'✅', "check mark button — the one review found still narrow"},
		{'✨', "sparkles"},
		{'❌', "cross mark"},
		{'⚡', "high voltage"},
		{'⭐', "star"},
		{'🆗', "OK button"},
		{'🟰', "heavy equals sign, next to the block the first fix added"},
		{'⌚', "watch"},
		{'♓', "pisces"},
	} {
		if got := RuneColumns(tc.r); got != 2 {
			t.Errorf("RuneColumns(%q) = %d, want 2 (%s)", tc.r, got, tc.name)
		}
	}
	// Latin, punctuation and the dingbats terminals draw in one cell stay at
	// one: widening those would cut titles a character early for everyone.
	// Ambiguous-width and Narrow runes stay at one. `·` and `…` are the
	// brief's own separators, and widening them would cut every title short.
	for _, r := range []rune{'a', ' ', '·', '…', '—', '✓', '→', 'é', 'ы'} {
		if got := RuneColumns(r); got != 1 {
			t.Errorf("RuneColumns(%q) = %d, want 1", r, got)
		}
	}
}
