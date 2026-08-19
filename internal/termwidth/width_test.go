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
