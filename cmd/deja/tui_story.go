package main

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// tuiStory is how a session went, ready to draw under the preview's answer:
// the asks and replies, the commands with how they ended, the files edited.
// Built by the detail worker, never on the event loop.
type tuiStory struct {
	turns  []digest.Turn
	runs   []tuiRun
	edited []string
	failed int
}

type tuiRun struct {
	cmd   string
	state int // runUnknown, runOK or runFailed
	times int // the same command run back to back
}

const (
	runUnknown = iota
	runOK
	runFailed
)

func tuiStoryOf(s model.Session) tuiStory {
	st := digest.StoryOf(s)
	out := tuiStory{turns: st.Turns}
	for _, r := range st.Runs {
		cmd, code, recorded := index.CommandExitOutcome(r.Command)
		cmd = strings.TrimPrefix(firstLineOf(cmd), "$ ")
		cmd = strings.Join(strings.Fields(cmd), " ")
		if cmd == "" {
			continue
		}
		state := runUnknown
		switch {
		case recorded && code == 0:
			state = runOK
		case recorded, index.LooksLikeError(r.Output):
			state = runFailed
		}
		if n := len(out.runs); n > 0 && out.runs[n-1].cmd == cmd {
			out.runs[n-1].state = state
			out.runs[n-1].times++
		} else {
			out.runs = append(out.runs, tuiRun{cmd: cmd, state: state, times: 1})
		}
	}
	for _, r := range out.runs {
		if r.state == runFailed {
			out.failed++
		}
	}
	for _, f := range st.Edited {
		out.edited = append(out.edited, projectRelative(f, displayProject(s)))
	}
	return out
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// projectRelative drops everything up to the project's own directory, which
// is the part of an absolute path a reader already knows.
func projectRelative(path, project string) string {
	p := filepath.ToSlash(path)
	name := filepath.Base(filepath.ToSlash(project))
	if name == "" || name == "." || name == "/" {
		return p
	}
	if i := strings.LastIndex(p, "/"+name+"/"); i >= 0 {
		return p[i+len(name)+2:]
	}
	return p
}

// storyShare hands out the rows below the answer between the sections, one
// line at a time round the sections, so a long timeline cannot push the
// commands off. Opening a section costs its heading and the gap after it.
func storyShare(rows int, wants []int) []int {
	got := make([]int, len(wants))
	rows++ // the last section needs no gap under it
	for moved := true; moved; {
		moved = false
		for i, w := range wants {
			cost := 1
			if got[i] == 0 {
				cost = 3
			}
			if got[i] < w && rows >= cost {
				got[i]++
				rows -= cost
				moved = true
			}
		}
	}
	return got
}

// drawStory fills the preview from row y down to the row above bottom.
// shown holds lines already on screen (the ask, the conclusions), which the
// timeline does not repeat.
func (a *tuiApp) drawStory(x, y, right, bottom int, st tuiStory, shown []string) {
	p := a.p
	var turns []digest.Turn
	for _, t := range st.turns {
		dup := false
		for _, s := range shown {
			dup = dup || sameLine(s, t.Text)
		}
		if !dup {
			turns = append(turns, t)
		}
	}
	share := storyShare(bottom-y, []int{min(len(turns), 10), min(len(st.runs), 8), min(len(st.edited), 8)})
	heading := func(label, count string) {
		lx := p.Put(x, y, label, fgs(cMuted), right)
		p.Put(lx+2, y, count, fgs(cFaint), right)
		y++
	}
	if n := share[0]; n > 0 {
		heading("HOW IT WENT", tuiCount(len(st.turns), "turn"))
		for _, i := range elide(len(turns), n) {
			if i < 0 {
				p.Put(x+2, y, "⋯ "+tuiCount(len(turns)-n+1, "more turn"), fgs(cFaint), right)
			} else if t := turns[i]; t.User {
				p.Put(x, y, "›", fgs(cAcc), right)
				p.PutClip(x+2, y, t.Text, fgs(cText), right)
			} else {
				p.PutClip(x+2, y, t.Text, fgs(cSub), right)
			}
			y++
		}
		y++
	}
	if n := share[1]; n > 0 {
		count := tuiCount(len(st.runs), "command")
		if st.failed > 0 {
			count += " · " + num(st.failed) + " failed"
		}
		heading("RAN", count)
		// The last commands say how it ended, which is what tells two
		// sessions on the same bug apart.
		for _, r := range st.runs[len(st.runs)-n:] {
			mark, c := "·", cFaint
			switch r.state {
			case runOK:
				mark, c = "✓", cGrn
			case runFailed:
				mark, c = "✗", cRed
			}
			p.Put(x, y, mark, fgs(c), right)
			end := right
			if r.times > 1 {
				tag := "×" + num(r.times)
				end = right - termwidth.Columns(tag) - 1
				p.Put(end+1, y, tag, fgs(cMuted), right)
			}
			p.PutClip(x+2, y, r.cmd, fgs(cText), end)
			y++
		}
		y++
	}
	if n := share[2]; n > 0 {
		heading("EDITED", tuiCount(len(st.edited), "file"))
		for i, f := range st.edited[:n] {
			if i == n-1 && n < len(st.edited) {
				f = "+ " + num(len(st.edited)-n+1) + " more"
				p.Put(x, y, f, fgs(cFaint), right)
			} else {
				p.Put(x, y, termwidth.CutRight(f, right-x), fgs(cPeach), right)
			}
			y++
		}
	}
}

// elide picks n of total rows to show: the opening, a marker (-1) for the
// skipped middle, and the end, which is where a session says how it went.
func elide(total, n int) []int {
	out := make([]int, 0, n)
	if n >= total {
		for i := range total {
			out = append(out, i)
		}
		return out
	}
	if n < 3 {
		for i := total - n; i < total; i++ {
			out = append(out, i)
		}
		return out
	}
	head := max(1, n/3)
	for i := range head {
		out = append(out, i)
	}
	out = append(out, -1)
	for i := total - (n - head - 1); i < total; i++ {
		out = append(out, i)
	}
	return out
}
