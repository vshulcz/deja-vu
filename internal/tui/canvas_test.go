package tui

import (
	"os"
	"strings"
	"testing"
)

func TestPutClipsAndKeepsTheSurface(t *testing.T) {
	c := NewCanvas(10, 2)
	surf := Hex("#26263a")
	c.Fill(0, 0, 10, 1, surf)
	if x := c.Put(2, 0, "hello world", Style{FG: Hex("#ffffff")}, 8); x != 8 {
		t.Errorf("Put stops at max, got x=%d", x)
	}
	if got := c.Text(0); got != "  hello   " {
		t.Errorf("row = %q", got)
	}
	if c.cells[3].bg != surf {
		t.Error("text written with no background must keep the surface under it")
	}
	if x := c.Put(0, 5, "off the canvas", Style{}, 10); x != 10 {
		t.Errorf("a row below the canvas still advances, got %d", x)
	}
}

func TestPutWideAndCombining(t *testing.T) {
	c := NewCanvas(6, 1)
	x := c.Put(0, 0, "日本x", Style{}, 6)
	if x != 5 || c.Text(0) != "日本x " {
		t.Errorf("wide runes take two cells: x=%d row=%q", x, c.Text(0))
	}
	c = NewCanvas(4, 1)
	if x := c.Put(0, 0, "日本", Style{}, 3); x != 2 || c.Text(0) != "日  " {
		t.Errorf("a wide rune that does not fit is left out: x=%d %q", x, c.Text(0))
	}
	c = NewCanvas(4, 1)
	c.Put(0, 0, "éx", Style{}, 4)
	if got := c.Text(0); got != "éx  " {
		t.Errorf("a combining mark is composed or rides on its letter, got %q", got)
	}
	c = NewCanvas(4, 1)
	c.Put(0, 0, string(rune(0x301))+"a\x01", Style{}, 4)
	if got := c.Text(0); got != "a   " {
		t.Errorf("a leading mark and control bytes draw nothing, got %q", got)
	}
}

func TestPutClip(t *testing.T) {
	c := NewCanvas(8, 1)
	c.PutClip(0, 0, "abcdefghij", Style{}, 6)
	if got := c.Text(0); got != "abcde…  " {
		t.Errorf("clipped = %q", got)
	}
	c = NewCanvas(8, 1)
	c.PutClip(0, 0, "abc", Style{}, 6)
	if got := c.Text(0); got != "abc     " {
		t.Errorf("fits = %q", got)
	}
	if x := c.PutClip(6, 0, "abc", Style{}, 6); x != 6 {
		t.Errorf("no room writes nothing, got %d", x)
	}
}

func TestRecolorAndLines(t *testing.T) {
	c := NewCanvas(3, 1)
	c.Put(0, 0, "ab", Style{FG: Hex("#ff0000"), BG: Hex("#000000"), Bold: true, Italic: true, Reverse: true}, 3)
	true_ := c.Lines(ModeTrue)[0]
	if !strings.Contains(true_, "38;2;255;0;0") || !strings.Contains(true_, ";1;3;7") {
		t.Errorf("truecolor line = %q", true_)
	}
	if l := c.Lines(Mode256)[0]; !strings.Contains(l, "38;5;196") {
		t.Errorf("256 line = %q", l)
	}
	mono := c.Lines(ModeMono)[0]
	if strings.Contains(mono, "38;") || strings.Contains(mono, ";3") || !strings.Contains(mono, ";7") {
		t.Errorf("mono keeps reverse and bold only: %q", mono)
	}
	c.Recolor(0, 0, 3, 1, Hex("#585b70"), Hex("#181825"))
	if p := c.cells[0]; p.b || p.rv || p.fg != Hex("#585b70") {
		t.Errorf("recolor drops the style: %+v", p)
	}
	if NewCanvas(0, 0).W != 1 {
		t.Error("a zero canvas is one cell")
	}
}

func TestColors(t *testing.T) {
	if Hex("#zzzzzz") != 0 || Hex("#123") != 0 {
		t.Error("a malformed colour is no colour")
	}
	for in, want := range map[string]int{"#ff0000": 196, "#000000": 16, "#ffffff": 231, "#808080": 244, "#1e1e2e": 234} {
		if got := to256(Hex(in)); got != want {
			t.Errorf("to256(%s) = %d, want %d", in, got, want)
		}
	}
	if Index256(208) != Hex("#ff8700") || Index256(240) != Hex("#585858") || Index256(3) != Hex("#bcbcbc") {
		t.Error("Index256 round trip")
	}
	for _, c := range []struct {
		env  map[string]string
		want Mode
	}{
		{map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, ModeMono},
		{map[string]string{"COLORTERM": "24bit"}, ModeTrue},
		{map[string]string{"WT_SESSION": "x"}, ModeTrue},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, ModeTrue},
		{map[string]string{}, Mode256},
	} {
		for _, k := range []string{"NO_COLOR", "COLORTERM", "WT_SESSION", "TERM_PROGRAM"} {
			t.Setenv(k, c.env[k])
		}
		if got := DetectMode(); got != c.want {
			t.Errorf("DetectMode(%v) = %v, want %v", c.env, got, c.want)
		}
	}
}

// Draw writes only the rows that changed since the last frame.
func TestDrawWritesOnlyChangedRows(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	term := &Term{out: f, Mode: ModeMono}
	c := NewCanvas(4, 3)
	c.Put(0, 0, "top", Style{}, 4)
	c.Put(0, 2, "end", Style{}, 4)
	term.Draw(c)
	c.Put(0, 2, "END", Style{}, 4)
	off, _ := f.Seek(0, 1)
	term.Draw(c)
	b, _ := os.ReadFile(f.Name())
	second := string(b[off:])
	if strings.Contains(second, "top") || !strings.Contains(second, "\x1b[3;1H") || !strings.Contains(second, "END") {
		t.Errorf("second frame = %q", second)
	}
	term.Invalidate()
	off, _ = f.Seek(0, 1)
	term.Draw(c)
	term.Write("x")
	b, _ = os.ReadFile(f.Name())
	if !strings.Contains(string(b[off:]), "top") {
		t.Error("after Invalidate every row is written")
	}
}
