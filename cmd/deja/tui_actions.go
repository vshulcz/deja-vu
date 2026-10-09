package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The smaller actions on one session: its id and its project's path for the
// clipboard, and forgetting it, which asks first.

// clip puts text on the clipboard, or on the test's catch.
func (a *tuiApp) clip(text string) error {
	if a.copy != nil {
		return a.copy(text)
	}
	return tuiCopy(a.t.Write, text)
}

func (a *tuiApp) copySessionID() {
	s, ok := a.current()
	if !ok {
		return
	}
	if err := a.clip(s.ID); err != nil {
		a.say("Could not copy: "+err.Error(), false)
		return
	}
	a.say("Session id copied · "+s.ID, true)
}

func (a *tuiApp) copyProjectPath() {
	s, ok := a.current()
	if !ok {
		return
	}
	if s.Project == "" {
		a.say("This session has no project directory.", false)
		return
	}
	if err := a.clip(s.Project); err != nil {
		a.say("Could not copy: "+err.Error(), false)
		return
	}
	a.say("Project path copied · "+s.Project, true)
}

func (a *tuiApp) askForget() {
	s, ok := a.current()
	if !ok {
		return
	}
	a.openModal(modalForget)
	a.m.src = s
}

func (a *tuiApp) handleForget(ev tui.Event) {
	switch ev.Key {
	case tui.KeyEnter:
		a.modal = modalNone
		a.forgetSession(a.m.src)
	case tui.KeyEsc:
		a.modal = modalNone
	case tui.KeyRune:
		if ev.Rune == 'q' || ev.Rune == 'n' {
			a.modal = modalNone
		}
	}
}

// forgetSession runs what `deja forget --session <id>` runs, off the loop:
// the rebuild that takes the records out can take a while.
func (a *tuiApp) forgetSession(s model.Session) {
	forget := a.forget
	if forget == nil {
		forget = forgetInChild(a.dir)
	}
	a.say("Forgetting "+search.ShortID(s.ID)+"…", true)
	go func() {
		err := forget(s.ID)
		a.post(func() {
			if err != nil {
				a.say("Not forgotten: "+err.Error(), false)
				return
			}
			delete(a.details, sessionKey(s))
			if a.view == viewReader && sessionKey(a.reader.s) == sessionKey(s) {
				a.view = viewList
			}
			// The card that took its place is selected, not the top one.
			at := a.sel
			a.reload()
			if len(a.query) == 0 && len(a.rows) > 0 {
				a.sel = min(at, len(a.rows)-1)
				a.want(a.rows[a.sel].s)
			}
			a.say("Forgotten. deja forget --unforget "+s.ID+" brings it back.", true)
		})
	}()
}

// forgetInChild runs the forget command as its own process: it prints as it
// goes, and the screen owns the terminal.
func forgetInChild(dir string) func(string) error {
	return func(id string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, "forget", "--session", id)
		cmd.Env = append(os.Environ(), "DEJA_INDEX_DIR="+dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if msg := strings.TrimPrefix(strings.TrimSpace(lines[len(lines)-1]), "deja: "); msg != "" {
			return errors.New(msg)
		}
		return err
	}
}

func (a *tuiApp) drawForget() {
	p := a.p
	s := a.m.src
	x, y, iw := a.modalBox(72, 10)
	right := x + iw
	cx := p.Put(x, y, "Forget this session?", bold(cText), right)
	p.PutClip(cx+3, y, agentName(s.Harness)+" · "+tuiProject(s)+" · "+search.ShortID(s.ID), fgs(cMuted), right)
	y += 2
	asked := strings.Join(strings.Fields(s.Title), " ")
	if asked == "" {
		asked = "(no prompt recorded)"
	}
	p.PutClip(x, y, asked, fgs(cText), right)
	y += 2
	p.PutClip(x, y, "Drops it from deja's search. "+agentName(s.Harness)+"'s own file stays.", fgs(cSub), right)
	p.PutClip(x, y+1, "deja forget --unforget "+s.ID+" brings it back.", fgs(cMuted), right)
	y += 3
	bx := p.button(x, y, "↵", "Forget", true, right)
	p.button(bx+2, y, "esc", "Cancel", false, right)
}
