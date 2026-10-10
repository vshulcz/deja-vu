package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// Actions on one session: resuming it, putting a deleted one back, its id
// and its project's path for the clipboard, and forgetting it, which asks
// first. The keys i, p and F work everywhere but are shown only in ^k, so the
// help and footer stay short.

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

// drawForget asks before forgetting, beside the cat with its ears down when
// the box has room for the whole animal.
func (a *tuiApp) drawForget() {
	p := a.p
	s := a.m.src
	x, y, iw := a.modalBox(80, 15)
	right := x + iw
	tx, ty := x, y
	if iw >= 64 && p.H >= 17 {
		a.drawCat(x+1, y+1, mark.Nothing)
		tx, ty = x+28, y+3
	}
	p.PutClip(tx, ty, "Forget this session?", bold(cText), right)
	asked := strings.Join(strings.Fields(s.Title), " ")
	if asked == "" {
		asked = "(no prompt recorded)"
	}
	p.PutClip(tx, ty+1, asked, fgs(cText), right)
	p.PutClip(tx, ty+2, agentName(s.Harness)+" · "+tuiProject(s)+" · "+tuiAgo(s.Updated, a.now), fgs(cMuted), right)
	p.PutClip(tx, ty+4, "deja stops finding it.", fgs(cSub), right)
	p.PutClip(tx, ty+5, agentName(s.Harness)+"'s own file stays.", fgs(cSub), right)
	bx := p.button(tx, ty+7, "↵", "Forget", true, right)
	p.button(bx+2, ty+7, "esc", "Keep it", false, right)
}

// resumeSelected leaves the screen and reopens the session in its own agent.
func (a *tuiApp) resumeSelected() {
	a.remember()
	s, ok := a.current()
	if !ok {
		return
	}
	if d := a.details[sessionKey(s)]; d != nil && d.gone {
		a.say(agentName(s.Harness)+" deleted this one. R puts it back first.", false)
		return
	}
	a.after = func() error {
		fmt.Fprintf(os.Stderr, "deja: resuming in %s\n", agentName(s.Harness))
		pickedOnScreen = &s
		return runResume(a.dir, []string{s.ID, "--exec"}, os.Stdout)
	}
	a.leaving = "Resuming in " + agentName(s.Harness) + "…"
	a.quit = true
}

// putBack writes a session the agent deleted back where it reads it.
func (a *tuiApp) putBack() {
	s, ok := a.current()
	if !ok {
		return
	}
	d := a.details[sessionKey(s)]
	if d == nil || !d.gone {
		a.say("Still in "+agentName(s.Harness)+", nothing to put back.", false)
		return
	}
	go func() {
		err := writeBackSession(a.dir, d.full, io.Discard)
		a.post(func() {
			if err != nil {
				a.say("Could not put it back: "+err.Error(), false)
				return
			}
			d.gone = false
			// Back where its agent reads it, it is no longer a kept one.
			k := sessionKey(s)
			delete(a.keptIDs, k)
			for i, ks := range a.kept {
				if sessionKey(ks) == k {
					a.kept = append(a.kept[:i:i], a.kept[i+1:]...)
					break
				}
			}
			if a.scope == scopeKept && a.view != viewReader {
				a.reload()
			}
			a.say("Put back. r resumes it in "+agentName(s.Harness)+".", true)
		})
	}()
}
