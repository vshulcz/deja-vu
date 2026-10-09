//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux || windows)

package tui

import (
	"os"
	"time"
)

// No raw mode here: the caller falls back to the text screens.
func makeRaw(in, out *os.File) (func(), error) { return nil, ErrNotTerminal }

func size(f *os.File) (int, int, bool) { return 0, 0, false }

func readTimeout(f *os.File, buf []byte, d time.Duration) (int, error) { return 0, errTimeout }

func (t *Term) watchSize() { t.wg.Done() }
